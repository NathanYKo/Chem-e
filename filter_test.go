package main

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		title      string
		level, fit string // empty level => rejected
	}{
		// ChemE-named roles
		{"Chemical Engineering Intern - Summer 2027", LevelIntern, FitChemE},
		{"Process Engineer Intern", LevelIntern, FitChemE},
		{"2027 Process Engineering Co-op", LevelCoop, FitChemE},
		{"Co-Op - Polymer Science", LevelCoop, FitChemE},
		{"Process Engineer I", LevelNewGrad, FitChemE},
		{"Associate Process Engineer", LevelNewGrad, FitChemE},
		{"Entry Level Chemical Engineer", LevelNewGrad, FitChemE},
		{"Refinery Process Engineer - New Grad", LevelNewGrad, FitChemE},
		{"Bioprocess Engineering Intern (Downstream)", LevelIntern, FitChemE},
		{"MS&T Engineer - Early Career", LevelNewGrad, FitChemE},
		{"Graduate Process Engineer", LevelNewGrad, FitChemE},
		{"Mechanical/Chemical Engineering Intern", LevelIntern, FitChemE},
		{"Production Engineer - University Graduate", LevelNewGrad, FitChemE},
		{"Formulation Scientist Intern", LevelIntern, FitChemE},
		// General engineering early talent at chem employers
		{"Engineering Intern", LevelIntern, FitEng},
		{"Manufacturing Engineering Co-op", LevelCoop, FitEng},
		{"Engineering Development Program - 2027 Start", LevelNewGrad, FitEng},
		{"Operations Leadership Development Program", LevelNewGrad, FitEng},
		{"Reliability Engineer Intern", LevelIntern, FitEng},
		// Rejected: other disciplines
		{"Software Engineering Intern", "", ""},
		{"Mechanical Engineering Intern", "", ""},
		{"Electrical Engineer I", "", ""},
		{"Finance Intern", "", ""},
		{"Supply Chain Co-op", "", ""},
		{"Data Science Intern", "", ""},
		// Rejected: seniority
		{"Senior Process Engineer", "", ""},
		{"Process Engineer II", "", ""},
		{"Lead Chemical Engineer", "", ""},
		{"Process Engineering Manager", "", ""},
		{"Principal Scientist, Formulation", "", ""},
		// Rejected: no early-career signal
		{"Process Engineer", "", ""},
		{"Chemical Engineer", "", ""},
		// Not fooled by "internal"/"international"
		{"Internal Audit Process Engineer", "", ""},
	}
	for _, c := range cases {
		level, fit, ok := Classify(c.title, false)
		if c.level == "" {
			if ok {
				t.Errorf("%q: want rejected, got %s/%s", c.title, level, fit)
			}
			continue
		}
		if !ok || level != c.level || fit != c.fit {
			t.Errorf("%q: want %s/%s, got %s/%s ok=%v", c.title, c.level, c.fit, level, fit, ok)
		}
	}
}

func TestClassifyStrict(t *testing.T) {
	if _, _, ok := Classify("Engineering Intern", true); ok {
		t.Error("strict mode should drop generic engineering roles")
	}
	if _, fit, ok := Classify("Process Engineering Intern", true); !ok || fit != FitChemE {
		t.Error("strict mode should keep ChemE roles")
	}
}

func TestIsLikelyUS(t *testing.T) {
	us := []string{"", "—", "USA, TX, Freeport", "Freeport, Texas", "US-TX-Houston", "Midland, MI",
		"Baton Rouge, LA", "3 Locations", "Houston", "United States", "Albuquerque, New Mexico",
		"Remote, US", "Rahway, New Jersey, United States of America", "Toronto, Canada; Houston, TX, United States"}
	foreign := []string{"Ludwigshafen, Germany", "Sarnia, ON", "Mississauga, ON, CA", "Bengaluru, Karnataka, India",
		"Shanghai, China", "London, United Kingdom", "Mexico City, Mexico", "Singapore", "Antwerp"}
	for _, l := range us {
		if !IsLikelyUS(l) {
			t.Errorf("%q should be US", l)
		}
	}
	for _, l := range foreign {
		if IsLikelyUS(l) {
			t.Errorf("%q should not be US", l)
		}
	}
}
