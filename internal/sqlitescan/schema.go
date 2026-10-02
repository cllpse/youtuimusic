package sqlitescan

import "strings"

// parseColumns pulls column names out of a CREATE TABLE statement, and
// reports which column (if any) aliases the rowid.
//
// Reading names from the schema rather than relying on position matters for
// Chromium: its cookie table has gained columns over the years, so a fixed
// index means a different column on a different browser build.
func parseColumns(sql string) (cols []string, rowidCol int) {
	rowidCol = -1
	lparen := strings.Index(sql, "(")
	rparen := strings.LastIndex(sql, ")")
	if lparen < 0 || rparen < lparen {
		return nil, -1
	}
	for _, part := range splitTopLevel(sql[lparen+1 : rparen]) {
		part = strings.TrimSpace(part)
		if part == "" || isConstraint(part) {
			continue
		}
		name, rest := firstIdentifier(part)
		if name == "" {
			continue
		}
		// A column declared INTEGER and PRIMARY KEY is the rowid, whatever
		// constraints sit between — except PRIMARY KEY DESC, which SQLite
		// keeps as an ordinary column for compatibility.
		if rowidCol < 0 && isRowidAlias(rest) {
			rowidCol = len(cols)
		}
		cols = append(cols, name)
	}
	return cols, rowidCol
}

// splitTopLevel splits on commas that are not inside parentheses or quotes.
func splitTopLevel(s string) []string {
	var out []string
	depth, start := 0, 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"' || c == '`':
			quote = c
		case c == '[':
			quote = ']' // SQL Server-style quoting, which SQLite also accepts
		case c == '(':
			depth++
		case c == ')':
			depth--
		case c == ',' && depth == 0:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// constraintKeywords begin a table constraint rather than a column.
var constraintKeywords = []string{
	"constraint", "primary", "unique", "check", "foreign",
}

func isConstraint(part string) bool {
	word := strings.ToLower(part)
	if i := strings.IndexAny(word, " \t\n("); i >= 0 {
		word = word[:i]
	}
	for _, k := range constraintKeywords {
		if word == k {
			return true
		}
	}
	return false
}

// firstIdentifier takes the column name off the front of a definition,
// unwrapping whichever quoting style was used, and returns the remainder.
func firstIdentifier(part string) (name, rest string) {
	if part == "" {
		return "", ""
	}
	var closer byte
	switch part[0] {
	case '"', '`', '\'':
		closer = part[0]
	case '[':
		closer = ']'
	}
	if closer != 0 {
		if end := strings.IndexByte(part[1:], closer); end >= 0 {
			return part[1 : 1+end], strings.TrimSpace(part[2+end:])
		}
		return "", ""
	}
	if i := strings.IndexAny(part, " \t\n("); i >= 0 {
		return part[:i], strings.TrimSpace(part[i:])
	}
	return part, ""
}

func isRowidAlias(rest string) bool {
	f := strings.Fields(strings.ToLower(rest))
	if len(f) == 0 || f[0] != "integer" {
		return false
	}
	for i := 1; i+1 < len(f); i++ {
		if f[i] == "primary" && f[i+1] == "key" {
			return i+2 >= len(f) || f[i+2] != "desc"
		}
	}
	return false
}
