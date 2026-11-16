package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVerifyJobsClean(t *testing.T) {
	jobs := []Job{{
		ID: "workday:dow:/job/a", Company: "Dow", Title: "Process Engineering Intern",
		Location: "Freeport, Texas", URL: "https://example.com/a", Level: LevelIntern,
		Fit: FitChemE, FirstSeen: "2026-11-01",
	}}
	if v := verifyJobs(jobs, verifyOptions{}, time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)); len(v) != 0 {
		t.Fatalf("unexpected violations: %v", v)
	}
}

func TestVerifyJobsCatchesBadTitle(t *testing.T) {
	jobs := []Job{{
		ID: "x", Company: "Dow", Title: "Senior Process Engineer",
		Location: "Freeport, Texas", URL: "https://example.com/a", Level: LevelNewGrad,
		Fit: FitChemE, FirstSeen: "2026-11-01",
	}}
	v := verifyJobs(jobs, verifyOptions{}, time.Now().UTC())
	if len(v) == 0 {
		t.Fatal("expected violation for senior title")
	}
}

func TestVerifyJobsCatchesLocation(t *testing.T) {
	jobs := []Job{{
		ID: "x", Company: "Dow", Title: "Process Engineering Intern",
		Location: "Terneuzen, Netherlands", URL: "https://example.com/a", Level: LevelIntern,
		Fit: FitChemE, FirstSeen: "2026-11-01",
	}}
	v := verifyJobs(jobs, verifyOptions{}, time.Now().UTC())
	if len(v) == 0 {
		t.Fatal("expected location violation")
	}
	v = verifyJobs(jobs, verifyOptions{allLocations: true}, time.Now().UTC())
	if len(v) != 0 {
		t.Fatalf("allLocations should allow: %v", v)
	}
}

func TestVerifyOutputsCrossCheck(t *testing.T) {
	dir := t.TempDir()
	jobs := []Job{{
		ID: "workday:dow:/job/a", Company: "Dow", Category: "Chemicals",
		Title: "Process Engineering Intern", Location: "Freeport, Texas",
		URL: "https://example.com/a", Source: "workday", Level: LevelIntern,
		Fit: FitChemE, FirstSeen: "2026-11-01",
	}}
	if err := writeOutputs(dir, jobs, 1, 0, "2026-11-16"); err != nil {
		t.Fatal(err)
	}
	v, err := verifyOutputs(dir, verifyOptions{}, time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(v) != 0 {
		t.Fatalf("clean outputs: %v", v)
	}

	// Corrupt the markdown count phrase.
	md := filepath.Join(dir, "JOBS.md")
	b, _ := os.ReadFile(md)
	_ = os.WriteFile(md, []byte(string(b)+ "\n"), 0o644)
	// Break count by rewriting with wrong header text.
	_ = os.WriteFile(md, []byte("# ChemE\n\n_Updated 2026-11-16 · 99 roles · 1 company boards scraped_\n"), 0o644)
	v, err = verifyOutputs(dir, verifyOptions{}, time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, x := range v {
		if strings.Contains(x.Reason, "JOBS.md") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected JOBS.md count violation, got %v", v)
	}
}
