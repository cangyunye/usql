package metacmd

// TUI modal connection manager backing \conns under the bubbletea input
// engine (USQL_INPUT=tui). It is a self-contained program with its own key
// bindings and a full-screen (alt screen) view; readline mode keeps the
// line-oriented manager in conns.go, which never rebinds keys.

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xo/dburl"
	"github.com/xo/usql/drivers"
	"github.com/xo/usql/env"
	"github.com/xo/usql/uitheme"
)

// connsMode is the modal's state machine.
type connsMode int

const (
	connsModeList connsMode = iota
	connsModeConfirm
	connsModeForm
)

// connsModel is the bubbletea model of the connection manager.
type connsModel struct {
	h Handler

	mode   connsMode
	names  []string // sorted named connections
	rowsel int      // highlighted list row
	err    string   // transient error line
	width  int
	height int

	// delete confirmation
	confirm string

	// program result: quit ("" when the user left) or connect
	quitKind string
	quitName string

	// form state (add or edit)
	formEdit string // name being edited ("" = add)
	formIdx  int    // active field
	formPw   string // stored password (kept when the password field stays empty)
	fields   []connsField
}

// connsField is one form field with a single-line editor.
type connsField struct {
	key    string
	label  string
	masked bool
	buf    []rune
	idx    int
}

// connsFieldSpec is the static definition of a form field.
type connsFieldSpec struct {
	key    string
	label  string
	masked bool
}

// connsFormSpecs are the form fields in entry order. The name field only
// applies to new connections.
var connsFormSpecs = []connsFieldSpec{
	{"name", "name", false},
	{"protocol", "protocol", false},
	{"username", "username", false},
	{"hostname", "hostname", false},
	{"port", "port", false},
	{"database", "database", false},
	{"parameters", "parameters", false},
	{"password", "password", true},
	{"encoding", "encoding", false},
}

// connsModal runs the bubbletea connection manager until quit or connect.
func connsModal(h Handler) error {
	stdout, stderr := h.IO().Stdout(), h.IO().Stderr()
	for {
		prog := tea.NewProgram(newConnsModel(h),
			tea.WithAltScreen(),
			tea.WithInput(h.IO().(interface{ ConsoleReader() io.Reader }).ConsoleReader()),
			tea.WithOutput(h.IO().Stdout()),
			tea.WithoutSignalHandler(),
		)
		fm, err := prog.Run()
		if err != nil {
			return nil // terminal hangup or similar: leave the manager
		}
		m := fm.(*connsModel)
		if m.quitKind != "connect" {
			return nil
		}
		// connecting may prompt for a password on the plain terminal, so it
		// runs outside the modal; a failure reopens the manager
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		err = h.Open(ctx, m.quitName)
		cancel()
		if err != nil {
			fmt.Fprintln(stderr, "connect:", err)
			continue
		}
		fmt.Fprintln(stdout, "connected to", m.quitName)
		return nil
	}
}

// newConnsModel builds the list-mode model.
func newConnsModel(h Handler) *connsModel {
	m := &connsModel{h: h, mode: connsModeList, rowsel: -1}
	m.reload()
	return m
}

// reload refreshes the sorted connection list.
func (m *connsModel) reload() {
	m.names = slices.Sorted(maps.Keys(env.Vars().Conn()))
	if m.rowsel >= len(m.names) {
		m.rowsel = len(m.names) - 1
	}
	if m.rowsel < 0 && len(m.names) > 0 {
		m.rowsel = 0
	}
}

// Init satisfies tea.Model.
func (m *connsModel) Init() tea.Cmd { return nil }

// Update dispatches a message.
func (m *connsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	}
	return m, nil
}

// handleKey processes one keystroke by mode.
func (m *connsModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case connsModeList:
		return m.handleListKey(msg)
	case connsModeConfirm:
		return m.handleConfirmKey(msg)
	default:
		return m.handleFormKey(msg)
	}
}

// handleListKey processes keys in list mode.
func (m *connsModel) handleListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key := msg.String(); key {
	case "q", "esc", "ctrl+c", "ctrl+d":
		return m, tea.Quit
	case "up", "k":
		if len(m.names) == 0 {
			return m, nil
		}
		if m.rowsel <= 0 {
			m.rowsel = len(m.names) - 1
		} else {
			m.rowsel--
		}
	case "down", "j":
		if len(m.names) == 0 {
			return m, nil
		}
		if m.rowsel+1 < len(m.names) {
			m.rowsel++
		} else {
			m.rowsel = 0
		}
	case "a":
		m.openForm("")
	case "c":
		if m.badSelection() {
			return m, nil
		}
		m.quitKind, m.quitName = "connect", m.names[m.rowsel]
		return m, tea.Quit
	case "d":
		if m.badSelection() {
			return m, nil
		}
		m.confirm = m.names[m.rowsel]
		m.mode = connsModeConfirm
		m.err = ""
	case "enter":
		if m.badSelection() {
			return m, nil
		}
		m.openForm(m.names[m.rowsel])
	default:
		// 1..9 edits the numbered row
		if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
			if n := int(key[0] - '1'); n < len(m.names) {
				m.openForm(m.names[n])
			}
		}
	}
	return m, nil
}

// badSelection reports whether a list action needs a selected row.
func (m *connsModel) badSelection() bool {
	if m.rowsel < 0 || m.rowsel >= len(m.names) {
		m.err = "select a connection first (up/down)"
		return true
	}
	return false
}

// handleConfirmKey processes keys in delete-confirmation mode.
func (m *connsModel) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch strings.ToLower(msg.String()) {
	case "y":
		if err := env.DeleteConn(m.confirm); err != nil {
			m.err = "delete: " + err.Error()
		} else {
			m.err = ""
		}
		m.confirm = ""
		m.mode = connsModeList
		m.reload()
	case "n", "esc", "ctrl+c":
		m.confirm = ""
		m.mode = connsModeList
	}
	return m, nil
}

// openForm switches to form mode, prefilled from the named connection when
// editing.
func (m *connsModel) openForm(name string) {
	m.formEdit = name
	m.formIdx = 0
	m.err = ""
	m.fields = nil
	comps := map[string]string{}
	if name != "" {
		if c, ok := env.ConnComponents(name); ok {
			for _, f := range connsFields {
				if v, ok := c[f]; ok {
					comps[f] = fmt.Sprint(v)
				}
			}
		}
		m.formPw, _ = env.Vars().GetSecret(name)
	} else {
		m.formPw = ""
	}
	for _, spec := range connsFormSpecs {
		if spec.key == "name" && name != "" {
			continue
		}
		cur := comps[spec.key]
		m.fields = append(m.fields, connsField{
			key:    spec.key,
			label:  spec.label,
			masked: spec.masked,
			buf:    []rune(cur),
			idx:    len(cur),
		})
	}
	m.mode = connsModeForm
}

// handleFormKey processes keys in form mode.
func (m *connsModel) handleFormKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := &m.fields[m.formIdx]
	switch key := msg.String(); key {
	case "esc", "ctrl+c":
		m.mode = connsModeList
		m.err = ""
		return m, nil
	case "down", "tab":
		if m.formIdx+1 < len(m.fields) {
			m.formIdx++
		} else {
			return m.saveForm()
		}
	case "up", "shift+tab":
		if m.formIdx > 0 {
			m.formIdx--
		}
	case "enter":
		return m.saveForm()
	case "left":
		if f.idx > 0 {
			f.idx--
		}
	case "right":
		if f.idx < len(f.buf) {
			f.idx++
		}
	case "home", "ctrl+a":
		f.idx = 0
	case "end", "ctrl+e":
		f.idx = len(f.buf)
	case "backspace":
		if f.idx > 0 {
			f.buf = append(f.buf[:f.idx-1], f.buf[f.idx:]...)
			f.idx--
		}
	case "delete":
		if f.idx < len(f.buf) {
			f.buf = append(f.buf[:f.idx], f.buf[f.idx+1:]...)
		}
	case "ctrl+u":
		f.buf, f.idx = nil, 0
	default:
		if rs := msg.Runes; len(rs) > 0 && !msg.Alt {
			f.buf = append(f.buf[:f.idx], append([]rune(nil), rs...)...)
			f.idx += len(rs)
		}
	}
	return m, nil
}

// saveForm validates and stores the form, staying open on errors.
func (m *connsModel) saveForm() (tea.Model, tea.Cmd) {
	vals := map[string]string{}
	name, proto := "", ""
	for _, f := range m.fields {
		v := strings.TrimSpace(string(f.buf))
		switch f.key {
		case "name":
			name = v
		case "protocol":
			proto = v
		default:
			vals[f.key] = v
		}
	}
	editing := m.formEdit != ""
	saved := m.formEdit
	if !editing {
		if name == "" {
			m.err = "name is required"
			return m, nil
		}
		if err := env.ValidIdentifier(name); err != nil {
			m.err = "invalid name: " + err.Error()
			return m, nil
		}
		if _, ok := env.Vars().GetConn(name); ok {
			m.err = fmt.Sprintf("connection %q already exists", name)
			return m, nil
		}
		saved = name
	}
	if proto == "" {
		m.err = "protocol is required"
		return m, nil
	}
	// a bare number picks a compiled-in driver
	if n, err := strconv.Atoi(proto); err == nil {
		candidates := sortedDrivers()
		if n < 1 || n > len(candidates) {
			m.err = fmt.Sprintf("driver number out of range (1..%d)", len(candidates))
			return m, nil
		}
		proto = candidates[n-1]
	}
	if _, err := dburl.Parse(proto + "://usql"); err != nil {
		m.err = "unknown protocol: " + proto
		return m, nil
	}
	pw := vals["password"]
	if pw == "" && editing {
		pw = m.formPw
	}
	if err := saveConnFields(saved, proto, vals, pw); err != nil {
		m.err = err.Error()
		return m, nil
	}
	m.mode = connsModeList
	m.err = ""
	m.reload()
	return m, nil
}

// sortedDrivers lists the compiled-in driver names.
func sortedDrivers() []string {
	available := drivers.Available()
	out := make([]string, 0, len(available))
	for k := range available {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// View renders the modal.
func (m *connsModel) View() string {
	t := uitheme.Current()
	var body string
	switch m.mode {
	case connsModeList:
		body = m.listView()
	case connsModeConfirm:
		body = t.Warn.Render(fmt.Sprintf("delete connection %q? [y/N]", m.confirm))
	default:
		body = m.formView()
	}
	var b strings.Builder
	b.WriteString(body)
	if m.err != "" {
		b.WriteString("\n")
		b.WriteString(t.ErrorMsg.Render(m.err))
	}
	return b.String()
}

// listView renders the connection table and key hints.
func (m *connsModel) listView() string {
	var b strings.Builder
	b.WriteString(connTable(m.names, m.rowsel))
	b.WriteString("\n")
	if len(m.names) == 0 {
		b.WriteString("  (no named connections yet; press a to add one)\n")
	}
	b.WriteString(uitheme.Current().Dim.Render("a add  1..n/↵ edit  c connect  d delete  q quit"))
	return b.String()
}

// formView renders the form fields with the active field's editor.
func (m *connsModel) formView() string {
	title := "add connection"
	if m.formEdit != "" {
		title = "edit connection: " + m.formEdit + "  (password: empty keeps the stored one)"
	}
	var b strings.Builder
	b.WriteString(title + "\n\n")
	for i := range m.fields {
		b.WriteString(m.fieldView(i))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	if m.fields[m.formIdx].key == "protocol" {
		b.WriteString(uitheme.Current().Dim.Render("drivers: "+strings.Join(sortedDrivers(), " ")) + "\n")
	}
	b.WriteString(uitheme.Current().Dim.Render("tab/↓ next field  ↵ saves on the last field  esc cancels"))
	return b.String()
}

// fieldView renders one field line, block cursor over the active one.
func (m *connsModel) fieldView(i int) string {
	t := uitheme.Current()
	f := &m.fields[i]
	var line string
	if i == m.formIdx {
		// masked fields render every rune as '*'
		mask := func(rs []rune) string {
			if !f.masked {
				return string(rs)
			}
			return strings.Repeat("*", len(rs))
		}
		at := f.idx
		if at > len(f.buf) {
			at = len(f.buf)
		}
		cur := " "
		if at < len(f.buf) {
			cur = mask(f.buf[at : at+1])
		}
		after := ""
		if at < len(f.buf) {
			after = mask(f.buf[at+1:])
		}
		line = mask(f.buf[:at]) + t.Selected.Render(cur) + after
	} else {
		line = string(f.buf)
		if f.masked {
			line = strings.Repeat("*", len(f.buf))
		}
	}
	label := f.label
	return t.Accent.Render(label+":") + strings.Repeat(" ", 12-len(label)) + line
}
