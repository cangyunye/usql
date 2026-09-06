package rline

import (
	"bufio"
	"os"
	"strings"
)

// tuiHistory is the in-memory history for the TUI input engine, backed by
// the same file format the readline engine uses: one trimmed line per
// entry, appended, compacted to maxLines at load.
type tuiHistory struct {
	path  string
	lines []string
}

// historyMaxLines matches readline's HistoryLimit.
const historyMaxLines = 500

// loadTUIHistory reads the history file, dropping entries past
// historyMaxLines. A missing file yields an empty history.
func loadTUIHistory(path string) *tuiHistory {
	h := &tuiHistory{path: path}
	if path == "" {
		return h
	}
	f, err := os.Open(path)
	if err != nil {
		return h
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		h.lines = append(h.lines, sc.Text())
	}
	if len(h.lines) > historyMaxLines {
		h.lines = append([]string(nil), h.lines[len(h.lines)-historyMaxLines:]...)
	}
	return h
}

// Save appends an entry to the in-memory history and, when a file is
// configured, to disk. Empty lines are skipped (readline's own history
// semantics).
func (h *tuiHistory) Save(s string) error {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	h.lines = append(h.lines, s)
	if h.path == "" {
		return nil
	}
	f, err := os.OpenFile(h.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(s + "\n")
	return err
}

// prev returns the previous (older) history entry relative to pos, with the
// current line as the newest draft. pos == len(lines) means the draft line.
func (h *tuiHistory) prev(pos int, draft string) (int, string, bool) {
	if pos == 0 || len(h.lines) == 0 {
		return pos, "", false
	}
	if pos > len(h.lines) {
		pos = len(h.lines) + 1
	}
	pos--
	if pos == len(h.lines) {
		return pos, draft, true
	}
	return pos, h.lines[pos], true
}

// next returns the next (newer) history entry relative to pos. Moving past
// the newest entry restores the draft and returns to pos == len(lines).
func (h *tuiHistory) next(pos int, draft string) (int, string, bool) {
	if pos >= len(h.lines) {
		return pos, draft, false
	}
	pos++
	if pos == len(h.lines) {
		return pos, draft, true
	}
	return pos, h.lines[pos], true
}

// search finds the previous history entry containing query (case-folded),
// scanning from pos downwards. It returns the new position and line.
func (h *tuiHistory) search(query string, pos int) (int, string, bool) {
	if query == "" {
		return pos, "", false
	}
	q := strings.ToLower(query)
	if pos > len(h.lines) {
		pos = len(h.lines)
	}
	for i := pos - 1; i >= 0; i-- {
		if strings.Contains(strings.ToLower(h.lines[i]), q) {
			return i, h.lines[i], true
		}
	}
	return pos, "", false
}

// suggest returns the remainder of the most recent history entry that
// extends line (case-sensitive prefix match, at least one byte longer).
func (h *tuiHistory) suggest(line []rune) []rune {
	if len(line) == 0 || len(h.lines) == 0 {
		return nil
	}
	s := string(line)
	for i := len(h.lines) - 1; i >= 0; i-- {
		if len(h.lines[i]) > len(s) && strings.HasPrefix(h.lines[i], s) {
			return []rune(h.lines[i][len(s):])
		}
	}
	return nil
}
