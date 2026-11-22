package main

import (
	"regexp"
	"strings"
)

// Location text varies wildly by ATS ("USA, TX, Freeport", "Freeport, Texas",
// "US-TX-Houston", "3 Locations", "Midland"). The rule: anything that names
// the US is kept; anything that only names another country is dropped;
// anything we can't tell is kept, because dropping a real US role is worse
// than showing one foreign one.

var (
	reUSExplicit = regexp.MustCompile(`(?i)\b(united states|usa|u\.s\.a?\.?|america)\b|\bUS\b|\bUS-|^US`)

	usStates = []string{"alabama", "alaska", "arizona", "arkansas", "california", "colorado",
		"connecticut", "delaware", "florida", "georgia", "hawaii", "idaho", "illinois", "indiana",
		"iowa", "kansas", "kentucky", "louisiana", "maine", "maryland", "massachusetts", "michigan",
		"minnesota", "mississippi", "missouri", "montana", "nebraska", "nevada", "new hampshire",
		"new jersey", "new mexico", "new york", "north carolina", "north dakota", "ohio", "oklahoma",
		"oregon", "pennsylvania", "rhode island", "south carolina", "south dakota", "tennessee",
		"texas", "utah", "vermont", "virginia", "washington", "west virginia", "wisconsin", "wyoming",
		"puerto rico", "district of columbia"}
	reUSState = regexp.MustCompile(`(?i)\b(` + strings.Join(usStates, "|") + `)\b`)
	// Two-letter codes are matched case-sensitively and only in list-like
	// positions (", TX" / "TX," / "-TX-") so "IN" and "OR" in prose don't count.
	reUSAbbr = regexp.MustCompile(`(^|[,\-/ ])(AL|AK|AZ|AR|CA|CO|CT|DE|FL|GA|HI|ID|IL|IN|IA|KS|KY|LA|ME|MD|MA|MI|MN|MS|MO|MT|NE|NV|NH|NJ|NM|NY|NC|ND|OH|OK|OR|PA|RI|SC|SD|TN|TX|UT|VT|VA|WA|WV|WI|WY|DC|PR)($|[,\-/ ])`)
	reUSCity = regexp.MustCompile(`(?i)\b(houston|nyc|new york city|san francisco|bay area|boston|chicago|philadelphia|pittsburgh|midland|baton rouge|lake charles|freeport|pasadena|beaumont|port arthur|corpus christi|deer park|baytown|wilmington|kingsport|midland|indianapolis|cincinnati|cleveland|princeton|rahway|kalamazoo|thousand oaks|south san francisco|cambridge, ma|research triangle|rtp|remote - us|remote, us)\b`)

	reForeign = regexp.MustCompile(`(?i)\b(canada|mexico|brazil|brasil|argentina|chile|colombia|peru|costa rica|` +
		`united kingdom|uk|england|scotland|wales|ireland|germany|deutschland|france|spain|italy|portugal|netherlands|belgium|` +
		`luxembourg|switzerland|austria|poland|czech|czechia|slovakia|hungary|romania|bulgaria|greece|turkey|türkiye|sweden|` +
		`norway|denmark|finland|israel|egypt|saudi|united arab emirates|uae|qatar|kuwait|oman|bahrain|south africa|nigeria|kenya|` +
		`india|china|hong kong|taiwan|japan|korea|singapore|malaysia|thailand|vietnam|indonesia|philippines|australia|new zealand|` +
		`toronto|montreal|montréal|calgary|edmonton|vancouver|mississauga|sarnia|ontario|alberta|quebec|québec|british columbia|london|paris|berlin|munich|` +
		`frankfurt|ludwigshafen|leverkusen|marl|basel|zurich|geneva|amsterdam|rotterdam|antwerp|brussels|dublin|cork|madrid|barcelona|` +
		`milan|warsaw|krakow|prague|budapest|bucharest|stockholm|copenhagen|oslo|helsinki|shanghai|beijing|shenzhen|guangzhou|` +
		`bangalore|bengaluru|hyderabad|mumbai|pune|chennai|gurgaon|gurugram|noida|tokyo|osaka|seoul|kuala lumpur|penang|manila|` +
		`bangkok|jakarta|sydney|melbourne|são paulo|sao paulo|mexico city|monterrey|guadalajara|bogota|bogotá|santiago|buenos aires|` +
		`grünheide|grunheide|brandenburg|tilburg|noord-brabant|east attica|santa oliva)\b`)
)

// IsLikelyUS reports whether a location string could be a US role.
func IsLikelyUS(loc string) bool {
	l := strings.TrimSpace(loc)
	if l == "" || l == "—" {
		return true
	}
	if reUSExplicit.MatchString(l) {
		return true
	}
	// "New Mexico" must win over "Mexico", "Georgia" is a US state here, etc.
	if reUSState.MatchString(l) || reUSAbbr.MatchString(l) || reUSCity.MatchString(l) {
		// …unless it is a foreign place that happens to share a token
		// ("London, ON, Canada" has no US state, "Paris, TX" does).
		if reForeign.MatchString(stripUSStates(l)) && !reUSState.MatchString(l) && !reUSCity.MatchString(l) {
			return false
		}
		return true
	}
	return !reForeign.MatchString(l)
}

func stripUSStates(l string) string { return reUSState.ReplaceAllString(l, "") }
