package main

import (
	"regexp"
	"strings"
)

// Classification works on the job title only: it is the one field every ATS
// returns, and it is what a recruiter writes to tell candidates who the role
// is for.

var (
	reIntern  = regexp.MustCompile(`(?i)\b(interns?|internships?|undergrad(uate)?|summer (student|analyst|associate)|student (worker|engineer|trainee)|work term|placement year|industrial placement)\b`)
	reCoop    = regexp.MustCompile(`(?i)\bco-?op\b|\bcooperative education\b`)
	reNewGrad = regexp.MustCompile(`(?i)\b(new[ -]?grads?|new college grad|recent (college )?grad(uate)?s?|graduates?\b|` +
		`entry[ -]level|early[ -]career|early[ -]talent|university (grad|graduate|hire|recruit)|campus( hire)?|college (hire|grad)|` +
		`rotational|rotation program|development program|leadership (development )?program|` +
		`engineer[- ]in[- ]training|\bEIT\b|junior|jr\.?|associate ([a-z&]+ ){0,2}engineer|` +
		`(engineer|scientist|technologist|specialist) (i|1)\b|level (i|1)\b|` +
		`(class|cohort) of 20\d\d|20(26|27) (grad|graduate|start))`)
	// Roles that are clearly beyond early career.
	reSenior = regexp.MustCompile(`(?i)\b(senior|sr|staff|principal|lead|leader|manager|mgr|director|head of|vice president|vp|supervisor|superintendent|chief|experienced|expert|fellow|postdoc|postdoctoral)\b|` +
		`\b(engineer|scientist|specialist|technologist|analyst) (ii|iii|iv|v|2|3|4|5)\b|\b(ii|iii|iv)$`)

	// Titles that name a ChemE discipline outright.
	reChemE = regexp.MustCompile(`(?i)(chemical|chem\.? ?e\b|chem eng|process (engineer|engineering|development|technology|design|safety|control|chemist|sciences?|scientist|improvement engineer)|` +
		`polymer|plastics|elastomer|rubber|resin|coatings?|adhesives?|bioprocess|biochemical|fermentation|cell culture|purification|downstream process|upstream process|` +
		`refin(ery|ing)|petrochemical|petroleum|catalys(t|is)|reaction engineer|separations?|distillation|formulation|` +
		`ms&t|manufacturing sciences?|manufacturing science and technology|technical services?|scale[- ]?up|pilot plant|` +
		`materials (engineer|engineering|science)|electrochem|battery (cell|process|materials)|cathode|anode|electrolyte|lithium|hydrogen|ammonia|` +
		`carbon capture|decarboni[sz]ation|process safety|production engineer|plant engineer|operations engineer|unit engineer|` +
		`environmental engineer|water treatment|wastewater|corrosion|thin film|wet etch|cvd|deposition|etch (process|engineer)|` +
		`pharmaceutical (engineer|manufacturing|sciences?)|drug (product|substance)|api manufacturing|` +
		// Roles added on request: controls, supply chain and R&D engineering.
		`controls? (engineer|engineering)|automation (engineer|engineering)|supply chain (engineer|engineering)|` +
		`(r ?& ?d|research (&|and) development) (engineer|engineering)|` +
		// Fab / cleanroom process and equipment roles.
		`cleanroom|clean room|nanofab|lithograph|photolitho|metrology|diffusion|wets?/cleans|plating|process integration|yield|` +
		`(fab|equipment|semiconductor) (engineer|engineering)|` +
		// Environmental, water and thermal/energy systems roles.
		`pfas|desalination|water (resources|technolog|engineer)|environmental (health|compliance|safety)|\behs\b|` +
		`thermal (engineer|engineering|systems)|energy (engineer|engineering|systems)|fuel cell|nuclear|fusion)`)

	// Roles a BS ChemE engineering candidate is not targeting, even when the
	// title also names a ChemE discipline.
	reNotFit = regexp.MustCompile(`(?i)\b(ph\.?d|doctorate|doctoral|mba|associates? degree|non[- ]engineering|` +
		`operators?|technicians?|maintenance specialist|pit crew|` +
		`oncology|immunology|translational|knowledge management|revenue|strategy|` +
		`reservoir|drilling|wellsite|subsurface)\b`)
	reMastersOnly = regexp.MustCompile(`(?i)\bmaster'?s\b`)
	reBachelors   = regexp.MustCompile(`(?i)\b(bachelor'?s?|bs|undergrad(uate)?)\b`)

	// General engineering early-talent roles a ChemE can apply to (they often
	// list "Chemical, Mechanical or Electrical Engineering" as eligible majors).
	reGenEng = regexp.MustCompile(`(?i)\b(engineer|engineering|technical|technology development|manufacturing|operations|reliability|project engineer|quality engineer|r&d|research (&|and) development|sustainability|energy|process)\b`)

	// Disciplines that are clearly not ChemE. Only applied when the title does
	// not ALSO name a ChemE discipline ("Mechanical/Chemical Engineering Intern").
	reOtherDiscipline = regexp.MustCompile(`(?i)\b(software|swe|developer|devops|full[- ]stack|front[- ]?end|back[- ]?end|web|mobile|ios|android|cloud|data (engineer|engineering|science|scientist|analyst|analytics)|` +
		`machine learning|\bml\b|\bai\b|artificial intelligence|cyber|security|it\b|information technology|network|firmware|embedded|fpga|asic|rtl|verification|` +
		`electrical|electronics?|hardware|rf|antenna|power systems|controls software|mechanical|civil|structural|architect|` +
		`design evaluation|(field|central|power) applications|algorithm|optical|mechatronics|design engineer|test engineer|` +
		`aerospace|avionics|industrial design|graphic|ux|ui|product (manager|management|design)|` +
		`sales|marketing|finance|financial|accounting|audit|tax|treasury|legal|paralegal|hr|human resources|recruit|talent acquisition|` +
		`supply chain|procurement|purchasing|logistics|buyer|commercial|business|communications|customer|clinical|medical affairs|regulatory affairs|` +
		`pharmacovigilance|biostatistics?|statistics?|statistician|economist|analyst|nurse|pharmacist|physician)\b`)
)

// Classify returns (level, fit, ok). ok is false when the job is not an
// early-career ChemE-relevant role. strict keeps only titles that name a ChemE
// discipline.
func Classify(title string, strict bool) (level, fit string, ok bool) {
	t := strings.TrimSpace(title)
	if t == "" {
		return "", "", false
	}

	switch {
	case reCoop.MatchString(t):
		level = LevelCoop
	case reIntern.MatchString(t):
		level = LevelIntern
	case reNewGrad.MatchString(t) && !reSenior.MatchString(t):
		level = LevelNewGrad
	default:
		return "", "", false
	}

	if reNotFit.MatchString(t) || (reMastersOnly.MatchString(t) && !reBachelors.MatchString(t)) {
		return "", "", false
	}
	if reChemE.MatchString(t) {
		return level, FitChemE, true
	}
	if strict {
		return "", "", false
	}
	if reGenEng.MatchString(t) && !reOtherDiscipline.MatchString(t) {
		return level, FitEng, true
	}
	return "", "", false
}
