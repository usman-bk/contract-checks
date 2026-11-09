package report_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/usmanvirk/contract-checks/internal/report"
)

func sample() *report.Result {
	r := &report.Result{}
	r.Add(report.Finding{Level: report.Risky, Rule: "enum-value-added", Subject: ".a.B.C", Detail: "new value"})
	r.Add(report.Finding{Level: report.Breaking, Rule: "rpc-removed", Subject: ".a.S/Z", Detail: "gone"})
	r.Add(report.Finding{Level: report.Breaking, Rule: "field-removed", Subject: ".a.M.f", Detail: "gone", Was: "string f = 1"})
	r.Checked = report.Checked{Messages: 3, Fields: 9, Services: 1, Methods: 2}
	return r
}

func TestBreakingFindingsComeFirst(t *testing.T) {
	r := sample()
	r.Sort()
	if r.Findings[0].Level != report.Breaking || r.Findings[1].Level != report.Breaking {
		t.Fatalf("risky findings are mixed in with breaking ones: %+v", r.Findings)
	}
	if r.Findings[len(r.Findings)-1].Level != report.Risky {
		t.Fatal("the risky finding is not last")
	}
}

func TestSortIsStable(t *testing.T) {
	a, b := sample(), sample()
	a.Sort()
	b.Sort()
	for i := range a.Findings {
		if a.Findings[i] != b.Findings[i] {
			t.Fatalf("two sorts of the same findings differ at %d:\n %+v\n %+v", i, a.Findings[i], b.Findings[i])
		}
	}
}

func TestBreakingReportsWhetherTheJobFails(t *testing.T) {
	if !sample().Breaking() {
		t.Fatal("a result with two breaking findings says the job passes")
	}
	only := &report.Result{}
	only.Add(report.Finding{Level: report.Risky, Rule: "enum-value-added", Subject: "x", Detail: "y"})
	if only.Breaking() {
		t.Fatal("a result with only risky findings fails the job, which would make teams disable the check")
	}
	if (&report.Result{}).Breaking() {
		t.Fatal("an empty result fails the job")
	}
}

func TestTextAlwaysSaysWhatWasChecked(t *testing.T) {
	var buf bytes.Buffer
	clean := &report.Result{Checked: report.Checked{Messages: 4, Fields: 11, Services: 2, Methods: 7}}
	clean.Text(&buf)
	out := buf.String()
	// A clean run has to state its coverage, or a run that checked nothing
	// looks exactly like a run that found nothing.
	for _, want := range []string{"4 messages", "11 fields", "7 methods", "no incompatible changes"} {
		if !strings.Contains(out, want) {
			t.Errorf("clean output does not contain %q:\n%s", want, out)
		}
	}
}

func TestTextCounts(t *testing.T) {
	var buf bytes.Buffer
	sample().Text(&buf)
	if !strings.Contains(buf.String(), "2 breaking, 1 risky") {
		t.Fatalf("the summary line is wrong:\n%s", buf.String())
	}
}

func TestJSONOfAnEmptyResultHasAnArrayNotNull(t *testing.T) {
	var buf bytes.Buffer
	if err := (&report.Result{}).JSON(&buf); err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	// null would make a consumer written in almost any language fall over
	// on a clean run, which is the run it will see most often.
	if _, ok := parsed["findings"].([]any); !ok {
		t.Fatalf("findings is %T on a clean run, want an array:\n%s", parsed["findings"], buf.String())
	}
}

func TestGitHubUsesErrorForBreakingAndWarningForRisky(t *testing.T) {
	var buf bytes.Buffer
	sample().GitHub(&buf)
	out := buf.String()
	if strings.Count(out, "::error ") != 2 {
		t.Fatalf("want two errors:\n%s", out)
	}
	if strings.Count(out, "::warning ") != 1 {
		t.Fatalf("want one warning:\n%s", out)
	}
}

// TestGitHubPutsEachFindingOnOneLine. A newline inside a workflow command
// ends it early, and the remainder is printed as ordinary log text.
func TestGitHubPutsEachFindingOnOneLine(t *testing.T) {
	r := &report.Result{}
	r.Add(report.Finding{
		Level: report.Breaking, Rule: "field-removed", Subject: ".a.M.f",
		Detail: "gone", Was: "string f = 1", Now: "",
	})
	var buf bytes.Buffer
	r.GitHub(&buf)

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("one finding produced %d lines:\n%s", len(lines), buf.String())
	}
	if !strings.Contains(lines[0], "was string f = 1") || !strings.Contains(lines[0], "now absent") {
		t.Fatalf("the before and after are missing from the line: %s", lines[0])
	}
}
