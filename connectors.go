package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Search terms for ATSs whose APIs only return keyword matches (Workday,
// Oracle, SmartRecruiters). Their keyword search is literal, so "intern" does
// not find "Co-op" and neither finds "Process Engineer I". The classifier
// does the real filtering afterwards; these just need to cast a wide net.
var searchTerms = []string{
	"intern",
	"co-op",
	"chemical engineer",
	"process engineer",
	"new grad",
	"graduate",
	"entry level",
	"early career",
	"controls engineer",
	"supply chain engineer",
	"R&D engineer",
}

// Fetcher pulls every candidate posting from one board. maxPages bounds how
// deep we paginate per search term on keyword-search ATSs.
type Fetcher func(ctx context.Context, c *Client, co Company, maxPages int) ([]Job, error)

var fetchers = map[string]Fetcher{
	"workday":         fetchWorkday,
	"greenhouse":      fetchGreenhouse,
	"lever":           fetchLever,
	"ashby":           fetchAshby,
	"smartrecruiters": fetchSmartRecruiters,
	"oracle":          fetchOracle,
}

// ---------------------------------------------------------------- Workday

type workdayResp struct {
	Total       int `json:"total"`
	JobPostings []struct {
		Title         string `json:"title"`
		ExternalPath  string `json:"externalPath"`
		LocationsText string `json:"locationsText"`
		PostedOn      string `json:"postedOn"`
	} `json:"jobPostings"`
}

const workdayPage = 20 // server-side cap per request

func workdayURLs(co Company) (api, base string) {
	if co.Host != "" { // path-style tenant
		return fmt.Sprintf("https://%s/wday/cxs/%s/%s/jobs", co.Host, co.Slug, co.Site),
			fmt.Sprintf("https://%s/recruiting/%s/%s", co.Host, co.Slug, co.Site)
	}
	host := fmt.Sprintf("%s.%s.myworkdayjobs.com", co.Slug, co.WD)
	return fmt.Sprintf("https://%s/wday/cxs/%s/%s/jobs", host, co.Slug, co.Site),
		fmt.Sprintf("https://%s/%s", host, co.Site)
}

var daysAgo = regexp.MustCompile(`(?i)(\d+)\s*\+?\s*days?\s+ago`)

// workdayPosted turns "Posted 6 Days Ago" into a date. "30+ Days Ago" is too
// vague to be a date, so it returns "" and is reported via workday30Plus.
func workdayPosted(text string, now time.Time) string {
	l := strings.ToLower(text)
	switch {
	case l == "":
		return ""
	case strings.Contains(l, "today"):
		return now.Format("2006-01-02")
	case strings.Contains(l, "yesterday"):
		return now.AddDate(0, 0, -1).Format("2006-01-02")
	case strings.Contains(l, "30+"):
		return ""
	}
	if m := daysAgo.FindStringSubmatch(l); m != nil {
		n, _ := strconv.Atoi(m[1])
		return now.AddDate(0, 0, -n).Format("2006-01-02")
	}
	return ""
}

func workday30Plus(text string) bool { return strings.Contains(text, "30+") }

func fetchWorkday(ctx context.Context, c *Client, co Company, maxPages int) ([]Job, error) {
	api, base := workdayURLs(co)
	now := time.Now().UTC()
	seen := map[string]Job{}
	var errs []error
	for _, term := range searchTerms {
		total := -1
		for page := 0; page < maxPages; page++ {
			offset := page * workdayPage
			body := map[string]any{"appliedFacets": map[string]any{}, "limit": workdayPage,
				"offset": offset, "searchText": term}
			var r workdayResp
			if err := c.PostJSON(ctx, api, body, &r); err != nil {
				errs = append(errs, err)
				break
			}
			// Workday sometimes reports total=0 on later pages; keep the first real one.
			if total < 0 && r.Total > 0 {
				total = r.Total
			}
			for _, p := range r.JobPostings {
				id := "workday:" + co.Slug + ":" + p.ExternalPath
				if _, ok := seen[id]; ok || p.ExternalPath == "" {
					continue
				}
				seen[id] = Job{ID: id, Company: co.Name, Category: co.Category,
					Title: p.Title, Location: p.LocationsText, URL: base + p.ExternalPath,
					Source: "workday", Posted: workdayPosted(p.PostedOn, now), Posted30: workday30Plus(p.PostedOn)}
			}
			if len(r.JobPostings) < workdayPage || (total > 0 && offset+len(r.JobPostings) >= total) {
				break
			}
		}
		// A tenant that rejects the very first request (bad slug/site, bot wall)
		// will reject every term; stop instead of hammering it.
		if len(seen) == 0 && len(errs) > 0 {
			return nil, errs[0]
		}
	}
	return values(seen), nil
}

// ------------------------------------------------------------- Greenhouse

func fetchGreenhouse(ctx context.Context, c *Client, co Company, _ int) ([]Job, error) {
	var r struct {
		Jobs []struct {
			ID             int64  `json:"id"`
			Title          string `json:"title"`
			AbsoluteURL    string `json:"absolute_url"`
			FirstPublished string `json:"first_published"`
			UpdatedAt      string `json:"updated_at"`
			Location       struct {
				Name string `json:"name"`
			} `json:"location"`
		} `json:"jobs"`
	}
	u := "https://boards-api.greenhouse.io/v1/boards/" + url.PathEscape(co.Slug) + "/jobs"
	if err := c.GetJSON(ctx, u, &r); err != nil {
		return nil, err
	}
	out := make([]Job, 0, len(r.Jobs))
	for _, p := range r.Jobs {
		posted := p.FirstPublished
		if posted == "" {
			posted = p.UpdatedAt
		}
		out = append(out, Job{ID: fmt.Sprintf("greenhouse:%s:%d", co.Slug, p.ID),
			Company: co.Name, Category: co.Category, Title: p.Title,
			Location: p.Location.Name, URL: p.AbsoluteURL, Source: "greenhouse",
			Posted: isoDate(posted)})
	}
	return out, nil
}

// ------------------------------------------------------------------ Lever

func fetchLever(ctx context.Context, c *Client, co Company, _ int) ([]Job, error) {
	var r []struct {
		ID         string `json:"id"`
		Text       string `json:"text"`
		HostedURL  string `json:"hostedUrl"`
		CreatedAt  int64  `json:"createdAt"`
		Categories struct {
			Location string `json:"location"`
		} `json:"categories"`
	}
	u := "https://api.lever.co/v0/postings/" + url.PathEscape(co.Slug) + "?mode=json"
	if err := c.GetJSON(ctx, u, &r); err != nil {
		return nil, err
	}
	out := make([]Job, 0, len(r))
	for _, p := range r {
		posted := ""
		if p.CreatedAt > 0 {
			posted = time.UnixMilli(p.CreatedAt).UTC().Format("2006-01-02")
		}
		out = append(out, Job{ID: "lever:" + co.Slug + ":" + p.ID, Company: co.Name,
			Category: co.Category, Title: p.Text, Location: p.Categories.Location,
			URL: p.HostedURL, Source: "lever", Posted: posted})
	}
	return out, nil
}

// ------------------------------------------------------------------ Ashby

func fetchAshby(ctx context.Context, c *Client, co Company, _ int) ([]Job, error) {
	var r struct {
		Jobs []struct {
			ID          string `json:"id"`
			Title       string `json:"title"`
			Location    string `json:"location"`
			JobURL      string `json:"jobUrl"`
			PublishedAt string `json:"publishedAt"`
			IsListed    *bool  `json:"isListed"`
		} `json:"jobs"`
	}
	u := "https://api.ashbyhq.com/posting-api/job-board/" + url.PathEscape(co.Slug)
	if err := c.GetJSON(ctx, u, &r); err != nil {
		return nil, err
	}
	out := make([]Job, 0, len(r.Jobs))
	for _, p := range r.Jobs {
		if p.IsListed != nil && !*p.IsListed {
			continue
		}
		out = append(out, Job{ID: "ashby:" + co.Slug + ":" + p.ID, Company: co.Name,
			Category: co.Category, Title: p.Title, Location: p.Location, URL: p.JobURL,
			Source: "ashby", Posted: isoDate(p.PublishedAt)})
	}
	return out, nil
}

// -------------------------------------------------------- SmartRecruiters

const srPage = 100

func fetchSmartRecruiters(ctx context.Context, c *Client, co Company, maxPages int) ([]Job, error) {
	seen := map[string]Job{}
	var errs []error
	for _, term := range searchTerms {
		for page := 0; page < maxPages; page++ {
			var r struct {
				TotalFound int `json:"totalFound"`
				Content    []struct {
					ID           string `json:"id"`
					Name         string `json:"name"`
					ReleasedDate string `json:"releasedDate"`
					Location     struct {
						City    string `json:"city"`
						Region  string `json:"region"`
						Country string `json:"country"`
						Remote  bool   `json:"remote"`
					} `json:"location"`
				} `json:"content"`
			}
			q := url.Values{"q": {term}, "limit": {strconv.Itoa(srPage)},
				"offset": {strconv.Itoa(page * srPage)}}
			u := "https://api.smartrecruiters.com/v1/companies/" + url.PathEscape(co.Slug) +
				"/postings?" + q.Encode()
			if err := c.GetJSON(ctx, u, &r); err != nil {
				errs = append(errs, err)
				break
			}
			for _, p := range r.Content {
				id := "smartrecruiters:" + co.Slug + ":" + p.ID
				loc := joinNonEmpty(", ", p.Location.City, p.Location.Region,
					strings.ToUpper(p.Location.Country))
				if p.Location.Remote {
					loc = joinNonEmpty(" ", loc, "(Remote)")
				}
				seen[id] = Job{ID: id, Company: co.Name, Category: co.Category,
					Title: p.Name, Location: loc, Source: "smartrecruiters",
					URL:    "https://jobs.smartrecruiters.com/" + co.Slug + "/" + p.ID,
					Posted: isoDate(p.ReleasedDate)}
			}
			if len(r.Content) < srPage || (page+1)*srPage >= r.TotalFound {
				break
			}
		}
		if len(seen) == 0 && len(errs) > 0 {
			return nil, errs[0]
		}
	}
	return values(seen), nil
}

// ----------------------------------------------------------------- Oracle

const oraclePage = 25

func fetchOracle(ctx context.Context, c *Client, co Company, maxPages int) ([]Job, error) {
	if co.Host == "" || co.Site == "" {
		return nil, errors.New("oracle board needs host and site")
	}
	api := "https://" + co.Host + "/hcmRestApi/resources/latest/recruitingCEJobRequisitions"
	base := "https://" + co.Host + "/hcmUI/CandidateExperience/en/sites/" + co.Site + "/job/"
	seen := map[string]Job{}
	var errs []error
	for _, term := range searchTerms {
		for page := 0; page < maxPages; page++ {
			offset := page * oraclePage
			var r struct {
				Items []struct {
					TotalJobsCount  int `json:"TotalJobsCount"`
					RequisitionList []struct {
						ID                 string `json:"Id"`
						Title              string `json:"Title"`
						PrimaryLocation    string `json:"PrimaryLocation"`
						PostedDate         string `json:"PostedDate"`
						SecondaryLocations []struct {
							Name string `json:"Name"`
						} `json:"secondaryLocations"`
					} `json:"requisitionList"`
				} `json:"items"`
			}
			q := url.Values{
				"onlyData": {"true"},
				"expand":   {"requisitionList.secondaryLocations"},
				"finder": {fmt.Sprintf("findReqs;siteNumber=%s,keyword=%s,limit=%d,offset=%d,sortBy=POSTING_DATES_DESC",
					co.Site, term, oraclePage, offset)},
			}
			if err := c.GetJSON(ctx, api+"?"+q.Encode(), &r); err != nil {
				errs = append(errs, err)
				break
			}
			if len(r.Items) == 0 {
				break
			}
			reqs := r.Items[0].RequisitionList
			for _, p := range reqs {
				id := "oracle:" + co.Host + ":" + p.ID
				locs := []string{p.PrimaryLocation}
				for _, s := range p.SecondaryLocations {
					if s.Name != "" && s.Name != p.PrimaryLocation {
						locs = append(locs, s.Name)
					}
				}
				seen[id] = Job{ID: id, Company: co.Name, Category: co.Category,
					Title: p.Title, Location: joinNonEmpty("; ", locs...), URL: base + p.ID,
					Source: "oracle", Posted: isoDate(p.PostedDate)}
			}
			total := r.Items[0].TotalJobsCount
			if len(reqs) < oraclePage || (total > 0 && offset+len(reqs) >= total) {
				break
			}
		}
		if len(seen) == 0 && len(errs) > 0 {
			return nil, errs[0]
		}
	}
	return values(seen), nil
}

// ---------------------------------------------------------------- helpers

func values(m map[string]Job) []Job {
	out := make([]Job, 0, len(m))
	for _, j := range m {
		out = append(out, j)
	}
	return out
}

func isoDate(s string) string {
	if len(s) >= 10 && s[4] == '-' && s[7] == '-' {
		return s[:10]
	}
	return ""
}

func joinNonEmpty(sep string, parts ...string) string {
	var keep []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			keep = append(keep, p)
		}
	}
	return strings.Join(keep, sep)
}
