package main

// Company is one career board to scrape. Which fields matter depends on ATS:
//
//	workday:         Slug (tenant), WD (wd1/wd5/...), Site, optional Host for
//	                 path-style tenants (wd3.myworkdaysite.com/recruiting/...)
//	greenhouse:      Slug (board token)
//	lever:           Slug
//	ashby:           Slug
//	smartrecruiters: Slug (case-sensitive company id)
//	oracle:          Host (xxxx.fa.us2.oraclecloud.com), Site (CX_1, ...)
//	workable:        Slug (account, as in apply.workable.com/{slug})
//	breezy:          Slug (subdomain, as in {slug}.breezy.hr)
//	rippling:        Slug (board, as in ats.rippling.com/{slug}/jobs)
//	bamboohr:        Slug (subdomain, as in {slug}.bamboohr.com/careers)
//	adp:             Slug (cid query on workforcenow.adp.com)
//	eightfold:       Host (jobs.example.com), Slug (domain query)
//	wordpress:       Host (example.com), Slug (REST base, e.g. jobpost)
//	successfactors:  Host (career site, e.g. jobs.cargill.com)
//	cornerstone:     Slug (portal, as in {slug}.csod.com), Site (career-site id)
//	tesla:           none — single Tesla careers catalogue
type Company struct {
	Name     string `json:"name"`
	Category string `json:"category"`
	ATS      string `json:"ats"`
	Slug     string `json:"slug,omitempty"`
	WD       string `json:"wd,omitempty"`
	Site     string `json:"site,omitempty"`
	Host     string `json:"host,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`
}

// Job is one posting after normalization. Level and Fit are filled in by the
// classifier; FirstSeen is carried across runs by the store.
type Job struct {
	ID        string `json:"id"`
	Company   string `json:"company"`
	Category  string `json:"category"`
	Title     string `json:"title"`
	Location  string `json:"location"`
	URL       string `json:"url"`
	Source    string `json:"source"`
	Posted    string `json:"posted,omitempty"`         // YYYY-MM-DD when the ATS tells us
	Posted30  bool   `json:"posted_30_plus,omitempty"` // Workday said "30+ Days Ago": at least 30 days old, exact date unknown
	Level     string `json:"level"`                    // Internship | Co-op | New Grad / Early Career
	Fit       string `json:"fit"`                      // ChemE | Engineering
	FirstSeen string `json:"first_seen"`
}

const (
	LevelIntern  = "Internship"
	LevelCoop    = "Co-op"
	LevelNewGrad = "New Grad / Early Career"

	FitChemE = "ChemE"       // title names a ChemE discipline (process, polymer, refining...)
	FitEng   = "Engineering" // general engineering early-talent role at a chem-heavy employer
)
