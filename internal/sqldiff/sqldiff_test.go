package sqldiff_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/usmanvirk/contract-checks/internal/report"
	"github.com/usmanvirk/contract-checks/internal/sqldiff"
)

func read(t *testing.T, dir string) *sqldiff.Schema {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", dir, "sql", "schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := sqldiff.Parse(string(b))
	if err != nil {
		t.Fatalf("parsing %s: %v", dir, err)
	}
	return s
}

func diff(t *testing.T, from, to string) *report.Result {
	t.Helper()
	r := &report.Result{}
	sqldiff.Diff(read(t, from), read(t, to), r)
	return r
}

func rules(r *report.Result) map[string][]string {
	out := map[string][]string{}
	for _, f := range r.Findings {
		out[f.Rule] = append(out[f.Rule], f.Subject)
	}
	return out
}

func TestParse(t *testing.T) {
	s := read(t, "base")
	if len(s.Tables) != 2 {
		t.Fatalf("found %d tables, want 2: %v", len(s.Tables), s.TableNames())
	}
	l, ok := s.Tables["listings"]
	if !ok {
		t.Fatal("listings was not parsed")
	}
	if len(l.Columns) != 10 {
		t.Fatalf("listings has %d columns, want 10: %v", len(l.Columns), l.Columns)
	}

	for _, tc := range []struct {
		col     string
		typ     string
		notNull bool
		def     string
	}{
		{"id", "text", false, ""},
		{"sku", "character varying(64)", true, ""},
		{"grade", "smallint", true, ""},
		{"price_cents", "bigint", true, ""},
		{"currency", "character(3)", true, "'EUR'"},
		{"notes", "text", false, ""},
		{"created_at", "timestamp with time zone", true, "now()"},
	} {
		c, ok := l.Columns[tc.col]
		if !ok {
			t.Errorf("%s was not parsed", tc.col)
			continue
		}
		if c.Type != tc.typ {
			t.Errorf("%s type is %q, want %q", tc.col, c.Type, tc.typ)
		}
		if c.NotNull != tc.notNull {
			t.Errorf("%s not null is %v, want %v", tc.col, c.NotNull, tc.notNull)
		}
		if c.Default != tc.def {
			t.Errorf("%s default is %q, want %q", tc.col, c.Default, tc.def)
		}
	}
}

// TestTableConstraintsAreNotColumns guards the mistake a line based parser
// makes: reading PRIMARY KEY (a, b) as a column called PRIMARY.
func TestTableConstraintsAreNotColumns(t *testing.T) {
	s := read(t, "base")
	w := s.Tables["watches"]
	if len(w.Columns) != 3 {
		t.Fatalf("watches has %d columns, want 3: %v", len(w.Columns), w.Columns)
	}
	for name := range w.Columns {
		if name == "primary" || name == "constraint" || name == "unique" {
			t.Fatalf("a table constraint was parsed as a column called %q", name)
		}
	}
}

func TestNoChange(t *testing.T) {
	r := diff(t, "base", "base")
	if len(r.Findings) != 0 {
		t.Fatalf("a schema compared with itself produced %d findings: %v", len(r.Findings), r.Findings)
	}
}

func TestSafeMigrationIsNotBreaking(t *testing.T) {
	r := diff(t, "base", "compatible")
	if r.Breaking() {
		var got []string
		for _, f := range r.Findings {
			if f.Level == report.Breaking {
				got = append(got, f.Rule+" on "+f.Subject)
			}
		}
		t.Fatalf("a widening plus two safe new columns was called breaking: %v", got)
	}
}

func TestUnsafeMigrationIsCaught(t *testing.T) {
	r := diff(t, "base", "breaking")
	got := rules(r)

	for rule, subject := range map[string]string{
		"table-dropped":          "watches",
		"column-type-changed":    "listings.price_cents",
		"column-made-not-null":   "listings.notes",
		"column-added-not-null":  "listings.warehouse",
		"column-default-dropped": "listings.currency",
	} {
		subjects, fired := got[rule]
		if !fired {
			t.Errorf("rule %s did not fire", rule)
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
		t.Fatal("a migration that drops a table and adds a NOT NULL column passed")
	}
}

// TestNarrowingIsBreakingAndWideningIsNot is the distinction the whole SQL
// half turns on. Calling every type change breaking would be useless advice.
func TestNarrowingIsBreakingAndWideningIsNot(t *testing.T) {
	cases := []struct {
		from, to string
		breaking bool
	}{
		{"bigint", "integer", true},
		{"integer", "bigint", false},
		{"integer", "smallint", true},
		{"smallint", "integer", false},
		{"varchar(64)", "varchar(32)", true},
		{"varchar(32)", "varchar(64)", false},
		{"varchar(50)", "text", false},
		{"text", "varchar(50)", true},
		{"integer", "text", true},
		{"text", "integer", true},
		{"timestamptz", "timestamp with time zone", false}, // the same type spelled two ways
		{"int4", "integer", false},
		{"bool", "boolean", false},
	}

	for _, tc := range cases {
		base := "CREATE TABLE t (c " + tc.from + " NOT NULL);"
		head := "CREATE TABLE t (c " + tc.to + " NOT NULL);"
		b, err := sqldiff.Parse(base)
		if err != nil {
			t.Fatalf("parsing %q: %v", base, err)
		}
		h, err := sqldiff.Parse(head)
		if err != nil {
			t.Fatalf("parsing %q: %v", head, err)
		}
		r := &report.Result{}
		sqldiff.Diff(b, h, r)

		if r.Breaking() != tc.breaking {
			t.Errorf("%s to %s: breaking=%v, want %v (findings %v)",
				tc.from, tc.to, r.Breaking(), tc.breaking, r.Findings)
		}
	}
}

// TestAliases. Rewriting timestamptz as timestamp with time zone
// changes nothing and must not be reported, or the output fills with noise
// the first time somebody runs a formatter over the schema.
func TestAliases(t *testing.T) {
	b, _ := sqldiff.Parse(`CREATE TABLE t (a timestamptz NOT NULL, b varchar(10), c int4, d bool);`)
	h, _ := sqldiff.Parse(`CREATE TABLE t (a timestamp with time zone NOT NULL, b character varying(10), c integer, d boolean);`)
	r := &report.Result{}
	sqldiff.Diff(b, h, r)
	if len(r.Findings) != 0 {
		t.Fatalf("spelling the same types differently produced %d findings: %v", len(r.Findings), r.Findings)
	}
}

func TestAddingANullableColumnIsSafe(t *testing.T) {
	b, _ := sqldiff.Parse(`CREATE TABLE t (a text);`)
	h, _ := sqldiff.Parse(`CREATE TABLE t (a text, b text);`)
	r := &report.Result{}
	sqldiff.Diff(b, h, r)
	if r.Breaking() {
		t.Fatalf("adding a nullable column was called breaking: %v", r.Findings)
	}
}

func TestAddingANotNullColumnWithADefaultIsSafe(t *testing.T) {
	b, _ := sqldiff.Parse(`CREATE TABLE t (a text);`)
	h, _ := sqldiff.Parse(`CREATE TABLE t (a text, b smallint NOT NULL DEFAULT 0);`)
	r := &report.Result{}
	sqldiff.Diff(b, h, r)
	if r.Breaking() {
		t.Fatalf("a NOT NULL column with a default was called breaking: %v", r.Findings)
	}
}

func TestCommentsAndQuotingDoNotConfuseTheParser(t *testing.T) {
	src := `
-- a line comment with a ( bracket and a , comma
/* a block
   comment, with CREATE TABLE decoy ( x text ); inside it */
CREATE TABLE IF NOT EXISTS "Mixed Case" (
    "odd name" numeric(10,2) NOT NULL DEFAULT 0.0,  -- a trailing comment
    other text
);`
	s, err := sqldiff.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Tables) != 1 {
		t.Fatalf("found %d tables, want 1: %v", len(s.Tables), s.TableNames())
	}
	tbl, ok := s.Tables["mixed case"]
	if !ok {
		t.Fatalf("the quoted table name was not handled: %v", s.TableNames())
	}
	c, ok := tbl.Columns["odd name"]
	if !ok {
		t.Fatalf("the quoted column name was not handled: %v", tbl.Columns)
	}
	// numeric(10,2) must survive as one piece rather than being split on the
	// comma inside its own parentheses.
	if c.Type != "numeric(10,2)" {
		t.Fatalf("the column type is %q, want numeric(10,2)", c.Type)
	}
	if len(tbl.Columns) != 2 {
		t.Fatalf("found %d columns, want 2: %v", len(tbl.Columns), tbl.Columns)
	}
}

func TestSerialCarriesAnImplicitDefault(t *testing.T) {
	b, _ := sqldiff.Parse(`CREATE TABLE t (a text);`)
	h, _ := sqldiff.Parse(`CREATE TABLE t (a text, id bigserial PRIMARY KEY);`)
	r := &report.Result{}
	sqldiff.Diff(b, h, r)
	if r.Breaking() {
		t.Fatalf("adding a bigserial was called breaking, but the sequence supplies the value: %v", r.Findings)
	}
}

func TestCounts(t *testing.T) {
	r := diff(t, "base", "base")
	if r.Checked.Tables != 2 || r.Checked.Columns != 13 {
		t.Fatalf("checked %d tables and %d columns, want 2 and 13", r.Checked.Tables, r.Checked.Columns)
	}
}
