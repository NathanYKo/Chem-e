package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// verifyOptions mirrors the scrape filters that an accuracy gate must re-check.
type verifyOptions struct {
	strict       bool
	allLocations bool
	maxAge       int
}

// Violation is one accuracy failure found in a published jobs list.
type Violation struct {
	ID     string
	Title  string
	Reason string
}

func (v Violation) String() string {
	id := v.ID
	if id == "" {
		id = "(no id)"
	}
	if v.Title != "" {
		return fmt.Sprintf("%s %q: %s", id, v.Title, v.Reason)
	}
	return fmt.Sprintf("%s: %s", id, v.Reason)
}

// verifyJobs checks that every stored role still satisfies the pipeline's
// promises (classifier, location, required fields, level/fit labels).
// Inspired by zshah101's tools/verify_accuracy.py.
func verifyJobs(jobs []Job, o verifyOptions, now time.Time) []Violation {
	var out []Violation
	seenID := map[string]bool{}
	for i, j := range jobs {
		prefix := fmt.Sprintf("index=%d", i)
		if j.ID == "" {
			out = append(out, Violation{Title: j.Title, Reason: prefix + " missing id"})
			continue
		}
		if seenID[j.ID] {
			out = append(out, Violation{ID: j.ID, Title: j.Title, Reason: "duplicate id"})
		}
		seenID[j.ID] = true

		if strings.TrimSpace(j.Title) == "" {
			out = append(out, Violation{ID: j.ID, Reason: "missing title"})
		}
		if strings.TrimSpace(j.Company) == "" {
			out = append(out, Violation{ID: j.ID, Title: j.Title, Reason: "missing company"})
		}
		if strings.TrimSpace(j.URL) == "" {
			out = append(out, Violation{ID: j.ID, Title: j.Title, Reason: "missing url"})
		}
		if j.FirstSeen == "" {
			out = append(out, Violation{ID: j.ID, Title: j.Title, Reason: "missing first_seen"})
		}
		switch j.Level {
		case LevelIntern, LevelCoop, LevelNewGrad:
		default:
			out = append(out, Violation{ID: j.ID, Title: j.Title, Reason: "invalid level " + j.Level})
		}
		switch j.Fit {
		case FitChemE, FitEng:
		default:
			out = append(out, Violation{ID: j.ID, Title: j.Title, Reason: "invalid fit " + j.Fit})
		}

		level, fit, ok := Classify(j.Title, o.strict)
		if !ok {
			out = append(out, Violation{ID: j.ID, Title: j.Title, Reason: "title fails Classify"})
			continue
		}
		if level != j.Level {
			out = append(out, Violation{ID: j.ID, Title: j.Title,
				Reason: fmt.Sprintf("level mismatch stored=%s classify=%s", j.Level, level)})
		}
		if fit != j.Fit {
			out = append(out, Violation{ID: j.ID, Title: j.Title,
				Reason: fmt.Sprintf("fit mismatch stored=%s classify=%s", j.Fit, fit)})
		}
		if !o.allLocations && !IsLikelyUS(j.Location) {
			out = append(out, Violation{ID: j.ID, Title: j.Title,
				Reason: "location fails US filter: " + j.Location})
		}
		if o.maxAge > 0 {
			if j.Posted == "" && j.Posted30 && o.maxAge <= 30 {
				out = append(out, Violation{ID: j.ID, Title: j.Title, Reason: "posted_30_plus exceeds max-age"})
			} else if a := ageDays(j, now); a >= 0 && a > o.maxAge {
				out = append(out, Violation{ID: j.ID, Title: j.Title,
					Reason: fmt.Sprintf("age %d days exceeds max-age %d", a, o.maxAge)})
			}
		}
	}
	return out
}

// verifyOutputs loads jobs.json (and cross-checks csv/md counts) from an
// output directory produced by a scrape run.
func verifyOutputs(outDir string, o verifyOptions, now time.Time) ([]Violation, error) {
	path := filepath.Join(outDir, "data", "jobs.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var jobs []Job
	if err := json.Unmarshal(data, &jobs); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	violations := verifyJobs(jobs, o, now)

	csvPath := filepath.Join(outDir, "data", "jobs.csv")
	if b, err := os.ReadFile(csvPath); err == nil {
		lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
		if len(lines) > 0 {
			rows := len(lines) - 1
			if rows != len(jobs) {
				violations = append(violations, Violation{
					Reason: fmt.Sprintf("jobs.csv row count %d != jobs.json %d", rows, len(jobs)),
				})
			}
		}
	}
	mdPath := filepath.Join(outDir, "JOBS.md")
	if b, err := os.ReadFile(mdPath); err == nil {
		needle := fmt.Sprintf("%d roles", len(jobs))
		if !strings.Contains(string(b), needle) {
			violations = append(violations, Violation{
				Reason: fmt.Sprintf("JOBS.md missing count phrase %q", needle),
			})
		}
	}
	return violations, nil
}
