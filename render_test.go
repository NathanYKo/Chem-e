package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteOutputsRefreshesReadmeJobList(t *testing.T) {
	dir := t.TempDir()
	readme := "# ChemE\n\nintro stays\n\n<!-- jobs:start -->\nstale row\n<!-- jobs:end -->\n\n## What this is\nkeep this\n"
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(readme), 0o644); err != nil {
		t.Fatal(err)
	}
	jobs := []Job{{
		Company: "Dow", Title: "Process Engineering Intern", URL: "https://example.com/j",
		Level: LevelIntern, Fit: FitChemE, Location: "Freeport, TX", Category: "Chemicals",
		Posted: "2026-11-16",
	}}
	if err := writeOutputs(dir, jobs, 3, 1, "2026-11-16"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	for _, want := range []string{"intro stays", "Process Engineering Intern", "## What this is", "keep this", "_Updated 2026-11-16 · 1 roles · 3 company boards scraped (1 unreachable this run)_"} {
		if !strings.Contains(s, want) {
			t.Errorf("README missing %q\n%s", want, s)
		}
	}
	if strings.Contains(s, "stale row") {
		t.Error("README still has the previous job list")
	}
	if strings.Contains(s, "# ChemE Internships") {
		t.Error("README should keep its own title, not a second copy of the JOBS.md heading")
	}
	start := strings.Index(s, "<!-- jobs:start -->")
	end := strings.Index(s, "<!-- jobs:end -->")
	if start < 0 || end < start {
		t.Fatalf("job markers missing or out of order:\n%s", s)
	}
}

func TestWriteOutputsReadmeWithoutMarkersFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# ChemE\n\nno markers\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := writeOutputs(dir, nil, 1, 0, "2026-11-16")
	if err == nil {
		t.Fatal("expected an error when README has no job-list markers")
	}
}

func TestWriteOutputsSkipsMissingReadme(t *testing.T) {
	dir := t.TempDir()
	if err := writeOutputs(dir, nil, 1, 0, "2026-11-16"); err != nil {
		t.Fatal(err)
	}
}

func TestRenderMarkdownJumpLinksAndDatePosted(t *testing.T) {
	jobs := []Job{
		{Company: "Dow", Title: "Intern", URL: "https://ex/i", Level: LevelIntern, Fit: FitChemE, Location: "TX", Category: "Chemicals", Posted: "2026-11-15"},
		{Company: "Dow", Title: "Second Intern", URL: "https://ex/i2", Level: LevelIntern, Fit: FitEng, Location: "MI", Category: "Chemicals", Posted: "2026-11-14"},
		{Company: "Merck", Title: "Co-op", URL: "https://ex/c", Level: LevelCoop, Fit: FitEng, Location: "PA", Category: "Pharma", FirstSeen: "2026-11-10"},
		{Company: "Amgen", Title: "New Grad", URL: "https://ex/n", Level: LevelNewGrad, Fit: FitChemE, Location: "CA", Category: "Pharma", Posted30: true, FirstSeen: "2026-11-16"},
	}
	md := renderMarkdown(jobs, 3, 0, "2026-11-16")
	for _, want := range []string{
		"**Jump to:** [Internships (2)](#internships) · [Co-ops (1)](#co-ops) · [New Grad / Early Career (1)](#new-grad)",
		`<a id="internships"></a>`,
		`<a id="co-ops"></a>`,
		`<a id="new-grad"></a>`,
		`<th>Company</th>`,
		`<th>Role</th>`,
		`<th>Location</th>`,
		`<th align="center">Application</th>`,
		`<th align="center">Date posted</th>`,
		`valign="top"`,
		`<td valign="top"><strong>Dow</strong></td>`,
		`<td valign="top">↳</td>`,
		`🧪 Intern`,
		`⚙️ Second Intern`,
		`<a href="https://ex/i">Apply</a>`,
		`<td valign="top" align="center">Nov 15</td>`,
		`<td valign="top" align="center">Nov 10</td>`,
		`<td valign="top" align="center">—</td>`,
	} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q\n%s", want, md)
		}
	}
	for _, bad := range []string{"| Age |", "| Sector |", "2026-11-15", "today", "<th>Fit</th>", "<th>Sector</th>"} {
		if strings.Contains(md, bad) {
			t.Errorf("unexpected %q in:\n%s", bad, md)
		}
	}
}

func TestDatePostedLabel(t *testing.T) {
	cases := []struct {
		j    Job
		want string
	}{
		{Job{Posted: "2026-11-15"}, "Nov 15"},
		{Job{FirstSeen: "2026-11-10"}, "Nov 10"},
		{Job{Posted30: true, FirstSeen: "2026-11-16"}, "—"},
		{Job{}, "—"},
	}
	for _, c := range cases {
		if got := datePostedLabel(c.j); got != c.want {
			t.Errorf("%+v: got %q want %q", c.j, got, c.want)
		}
	}
}
