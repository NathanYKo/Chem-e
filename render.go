package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
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
	return os.WriteFile(filepath.Join(outDir, "JOBS.md"), []byte(renderMarkdown(jobs, boards, failed, today)), 0o644)
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
	now, err := time.Parse("2006-01-02", today)
	if err != nil {
		now = time.Now().UTC()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# ChemE Internships, Co-ops & New Grad Roles\n\n")
	fmt.Fprintf(&b, "_Updated %s · %d roles · %d company boards scraped", today, len(jobs), boards)
	if failed > 0 {
		fmt.Fprintf(&b, " (%d unreachable this run)", failed)
	}
	b.WriteString("_\n\n")
	b.WriteString("**Fit:** 🧪 = title names a ChemE discipline (process, polymer, refining, bioprocess…) · " +
		"⚙️ = general engineering early-talent role at a chem-heavy employer (usually open to ChemE majors)\n\n")

	sections := []struct{ title, level string }{
		{"🎓 Internships", LevelIntern},
		{"🔁 Co-ops", LevelCoop},
		{"🚀 New Grad / Early Career", LevelNewGrad},
	}
	for _, s := range sections {
		var rows []Job
		for _, j := range jobs {
			if j.Level == s.level {
				rows = append(rows, j)
			}
		}
		fmt.Fprintf(&b, "## %s (%d)\n\n", s.title, len(rows))
		if len(rows) == 0 {
			b.WriteString("_None right now._\n\n")
			continue
		}
		b.WriteString("| Company | Role | Fit | Location | Age | Sector |\n| --- | --- | :-: | --- | --- | --- |\n")
		for _, j := range rows {
			fit := "⚙️"
			if j.Fit == FitChemE {
				fit = "🧪"
			}
			posted := ageLabel(j, now)
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

// ageLabel renders "today", "3d", or "30d+" the way the big job lists do.
// Dates we only know from first-seen get a "~" since the role may be older.
func ageLabel(j Job, now time.Time) string {
	if j.Posted == "" && j.Posted30 {
		return "30d+"
	}
	a := ageDays(j, now)
	if a < 0 {
		return "—"
	}
	label := fmt.Sprintf("%dd", a)
	if a == 0 {
		label = "today"
	}
	if j.Posted == "" {
		label = "~" + label
	}
	return label
}
