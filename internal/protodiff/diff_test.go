package protodiff_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usmanvirk/contract-checks/internal/protodiff"
	"github.com/usmanvirk/contract-checks/internal/report"
)

func load(t *testing.T, dir string) *protodiff.Model {
	t.Helper()
	m, err := protodiff.Load(context.Background(), filepath.Join("..", "..", "testdata", dir, "proto"))
	if err != nil {
		t.Fatalf("loading %s: %v", dir, err)
	}
	return m
}

func diff(t *testing.T, from, to string) *report.Result {
	t.Helper()
	r := &report.Result{}
	protodiff.Diff(load(t, from), load(t, to), r)
	return r
}

// rules returns the rule names that fired, so a test can say what it expects
// without depending on wording.
func rules(r *report.Result) map[string][]string {
	out := map[string][]string{}
	for _, f := range r.Findings {
		out[f.Rule] = append(out[f.Rule], f.Subject)
	}
	return out
}

func TestIdenticalProtosAreClean(t *testing.T) {
	r := diff(t, "base", "base")
	if len(r.Findings) != 0 {
		t.Fatalf("comparing a contract with itself found %d problems: %v", len(r.Findings), r.Findings)
	}
	if r.Breaking() {
		t.Fatal("identical contracts reported as breaking")
	}
}

// TestAdditionsAreNotBreaking is the half that decides whether anybody keeps
// the check switched on. A tool that fails on a new field gets removed.
func TestAdditionsAreNotBreaking(t *testing.T) {
	r := diff(t, "base", "compatible")
	if r.Breaking() {
		var got []string
		for _, f := range r.Findings {
			if f.Level == report.Breaking {
				got = append(got, f.Rule+" on "+f.Subject)
			}
		}
		t.Fatalf("adding fields, messages and an endpoint was called breaking: %s", strings.Join(got, "; "))
	}
}

func TestCheckedCounts(t *testing.T) {
	r := diff(t, "base", "base")
	if r.Checked.Messages == 0 || r.Checked.Fields == 0 || r.Checked.Services == 0 || r.Checked.Methods == 0 {
		t.Fatalf("a run reported checking nothing: %+v", r.Checked)
	}
	if r.Checked.Methods != 3 {
		t.Fatalf("found %d methods, want 3", r.Checked.Methods)
	}
}

func TestBreakingChangesAreCaught(t *testing.T) {
	r := diff(t, "base", "breaking")
	got := rules(r)

	want := map[string]string{
		"field-renamed":             ".marketplace.v1.Listing field 3",
		"field-type-changed":        ".marketplace.v1.Listing.price",
		"field-cardinality-changed": ".marketplace.v1.Listing.image_urls",
		"field-removed":             ".marketplace.v1.Listing.in_stock",
		"field-number-reused":       ".marketplace.v1.SearchResponse field 3",
		"enum-value-removed":        ".marketplace.v1.Grade.GRADE_FAIR",
		"rpc-removed":               ".marketplace.v1.Catalogue/Watch",
		"rpc-streaming-changed":     ".marketplace.v1.Catalogue/Search",
		"message-removed":           ".marketplace.v1.WatchResponse",
	}
	for rule, subject := range want {
		subjects, fired := got[rule]
		if !fired {
			t.Errorf("rule %s did not fire at all", rule)
			continue
		}
		found := false
		for _, s := range subjects {
			if s == subject {
				found = true
			}
		}
		if !found {
			t.Errorf("rule %s fired on %v, want it on %s", rule, subjects, subject)
		}
	}

	if !r.Breaking() {
		t.Fatal("a contract with nine breaking changes in it passed")
	}
}

// TestRemovingAFieldIsBreakingEvenWhenReserved. Reserving the number is the
// right thing to do and it does not give the client the field back.
func TestRemovingAFieldIsBreakingEvenWhenReserved(t *testing.T) {
	r := diff(t, "base", "breaking")
	for _, f := range r.Findings {
		if f.Rule == "field-removed" && f.Subject == ".marketplace.v1.Listing.in_stock" {
			if f.Level != report.Breaking {
				t.Fatalf("a reserved removal was reported as %s", f.Level)
			}
			if !strings.Contains(f.Detail, "reserved") {
				t.Fatalf("the finding does not mention that the number was reserved: %s", f.Detail)
			}
			return
		}
	}
	t.Fatal("removing in_stock was not reported")
}

// TestAddingAnEnumValueIsRiskyNotBreaking. It does not break the wire, and a
// client that has never heard of the value still has to do something with it.
func TestAddingAnEnumValueIsRiskyNotBreaking(t *testing.T) {
	r := diff(t, "base", "compatible")
	protodiff.Diff(load(t, "base"), load(t, "breaking"), r)

	var found bool
	for _, f := range r.Findings {
		if f.Rule == "enum-value-added" {
			found = true
			if f.Level != report.Risky {
				t.Fatalf("a new enum value was reported as %s, which would make teams switch the check off", f.Level)
			}
		}
	}
	if !found {
		t.Fatal("adding GRADE_ACCEPTABLE was not reported at all")
	}
}

// TestNumberReuseIsReportedOnceAsReuse. A number that now has a different
// name and a different type is recycling, not a rename, and reporting it
// twice sends somebody looking for two problems.
func TestNumberReuseIsReportedOnceAsReuse(t *testing.T) {
	r := diff(t, "base", "breaking")
	n := 0
	for _, f := range r.Findings {
		if strings.HasPrefix(f.Subject, ".marketplace.v1.SearchResponse field 3") {
			n++
			if f.Rule != "field-number-reused" {
				t.Fatalf("recycled number reported as %s", f.Rule)
			}
		}
	}
	if n != 1 {
		t.Fatalf("the recycled number produced %d findings, want 1", n)
	}
}

func TestDiffIsStable(t *testing.T) {
	first := diff(t, "base", "breaking")
	first.Sort()
	for i := 0; i < 5; i++ {
		again := diff(t, "base", "breaking")
		again.Sort()
		if len(again.Findings) != len(first.Findings) {
			t.Fatalf("run %d produced %d findings, first produced %d", i, len(again.Findings), len(first.Findings))
		}
		for j := range again.Findings {
			if again.Findings[j] != first.Findings[j] {
				t.Fatalf("run %d finding %d differs:\n %+v\n %+v", i, j, again.Findings[j], first.Findings[j])
			}
		}
	}
}

func TestLoadRefusesADirectoryWithNoProtos(t *testing.T) {
	dir := t.TempDir()
	if _, err := protodiff.Load(context.Background(), dir); err == nil {
		t.Fatal("an empty directory loaded without complaint, so a mistyped path would pass the check")
	}
}

func TestLoadFailsOnAProtoThatDoesNotCompile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad.proto"), []byte("syntax = \"proto3\"; message {"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := protodiff.Load(context.Background(), dir); err == nil {
		t.Fatal("a proto that does not compile was loaded as though it were fine")
	}
}

// TestMapsAndNestedTypes guards the two shapes that break a naive
// walker: a map field generates a synthetic entry message, and nested types
// have to be reached at all.
func TestMapsAndNestedTypes(t *testing.T) {
	dir := t.TempDir()
	src := `syntax = "proto3";
package t;
message Outer {
  map<string, string> labels = 1;
  message Inner { string a = 1; }
  Inner inner = 2;
  enum Kind { KIND_UNSPECIFIED = 0; KIND_A = 1; }
  Kind kind = 3;
}`
	if err := os.WriteFile(filepath.Join(dir, "t.proto"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := protodiff.Load(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Messages[".t.Outer.Inner"]; !ok {
		t.Fatalf("the nested message was not collected: %v", keys(m.Messages))
	}
	if _, ok := m.Enums[".t.Outer.Kind"]; !ok {
		t.Fatalf("the nested enum was not collected: %v", keys(m.Enums))
	}
	for name := range m.Messages {
		if strings.HasSuffix(name, "LabelsEntry") {
			t.Fatalf("the synthetic map entry message was collected as part of the contract: %s", name)
		}
	}
	outer := m.Messages[".t.Outer"]
	if outer.Fields[1].Card != protodiff.Map {
		t.Fatalf("the map field was recorded as %s", outer.Fields[1].Card)
	}
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
