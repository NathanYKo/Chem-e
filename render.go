package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func writeOutputs(outDir string, jobs []Job, boards, failed int, today string) error {
	if err := os.MkdirAll(filepath.Join(outDir, "data"), 0o755); err != nil {
		return err
	}
	if jobs == nil {
		jobs = []Job{}
	}
	js, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "data", "jobs.json"), append(js, '\n'), 0o644); err != nil {
		return err
	}
	if err := writeCSV(filepath.Join(outDir, "data", "jobs.csv"), jobs); err != nil {
		return err
	}
	md := renderMarkdown(jobs, boards, failed, today)
	if err := os.WriteFile(filepath.Join(outDir, "JOBS.md"), []byte(md), 0o644); err != nil {
		return err
	}
	return refreshReadme(outDir, md)
}

const (
	jobsStartMarker = "<!-- jobs:start -->"
	jobsEndMarker   = "<!-- jobs:end -->"
)

// refreshReadme replaces the marked job list in README.md so the GitHub repo
// page shows the same table as JOBS.md. A directory with no README (isolated
// scrape outs) is left alone.
func refreshReadme(outDir, jobsMD string) error {
	path := filepath.Join(outDir, "README.md")
	readme, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	updated, err := spliceJobs(string(readme), jobsMD)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(updated), 0o644)
}

func spliceJobs(readme, jobsMD string) (string, error) {
	start := strings.Index(readme, jobsStartMarker)
	end := strings.Index(readme, jobsEndMarker)
	if start < 0 || end < 0 || end < start {
		return "", fmt.Errorf("README missing %s / %s markers", jobsStartMarker, jobsEndMarker)
	}
	var b strings.Builder
	b.WriteString(readme[:start])
	b.WriteString(jobsStartMarker)
	b.WriteString("\n\n")
	b.WriteString(jobsBody(jobsMD))
	b.WriteString("\n\n")
	b.WriteString(readme[end:])
	return b.String(), nil
}

// jobsBody drops the JOBS.md title so the README keeps its own heading.
func jobsBody(md string) string {
	md = strings.TrimSpace(md)
	if _, rest, ok := strings.Cut(md, "\n"); ok {
		return strings.TrimSpace(rest)
	}
	return md
}

func writeCSV(path string, jobs []Job) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	_ = w.Write([]string{"company", "category", "title", "level", "fit", "location", "posted", "posted_30_plus", "first_seen", "source", "url"})
	for _, j := range jobs {
		_ = w.Write([]string{j.Company, j.Category, j.Title, j.Level, j.Fit, j.Location, j.Posted, fmt.Sprint(j.Posted30), j.FirstSeen, j.Source, j.URL})
	}
	w.Flush()
	return w.Error()
}

func mdEscape(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	return strings.ReplaceAll(s, "\n", " ")
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func renderMarkdown(jobs []Job, boards, failed int, today string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# ChemE Internships, Co-ops & New Grad Roles\n\n")
	fmt.Fprintf(&b, "_Updated %s · %d roles · %d company boards scraped", today, len(jobs), boards)
	if failed > 0 {
		fmt.Fprintf(&b, " (%d unreachable this run)", failed)
	}
	b.WriteString("_\n\n")
	b.WriteString("**Fit:** 🧪 = title names a ChemE discipline (process, polymer, refining, bioprocess…) · " +
		"⚙️ = general engineering early-talent role at a chem-heavy employer (usually open to ChemE majors)\n\n")

	sections := []struct{ title, level, anchor string }{
		{"🎓 Internships", LevelIntern, "internships"},
		{"🔁 Co-ops", LevelCoop, "co-ops"},
		{"🚀 New Grad / Early Career", LevelNewGrad, "new-grad"},
	}
	counts := map[string]int{}
	for _, j := range jobs {
		counts[j.Level]++
	}
	fmt.Fprintf(&b, "**Jump to:** [Internships (%d)](#internships) · [Co-ops (%d)](#co-ops) · [New Grad / Early Career (%d)](#new-grad)\n\n",
		counts[LevelIntern], counts[LevelCoop], counts[LevelNewGrad])

	for _, s := range sections {
		var rows []Job
		for _, j := range jobs {
			if j.Level == s.level {
				rows = append(rows, j)
			}
		}
		fmt.Fprintf(&b, "<a id=\"%s\"></a>\n## %s (%d)\n\n", s.anchor, s.title, len(rows))
		if len(rows) == 0 {
			b.WriteString("_None right now._\n\n")
			continue
		}
		b.WriteString("| Company | Role | Fit | Location | Date posted | Sector |\n| --- | --- | :-: | --- | :-: | --- |\n")
		for _, j := range rows {
			fit := "⚙️"
			if j.Fit == FitChemE {
				fit = "🧪"
			}
			posted := datePostedLabel(j)
			loc := j.Location
			if loc == "" {
				loc = "—"
			}
			fmt.Fprintf(&b, "| %s | [%s](%s) | %s | %s | %s | %s |\n",
				mdEscape(j.Company), mdEscape(j.Title), j.URL, fit,
				mdEscape(truncate(loc, 45)), posted, j.Category)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// datePostedLabel is the calendar date shown in the board: ATS posted date,
// else first-seen. Vague Workday "30+ days" and missing dates render as "—".
func datePostedLabel(j Job) string {
	if j.Posted == "" && j.Posted30 {
		return "—"
	}
	if d := postedDate(j); d != "" {
		return d
	}
	return "—"
}
