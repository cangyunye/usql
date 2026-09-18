package rline

import "unicode"

// AtStatementEnd reports whether the word at the cursor is empty and the
// statement before it is already terminated: the last non-whitespace rune
// before pos is a ';' that is not inside a string literal, quoted identifier
// or comment. Nothing sensible can be completed after a finished statement
// until a new word is typed, so completion stays quiet there.
func AtStatementEnd(line []rune, pos int) bool {
	if pos > len(line) {
		pos = len(line)
	}
	i := pos - 1
	for i >= 0 && unicode.IsSpace(line[i]) {
		i--
	}
	if i < 0 || line[i] != ';' {
		return false
	}
	return !insideQuoteOrComment(line, i)
}

// LastStatementStart returns the index just past the last ';' before pos
// that is not inside a string literal, quoted identifier or comment — the
// position where the statement under the cursor starts. It returns 0 when
// no terminator precedes the cursor, so the whole line is one statement.
func LastStatementStart(line []rune, pos int) int {
	if pos > len(line) {
		pos = len(line)
	}
	for i := pos - 1; i >= 0; i-- {
		if line[i] == ';' && !insideQuoteOrComment(line, i) {
			return i + 1
		}
	}
	return 0
}

// insideQuoteOrComment reports whether the rune at index i sits inside a
// string literal, quoted identifier or comment, tracking quote state with a
// forward scan from the start of the line. Doubled quotes (” and "") resolve
// correctly, since each toggles the state twice; block comments do not nest.
func insideQuoteOrComment(line []rune, i int) bool {
	var quote rune
	for j := 0; j < i; j++ {
		r := line[j]
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"' || r == '`':
			quote = r
		case r == '[':
			// [quoted identifier]: skip to its closing bracket
			for j < i && line[j] != ']' {
				j++
			}
		case r == '-' && j+1 < len(line) && line[j+1] == '-':
			// the rest of the line is a comment
			return true
		case r == '/' && j+1 < len(line) && line[j+1] == '*':
			k := j + 2
			for k+1 < len(line) && !(line[k] == '*' && line[k+1] == '/') {
				k++
			}
			if k+1 >= len(line) || k >= i {
				// unterminated, or closes at/after i: i is inside
				return true
			}
			j = k + 1
		}
	}
	return quote != 0
}
