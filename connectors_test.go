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
