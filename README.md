# ChemE Job Scraper

A Go scraper that pulls **internships, co-ops and new-grad / early-career roles for chemical engineers** straight from the public job-board APIs of top US ChemE employers. The output is [`JOBS.md`](JOBS.md), plus [`data/jobs.csv`](data/jobs.csv) and [`data/jobs.json`](data/jobs.json).

The design borrows from two projects:
- [zshah101/Automated-List-Of-Summer-2027-and-Fall-2026-Tech-Internships](https://github.com/zshah101/Automated-List-Of-Summer-2027-and-Fall-2026-Tech-Internships): reads ATS feeds directly. Every board in `data/companies.json` comes from that project's verified board registry.
- [DereC4/internships-and-newgrad](https://github.com/DereC4/internships-and-newgrad): a single Go binary that dedupes jobs and renders a Markdown table.

## Run it

```bash
go run .                     # scrape every board in data/companies.json
go run . -only dow           # just one company (substring match)
go run . -max-age 7          # only roles posted in the last week
go run . -strict             # only titles that name a ChemE discipline
go run . -all-locations      # keep non-US roles too
go test ./...
```

| Flag | Default | Meaning |
| --- | --- | --- |
| `-companies` | `data/companies.json` | Board list |
| `-out` | `.` | Where `JOBS.md` and `data/` are written |
| `-max-age` | 30 | Only roles posted in the last N days (`0` = no limit) |
| `-strict` | off | Drop the generic "Engineering Intern" ⚙️ roles |
| `-all-locations` | off | Keep roles outside the US |
| `-max-pages` | 5 | Pages per search term on Workday/Oracle/SmartRecruiters |
| `-concurrency` | 12 | Max in-flight HTTP requests |
| `-boards` | 8 | Boards scraped in parallel |

The GitHub Action in `.github/workflows/scrape.yml` runs it daily and commits the refreshed list. You can also start it by hand from the **Actions** tab.

## How it works

1. **Connectors** (`connectors.go`) cover Workday, Greenhouse, Lever, Ashby, SmartRecruiters and Oracle Recruiting Cloud. Workday, Oracle and SmartRecruiters only return keyword hits, so each board is searched for `intern`, `co-op`, `chemical engineer`, `process engineer`, `new grad`, `graduate`, `entry level`, `early career`, `controls engineer`, `supply chain engineer` and `R&D engineer`.
2. **Classifier** (`filter.go`) works from the job title:
   - **Level:** Internship, Co-op, or New Grad / Early Career (Engineer I, Associate Engineer, rotational / development programs, and similar). Senior, II/III, lead, manager and postdoc titles are dropped.
   - **Fit:** 🧪 **ChemE** means the title names a ChemE discipline (process, chemical, polymer, refining, bioprocess, formulation, MS&T, catalysis, battery materials, plus controls, supply chain and R&D engineering). ⚙️ **Engineering** means a general engineering early-talent role at the same employers, which usually lists ChemE as an eligible major. Clearly different disciplines (software, electrical, mechanical, finance, supply chain analyst…) are dropped.
3. **Location** (`location.go`) keeps US roles. If a location can't be identified, the role is kept rather than risk losing a real US job.
4. **Store:** jobs are deduped across career sites, `first_seen` is carried over between runs, and a board that fails keeps last run's roles.

## Companies covered (90 boards)

Covered sectors: chemicals and materials, oil/gas/energy, pharma and biotech, consumer/food/ag, semiconductors, and batteries/climate/nuclear. Examples: Dow, 3M, Chemours, Corteva, Air Products, Albemarle, PPG, Honeywell, Sherwin-Williams, Chevron, ConocoPhillips, Marathon, Oxy, Shell, BP, P&G, Clorox, Kraft Heinz, Pfizer, Merck, Lilly, J&J, AbbVie, Amgen, Intel, Applied Materials, Micron, Redwood Materials, Plug Power, X-energy and more.

### Adding a company
Add an entry to `data/companies.json`. To find the values, open the company's careers page:

| ATS | Careers URL looks like | Entry |
| --- | --- | --- |
| Workday | `https://dow.wd1.myworkdayjobs.com/ExternalCareers` | `{"ats":"workday","slug":"dow","wd":"wd1","site":"ExternalCareers"}` |
| Workday (path style) | `https://wd3.myworkdaysite.com/recruiting/mdlz/External` | `{"ats":"workday","slug":"mdlz","wd":"wd3","site":"External","host":"wd3.myworkdaysite.com"}` |
| Greenhouse | `job-boards.greenhouse.io/oklo` | `{"ats":"greenhouse","slug":"oklo"}` |
| Lever | `jobs.lever.co/cfsenergy` | `{"ats":"lever","slug":"cfsenergy"}` |
| Ashby | `jobs.ashbyhq.com/acme` | `{"ats":"ashby","slug":"acme"}` |
| SmartRecruiters | `jobs.smartrecruiters.com/AbbVie` | `{"ats":"smartrecruiters","slug":"AbbVie"}` |
| Oracle | `https://xxxx.fa.us6.oraclecloud.com/hcmUI/CandidateExperience/en/sites/CX_2` | `{"ats":"oracle","host":"xxxx.fa.us6.oraclecloud.com","site":"CX_2"}` |

Each entry also needs `name` and `category`. Set `"disabled": true` to skip a board without deleting it.

**Not covered yet:** ExxonMobil, BASF, Eastman, Celanese, LyondellBasell, Linde, Cargill, Kimberly-Clark, Phillips 66, Valero and Koch run custom or SuccessFactors career sites that have no public JSON feed.
