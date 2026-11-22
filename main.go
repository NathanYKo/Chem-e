// Command chemejobs scrapes public ATS job boards (Workday, Greenhouse, Lever,
// Ashby, SmartRecruiters, Oracle, Workable, Breezy, Rippling, BambooHR, ADP,
// Eightfold, WordPress, SuccessFactors, Cornerstone) of top US
// chemical-engineering employers and
// keeps internships, co-ops and new-grad / early-career roles that fit a
// ChemE background.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type options struct {
	companiesPath string
	outDir        string
	strict        bool
	allLocations  bool
	concurrency   int
	boardWorkers  int
	maxPages      int
	timeout       time.Duration
	only          string
	maxAge        int
	verifyOnly    bool
}

func main() {
	var o options
	flag.StringVar(&o.companiesPath, "companies", "data/companies.json", "company/board list (JSON)")
	flag.StringVar(&o.outDir, "out", ".", "directory for README.md, JOBS.md, data/jobs.json and data/jobs.csv")
	flag.BoolVar(&o.strict, "strict", false, "only keep titles that name a ChemE discipline (drop generic engineering roles)")
	flag.BoolVar(&o.allLocations, "all-locations", false, "keep non-US roles too")
	flag.IntVar(&o.concurrency, "concurrency", 12, "max in-flight HTTP requests")
	flag.IntVar(&o.boardWorkers, "boards", 8, "boards scraped in parallel")
	flag.IntVar(&o.maxPages, "max-pages", 5, "max pages per search term on Workday/Oracle/SmartRecruiters")
	flag.DurationVar(&o.timeout, "timeout", 30*time.Second, "per-request timeout")
	flag.IntVar(&o.maxAge, "max-age", 30, "only keep roles posted within this many days (0 = no limit)")
	flag.StringVar(&o.only, "only", "", "only scrape companies whose name contains this (case-insensitive)")
	flag.BoolVar(&o.verifyOnly, "verify", false, "check existing JOBS.md / data/jobs.json against classifier rules; do not scrape")
	flag.Parse()

	if err := run(context.Background(), o); err != nil {
		log.Fatal(err)
	}
}

type boardResult struct {
	co   Company
	jobs []Job
	err  error
}

func run(ctx context.Context, o options) error {
	if o.verifyOnly {
		vopts := verifyOptions{strict: o.strict, allLocations: o.allLocations, maxAge: o.maxAge}
		violations, err := verifyOutputs(o.outDir, vopts, time.Now().UTC())
		if err != nil {
			return err
		}
		if len(violations) == 0 {
			log.Printf("verify ok: %s", filepath.Join(o.outDir, "data", "jobs.json"))
			return nil
		}
		for _, v := range violations {
			log.Printf("  VIOLATION %s", v)
		}
		return fmt.Errorf("verify failed: %d violation(s)", len(violations))
	}

	companies, err := loadCompanies(o.companiesPath)
	if err != nil {
		return err
	}
	if o.only != "" {
		var keep []Company
		for _, c := range companies {
			if strings.Contains(strings.ToLower(c.Name), strings.ToLower(o.only)) {
				keep = append(keep, c)
			}
		}
		companies = keep
	}
	client := NewClient(o.concurrency, o.timeout)
	results := scrapeAll(ctx, client, companies, o.boardWorkers, o.maxPages)

	var raw []Job
	var failed []string
	for _, r := range results {
		if r.err != nil {
			failed = append(failed, fmt.Sprintf("%s (%s/%s): %v", r.co.Name, r.co.ATS, boardKey(r.co), r.err))
			continue
		}
		raw = append(raw, r.jobs...)
	}

	if len(companies) > 0 && len(failed) == len(companies) {
		for _, f := range failed {
			log.Printf("  FAILED %s", f)
		}
		return fmt.Errorf("all %d boards failed; leaving previous output untouched", len(companies))
	}

	jsonPath := filepath.Join(o.outDir, "data", "jobs.json")
	matched := filterJobs(raw, o.strict, o.allLocations)
	// A board that is down this run keeps last run's roles instead of
	// vanishing from the list.
	matched = append(matched, keepFromFailed(results, jsonPath)...)

	today := time.Now().UTC().Format("2006-01-02")
	matched = carryFirstSeen(matched, jsonPath, today)
	matched = filterRecent(matched, o.maxAge, time.Now().UTC())
	sortJobs(matched)

	if err := writeOutputs(o.outDir, matched, len(companies), len(failed), today); err != nil {
		return err
	}

	vopts := verifyOptions{strict: o.strict, allLocations: o.allLocations, maxAge: o.maxAge}
	if violations := verifyJobs(matched, vopts, time.Now().UTC()); len(violations) > 0 {
		for _, v := range violations {
			log.Printf("  VIOLATION %s", v)
		}
		return fmt.Errorf("post-scrape verify failed: %d violation(s)", len(violations))
	}

	log.Printf("scraped %d boards (%d failed), %d raw postings -> %d ChemE early-career roles",
		len(companies), len(failed), len(raw), len(matched))
	for _, f := range failed {
		log.Printf("  FAILED %s", f)
	}
	return nil
}

func idPrefix(c Company) string {
	if c.ATS == "oracle" {
		return "oracle:" + c.Host + ":"
	}
	return c.ATS + ":" + c.Slug + ":"
}

func loadPrevious(path string) []Job {
	var old []Job
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &old)
	}
	return old
}

func keepFromFailed(results []boardResult, prevPath string) []Job {
	var prefixes []string
	for _, r := range results {
		if r.err != nil {
			prefixes = append(prefixes, idPrefix(r.co))
		}
	}
	if len(prefixes) == 0 {
		return nil
	}
	var keep []Job
	for _, j := range loadPrevious(prevPath) {
		for _, p := range prefixes {
			if strings.HasPrefix(j.ID, p) {
				keep = append(keep, j)
				break
			}
		}
	}
	return keep
}

func loadCompanies(path string) ([]Company, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read companies: %w", err)
	}
	var all []Company
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	var out []Company
	for _, c := range all {
		if c.Disabled {
			continue
		}
		if _, ok := fetchers[c.ATS]; !ok {
			log.Printf("skipping %s: unknown ats %q", c.Name, c.ATS)
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

func boardKey(c Company) string {
	switch c.ATS {
	case "workday":
		return c.Slug + "/" + c.Site
	case "oracle":
		return c.Host + "/" + c.Site
	}
	return c.Slug
}

func scrapeAll(ctx context.Context, client *Client, companies []Company, workers, maxPages int) []boardResult {
	if workers < 1 {
		workers = 1
	}
	in := make(chan Company)
	out := make(chan boardResult)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for co := range in {
				start := time.Now()
				jobs, err := fetchers[co.ATS](ctx, client, co, maxPages)
				if err == nil {
					log.Printf("%-28s %-15s %4d postings  (%s)", co.Name, co.ATS, len(jobs), time.Since(start).Round(time.Millisecond))
				}
				out <- boardResult{co, jobs, err}
			}
		}()
	}
	go func() {
		for _, c := range companies {
			in <- c
		}
		close(in)
		wg.Wait()
		close(out)
	}()
	var results []boardResult
	for r := range out {
		results = append(results, r)
	}
	return results
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func norm(s string) string {
	return strings.TrimSpace(nonAlnum.ReplaceAllString(strings.ToLower(s), " "))
}

// filterJobs classifies, applies the location filter and removes duplicates
// (the same requisition is often posted to two career sites of one company).
func filterJobs(raw []Job, strict, allLocations bool) []Job {
	seenID := map[string]bool{}
	seenKey := map[string]bool{}
	var out []Job
	for _, j := range raw {
		j.Title = strings.Join(strings.Fields(j.Title), " ")
		j.Location = strings.Join(strings.Fields(j.Location), " ")
		level, fit, ok := Classify(j.Title, strict)
		if !ok {
			continue
		}
		if !allLocations && !IsLikelyUS(j.Location) {
			continue
		}
		key := norm(j.Company) + "|" + norm(j.Title) + "|" + norm(j.Location)
		if seenID[j.ID] || seenKey[key] {
			continue
		}
		seenID[j.ID], seenKey[key] = true, true
		j.Level, j.Fit = level, fit
		out = append(out, j)
	}
	return out
}

// carryFirstSeen keeps the date a role was first spotted across runs, so the
// list can show what is new even when the ATS gives no posting date.
func carryFirstSeen(jobs []Job, prevPath, today string) []Job {
	prev := map[string]string{}
	for _, j := range loadPrevious(prevPath) {
		prev[j.ID] = j.FirstSeen
	}
	for i := range jobs {
		if fs, ok := prev[jobs[i].ID]; ok && fs != "" {
			jobs[i].FirstSeen = fs
		} else {
			jobs[i].FirstSeen = today
		}
	}
	return jobs
}

// postedDate is the best date we have for a role: the ATS posting date, or
// the day this scraper first saw it.
func postedDate(j Job) string {
	if j.Posted != "" {
		return j.Posted
	}
	return j.FirstSeen
}

// ageDays is how many days ago the role was posted; -1 if unknown.
func ageDays(j Job, now time.Time) int {
	d, err := time.Parse("2006-01-02", postedDate(j))
	if err != nil {
		return -1
	}
	n := int(now.Sub(d).Hours() / 24)
	if j.Posted == "" && j.Posted30 && n < 30 {
		n = 30 // Workday only told us "30+ days"
	}
	if n < 0 {
		n = 0
	}
	return n
}

// filterRecent drops roles older than maxAge days. Roles with no date at all
// are kept, since we can't prove they are old.
func filterRecent(jobs []Job, maxAge int, now time.Time) []Job {
	if maxAge <= 0 {
		return jobs
	}
	out := jobs[:0]
	for _, j := range jobs {
		if j.Posted == "" && j.Posted30 && maxAge <= 30 {
			continue
		}
		if a := ageDays(j, now); a >= 0 && a > maxAge {
			continue
		}
		out = append(out, j)
	}
	return out
}

// sortJobs: newest first (posted date, falling back to first-seen), then company.
func sortJobs(jobs []Job) {
	date := func(j Job) string {
		if j.Posted == "" && j.Posted30 {
			return "0000" // vague "30+ days" sorts last
		}
		return postedDate(j)
	}
	sort.SliceStable(jobs, func(a, b int) bool {
		da, db := date(jobs[a]), date(jobs[b])
		if da != db {
			return da > db
		}
		if jobs[a].Company != jobs[b].Company {
			return jobs[a].Company < jobs[b].Company
		}
		return jobs[a].Title < jobs[b].Title
	})
}
