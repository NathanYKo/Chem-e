package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// redirect sends every request (whatever its real host) to the test server,
// keeping the original host in X-Orig-Host so handlers can assert on it.
type redirect struct{ target *url.URL }

func (r redirect) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("X-Orig-Host", req.URL.Host)
	req.URL.Scheme, req.URL.Host = r.target.Scheme, r.target.Host
	return http.DefaultTransport.RoundTrip(req)
}

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	c := NewClient(4, 5*time.Second)
	c.HTTP.Transport = redirect{u}
	c.Backoff = time.Millisecond
	return c
}

func TestWorkdayPaginatesAndDedupes(t *testing.T) {
	calls := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.Header.Get("X-Orig-Host") != "dow.wd1.myworkdayjobs.com" ||
			r.URL.Path != "/wday/cxs/dow/ExternalCareers/jobs" {
			t.Errorf("unexpected request %s %s%s", r.Method, r.Header.Get("X-Orig-Host"), r.URL.Path)
		}
		var body struct {
			Offset     int    `json:"offset"`
			SearchText string `json:"searchText"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		type p = map[string]string
		var posts []p
		if body.SearchText == "intern" {
			n := 20
			if body.Offset == 20 {
				n = 3 // second, short page ends pagination
			}
			for i := 0; i < n; i++ {
				posts = append(posts, p{"title": fmt.Sprintf("Process Engineering Intern %d", body.Offset+i),
					"externalPath":  fmt.Sprintf("/job/Freeport/X_R%d", body.Offset+i),
					"locationsText": "Freeport, Texas", "postedOn": "Posted 3 Days Ago"})
			}
		} else {
			// Overlapping hit from another term must not duplicate.
			posts = append(posts, p{"title": "Process Engineering Intern 0", "externalPath": "/job/Freeport/X_R0",
				"locationsText": "Freeport, Texas", "postedOn": "Posted Today"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"total": 23, "jobPostings": posts})
	})
	co := Company{Name: "Dow", ATS: "workday", Slug: "dow", WD: "wd1", Site: "ExternalCareers"}
	jobs, err := fetchWorkday(context.Background(), c, co, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 23 {
		t.Fatalf("want 23 unique jobs, got %d", len(jobs))
	}
	if want := 2 + len(searchTerms) - 1; calls != want {
		t.Errorf("want %d requests, got %d", want, calls)
	}
	for _, j := range jobs {
		if !strings.HasPrefix(j.URL, "https://dow.wd1.myworkdayjobs.com/ExternalCareers/job/") {
			t.Errorf("bad url %s", j.URL)
		}
		if j.Posted == "" {
			t.Errorf("relative date not resolved for %s", j.Title)
		}
	}
}

func TestWorkdayBadTenantFailsFast(t *testing.T) {
	calls := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusNotFound)
	})
	_, err := fetchWorkday(context.Background(), c, Company{Slug: "x", WD: "wd1", Site: "y"}, 5)
	if err == nil || calls != 1 {
		t.Fatalf("want one failed request and an error, got calls=%d err=%v", calls, err)
	}
}

func TestGreenhouseLeverAshby(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("X-Orig-Host") {
		case "boards-api.greenhouse.io":
			io.WriteString(w, `{"jobs":[{"id":7,"title":"Process Engineering Intern","absolute_url":"https://gh/7","first_published":"2026-11-01T10:00:00-05:00","location":{"name":"Reno, NV"}}]}`)
		case "api.lever.co":
			io.WriteString(w, `[{"id":"ab","text":"Chemical Engineer I","hostedUrl":"https://lever/ab","createdAt":1762000000000,"categories":{"location":"Devens, MA"}}]`)
		case "api.ashbyhq.com":
			io.WriteString(w, `{"jobs":[{"id":"z","title":"Process Intern","location":"Remote","jobUrl":"https://ashby/z","publishedAt":"2026-10-02T00:00:00Z"},{"id":"h","title":"Hidden","isListed":false}]}`)
		default:
			http.NotFound(w, r)
		}
	})
	ctx := context.Background()
	gh, err := fetchGreenhouse(ctx, c, Company{Name: "Redwood", Slug: "redwoodmaterials"}, 1)
	if err != nil || len(gh) != 1 || gh[0].Posted != "2026-11-01" || gh[0].Location != "Reno, NV" {
		t.Errorf("greenhouse: %+v %v", gh, err)
	}
	lv, err := fetchLever(ctx, c, Company{Name: "CFS", Slug: "cfsenergy"}, 1)
	if err != nil || len(lv) != 1 || lv[0].Posted == "" || lv[0].URL != "https://lever/ab" {
		t.Errorf("lever: %+v %v", lv, err)
	}
	ab, err := fetchAshby(ctx, c, Company{Name: "A", Slug: "a"}, 1)
	if err != nil || len(ab) != 1 {
		t.Errorf("ashby: %+v %v", ab, err)
	}
}

func TestTeslaJobURLAndParse(t *testing.T) {
	got := teslaJobURL(" Internship, Chemical Engineer, Cell Engineering (Winter/Spring 2027)", "280192")
	want := "https://www.tesla.com/careers/search/job/-internship-chemical-engineer-cell-engineering-winter-spring-2027-280192"
	if got != want {
		t.Errorf("slug with leading space:\n got %s\nwant %s", got, want)
	}
	got = teslaJobURL("Logistics Industrial Engineer Intern, Cell Engineering (Winter/Spring 2027)", "287545")
	want = "https://www.tesla.com/careers/search/job/logistics-industrial-engineer-intern-cell-engineering-winter-spring-2027-287545"
	if got != want {
		t.Errorf("slug:\n got %s\nwant %s", got, want)
	}

	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Orig-Host") != "www.tesla.com" || r.URL.Path != "/cua-api/apps/careers/state" {
			t.Errorf("request %s %s", r.Header.Get("X-Orig-Host"), r.URL.Path)
		}
		io.WriteString(w, `{"lookup":{"locations":{"9":"Fremont, California"}},"listings":[{"id":"280192","t":" Internship, Chemical Engineer, Cell Engineering (Winter/Spring 2027)","l":"9"},{"id":"","t":"skip"}]}`)
	})
	jobs, err := fetchTesla(context.Background(), c, Company{Name: "Tesla", Category: "Batteries, Climate & Nuclear", ATS: "tesla"}, 1)
	if err != nil || len(jobs) != 1 || jobs[0].ID != "tesla:280192" || jobs[0].Location != "Fremont, California" || jobs[0].Title != "Internship, Chemical Engineer, Cell Engineering (Winter/Spring 2027)" {
		t.Fatalf("parse: %+v %v", jobs, err)
	}
	if jobs[0].URL != "https://www.tesla.com/careers/search/job/-internship-chemical-engineer-cell-engineering-winter-spring-2027-280192" {
		t.Errorf("url %s", jobs[0].URL)
	}
}

func TestWorkableAndBreezy(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("X-Orig-Host") {
		case "apply.workable.com":
			if r.URL.Path != "/api/v1/widget/accounts/amprius" {
				t.Errorf("workable path %s", r.URL.Path)
			}
			io.WriteString(w, `{"jobs":[{"title":"Electrode Process Intern","shortcode":"ABC","url":"https://apply.workable.com/j/ABC","published_on":"2026-11-02","city":"Fremont","state":"California","country":"United States"},{"title":"","shortcode":"skip"}]}`)
		case "forge-nano.breezy.hr":
			if r.URL.Path != "/json" {
				t.Errorf("breezy path %s", r.URL.Path)
			}
			io.WriteString(w, `[{"id":"p1","name":"Process Engineer I","url":"https://forge-nano.breezy.hr/p/p1","published_date":"2026-11-16T17:44:33.094Z","location":{"city":"Thornton","state":{"name":"Colorado"},"country":{"name":"United States"}}}]`)
		default:
			http.NotFound(w, r)
		}
	})
	ctx := context.Background()
	wk, err := fetchWorkable(ctx, c, Company{Name: "Amprius", Slug: "amprius"}, 1)
	if err != nil || len(wk) != 1 || wk[0].Posted != "2026-11-02" || wk[0].Location != "Fremont, California, United States" || wk[0].URL != "https://apply.workable.com/j/ABC" {
		t.Errorf("workable: %+v %v", wk, err)
	}
	bz, err := fetchBreezy(ctx, c, Company{Name: "Forge Nano", Slug: "forge-nano"}, 1)
	if err != nil || len(bz) != 1 || bz[0].Posted != "2026-11-16" || bz[0].Location != "Thornton, Colorado, United States" || bz[0].ID != "breezy:forge-nano:p1" {
		t.Errorf("breezy: %+v %v", bz, err)
	}
}

func TestOracleAndSmartRecruiters(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch r.Header.Get("X-Orig-Host") {
		case "ejhp.fa.us6.oraclecloud.com":
			if !strings.Contains(q.Get("finder"), "siteNumber=CX_2") {
				t.Errorf("finder missing site: %s", q.Get("finder"))
			}
			io.WriteString(w, `{"items":[{"TotalJobsCount":1,"requisitionList":[{"Id":"123","Title":"Coatings Chemical Engineering Intern","PrimaryLocation":"Cleveland, OH, United States","PostedDate":"2026-11-10"}]}]}`)
		case "api.smartrecruiters.com":
			io.WriteString(w, `{"totalFound":1,"content":[{"id":"9","name":"Process Development Co-op","releasedDate":"2026-11-12T00:00:00Z","location":{"city":"North Chicago","region":"IL","country":"us"}}]}`)
		default:
			http.NotFound(w, r)
		}
	})
	ctx := context.Background()
	or, err := fetchOracle(ctx, c, Company{Name: "Sherwin-Williams", Host: "ejhp.fa.us6.oraclecloud.com", Site: "CX_2"}, 3)
	if err != nil || len(or) != 1 || or[0].URL != "https://ejhp.fa.us6.oraclecloud.com/hcmUI/CandidateExperience/en/sites/CX_2/job/123" {
		t.Errorf("oracle: %+v %v", or, err)
	}
	sr, err := fetchSmartRecruiters(ctx, c, Company{Name: "AbbVie", Slug: "AbbVie"}, 3)
	if err != nil || len(sr) != 1 || sr[0].Location != "North Chicago, IL, US" {
		t.Errorf("smartrecruiters: %+v %v", sr, err)
	}
}

func TestPublicBoardFeeds(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch r.Header.Get("X-Orig-Host") {
		case "ats.rippling.com":
			if r.URL.Path != "/api/v2/board/lilac-solutions/jobs" {
				t.Errorf("rippling path %s", r.URL.Path)
			}
			page := q.Get("page")
			if page == "0" {
				io.WriteString(w, `{"totalPages":2,"items":[{"id":"a","name":"Associate Process Engineer","url":"https://ats.rippling.com/lilac-solutions/jobs/a","locations":[{"city":"Berkeley","state":"CA","country":"United States"},{"city":"","state":"","country":"","name":"Remote"}]}]}`)
				return
			}
			io.WriteString(w, `{"totalPages":2,"items":[{"id":"a","name":"Associate Process Engineer","url":"https://ats.rippling.com/lilac-solutions/jobs/a","locations":[{"city":"Berkeley","state":"CA","country":"United States"}]}]}`)
		case "ensurge.bamboohr.com":
			if r.URL.Path != "/careers/list" {
				t.Errorf("bamboo path %s", r.URL.Path)
			}
			io.WriteString(w, `{"meta":{"totalCount":1},"result":[{"id":"85","jobOpeningName":"Thin Film Engineer","location":{"city":"San Jose","state":"California"},"atsLocation":{"country":"United States"}}]}`)
		case "workforcenow.adp.com":
			if q.Get("cid") != "cid-1" || !strings.Contains(r.URL.RawQuery, "top=") {
				t.Errorf("adp query %s", r.URL.RawQuery)
			}
			io.WriteString(w, `{"meta":{"totalNumber":1},"jobRequisitions":[{"itemID":"99_1","requisitionTitle":"Process Engineer","postDate":"2026-11-15T04:56:00","requisitionLocations":[{"nameCode":{"shortName":"US - Milpitas, CA"},"address":{"cityName":"Milpitas"}}]}]}`)
		case "jobs.infineon.com":
			if q.Get("domain") != "infineon.com" || r.URL.Path != "/api/pcsx/search" {
				t.Errorf("eightfold %s %s", r.URL.Path, r.URL.RawQuery)
			}
			start, _ := strconv.Atoi(q.Get("start"))
			const total = 11
			n := 10
			if start+n > total {
				n = total - start
			}
			var positions []string
			for i := 0; i < n; i++ {
				id := start + i + 1
				positions = append(positions, fmt.Sprintf(`{"id":%d,"name":"Intern %d","locations":["San Jose","CA (United States)"],"postedTs":1700000000,"positionUrl":"/careers/job/%d"}`, id, id, id))
			}
			fmt.Fprintf(w, `{"data":{"count":11,"positions":[%s]}}`, strings.Join(positions, ","))
		case "aem.com.sg":
			if !strings.Contains(r.URL.Path, "/wp-json/wp/v2/awsm_job_openings") {
				t.Errorf("wp path %s", r.URL.Path)
			}
			io.WriteString(w, `[{"id":7,"date":"2026-11-13T09:08:54","status":"publish","link":"https://aem.com.sg/job-openings/rd","title":{"rendered":"R&amp;D Intern"},"class_list":["job-location-singapore"]}]`)
		case "full.example":
			if r.URL.Query().Get("page") == "2" {
				w.WriteHeader(http.StatusBadRequest)
				io.WriteString(w, `{"code":"rest_post_invalid_page_number"}`)
				return
			}
			var posts []string
			for i := 1; i <= 100; i++ {
				posts = append(posts, fmt.Sprintf(`{"id":%d,"date":"2026-11-01","status":"publish","link":"https://full.example/j/%d","title":{"rendered":"Engineer %d"},"class_list":["job-location-austin"]}`, i, i, i))
			}
			fmt.Fprintf(w, `[%s]`, strings.Join(posts, ","))
		case "multibeamcorp.com":
			io.WriteString(w, `[{"id":3,"date":"2026-11-12","status":"publish","link":"https://multibeamcorp.com/jobs/servo","title":{"rendered":"Servo Engineer"},"class_list":["jobpost_location-sunnyvale"],"_embedded":{"wp:term":[[{"taxonomy":"jobpost_location","name":"Sunnyvale CA"}]]}}]`)
		default:
			http.NotFound(w, r)
		}
	})
	ctx := context.Background()

	rp, err := fetchRippling(ctx, c, Company{Name: "Lilac Solutions", Slug: "lilac-solutions"}, 1)
	if err != nil || len(rp) != 1 || rp[0].Title != "Associate Process Engineer" || rp[0].Location != "Berkeley, CA, United States; Remote" || rp[0].URL != "https://ats.rippling.com/lilac-solutions/jobs/a" || rp[0].ID != "rippling:lilac-solutions:a" {
		t.Errorf("rippling: %+v %v", rp, err)
	}
	bb, err := fetchBambooHR(ctx, c, Company{Name: "Ensurge", Slug: "ensurge"}, 1)
	if err != nil || len(bb) != 1 || bb[0].Title != "Thin Film Engineer" || bb[0].Location != "San Jose, California, United States" || bb[0].URL != "https://ensurge.bamboohr.com/careers/85" {
		t.Errorf("bamboohr: %+v %v", bb, err)
	}
	ad, err := fetchADP(ctx, c, Company{Name: "Diodes", Slug: "cid-1"}, 1)
	if err != nil || len(ad) != 1 || ad[0].Title != "Process Engineer" || ad[0].Posted != "2026-11-15" || ad[0].Location != "US - Milpitas, CA" || !strings.Contains(ad[0].URL, "jobId=99_1") {
		t.Errorf("adp: %+v %v", ad, err)
	}
	ef, err := fetchEightfold(ctx, c, Company{Name: "Infineon", Host: "jobs.infineon.com", Slug: "infineon.com"}, 1)
	if err != nil || len(ef) != 11 {
		t.Errorf("eightfold: n=%d %v", len(ef), err)
	}
	for _, j := range ef {
		if j.URL == "https://jobs.infineon.com/careers/job/1" && (j.Posted != "2023-11-14" || j.Location != "San Jose, CA (United States)") {
			t.Errorf("eightfold job %+v", j)
		}
	}
	wp, err := fetchWordPress(ctx, c, Company{Name: "AEM", Host: "aem.com.sg", Slug: "awsm_job_openings"}, 1)
	if err != nil || len(wp) != 1 || wp[0].Title != "R&D Intern" || wp[0].Posted != "2026-11-13" || wp[0].Location != "singapore" || wp[0].URL != "https://aem.com.sg/job-openings/rd" {
		t.Errorf("wordpress: %+v %v", wp, err)
	}
	full, err := fetchWordPress(ctx, c, Company{Name: "Full", Host: "full.example", Slug: "jobpost"}, 1)
	if err != nil || len(full) != 100 {
		t.Errorf("wordpress page end: n=%d %v", len(full), err)
	}
	mb, err := fetchWordPress(ctx, c, Company{Name: "Multibeam", Host: "multibeamcorp.com", Slug: "jobpost"}, 1)
	if err != nil || len(mb) != 1 || mb[0].Location != "Sunnyvale CA" {
		t.Errorf("wordpress term: %+v %v", mb, err)
	}
}

func TestSuccessFactorsRSS(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Orig-Host") != "careers.skyworksinc.com" || r.URL.Path != "/services/rss/job/" || r.URL.Query().Get("rows") == "" {
			t.Errorf("request %s %s?%s", r.Header.Get("X-Orig-Host"), r.URL.Path, r.URL.RawQuery)
		}
		if !strings.Contains(r.Header.Get("Accept"), "xml") {
			http.Error(w, "not acceptable", http.StatusNotAcceptable)
			return
		}
		io.WriteString(w, `<?xml version="1.0" encoding="UTF-8" ?><rss version='2.0'><channel>
<item><title><![CDATA[RF Engineer Summer/Fall Co-Op (June-Dec '27) (San Jose, CA, US, 95134)]]></title><pubDate>Sat, 21 Nov 2026 8:00:00 GMT</pubDate><link>https://careers.skyworksinc.com/job/San-Jose-RF/1433516700/?feedId=null&amp;utm_source=J2WRSS</link></item>
<item><title><![CDATA[Intern, Process Engineering (HK)]]></title><link>https://careers.skyworksinc.com/job/HK/1443131901/</link></item>
<item><title><![CDATA[Process Engineer (Curitiba, PR, BR, 80420-000)]]></title><pubDate>Wed, 04 Nov 2026 8:00:00 GMT</pubDate><link>https://careers.skyworksinc.com/job/Curitiba/1443131900/?feedId=</link></item>
<item><title><![CDATA[Staff Engineer - Req 1608 (CA, US, 95110)]]></title><pubDate>Wed, 18 Nov 2026 8:00:00 GMT</pubDate><link>https://careers.skyworksinc.com/job/Staff/1442110800/</link></item>
<item><title><![CDATA[Supervisor de Ventas (Coban, Alta Verapaz, GT Guatemala, 16001)]]></title><link>https://careers.skyworksinc.com/job/Coban/1370343657/</link></item>
</channel></rss>`)
	})
	jobs, err := fetchSuccessFactors(context.Background(), c, Company{Name: "Skyworks", Host: "careers.skyworksinc.com"}, 1)
	if err != nil || len(jobs) != 5 {
		t.Fatalf("got %d jobs, err %v", len(jobs), err)
	}
	byID := map[string]Job{}
	for _, j := range jobs {
		byID[j.ID] = j
	}
	sj := byID["successfactors:careers.skyworksinc.com:1433516700"]
	if sj.Title != "RF Engineer Summer/Fall Co-Op (June-Dec '27)" || sj.Location != "San Jose, CA, United States" ||
		sj.Posted != "2026-11-21" || sj.URL != "https://careers.skyworksinc.com/job/San-Jose-RF/1433516700/" {
		t.Errorf("us job %+v", sj)
	}
	br := byID["successfactors:careers.skyworksinc.com:1443131900"]
	if br.Location != "Curitiba, Brazil" || IsLikelyUS(br.Location) {
		t.Errorf("brazil job location %q", br.Location)
	}
	if qs := byID["successfactors:careers.skyworksinc.com:1442110800"]; qs.Location != "CA, United States" || qs.Title != "Staff Engineer - Req 1608" {
		t.Errorf("state-only job %+v", qs)
	}
	if gt := byID["successfactors:careers.skyworksinc.com:1370343657"]; gt.Location != "Coban, Guatemala" || gt.Posted != "" {
		t.Errorf("named-country job %+v", gt)
	}
	if hk := byID["successfactors:careers.skyworksinc.com:1443131901"]; hk.Location != "Hong Kong" || hk.Title != "Intern, Process Engineering" || IsLikelyUS(hk.Location) {
		t.Errorf("country-only job %+v", hk)
	}
}

func TestCornerstone(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("X-Orig-Host") {
		case "linde.csod.com":
			if r.URL.Path != "/ux/ats/careersite/23/home" || r.URL.Query().Get("c") != "linde" {
				t.Errorf("page %s?%s", r.URL.Path, r.URL.RawQuery)
			}
			io.WriteString(w, `<script>csod.context={"corp":"linde","endpoints":{"cloud":"https://eu-fra.api.csod.com/","api":"/"},"token":"tok123"};</script>`)
		case "eu-fra.api.csod.com":
			if r.URL.Path != "/rec-job-search/external/jobs" || r.Header.Get("Authorization") != "Bearer tok123" {
				t.Errorf("api %s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
			}
			var body struct {
				CareerSiteID int `json:"careerSiteId"`
				PageNumber   int `json:"pageNumber"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.CareerSiteID != 23 {
				t.Errorf("career site %d", body.CareerSiteID)
			}
			if body.PageNumber == 1 {
				io.WriteString(w, `{"data":{"totalCount":2,"requisitions":[{"requisitionId":34833,"postingEffectiveDate":"10/2/2026","displayJobTitle":"Engineering Co-op","locations":[{"city":"Alpha","state":"NJ","country":"US"},{"city":"Tonawanda","state":"NY","country":"US"}]}]}}`)
				return
			}
			io.WriteString(w, `{"data":{"totalCount":2,"requisitions":[{"requisitionId":35000,"postingEffectiveDate":"-","displayJobTitle":"Process Engineer","locations":[{"city":"Leuna","state":"","country":"DE"}]}]}}`)
		default:
			http.NotFound(w, r)
		}
	})
	jobs, err := fetchCornerstone(context.Background(), c, Company{Name: "Linde", Slug: "linde", Site: "23"}, 5)
	if err != nil || len(jobs) != 2 {
		t.Fatalf("got %d jobs, err %v", len(jobs), err)
	}
	byID := map[string]Job{}
	for _, j := range jobs {
		byID[j.ID] = j
	}
	co := byID["cornerstone:linde:34833"]
	if co.Title != "Engineering Co-op" || co.Posted != "2026-10-02" || co.Location != "Alpha, NJ, United States; Tonawanda, NY, United States" ||
		co.URL != "https://linde.csod.com/ux/ats/careersite/23/home/requisition/34833?c=linde" {
		t.Errorf("co-op %+v", co)
	}
	if de := byID["cornerstone:linde:35000"]; de.Location != "Leuna, Germany" || de.Posted != "" {
		t.Errorf("foreign %+v", de)
	}
}

func TestRetryOn5xx(t *testing.T) {
	n := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		n++
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		io.WriteString(w, `{"jobs":[]}`)
	})
	if _, err := fetchGreenhouse(context.Background(), c, Company{Slug: "x"}, 1); err != nil || n != 3 {
		t.Fatalf("want success on 3rd try, got n=%d err=%v", n, err)
	}
}

// End-to-end: fake boards -> filter -> outputs, and first_seen survives a rerun.
func TestRunEndToEnd(t *testing.T) {
	dir := t.TempDir()
	companies := []Company{
		{Name: "Dow", Category: "Chemicals & Materials", ATS: "workday", Slug: "dow", WD: "wd1", Site: "ExternalCareers"},
		{Name: "Broken", ATS: "greenhouse", Slug: "nope"},
	}
	cp := filepath.Join(dir, "companies.json")
	b, _ := json.Marshal(companies)
	os.WriteFile(cp, b, 0o644)

	h := func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Orig-Host") != "dow.wd1.myworkdayjobs.com" {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, `{"total":5,"jobPostings":[
			{"title":"Process Engineering Intern - Summer 2027","externalPath":"/job/a_R1","locationsText":"Freeport, Texas","postedOn":"Posted Today"},
			{"title":"Engineering Co-op","externalPath":"/job/b_R2","locationsText":"Midland, MI","postedOn":"Posted 30+ Days Ago"},
			{"title":"Senior Process Engineer","externalPath":"/job/c_R3","locationsText":"Freeport, Texas","postedOn":"Posted Today"},
			{"title":"Chemical Engineer I","externalPath":"/job/d_R4","locationsText":"Terneuzen, Netherlands","postedOn":"Posted Today"},
			{"title":"Software Engineering Intern","externalPath":"/job/e_R5","locationsText":"Midland, MI","postedOn":"Posted Today"}]}`)
	}
	srv := httptest.NewServer(http.HandlerFunc(h))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	orig := http.DefaultClient.Transport
	defer func() { http.DefaultClient.Transport = orig }()

	o := options{companiesPath: cp, outDir: dir, concurrency: 4, boardWorkers: 2, maxPages: 2, timeout: 5 * time.Second}
	runWith := func() []Job {
		companies, _ := loadCompanies(o.companiesPath)
		c := NewClient(4, time.Second)
		c.HTTP.Transport = redirect{u}
		c.Retries = 0
		res := scrapeAll(context.Background(), c, companies, 2, 2)
		var raw []Job
		for _, r := range res {
			raw = append(raw, r.jobs...)
		}
		jobs := carryFirstSeen(filterJobs(raw, false, false), filepath.Join(dir, "data", "jobs.json"), "2026-11-16")
		sortJobs(jobs)
		if err := writeOutputs(dir, jobs, len(companies), 1, "2026-11-16"); err != nil {
			t.Fatal(err)
		}
		return jobs
	}
	jobs := runWith()
	if len(jobs) != 2 {
		t.Fatalf("want 2 matched jobs, got %d: %+v", len(jobs), jobs)
	}
	md, _ := os.ReadFile(filepath.Join(dir, "JOBS.md"))
	for _, want := range []string{"Process Engineering Intern - Summer 2027", "Engineering Co-op", "## 🎓 Internships (1)", "## 🔁 Co-ops (1)"} {
		if !strings.Contains(string(md), want) {
			t.Errorf("JOBS.md missing %q", want)
		}
	}
	if strings.Contains(string(md), "Senior") || strings.Contains(string(md), "Software") || strings.Contains(string(md), "Netherlands") {
		t.Error("JOBS.md contains filtered roles")
	}

	// Simulate an earlier first-seen date and confirm it is preserved.
	var stored []Job
	data, _ := os.ReadFile(filepath.Join(dir, "data", "jobs.json"))
	json.Unmarshal(data, &stored)
	for i := range stored {
		stored[i].FirstSeen = "2026-11-01"
	}
	data, _ = json.Marshal(stored)
	os.WriteFile(filepath.Join(dir, "data", "jobs.json"), data, 0o644)
	for _, j := range runWith() {
		if j.FirstSeen != "2026-11-01" {
			t.Errorf("first_seen not carried for %s: %s", j.Title, j.FirstSeen)
		}
	}
}

func TestKeepFromFailed(t *testing.T) {
	dir := t.TempDir()
	prev := []Job{
		{ID: "workday:dow:/job/a", Title: "Process Engineering Intern"},
		{ID: "greenhouse:oklo:1", Title: "Engineering Intern"},
	}
	b, _ := json.Marshal(prev)
	p := filepath.Join(dir, "jobs.json")
	os.WriteFile(p, b, 0o644)
	res := []boardResult{
		{co: Company{ATS: "workday", Slug: "dow"}, err: fmt.Errorf("blocked")},
		{co: Company{ATS: "greenhouse", Slug: "oklo"}},
	}
	got := keepFromFailed(res, p)
	if len(got) != 1 || got[0].ID != "workday:dow:/job/a" {
		t.Fatalf("got %+v", got)
	}
}
