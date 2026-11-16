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
