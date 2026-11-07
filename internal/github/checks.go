package github

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// LogTailMax is the most of a CI log a fix_checks brief carries.
const LogTailMax = 4000

const runSummaryMax = 4000

// failingConclusions are the conclusions a code change can fix; cancelled, neutral, skipped, stale and
// action_required are not failures.
var failingConclusions = map[string]bool{"failure": true, "timed_out": true, "startup_failure": true, "error": true}

// IsFailing reports whether a check failed.
func IsFailing(r api.CheckRun) bool { return failingConclusions[r.Conclusion] }

// ChecksState is pending, success, failure or unknown (no checks at all).
func ChecksState(runs []api.CheckRun) string {
	if len(runs) == 0 {
		return api.ChecksUnknown
	}
	pending := false
	for _, r := range runs {
		if IsFailing(r) {
			return api.ChecksFailure
		}
		if r.Status != "completed" {
			pending = true
		}
	}
	if pending {
		return api.ChecksPending
	}
	return api.ChecksSuccess
}

var actionsJobRe = regexp.MustCompile(`/actions/runs/(\d+)/jobs?/(\d+)`)

type ghCheckRun struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	HTMLURL    string `json:"html_url"`
	DetailsURL string `json:"details_url"`
	App        *struct {
		Slug string `json:"slug"`
	} `json:"app"`
	Output *struct {
		Title   string `json:"title"`
		Summary string `json:"summary"`
	} `json:"output"`
}

type ghStatus struct {
	ID          int64  `json:"id"`
	State       string `json:"state"`
	Context     string `json:"context"`
	TargetURL   string `json:"target_url"`
	Description string `json:"description"`
}

// ParseChecks reads the answers of the check-runs and the combined-status endpoints (either may be nil) of commit
// sha.
func ParseChecks(checkRuns, statuses []byte, sha string) api.TicketChecks {
	runs := []api.CheckRun{}
	var cr struct {
		CheckRuns []ghCheckRun `json:"check_runs"`
	}
	if len(checkRuns) > 0 && json.Unmarshal(checkRuns, &cr) == nil {
		for _, c := range cr.CheckRuns {
			details := c.DetailsURL
			if details == "" {
				details = c.HTMLURL
			}
			r := api.CheckRun{Kind: "check", ID: c.ID, Name: oneLine(c.Name, 200), Status: c.Status,
				Conclusion: c.Conclusion, URL: httpURL(c.HTMLURL)}
			if r.Name == "" {
				r.Name = "check"
			}
			if r.Status == "" {
				r.Status = "completed"
			}
			if r.URL == "" {
				r.URL = httpURL(c.DetailsURL)
			}
			if c.App != nil {
				r.App = c.App.Slug
			}
			if m := actionsJobRe.FindStringSubmatch(details); m != nil {
				r.RunID, _ = strconv.ParseInt(m[1], 10, 64)
				r.JobID, _ = strconv.ParseInt(m[2], 10, 64)
			} else if r.App == "github-actions" && c.ID > 0 {
				r.JobID = c.ID
			}
			if c.Output != nil {
				parts := []string{}
				for _, s := range []string{c.Output.Title, c.Output.Summary} {
					if strings.TrimSpace(s) != "" {
						parts = append(parts, s)
					}
				}
				r.Summary = clipRunes(strings.Join(parts, "\n"), runSummaryMax)
			}
			runs = append(runs, r)
		}
	}
	var st struct {
		Statuses []ghStatus `json:"statuses"`
	}
	if len(statuses) > 0 && json.Unmarshal(statuses, &st) == nil {
		for _, s := range st.Statuses {
			state := s.State
			if state == "" {
				state = "pending"
			}
			r := api.CheckRun{Kind: "status", ID: s.ID, Name: oneLine(s.Context, 200), URL: httpURL(s.TargetURL),
				Summary: clipRunes(strings.TrimSpace(s.Description), runSummaryMax)}
			if r.Name == "" {
				r.Name = "status"
			}
			if state == "pending" {
				r.Status = "pending"
			} else {
				r.Status, r.Conclusion = "completed", state
			}
			runs = append(runs, r)
		}
	}
	if len(runs) > 100 {
		runs = runs[:100]
	}
	return api.TicketChecks{State: ChecksState(runs), Runs: runs, HeadSha: sha}
}

var (
	ansiRe      = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
	logStampRe  = regexp.MustCompile(`(?m)^\x{FEFF}?\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z ?`)
	trailingWsp = regexp.MustCompile(`\s+$`)
)

// LogTail is the end of a CI log: no ANSI colours, no per-line timestamps, at most max bytes, starting on a line
// boundary when one is near.
func LogTail(text string, max int) string {
	s := strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	s = ansiRe.ReplaceAllString(s, "")
	s = logStampRe.ReplaceAllString(s, "")
	s = trailingWsp.ReplaceAllString(s, "")
	if len(s) <= max {
		return s
	}
	tail := s[len(s)-max:]
	// Start on a whole rune.
	for len(tail) > 0 && tail[0]&0xC0 == 0x80 {
		tail = tail[1:]
	}
	if nl := strings.IndexByte(tail, '\n'); nl >= 0 && nl < max/4 {
		tail = tail[nl+1:]
	}
	return tail
}
