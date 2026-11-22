package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
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
	"workable":        fetchWorkable,
	"breezy":          fetchBreezy,
	"rippling":        fetchRippling,
	"bamboohr":        fetchBambooHR,
	"adp":             fetchADP,
	"eightfold":       fetchEightfold,
	"wordpress":       fetchWordPress,
	"successfactors":  fetchSuccessFactors,
	"cornerstone":     fetchCornerstone,
	"tesla":           fetchTesla,
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
	var mu sync.Mutex

	search := func(term string) error {
		total := -1
		for page := 0; page < maxPages; page++ {
			offset := page * workdayPage
			body := map[string]any{"appliedFacets": map[string]any{}, "limit": workdayPage,
				"offset": offset, "searchText": term}
			var r workdayResp
			if err := c.PostJSON(ctx, api, body, &r); err != nil {
				return err
			}
			// Workday sometimes reports total=0 on later pages; keep the first real one.
			if total < 0 && r.Total > 0 {
				total = r.Total
			}
			mu.Lock()
			for _, p := range r.JobPostings {
				id := "workday:" + co.Slug + ":" + p.ExternalPath
				if _, ok := seen[id]; ok || p.ExternalPath == "" {
					continue
				}
				seen[id] = Job{ID: id, Company: co.Name, Category: co.Category,
					Title: p.Title, Location: p.LocationsText, URL: base + p.ExternalPath,
					Source: "workday", Posted: workdayPosted(p.PostedOn, now), Posted30: workday30Plus(p.PostedOn)}
			}
			mu.Unlock()
			if len(r.JobPostings) < workdayPage || (total > 0 && offset+len(r.JobPostings) >= total) {
				break
			}
		}
		return nil
	}

	if err := runSearchTerms(search, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(seen) == 0
	}); err != nil {
		return nil, err
	}
	mu.Lock()
	defer mu.Unlock()
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

// --------------------------------------------------------------- Workable

func fetchWorkable(ctx context.Context, c *Client, co Company, _ int) ([]Job, error) {
	var r struct {
		Jobs []struct {
			Title       string `json:"title"`
			Shortcode   string `json:"shortcode"`
			URL         string `json:"url"`
			PublishedOn string `json:"published_on"`
			City        string `json:"city"`
			State       string `json:"state"`
			Country     string `json:"country"`
		} `json:"jobs"`
	}
	u := "https://apply.workable.com/api/v1/widget/accounts/" + url.PathEscape(co.Slug)
	if err := c.GetJSON(ctx, u, &r); err != nil {
		return nil, err
	}
	out := make([]Job, 0, len(r.Jobs))
	for _, p := range r.Jobs {
		if p.Shortcode == "" || p.Title == "" {
			continue
		}
		out = append(out, Job{ID: "workable:" + co.Slug + ":" + p.Shortcode,
			Company: co.Name, Category: co.Category, Title: p.Title,
			Location: joinNonEmpty(", ", p.City, p.State, p.Country),
			URL:      p.URL, Source: "workable", Posted: isoDate(p.PublishedOn)})
	}
	return out, nil
}

// ------------------------------------------------------------------ Breezy

func fetchBreezy(ctx context.Context, c *Client, co Company, _ int) ([]Job, error) {
	var r []struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		URL           string `json:"url"`
		PublishedDate string `json:"published_date"`
		Location      struct {
			City  string `json:"city"`
			State struct {
				Name string `json:"name"`
			} `json:"state"`
			Country struct {
				Name string `json:"name"`
			} `json:"country"`
		} `json:"location"`
	}
	u := "https://" + url.PathEscape(co.Slug) + ".breezy.hr/json"
	if err := c.GetJSON(ctx, u, &r); err != nil {
		return nil, err
	}
	out := make([]Job, 0, len(r))
	for _, p := range r {
		if p.ID == "" || p.Name == "" {
			continue
		}
		out = append(out, Job{ID: "breezy:" + co.Slug + ":" + p.ID,
			Company: co.Name, Category: co.Category, Title: p.Name,
			Location: joinNonEmpty(", ", p.Location.City, p.Location.State.Name, p.Location.Country.Name),
			URL:      p.URL, Source: "breezy", Posted: isoDate(p.PublishedDate)})
	}
	return out, nil
}

// ---------------------------------------------------------------- Rippling
//
// Public board: https://ats.rippling.com/{slug}/jobs
// JSON:         https://ats.rippling.com/api/v2/board/{slug}/jobs

const ripplingPage = 1000

func fetchRippling(ctx context.Context, c *Client, co Company, _ int) ([]Job, error) {
	if co.Slug == "" {
		return nil, errors.New("rippling board needs slug")
	}
	seen := map[string]Job{}
	for page := 0; ; page++ {
		u := fmt.Sprintf("https://ats.rippling.com/api/v2/board/%s/jobs?page=%d&pageSize=%d",
			url.PathEscape(co.Slug), page, ripplingPage)
		var r struct {
			TotalPages int `json:"totalPages"`
			Items      []struct {
				ID        string `json:"id"`
				Name      string `json:"name"`
				URL       string `json:"url"`
				Locations []struct {
					Name    string `json:"name"`
					Country string `json:"country"`
					State   string `json:"state"`
					City    string `json:"city"`
				} `json:"locations"`
			} `json:"items"`
		}
		if err := c.GetJSON(ctx, u, &r); err != nil {
			return nil, err
		}
		for _, p := range r.Items {
			if p.ID == "" || p.Name == "" {
				continue
			}
			if _, ok := seen[p.ID]; ok {
				continue
			}
			jobURL := p.URL
			if jobURL == "" {
				jobURL = "https://ats.rippling.com/" + co.Slug + "/jobs/" + p.ID
			}
			var places []string
			for _, l := range p.Locations {
				if place := joinNonEmpty(", ", l.City, l.State, l.Country); place != "" {
					places = append(places, place)
				} else if name := strings.TrimSpace(l.Name); name != "" {
					places = append(places, name)
				}
			}
			seen[p.ID] = Job{ID: "rippling:" + co.Slug + ":" + p.ID,
				Company: co.Name, Category: co.Category, Title: p.Name,
				Location: strings.Join(places, "; "), URL: jobURL, Source: "rippling"}
		}
		if len(r.Items) == 0 || page+1 >= r.TotalPages {
			break
		}
	}
	return values(seen), nil
}

// ---------------------------------------------------------------- BambooHR
//
// JSON: https://{slug}.bamboohr.com/careers/list

func fetchBambooHR(ctx context.Context, c *Client, co Company, _ int) ([]Job, error) {
	if co.Slug == "" {
		return nil, errors.New("bamboohr board needs slug")
	}
	var r struct {
		Result []struct {
			ID             string `json:"id"`
			JobOpeningName string `json:"jobOpeningName"`
			Location       struct {
				City  string `json:"city"`
				State string `json:"state"`
			} `json:"location"`
			ATSLocation struct {
				Country string `json:"country"`
			} `json:"atsLocation"`
		} `json:"result"`
	}
	u := "https://" + url.PathEscape(co.Slug) + ".bamboohr.com/careers/list"
	if err := c.GetJSON(ctx, u, &r); err != nil {
		return nil, err
	}
	out := make([]Job, 0, len(r.Result))
	for _, p := range r.Result {
		if p.ID == "" || p.JobOpeningName == "" {
			continue
		}
		out = append(out, Job{ID: "bamboohr:" + co.Slug + ":" + p.ID,
			Company: co.Name, Category: co.Category, Title: p.JobOpeningName,
			Location: joinNonEmpty(", ", p.Location.City, p.Location.State, p.ATSLocation.Country),
			URL:      "https://" + co.Slug + ".bamboohr.com/careers/" + p.ID,
			Source:   "bamboohr"})
	}
	return out, nil
}

// -------------------------------------------------------------------- ADP
//
// cid is the career-center id from workforcenow.adp.com/...&cid=

const adpPage = 100

func fetchADP(ctx context.Context, c *Client, co Company, _ int) ([]Job, error) {
	if co.Slug == "" {
		return nil, errors.New("adp board needs slug (cid)")
	}
	seen := map[string]Job{}
	for skip := 0; ; {
		q := url.Values{}
		q.Set("cid", co.Slug)
		q.Set("$top", strconv.Itoa(adpPage))
		q.Set("$skip", strconv.Itoa(skip))
		u := "https://workforcenow.adp.com/mascsr/default/careercenter/public/events/staffing/v1/job-requisitions?" + q.Encode()
		var r struct {
			Meta struct {
				TotalNumber int `json:"totalNumber"`
			} `json:"meta"`
			JobRequisitions []struct {
				ItemID               string `json:"itemID"`
				RequisitionTitle     string `json:"requisitionTitle"`
				PostDate             string `json:"postDate"`
				RequisitionLocations []struct {
					NameCode struct {
						ShortName string `json:"shortName"`
					} `json:"nameCode"`
					Address struct {
						CityName                 string `json:"cityName"`
						CountrySubdivisionLevel1 struct {
							CodeValue string `json:"codeValue"`
						} `json:"countrySubdivisionLevel1"`
					} `json:"address"`
				} `json:"requisitionLocations"`
			} `json:"jobRequisitions"`
		}
		if err := c.GetJSON(ctx, u, &r); err != nil {
			return nil, err
		}
		for _, p := range r.JobRequisitions {
			if p.ItemID == "" || p.RequisitionTitle == "" {
				continue
			}
			var places []string
			for _, l := range p.RequisitionLocations {
				name := strings.TrimSpace(l.NameCode.ShortName)
				if name == "" {
					name = joinNonEmpty(", ", l.Address.CityName, l.Address.CountrySubdivisionLevel1.CodeValue)
				}
				if name != "" {
					places = append(places, name)
				}
			}
			jobURL := "https://workforcenow.adp.com/mascsr/default/mdf/recruitment/recruitment.html?" + url.Values{
				"cid":   {co.Slug},
				"jobId": {p.ItemID},
			}.Encode()
			seen[p.ItemID] = Job{ID: "adp:" + co.Slug + ":" + p.ItemID,
				Company: co.Name, Category: co.Category, Title: p.RequisitionTitle,
				Location: strings.Join(places, "; "), URL: jobURL, Source: "adp",
				Posted: isoDate(p.PostDate)}
		}
		skip += len(r.JobRequisitions)
		if len(r.JobRequisitions) == 0 || len(r.JobRequisitions) < adpPage || (r.Meta.TotalNumber > 0 && skip >= r.Meta.TotalNumber) {
			break
		}
	}
	return values(seen), nil
}

// -------------------------------------------------------------- Eightfold
//
// Host is the career site (jobs.infineon.com). Slug is the domain query
// (infineon.com). The public search returns 10 postings per request.

const eightfoldPage = 10

func fetchEightfold(ctx context.Context, c *Client, co Company, _ int) ([]Job, error) {
	if co.Host == "" || co.Slug == "" {
		return nil, errors.New("eightfold board needs host and slug (domain)")
	}
	seen := map[string]Job{}
	for start := 0; ; {
		q := url.Values{}
		q.Set("domain", co.Slug)
		q.Set("query", "")
		q.Set("start", strconv.Itoa(start))
		q.Set("num", strconv.Itoa(eightfoldPage))
		u := "https://" + co.Host + "/api/pcsx/search?" + q.Encode()
		var r struct {
			Data struct {
				Count     int `json:"count"`
				Positions []struct {
					ID          int64    `json:"id"`
					Name        string   `json:"name"`
					Locations   []string `json:"locations"`
					PostedTs    int64    `json:"postedTs"`
					PositionURL string   `json:"positionUrl"`
				} `json:"positions"`
			} `json:"data"`
		}
		if err := c.GetJSON(ctx, u, &r); err != nil {
			return nil, err
		}
		for _, p := range r.Data.Positions {
			if p.ID == 0 || p.Name == "" {
				continue
			}
			id := strconv.FormatInt(p.ID, 10)
			jobURL := p.PositionURL
			if jobURL == "" {
				jobURL = "/careers/job/" + id
			}
			if strings.HasPrefix(jobURL, "/") {
				jobURL = "https://" + co.Host + jobURL
			}
			seen[id] = Job{ID: "eightfold:" + co.Host + ":" + id,
				Company: co.Name, Category: co.Category, Title: p.Name,
				Location: strings.Join(p.Locations, ", "), URL: jobURL, Source: "eightfold",
				Posted: unixDate(p.PostedTs)}
		}
		start += len(r.Data.Positions)
		if len(r.Data.Positions) == 0 || len(r.Data.Positions) < eightfoldPage || (r.Data.Count > 0 && start >= r.Data.Count) {
			break
		}
	}
	return values(seen), nil
}

// ------------------------------------------------------------- WordPress
//
// Host is the site (aem.com.sg). Slug is the REST base of the job post type
// (awsm_job_openings, jobpost). Location comes from an embedded taxonomy
// whose name contains "location", or from a job-location-* CSS class.

const wpPage = 100

var htmlTag = regexp.MustCompile(`<[^>]*>`)

func fetchWordPress(ctx context.Context, c *Client, co Company, _ int) ([]Job, error) {
	if co.Host == "" || co.Slug == "" {
		return nil, errors.New("wordpress board needs host and slug (rest base)")
	}
	var out []Job
	for page := 1; ; page++ {
		q := url.Values{}
		q.Set("per_page", strconv.Itoa(wpPage))
		q.Set("page", strconv.Itoa(page))
		q.Set("_embed", "wp:term")
		u := "https://" + co.Host + "/wp-json/wp/v2/" + url.PathEscape(co.Slug) + "?" + q.Encode()
		data, err := c.do(ctx, http.MethodGet, u, nil)
		if err != nil {
			var he *httpError
			if page > 1 && errors.As(err, &he) && he.Status == http.StatusBadRequest {
				break
			}
			return nil, err
		}
		var posts []struct {
			ID     int64  `json:"id"`
			Date   string `json:"date"`
			Status string `json:"status"`
			Link   string `json:"link"`
			Title  struct {
				Rendered string `json:"rendered"`
			} `json:"title"`
			ClassList []string `json:"class_list"`
			Embedded  struct {
				Terms [][]struct {
					Taxonomy string `json:"taxonomy"`
					Name     string `json:"name"`
				} `json:"wp:term"`
			} `json:"_embedded"`
		}
		if err := json.Unmarshal(data, &posts); err != nil {
			return nil, fmt.Errorf("decode %s: %w", u, err)
		}
		if len(posts) == 0 {
			break
		}
		for _, p := range posts {
			title := plainTitle(p.Title.Rendered)
			if p.ID == 0 || title == "" || (p.Status != "" && p.Status != "publish") {
				continue
			}
			out = append(out, Job{ID: "wordpress:" + co.Host + ":" + strconv.FormatInt(p.ID, 10),
				Company: co.Name, Category: co.Category, Title: title,
				Location: wpLocation(p.ClassList, p.Embedded.Terms), URL: p.Link,
				Source: "wordpress", Posted: isoDate(p.Date)})
		}
		if len(posts) < wpPage {
			break
		}
	}
	return out, nil
}

func wpLocation(classList []string, terms [][]struct {
	Taxonomy string `json:"taxonomy"`
	Name     string `json:"name"`
}) string {
	var fromTerms []string
	for _, group := range terms {
		for _, t := range group {
			if strings.Contains(t.Taxonomy, "location") && strings.TrimSpace(t.Name) != "" {
				fromTerms = append(fromTerms, strings.TrimSpace(t.Name))
			}
		}
	}
	if len(fromTerms) > 0 {
		return strings.Join(fromTerms, "; ")
	}
	var fromClass []string
	for _, class := range classList {
		for _, prefix := range []string{"job-location-", "jobpost_location-"} {
			if strings.HasPrefix(class, prefix) {
				fromClass = append(fromClass, strings.ReplaceAll(class[len(prefix):], "-", " "))
			}
		}
	}
	return strings.Join(fromClass, "; ")
}

// --------------------------------------------------------- SuccessFactors
//
// Host is the career site (careers.skyworksinc.com). Its RSS feed ignores
// keywords but returns the whole catalogue, newest first, up to rows items.
// Titles end with the location: "Title (City, ST, CC, zip)".

const successFactorsRows = 5000

// The RSS feed ignores paging and returns every posting with full
// descriptions; large employers (Cargill, ~13 MB) take over a minute.
const successFactorsTimeout = 4 * time.Minute

var (
	sfTrailingLoc     = regexp.MustCompile(`^(.*\S)\s*\(([^()]*,[^()]*)\)\s*$`)
	sfTrailingCountry = regexp.MustCompile(`^(.*\S)\s*\(([A-Z]{2})\)\s*$`)
	sfJobID           = regexp.MustCompile(`/(\d+)/?$`)
	sfCountryCode     = regexp.MustCompile(`^([A-Z]{2})\b\s*(.*)$`)
)

// countryNames covers the codes SuccessFactors feeds use most, so foreign
// postings reach the location filter as a country it recognises. A
// non-US region code like "PR" (Paraná) is dropped to avoid reading it as a
// US state.
var countryNames = map[string]string{
	"AE": "United Arab Emirates", "AR": "Argentina", "AT": "Austria", "AU": "Australia",
	"BE": "Belgium", "BG": "Bulgaria", "BR": "Brazil", "CA": "Canada", "CH": "Switzerland",
	"CL": "Chile", "CN": "China", "CO": "Colombia", "CR": "Costa Rica", "CZ": "Czechia",
	"DE": "Germany", "DK": "Denmark", "EG": "Egypt", "ES": "Spain", "FI": "Finland",
	"FR": "France", "GB": "United Kingdom", "GR": "Greece", "HK": "Hong Kong", "HU": "Hungary",
	"ID": "Indonesia", "IE": "Ireland", "IL": "Israel", "IN": "India", "IT": "Italy",
	"JP": "Japan", "KE": "Kenya", "KR": "Korea", "KW": "Kuwait", "LU": "Luxembourg",
	"MX": "Mexico", "MY": "Malaysia", "NG": "Nigeria", "NL": "Netherlands", "NO": "Norway",
	"NZ": "New Zealand", "OM": "Oman", "PE": "Peru", "PH": "Philippines", "PL": "Poland",
	"PT": "Portugal", "QA": "Qatar", "RO": "Romania", "SA": "Saudi Arabia", "SE": "Sweden",
	"SG": "Singapore", "SK": "Slovakia", "TH": "Thailand", "TR": "Turkey", "TW": "Taiwan",
	"UK": "United Kingdom", "VN": "Vietnam", "ZA": "South Africa",
}

func fetchSuccessFactors(ctx context.Context, c *Client, co Company, _ int) ([]Job, error) {
	if co.Host == "" {
		return nil, errors.New("successfactors board needs host")
	}
	u := fmt.Sprintf("https://%s/services/rss/job/?locale=en_US&rows=%d", co.Host, successFactorsRows)
	slow := *c
	if c.HTTP.Timeout < successFactorsTimeout {
		hc := *c.HTTP
		hc.Timeout = successFactorsTimeout
		slow.HTTP = &hc
	}
	data, err := slow.do(ctx, http.MethodGet, u, nil, http.Header{"Accept": {"application/rss+xml, application/xml;q=0.9, */*;q=0.8"}})
	if err != nil {
		return nil, err
	}
	var feed struct {
		Items []struct {
			Title   string `xml:"title"`
			Link    string `xml:"link"`
			PubDate string `xml:"pubDate"`
		} `xml:"channel>item"`
	}
	if err := xml.Unmarshal(data, &feed); err != nil {
		return nil, fmt.Errorf("decode %s: %w", u, err)
	}
	seen := map[string]Job{}
	for _, it := range feed.Items {
		link := strings.TrimSpace(it.Link)
		if i := strings.IndexByte(link, '?'); i >= 0 {
			link = link[:i]
		}
		m := sfJobID.FindStringSubmatch(link)
		title := strings.TrimSpace(it.Title)
		if m == nil || title == "" {
			continue
		}
		loc := ""
		if t := sfTrailingLoc.FindStringSubmatch(title); t != nil {
			title, loc = t[1], sfLocation(t[2])
		} else if t := sfTrailingCountry.FindStringSubmatch(title); t != nil && countryName(t[2]) != t[2] {
			title, loc = t[1], countryName(t[2])
		}
		seen[m[1]] = Job{ID: "successfactors:" + co.Host + ":" + m[1],
			Company: co.Name, Category: co.Category, Title: title, Location: loc,
			URL: link, Source: "successfactors", Posted: rssDate(it.PubDate)}
	}
	return values(seen), nil
}

// sfLocation turns "San Jose, CA, US, 95134" into "San Jose, CA, United States"
// and "Curitiba, PR, BR, 80420-000" into "Curitiba, Brazil".
func sfLocation(raw string) string {
	var parts []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	if n := len(parts); n > 0 && strings.ContainsAny(parts[n-1], "0123456789") {
		parts = parts[:n-1]
	}
	if len(parts) < 2 {
		return strings.Join(parts, ", ")
	}
	last := parts[len(parts)-1]
	m := sfCountryCode.FindStringSubmatch(last)
	if m == nil {
		return strings.Join(parts, ", ")
	}
	code, name := m[1], strings.TrimSpace(m[2])
	if code == "US" {
		return strings.Join(append(parts[:len(parts)-1:len(parts)-1], "United States"), ", ")
	}
	if name == "" {
		name = countryNames[code]
	}
	if name == "" {
		return strings.Join(parts, ", ")
	}
	return parts[0] + ", " + name
}

// ------------------------------------------------------------ Cornerstone
//
// Slug is the portal (linde in linde.csod.com), Site the career-site id
// (23 in /ux/ats/careersite/23/home). The page embeds a short-lived bearer
// token and the regional API host that serves the job search.

const cornerstonePage = 500

var (
	csodToken = regexp.MustCompile(`"token"\s*:\s*"([^"]+)"`)
	csodCloud = regexp.MustCompile(`"cloud"\s*:\s*"(https://[^"]+)"`)
)

func fetchCornerstone(ctx context.Context, c *Client, co Company, _ int) ([]Job, error) {
	siteID, err := strconv.Atoi(co.Site)
	if co.Slug == "" || err != nil {
		return nil, errors.New("cornerstone board needs slug (portal) and numeric site")
	}
	base := fmt.Sprintf("https://%s.csod.com/ux/ats/careersite/%d/home", url.PathEscape(co.Slug), siteID)
	page, err := c.do(ctx, http.MethodGet, base+"?c="+url.QueryEscape(co.Slug), nil, http.Header{"Accept": {"text/html"}})
	if err != nil {
		return nil, err
	}
	tok, cloud := csodToken.FindSubmatch(page), csodCloud.FindSubmatch(page)
	if tok == nil || cloud == nil {
		return nil, fmt.Errorf("cornerstone %s: no token or api host on career page", co.Slug)
	}
	api := strings.TrimSuffix(string(cloud[1]), "/") + "/rec-job-search/external/jobs"
	auth := http.Header{"Authorization": {"Bearer " + string(tok[1])}}

	seen := map[string]Job{}
	for pageNum := 1; ; pageNum++ {
		body, _ := json.Marshal(map[string]any{
			"careerSiteId": siteID, "careerSitePageId": siteID, "pageNumber": pageNum,
			"pageSize": cornerstonePage, "cultureId": 1, "cultureName": "en-US", "searchText": "",
			"states": []string{}, "countryCodes": []string{}, "cities": []string{}, "placeID": "",
			"customFieldCheckboxKeys": []string{}, "customFieldDropdowns": []string{}, "customFieldRadios": []string{},
		})
		data, err := c.do(ctx, http.MethodPost, api, body, auth)
		if err != nil {
			return nil, err
		}
		var r struct {
			Data struct {
				TotalCount   int `json:"totalCount"`
				Requisitions []struct {
					RequisitionID        int64  `json:"requisitionId"`
					PostingEffectiveDate string `json:"postingEffectiveDate"`
					DisplayJobTitle      string `json:"displayJobTitle"`
					Locations            []struct {
						City    string `json:"city"`
						State   string `json:"state"`
						Country string `json:"country"`
					} `json:"locations"`
				} `json:"requisitions"`
			} `json:"data"`
		}
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, fmt.Errorf("decode %s: %w", api, err)
		}
		for _, p := range r.Data.Requisitions {
			if p.RequisitionID == 0 || p.DisplayJobTitle == "" {
				continue
			}
			id := strconv.FormatInt(p.RequisitionID, 10)
			var places []string
			for _, l := range p.Locations {
				if place := joinNonEmpty(", ", l.City, l.State, countryName(l.Country)); place != "" {
					places = append(places, place)
				}
			}
			seen[id] = Job{ID: "cornerstone:" + co.Slug + ":" + id,
				Company: co.Name, Category: co.Category, Title: strings.TrimSpace(p.DisplayJobTitle),
				Location: strings.Join(places, "; "),
				URL:      base + "/requisition/" + id + "?c=" + url.QueryEscape(co.Slug),
				Source:   "cornerstone", Posted: usDate(p.PostingEffectiveDate)}
		}
		if len(r.Data.Requisitions) == 0 || len(seen) >= r.Data.TotalCount {
			break
		}
	}
	return values(seen), nil
}

func countryName(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "US" || code == "USA" {
		return "United States"
	}
	if name, ok := countryNames[code]; ok {
		return name
	}
	return code
}

func usDate(s string) string {
	if t, err := time.Parse("1/2/2006", strings.TrimSpace(s)); err == nil {
		return t.Format("2006-01-02")
	}
	return ""
}

func rssDate(s string) string {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC1123, "Mon, 2 Jan 2006 15:04:05 MST", time.RFC1123Z} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC().Format("2006-01-02")
		}
	}
	return ""
}

func plainTitle(s string) string {
	return strings.TrimSpace(htmlTag.ReplaceAllString(html.UnescapeString(s), ""))
}

func unixDate(sec int64) string {
	if sec <= 0 {
		return ""
	}
	return time.Unix(sec, 0).UTC().Format("2006-01-02")
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
	var mu sync.Mutex

	search := func(term string) error {
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
				return err
			}
			mu.Lock()
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
			mu.Unlock()
			if len(r.Content) < srPage || (page+1)*srPage >= r.TotalFound {
				break
			}
		}
		return nil
	}

	if err := runSearchTerms(search, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(seen) == 0
	}); err != nil {
		return nil, err
	}
	mu.Lock()
	defer mu.Unlock()
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
	var mu sync.Mutex

	search := func(term string) error {
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
				return err
			}
			if len(r.Items) == 0 {
				return nil
			}
			reqs := r.Items[0].RequisitionList
			mu.Lock()
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
			mu.Unlock()
			total := r.Items[0].TotalJobsCount
			if len(reqs) < oraclePage || (total > 0 && offset+len(reqs) >= total) {
				break
			}
		}
		return nil
	}

	if err := runSearchTerms(search, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(seen) == 0
	}); err != nil {
		return nil, err
	}
	mu.Lock()
	defer mu.Unlock()
	return values(seen), nil
}

// ---------------------------------------------------------------- helpers

// runSearchTerms probes searchTerms[0] first so a dead tenant fails fast,
// then runs the rest concurrently. search may be called from multiple
// goroutines; the caller owns synchronization of any shared state.
// empty reports whether any jobs have been recorded yet.
func runSearchTerms(search func(term string) error, empty func() bool) error {
	var mu sync.Mutex
	var errs []error
	if err := search(searchTerms[0]); err != nil {
		if empty() {
			return err
		}
		errs = append(errs, err)
	}
	var wg sync.WaitGroup
	for _, term := range searchTerms[1:] {
		term := term
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := search(term); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if empty() && len(errs) > 0 {
		return errs[0]
	}
	return nil
}

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
