// Package report is what a check produces and how it is printed.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Level says what a finding means for a client that is already out there and
// cannot be forced to update.
type Level string

const (
	// Breaking means a shipped client stops working. The job fails.
	Breaking Level = "breaking"

	// Risky means nothing breaks at the wire level and something can still
	// go wrong in a client that has not been updated. Adding an enum value
	// is the example: old clients receive a number they have no name for.
	// It is reported and does not fail the job, because treating it as fatal
	// means a team that needs to add an enum value learns to pass the flag
	// that turns the whole check off.
	Risky Level = "risky"
)

// Finding is one problem, named precisely enough to fix without going
// looking.
type Finding struct {
	Level   Level  `json:"level"`
	Rule    string `json:"rule"`
	Subject string `json:"subject"`
	Detail  string `json:"detail"`
	Was     string `json:"was,omitempty"`
	Now     string `json:"now,omitempty"`
}

func (f Finding) String() string {
	s := fmt.Sprintf("%s  %s  %s: %s", strings.ToUpper(string(f.Level)), f.Rule, f.Subject, f.Detail)
	if f.Was != "" || f.Now != "" {
		s += fmt.Sprintf("\n        was: %s\n        now: %s", or(f.Was, "absent"), or(f.Now, "absent"))
	}
	return s
}

func or(a, b string) string {
	if a == "" {
		return b
	}
	return a
}

// Result is a whole run.
type Result struct {
	Findings []Finding `json:"findings"`
	Checked  Checked   `json:"checked"`
}

// Checked is what the run looked at. A check that silently examined nothing
// passes, which is the most dangerous way for this tool to fail, so the
// numbers are printed on every run and the command refuses an empty one.
type Checked struct {
	Messages int `json:"messages"`
	Fields   int `json:"fields"`
	Enums    int `json:"enums"`
	Services int `json:"services"`
	Methods  int `json:"methods"`
	Tables   int `json:"tables"`
	Columns  int `json:"columns"`
}

func (r *Result) Add(f Finding) { r.Findings = append(r.Findings, f) }

func (r *Result) Count(l Level) int {
	n := 0
	for _, f := range r.Findings {
		if f.Level == l {
			n++
		}
	}
	return n
}

// Breaking reports whether the job should fail.
func (r *Result) Breaking() bool { return r.Count(Breaking) > 0 }

// Sort puts breaking findings first and makes the output stable, so two runs
// on the same input produce the same bytes and a diff of two runs is
// meaningful.
func (r *Result) Sort() {
	sort.SliceStable(r.Findings, func(i, j int) bool {
		a, b := r.Findings[i], r.Findings[j]
		if a.Level != b.Level {
			return a.Level == Breaking
		}
		if a.Subject != b.Subject {
			return a.Subject < b.Subject
		}
		return a.Rule < b.Rule
	})
}

// Text is the human format.
func (r *Result) Text(w io.Writer) {
	r.Sort()
	c := r.Checked
	fmt.Fprintf(w, "checked %d messages, %d fields, %d enums, %d services, %d methods",
		c.Messages, c.Fields, c.Enums, c.Services, c.Methods)
	if c.Tables > 0 || c.Columns > 0 {
		fmt.Fprintf(w, ", %d tables, %d columns", c.Tables, c.Columns)
	}
	fmt.Fprintln(w)

	if len(r.Findings) == 0 {
		fmt.Fprintln(w, "no incompatible changes")
		return
	}
	for _, f := range r.Findings {
		fmt.Fprintln(w, f.String())
	}
	fmt.Fprintf(w, "\n%d breaking, %d risky\n", r.Count(Breaking), r.Count(Risky))
}

// JSON is the format another tool reads.
func (r *Result) JSON(w io.Writer) error {
	r.Sort()
	if r.Findings == nil {
		r.Findings = []Finding{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// GitHub prints workflow commands, so a finding appears on the pull request
// itself rather than only in a log nobody opens.
func (r *Result) GitHub(w io.Writer) {
	r.Sort()
	for _, f := range r.Findings {
		kind := "error"
		if f.Level == Risky {
			kind = "warning"
		}
		msg := fmt.Sprintf("%s: %s", f.Subject, f.Detail)
		if f.Was != "" || f.Now != "" {
			msg += fmt.Sprintf(" (was %s, now %s)", or(f.Was, "absent"), or(f.Now, "absent"))
		}
		// A newline would end the workflow command early.
		msg = strings.ReplaceAll(msg, "\n", " ")
		fmt.Fprintf(w, "::%s title=%s::%s\n", kind, f.Rule, msg)
	}
}
