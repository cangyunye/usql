package rline

import "testing"

func TestAtStatementEnd(t *testing.T) {
	cases := []struct {
		name string
		line string
		pos  int
		exp  bool
	}{
		{"cursor right after terminator", "select 1;", 10, true},
		{"trailing space after terminator", "select 1; ", 11, true},
		{"new word started", "select 1; sel", 12, false},
		{"meta command after terminator", `select 1; \dt `, 14, false},
		{"no terminator", "select 1", 8, false},
		{"empty line", "", 0, false},
		{"mid-statement semicolon in string", "select ';' as x;", 16, true},
		{"cursor inside string before terminator", "select ';", 9, false},
		{"semicolon inside string then real one", "select ';' ; ", 14, true},
		{"line comment swallows semicolon", "select 1 -- ;", 13, false},
		{"block comment swallows semicolon", "select 1 /* ; */;", 17, true},
		{"unterminated block comment", "select 1 /* ;", 13, false},
		{"quoted identifier with semicolon", `select "a;b";`, 13, true},
		{"second statement mid-typing", "select 1; select ", 17, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := AtStatementEnd([]rune(c.line), c.pos); got != c.exp {
				t.Errorf("AtStatementEnd(%q, %d) = %v, expected %v", c.line, c.pos, got, c.exp)
			}
		})
	}
}
