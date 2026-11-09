// Package sqldiff reads CREATE TABLE statements and reports changes that
// would break a deploy.
//
// It parses the schema file rather than connecting to a database, because the
// check runs on a pull request, where the only thing that exists is the file.
// The parser handles the subset this schema is written in and refuses what it
// does not understand instead of guessing: a silently skipped table is a
// check that passes by looking at nothing, which is the most dangerous way
// for this tool to fail.
package sqldiff

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Column is one column, reduced to what a deploy can break.
type Column struct {
	Name     string
	Type     string // normalised lower case, with its length or precision
	NotNull  bool
	Default  string
	Position int
}

// Table is one table.
type Table struct {
	Name    string
	Columns map[string]Column
}

// Schema is a whole file or directory of them.
type Schema struct {
	Tables map[string]Table
}

func NewSchema() *Schema { return &Schema{Tables: map[string]Table{}} }

func (s *Schema) Counts() (tables, columns int) {
	tables = len(s.Tables)
	for _, t := range s.Tables {
		columns += len(t.Columns)
	}
	return
}

// TableNames is sorted, so output does not depend on map order.
func (s *Schema) TableNames() []string {
	out := make([]string, 0, len(s.Tables))
	for k := range s.Tables {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

var (
	lineComment  = regexp.MustCompile(`--[^\n]*`)
	blockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	createTable  = regexp.MustCompile(`(?is)\bCREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-zA-Z_][\w.]*|"[^"]+")\s*\(`)
)

// tableConstraint prefixes: a line starting with one of these describes the
// table, not a column, and must not be read as a column called PRIMARY.
var tableConstraints = []string{
	"primary key", "foreign key", "unique", "check", "constraint", "exclude", "like",
}

// Parse reads every CREATE TABLE in src.
func Parse(src string) (*Schema, error) {
	src = blockComment.ReplaceAllString(src, " ")
	src = lineComment.ReplaceAllString(src, "")

	s := NewSchema()
	for _, loc := range createTable.FindAllStringSubmatchIndex(src, -1) {
		name := unquote(src[loc[2]:loc[3]])
		body, err := balanced(src, loc[1]-1)
		if err != nil {
			return nil, fmt.Errorf("table %s: %w", name, err)
		}
		t := Table{Name: strings.ToLower(name), Columns: map[string]Column{}}
		for i, part := range splitTop(body) {
			col, ok, err := parseColumn(part, i)
			if err != nil {
				return nil, fmt.Errorf("table %s: %w", name, err)
			}
			if ok {
				t.Columns[col.Name] = col
			}
		}
		s.Tables[t.Name] = t
	}
	return s, nil
}

// balanced returns the text inside the parentheses that open at start.
func balanced(src string, start int) (string, error) {
	depth := 0
	inStr := false
	for i := start; i < len(src); i++ {
		c := src[i]
		switch {
		case c == '\'' && (i == 0 || src[i-1] != '\\'):
			inStr = !inStr
		case inStr:
		case c == '(':
			depth++
		case c == ')':
			depth--
			if depth == 0 {
				return src[start+1 : i], nil
			}
		}
	}
	return "", fmt.Errorf("unbalanced parentheses")
}

// splitTop splits on commas that are not inside parentheses, so a
// numeric(10,2) stays in one piece.
func splitTop(body string) []string {
	var parts []string
	depth, last, inStr := 0, 0, false
	for i := 0; i < len(body); i++ {
		switch c := body[i]; {
		case c == '\'' && (i == 0 || body[i-1] != '\\'):
			inStr = !inStr
		case inStr:
		case c == '(':
			depth++
		case c == ')':
			depth--
		case c == ',' && depth == 0:
			parts = append(parts, body[last:i])
			last = i + 1
		}
	}
	parts = append(parts, body[last:])
	return parts
}

func parseColumn(part string, pos int) (Column, bool, error) {
	f := strings.TrimSpace(part)
	if f == "" {
		return Column{}, false, nil
	}
	lower := strings.ToLower(f)
	for _, kw := range tableConstraints {
		if strings.HasPrefix(lower, kw) {
			return Column{}, false, nil
		}
	}

	name, rest, err := splitName(f)
	if err != nil {
		return Column{}, false, err
	}
	if strings.TrimSpace(rest) == "" {
		return Column{}, false, fmt.Errorf("cannot read column definition %q", f)
	}

	c := Column{Name: strings.ToLower(name), Position: pos}
	c.Type = normaliseType(rest)
	c.NotNull = regexp.MustCompile(`(?i)\bNOT\s+NULL\b`).MatchString(f)

	if m := regexp.MustCompile(`(?i)\bDEFAULT\s+(.+?)(?:\s+NOT\s+NULL|\s+PRIMARY\s+KEY|\s+UNIQUE|\s+REFERENCES\b|\s+CHECK\b|$)`).FindStringSubmatch(f); m != nil {
		c.Default = strings.TrimSpace(m[1])
	}
	// SERIAL and friends carry an implicit default and an implicit NOT NULL.
	if strings.HasPrefix(c.Type, "serial") || strings.HasPrefix(c.Type, "bigserial") {
		c.NotNull = true
		if c.Default == "" {
			c.Default = "sequence"
		}
	}
	return c, true, nil
}

// normaliseType reduces a type to the part that decides compatibility, so
// that "VARCHAR (100) NOT NULL DEFAULT ”" and "varchar(100)" compare equal.
func normaliseType(s string) string {
	s = regexp.MustCompile(`(?i)\s+(NOT\s+NULL|NULL|PRIMARY\s+KEY|UNIQUE|DEFAULT\s+.*|REFERENCES\s+.*|CHECK\s*\(.*|GENERATED\s+.*|COLLATE\s+\S+)$`).ReplaceAllString(s, "")
	for {
		t := regexp.MustCompile(`(?i)\s+(NOT\s+NULL|NULL|PRIMARY\s+KEY|UNIQUE|DEFAULT\s+.*|REFERENCES\s+.*|CHECK\s*\(.*)$`).ReplaceAllString(s, "")
		if t == s {
			break
		}
		s = t
	}
	s = strings.ToLower(strings.TrimSpace(s))
	s = regexp.MustCompile(`\s*\(\s*`).ReplaceAllString(s, "(")
	s = regexp.MustCompile(`\s*\)\s*`).ReplaceAllString(s, ")")
	s = regexp.MustCompile(`\s*,\s*`).ReplaceAllString(s, ",")

	// Spellings Postgres treats as the same type.
	alias := map[string]string{
		"int4": "integer", "int": "integer", "int8": "bigint", "int2": "smallint",
		"bool": "boolean", "float8": "double precision", "float4": "real",
		"timestamptz": "timestamp with time zone", "varchar": "character varying",
		"char": "character",
	}
	base, rest := s, ""
	if i := strings.Index(s, "("); i >= 0 {
		base, rest = s[:i], s[i:]
	}
	if a, ok := alias[strings.TrimSpace(base)]; ok {
		base = a
	}
	return strings.TrimSpace(base) + rest
}

// splitName takes the column name off the front of a definition and returns
// the rest.
//
// Splitting on whitespace is wrong, because a quoted identifier may contain
// a space, and "odd name" numeric(10,2) then parses as a column called "odd
// with a type of name. Quoted identifiers are rare and they are exactly the
// ones somebody reached for because the name was awkward.
func splitName(f string) (name, rest string, err error) {
	f = strings.TrimSpace(f)
	if strings.HasPrefix(f, `"`) {
		end := strings.Index(f[1:], `"`)
		if end < 0 {
			return "", "", fmt.Errorf("unterminated quoted name in %q", f)
		}
		return f[1 : 1+end], strings.TrimSpace(f[end+2:]), nil
	}
	i := strings.IndexFunc(f, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' })
	if i < 0 {
		return f, "", nil
	}
	return f[:i], strings.TrimSpace(f[i:]), nil
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// width returns the declared length of a type, and whether it has one.
func width(t string) (int, bool) {
	m := regexp.MustCompile(`\((\d+)\)$`).FindStringSubmatch(t)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	return n, err == nil
}

// base strips the length from a type.
func base(t string) string {
	if i := strings.Index(t, "("); i >= 0 {
		return t[:i]
	}
	return t
}

// intRank orders the integer types so that narrowing can be told from
// widening. Anything not here returns 0.
var intRank = map[string]int{"smallint": 1, "integer": 2, "bigint": 3}
