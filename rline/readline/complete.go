package readline

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"sync"
)

type AutoCompleter interface {
	// Readline will pass the whole line and current offset to it
	// Completer need to pass all the candidates, and how long they shared the same characters in line
	// Example:
	//   [go, git, git-shell, grep]
	//   Do("g", 1) => ["o", "it", "it-shell", "rep"], 1
	//   Do("gi", 2) => ["t", "t-shell"], 2
	//   Do("git", 3) => ["", "-shell"], 3
	Do(line []rune, pos int) (newLine [][]rune, length int)
}

// LiveAutoCompleter is an optional interface for AutoCompleters that serve
// typing-time completions without blocking the input loop. DoLive may return
// no candidates while a background query runs, and call kick once results
// are available to re-render the menu. (usql fork)
type LiveAutoCompleter interface {
	AutoCompleter
	DoLive(line []rune, pos int) (newLine [][]rune, length int)
	SetLiveKick(kick func())
}

type TabCompleter struct{}

func (t *TabCompleter) Do([]rune, int) ([][]rune, int) {
	return [][]rune{[]rune("\t")}, 0
}

// opCompleter renders the candidate menu below the prompt line. All of its
// methods are safe for concurrent use: the input loop calls them while
// handling keys, and LiveKick calls LiveComplete from a completer's
// background goroutine to publish late-arriving candidates. (usql fork)
type opCompleter struct {
	mu    sync.Mutex
	w     io.Writer
	op    *Operation
	width int

	inCompleteMode  bool
	inSelectMode    bool
	candidate       [][]rune
	candidateSource []rune
	candidateOff    int
	candidateChoise int
	candidateColNum int
}

func newOpCompleter(w io.Writer, op *Operation, width int) *opCompleter {
	return &opCompleter{
		w:     w,
		op:    op,
		width: width,
	}
}

func (o *opCompleter) doSelectLocked() {
	if len(o.candidate) == 1 {
		o.op.buf.WriteRunes(o.candidate[0])
		o.exitCompleteModeLocked(false)
		return
	}
	o.nextCandidate(1)
	o.completeRefreshLocked()
}

func (o *opCompleter) nextCandidate(i int) {
	o.candidateChoise += i
	o.candidateChoise = o.candidateChoise % len(o.candidate)
	if o.candidateChoise < 0 {
		o.candidateChoise = len(o.candidate) + o.candidateChoise
	}
}

func (o *opCompleter) OnComplete() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.onCompleteLocked()
}

func (o *opCompleter) onCompleteLocked() bool {
	if o.width == 0 {
		return false
	}
	if o.IsInCompleteSelectMode() {
		o.doSelectLocked()
		return true
	}

	buf := o.op.buf
	rs := buf.Runes()

	if o.IsInCompleteMode() && o.candidateSource != nil && runes.Equal(rs, o.candidateSource) {
		o.enterCompleteSelectModeLocked()
		o.doSelectLocked()
		return true
	}

	o.exitCompleteSelectModeLocked()
	o.candidateSource = rs
	newLines, offset := o.op.cfg.AutoComplete.Do(rs, buf.idx)
	if len(newLines) == 0 {
		o.exitCompleteModeLocked(false)
		return true
	}

	// only Aggregate candidates in non-complete mode
	if !o.IsInCompleteMode() {
		if len(newLines) == 1 {
			buf.WriteRunes(newLines[0])
			o.exitCompleteModeLocked(false)
			return true
		}

		same, size := runes.Aggregate(newLines)
		if size > 0 {
			buf.WriteRunes(same)
			o.exitCompleteModeLocked(false)
			return true
		}
	}

	o.enterCompleteModeLocked(offset, newLines)
	return true
}

// LiveComplete refreshes the candidate menu for the current buffer without
// inserting anything into it: the display-only, typing-time variant of
// OnComplete. Candidates come from DoLive when the completer provides it,
// so cold queries need not block the input loop. (usql fork)
func (o *opCompleter) LiveComplete() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.width == 0 || o.op.cfg.AutoComplete == nil || !o.op.cfg.LiveComplete {
		return
	}
	o.exitCompleteSelectModeLocked()
	rs := o.op.buf.Runes()
	newLines, offset := o.doAutoComplete(rs, o.op.buf.idx)
	if len(newLines) == 0 {
		o.exitCompleteModeLocked(false)
		return
	}
	o.candidateSource = rs
	o.enterCompleteModeLocked(offset, newLines)
}

// doAutoComplete calls the DoLive fast path when the configured completer
// provides one, falling back to the synchronous Do. (usql fork)
func (o *opCompleter) doAutoComplete(rs []rune, idx int) ([][]rune, int) {
	if lc, ok := o.op.cfg.AutoComplete.(LiveAutoCompleter); ok {
		return lc.DoLive(rs, idx)
	}
	return o.op.cfg.AutoComplete.Do(rs, idx)
}

func (o *opCompleter) IsInCompleteSelectMode() bool {
	return o.inSelectMode
}

func (o *opCompleter) IsInCompleteMode() bool {
	return o.inCompleteMode
}

func (o *opCompleter) HandleCompleteSelect(r rune) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.handleCompleteSelectLocked(r)
}

func (o *opCompleter) handleCompleteSelectLocked(r rune) bool {
	next := true
	switch r {
	case CharEnter, CharCtrlJ:
		next = false
		o.op.buf.WriteRunes(o.op.candidate[o.op.candidateChoise])
		o.exitCompleteModeLocked(false)
	case CharLineStart:
		num := o.candidateChoise % o.candidateColNum
		o.nextCandidate(-num)
	case CharLineEnd:
		num := o.candidateColNum - o.candidateChoise%o.candidateColNum - 1
		o.candidateChoise += num
		if o.candidateChoise >= len(o.candidate) {
			o.candidateChoise = len(o.candidate) - 1
		}
	case CharBackspace:
		o.exitCompleteSelectModeLocked()
		next = false
	case CharTab, CharForward:
		o.doSelectLocked()
	case CharBell, CharInterrupt:
		o.exitCompleteModeLocked(true)
		next = false
	case CharNext:
		tmpChoise := o.candidateChoise + o.candidateColNum
		if tmpChoise >= o.getMatrixSize() {
			tmpChoise -= o.getMatrixSize()
		} else if tmpChoise >= len(o.candidate) {
			tmpChoise += o.candidateColNum
			tmpChoise -= o.getMatrixSize()
		}
		o.candidateChoise = tmpChoise
	case CharBackward, MetaShiftTab:
		o.nextCandidate(-1)
	case CharPrev:
		tmpChoise := o.candidateChoise - o.candidateColNum
		if tmpChoise < 0 {
			tmpChoise += o.getMatrixSize()
			if tmpChoise >= len(o.candidate) {
				tmpChoise -= o.candidateColNum
			}
		}
		o.candidateChoise = tmpChoise
	default:
		next = false
		o.exitCompleteSelectModeLocked()
	}
	if next {
		o.completeRefreshLocked()
		return true
	}
	return false
}

func (o *opCompleter) getMatrixSize() int {
	line := len(o.candidate) / o.candidateColNum
	if len(o.candidate)%o.candidateColNum != 0 {
		line++
	}
	return line * o.candidateColNum
}

func (o *opCompleter) OnWidthChange(newWidth int) {
	o.width = newWidth
}

func (o *opCompleter) CompleteRefresh() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.completeRefreshLocked()
}

func (o *opCompleter) completeRefreshLocked() {
	if !o.inCompleteMode {
		return
	}
	lineCnt := o.op.buf.CursorLineCount()
	colWidth := 0
	for _, c := range o.candidate {
		w := runes.WidthAll(c)
		if w > colWidth {
			colWidth = w
		}
	}
	colWidth += o.candidateOff + 1
	same := o.op.buf.RuneSlice(-o.candidateOff)

	// -1 to avoid reach the end of line
	width := o.width - 1
	colNum := width / colWidth
	colWidth += (width - (colWidth * colNum)) / colNum

	o.candidateColNum = colNum
	buf := bufio.NewWriter(o.w)
	buf.Write(bytes.Repeat([]byte("\n"), lineCnt))

	// Line skipper // Page behaviour
	lineSkip := 0
	if o.IsInCompleteSelectMode() {
		targetPage := (o.candidateChoise / colNum) / o.op.cfg.MaxCompleteLines
		lineSkip = targetPage * o.op.cfg.MaxCompleteLines
	}

	lines := 1 // do not count first line
	realLines := 0
	buf.WriteString("\033[J")
	for idx, c := range o.candidate {
		colIdx := idx % colNum // currentLine Index
		realLines = idx/colNum + 1
		if colIdx == 0 && idx != 0 && realLines > lineSkip+1 { // If its not the first char we increase a line
			buf.WriteString("\n") // Print the line
			lines++               // Increase the display line
		}
		if lines > o.op.cfg.MaxCompleteLines { // If lines greater than max, we exit
			break
		}
		if realLines <= lineSkip { // Ignore content for the first lines
			continue
		}
		inSelect := idx == o.candidateChoise && o.IsInCompleteSelectMode()
		if inSelect {
			buf.WriteString("\033[30;47m")
		}
		buf.WriteString(string(same))
		buf.WriteString(string(c))
		buf.Write(bytes.Repeat([]byte(" "), colWidth-len(c)-len(same)))

		if inSelect {
			buf.WriteString("\033[0m")
		}
	}

	// move back
	fmt.Fprintf(buf, "\033[%dA\r", lineCnt-1+lines)
	fmt.Fprintf(buf, "\033[%dC", o.op.buf.idx+o.op.buf.PromptLen())
	buf.Flush()
}

func (o *opCompleter) aggCandidate(candidate [][]rune) int {
	offset := 0
	for i := 0; i < len(candidate[0]); i++ {
		for j := 0; j < len(candidate)-1; j++ {
			if i > len(candidate[j]) {
				goto aggregate
			}
			if candidate[j][i] != candidate[j+1][i] {
				goto aggregate
			}
		}
		offset = i
	}
aggregate:
	return offset
}

func (o *opCompleter) EnterCompleteSelectMode() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.enterCompleteSelectModeLocked()
}

func (o *opCompleter) enterCompleteSelectModeLocked() {
	o.inSelectMode = true
	o.candidateChoise = -1
	o.completeRefreshLocked()
}

func (o *opCompleter) EnterCompleteMode(offset int, candidate [][]rune) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.enterCompleteModeLocked(offset, candidate)
}

func (o *opCompleter) enterCompleteModeLocked(offset int, candidate [][]rune) {
	o.inCompleteMode = true
	o.candidate = candidate
	o.candidateOff = offset
	o.completeRefreshLocked()
}

func (o *opCompleter) ExitCompleteSelectMode() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.exitCompleteSelectModeLocked()
}

func (o *opCompleter) exitCompleteSelectModeLocked() {
	o.inSelectMode = false
	o.candidate = nil
	o.candidateChoise = -1
	o.candidateOff = -1
	o.candidateSource = nil
}

func (o *opCompleter) ExitCompleteMode(revent bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.exitCompleteModeLocked(revent)
}

func (o *opCompleter) exitCompleteModeLocked(bool) {
	o.inCompleteMode = false
	o.exitCompleteSelectModeLocked()
}
