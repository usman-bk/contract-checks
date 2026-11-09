package sqldiff

import (
	"fmt"
	"sort"

	"github.com/usmanvirk/contract-checks/internal/report"
)

// Diff reports what moving from base to head does to code that is already
// running.
//
// The deploy is not atomic. For some minutes the old version and the new one
// are both live against the same database, so a change has to be compatible
// in both directions: the old code must survive the new schema, and the new
// code must survive the old one. That is a stricter rule than "the migration
// applies cleanly", and it is the rule that actually matters during a
// rolling release.
func Diff(baseS, headS *Schema, r *report.Result) {
	for _, name := range baseS.TableNames() {
		b := baseS.Tables[name]
		h, ok := headS.Tables[name]
		if !ok {
			r.Add(report.Finding{
				Level: report.Breaking, Rule: "table-dropped", Subject: name,
				Detail: "the old version of the service is still reading this table while the deploy rolls",
			})
			continue
		}
		diffColumns(name, b, h, r)
	}

	for _, name := range headS.TableNames() {
		h := headS.Tables[name]
		if _, existed := baseS.Tables[name]; existed {
			continue
		}
		_ = h // a new table is safe: nothing running refers to it yet
	}

	t, c := headS.Counts()
	r.Checked.Tables, r.Checked.Columns = t, c
}

func diffColumns(table string, b, h Table, r *report.Result) {
	names := make([]string, 0, len(b.Columns))
	for k := range b.Columns {
		names = append(names, k)
	}
	sort.Strings(names)

	for _, cn := range names {
		bc := b.Columns[cn]
		subject := fmt.Sprintf("%s.%s", table, cn)

		hc, ok := h.Columns[cn]
		if !ok {
			r.Add(report.Finding{
				Level: report.Breaking, Rule: "column-dropped", Subject: subject,
				Detail: "the old version still selects this column, so it fails the moment the migration lands",
				Was:    bc.Type,
			})
			continue
		}

		if bc.Type != hc.Type {
			level, detail := report.Risky, "type changed, which is a widening and safe for existing rows"
			if narrowing(bc.Type, hc.Type) {
				level = report.Breaking
				detail = "type narrowed, so existing values may not fit and existing writes may start failing"
			}
			r.Add(report.Finding{
				Level: level, Rule: "column-type-changed", Subject: subject,
				Detail: detail, Was: bc.Type, Now: hc.Type,
			})
		}

		if !bc.NotNull && hc.NotNull && hc.Default == "" {
			r.Add(report.Finding{
				Level: report.Breaking, Rule: "column-made-not-null", Subject: subject,
				Detail: "NOT NULL added with no default, so the old version's inserts, which omit this column, start failing",
				Was:    "nullable", Now: "not null",
			})
		}

		if bc.NotNull && !hc.NotNull {
			r.Add(report.Finding{
				Level: report.Risky, Rule: "column-made-nullable", Subject: subject,
				Detail: "the column may now be null, and code that assumed otherwise is still running",
				Was:    "not null", Now: "nullable",
			})
		}

		if bc.Default != "" && hc.Default == "" {
			r.Add(report.Finding{
				Level: report.Risky, Rule: "column-default-dropped", Subject: subject,
				Detail: "an insert that relied on this default now has to supply the value",
				Was:    bc.Default,
			})
		}
	}

	// A new column is only safe if the old version can keep inserting rows
	// without it.
	hnames := make([]string, 0, len(h.Columns))
	for k := range h.Columns {
		hnames = append(hnames, k)
	}
	sort.Strings(hnames)

	for _, cn := range hnames {
		hc := h.Columns[cn]
		if _, existed := b.Columns[cn]; existed {
			continue
		}
		if hc.NotNull && hc.Default == "" {
			r.Add(report.Finding{
				Level: report.Breaking, Rule: "column-added-not-null", Subject: fmt.Sprintf("%s.%s", table, cn),
				Detail: "a NOT NULL column with no default. The old version is still inserting rows and knows nothing about it, so every one of those inserts fails until the deploy finishes",
				Now:    hc.Type + " not null",
			})
		}
	}
}

// stringTypes are the ones whose capacity is a length rather than a range,
// so text and character varying are comparable with each other.
var stringTypes = map[string]bool{
	"text": true, "character varying": true, "character": true,
}

// narrowing reports whether moving from a to b can lose data.
//
// The distinction matters more than the detection does. A tool that calls
// every type change breaking gives advice nobody can act on, and the first
// person who needs to widen a column learns to pass the flag that turns the
// check off.
func narrowing(a, b string) bool {
	ab, bb := base(a), base(b)

	if ra, ok := intRank[ab]; ok {
		if rb, ok2 := intRank[bb]; ok2 {
			return rb < ra
		}
		// An integer becoming something that is not an integer is a change
		// no existing row is guaranteed to survive.
		return true
	}

	if stringTypes[ab] && stringTypes[bb] {
		// An absent length means unbounded, which is text, and also a
		// character varying written without one.
		wa, bounded_a := width(a)
		wb, bounded_b := width(b)
		switch {
		case !bounded_b:
			return false // going to unbounded always fits
		case !bounded_a:
			return true // unbounded to bounded may not
		default:
			return wb < wa
		}
	}

	if ab != bb {
		return true
	}

	wa, oka := width(a)
	wb, okb := width(b)
	switch {
	case oka && okb:
		return wb < wa
	case oka && !okb:
		// Losing the limit is a widening.
		return false
	case !oka && okb:
		// Gaining one is a narrowing.
		return true
	}
	return false
}
