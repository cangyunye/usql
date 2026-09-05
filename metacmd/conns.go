package metacmd

// Interactive named-connection manager backing the \conns meta command.
//
// Keyboard policy: this manager never rebinds readline keys and never reads
// single keystrokes. All input is entered on ordinary readline prompt lines,
// so every editing binding (arrows, Ctrl-A/E/W/U, history, tab) keeps working
// unchanged; the menu "hotkeys" below are plain printable words/numbers typed
// at the menu prompt only:
//
//	a | add        add a new named connection (form)
//	<row number>   edit the connection in that table row (form)
//	c <name|#>     connect to a named connection
//	d <name|#>     delete a named connection (asks for confirmation)
//	q | quit       leave the manager
//
// There are no global shortcuts, so nothing conflicts with readline, the
// completer, or other meta commands.

import (
	"context"
	"fmt"
	"maps"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/xo/dburl"
	"github.com/xo/usql/drivers"
	"github.com/xo/usql/env"
	"github.com/xo/usql/text"
)

// connsFields are the form fields of a named connection, in entry order.
var connsFields = []string{"protocol", "username", "hostname", "port", "database", "parameters"}

// fileScheme reports whether a protocol addresses a local file database,
// whose database field is a path.
var fileScheme = map[string]bool{
	"file": true, "sqlite3": true, "sqlite": true, "sq": true,
	"duckdb": true, "csvq": true,
}

// connTable renders the named-connection list as a bordered table.
func connTable(names []string) string {
	headers := []string{"#", "name", "source", "driver", "user", "host", "port", "database", "password"}
	rows := make([][]string, 0, len(names))
	for i, name := range names {
		comps, _ := env.ConnComponents(name)
		src := env.Vars().GetConnSource(name)
		if src == "" {
			src = "session"
		}
		_, hasPw := env.Vars().GetSecret(name)
		row := []string{strconv.Itoa(i + 1), name, src, "-", "", "", "", "", ""}
		if v, ok := comps["protocol"]; ok {
			row[3] = fmt.Sprint(v)
		}
		if v, ok := comps["username"]; ok {
			row[4] = fmt.Sprint(v)
		}
		if v, ok := comps["hostname"]; ok {
			row[5] = fmt.Sprint(v)
		}
		if v, ok := comps["port"]; ok {
			row[6] = fmt.Sprint(v)
		}
		if v, ok := comps["database"]; ok {
			row[7] = fmt.Sprint(v)
		}
		if hasPw {
			row[8] = "set"
		}
		rows = append(rows, row)
	}
	style := lipgloss.NewStyle()
	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(style.Foreground(lipgloss.Color("240"))).
		Headers(headers...).
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			switch {
			case row == 0:
				return style.Bold(true)
			case row%2 == 0:
				return style.Background(lipgloss.Color("235"))
			default:
				return style
			}
		})
	return t.Render()
}

// connsManage runs the interactive connection manager loop.
func connsManage(h Handler) error {
	stdout, stderr := h.IO().Stdout(), h.IO().Stderr()
	for {
		names := slices.Sorted(maps.Keys(env.Vars().Conn()))
		fmt.Fprintln(stdout, connTable(names))
		if len(names) == 0 {
			fmt.Fprintln(stdout, "  (no named connections yet; type `a` to add one)")
		}
		v, err := h.ReadVar("string", "\\conns> [a]dd  [1..n] edit  [c <name|#>]onnect  [d <name|#>]elete  [q]uit: ")
		if err != nil {
			return nil // ^C / ^D / EOF leaves the manager
		}
		fields := strings.Fields(strings.TrimSpace(v))
		if len(fields) == 0 {
			continue
		}
		cmd := strings.ToLower(fields[0])
		switch {
		case cmd == "q" || cmd == "quit":
			return nil
		case cmd == "a" || cmd == "add":
			connsAdd(h)
		case cmd == "c" || cmd == "connect" || cmd == "d" || cmd == "delete":
			target := ""
			if len(fields) > 1 {
				target = fields[1]
			}
			if target == "" {
				if target, err = askField(h, "name or #", "", false); err != nil {
					return nil
				}
				target = strings.TrimSpace(target)
			}
			name := resolveConnTarget(names, target)
			if name == "" {
				fmt.Fprintln(stderr, "no such connection:", target)
				continue
			}
			switch {
			case cmd == "c" || cmd == "connect":
				ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
				if err := h.Open(ctx, name); err != nil {
					cancel()
					fmt.Fprintln(stderr, "connect:", err)
					continue
				}
				cancel()
				fmt.Fprintln(stdout, "connected to", name)
				return nil
			case cmd == "d" || cmd == "delete":
				confirm, err := h.ReadVar("string", fmt.Sprintf("delete connection %q? [y/N]: ", name))
				if err != nil {
					return nil
				}
				if strings.ToLower(strings.TrimSpace(confirm)) != "y" {
					continue
				}
				if err := env.DeleteConn(name); err != nil {
					fmt.Fprintln(stderr, "delete:", err)
					continue
				}
				fmt.Fprintln(stdout, "deleted connection", name)
			}
		default:
			// row number shortcut for edit
			if n, err := strconv.Atoi(cmd); err == nil && n >= 1 && n <= len(names) {
				connsEdit(h, names[n-1])
				continue
			}
			fmt.Fprintln(stderr, "unknown command:", cmd, "(type q to leave)")
		}
	}
}

// resolveConnTarget resolves a table row number or connection name.
func resolveConnTarget(names []string, target string) string {
	if n, err := strconv.Atoi(target); err == nil && n >= 1 && n <= len(names) {
		return names[n-1]
	}
	if _, ok := env.Vars().GetConn(target); ok {
		return target
	}
	return ""
}

// connsAdd runs the add-connection form.
func connsAdd(h Handler) {
	if err := connsForm(h, "", false); err != nil {
		fmt.Fprintln(h.IO().Stderr(), "add:", err)
	}
}

// connsEdit runs the edit-connection form for an existing connection.
func connsEdit(h Handler, name string) {
	if err := connsForm(h, name, true); err != nil {
		fmt.Fprintln(h.IO().Stderr(), "edit:", err)
	}
}

// connsForm collects the fields of a connection. When name is non-empty the
// form is prefilled from the stored connection and updates it; otherwise a
// new connection is created.
func connsForm(h Handler, name string, editing bool) error {
	stdout := h.IO().Stdout()
	if editing {
		fmt.Fprintln(stdout, lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Padding(0, 1).
			Render("edit connection: "+name+"\n(leave a field empty to keep its current value)"))
	} else {
		fmt.Fprintln(stdout, lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Padding(0, 1).
			Render("add connection\n(leave a field empty to skip it)"))
	}

	comps := map[string]string{}
	if editing {
		if c, ok := env.ConnComponents(name); ok {
			for _, f := range connsFields {
				if v, ok := c[f]; ok {
					comps[f] = fmt.Sprint(v)
				}
			}
		}
	}

	// name
	if !editing {
		n, err := askField(h, "name (identifier, e.g. ob_oracle)", "", false)
		if err != nil {
			return err
		}
		name = strings.TrimSpace(n)
		if name == "" {
			return text.ErrMissingRequiredArgument
		}
		if err := env.ValidIdentifier(name); err != nil {
			return fmt.Errorf("%w (only letters, digits, and _ are allowed, e.g. ob_oracle)", err)
		}
		if _, ok := env.Vars().GetConn(name); ok {
			return fmt.Errorf("connection %q already exists", name)
		}
	}

	// protocol
	proto, err := askProtocol(h, comps["protocol"])
	if err != nil {
		return err
	}

	// remaining fields
	def := map[string]string{}
	if v, ok := comps["username"]; ok {
		def["username"] = v
	}
	user := def["username"]
	if user == "" {
		user = h.User().Username
	}
	vals := map[string]string{}
	for _, f := range []string{"username", "hostname", "port", "database", "parameters"} {
		label := f
		if f == "username" {
			label = "username (e.g. user@tenant or root@obmysql)"
		}
		val, err := askField(h, label, comps[f], false)
		if err != nil {
			return err
		}
		vals[f] = strings.TrimSpace(val)
	}
	if vals["username"] == "" {
		vals["username"] = user
	}
	for _, f := range []string{"username", "hostname", "port", "database", "parameters"} {
		if vals[f] == "" {
			delete(vals, f)
		}
	}

	// password (masked): empty keeps the stored password when editing
	oldPw, hasOld := env.Vars().GetSecret(name)
	if !editing {
		hasOld = false
	}
	pw := ""
	if hasOld {
		fmt.Fprintln(stdout, "(password already set; leave empty to keep it)")
	}
	if pw, err = askField(h, "password", "", true); err != nil {
		return err
	}
	pw = strings.TrimSpace(pw)
	if pw == "" && hasOld {
		pw = oldPw
	}

	// assemble and save
	components := map[string]any{"protocol": proto}
	for k, v := range vals {
		components[k] = v
	}
	if db := vals["database"]; db != "" {
		_, hasHost := vals["hostname"]
		// local file databases use the raw path component (dburl escapes the
		// database component and would double-escape absolute paths); server
		// databases with multi-segment paths (instance/db) use a path too.
		delete(components, "database")
		switch {
		case !hasHost && fileScheme[proto]:
			components["path"] = db
		case !hasHost && strings.HasPrefix(db, "/"):
			components["path"] = db
		case hasHost && strings.Contains(db, "/"):
			components["path"] = "/" + strings.TrimLeft(db, "/")
		default:
			components["database"] = db
		}
	}
	if err := env.SaveConn(name, components, pw); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "saved connection", name)
	return nil
}

// askProtocol offers the compiled-in drivers and returns a validated protocol
// scheme name.
func askProtocol(h Handler, current string) (string, error) {
	available := drivers.Available()
	candidates := make([]string, 0, len(available))
	for k := range available {
		candidates = append(candidates, k)
	}
	slices.Sort(candidates)
	stdout := h.IO().Stdout()
	// offer in rows of four
	var sb strings.Builder
	for i, c := range candidates {
		if i > 0 && i%4 == 0 {
			sb.WriteString("\n")
		}
		fmt.Fprintf(&sb, "%2d) %-14s", i+1, c)
	}
	fmt.Fprintln(stdout, "drivers:", sb.String())
	def := ""
	if current != "" {
		def = current
	}
	for {
		v, err := askField(h, "protocol [# or name]", def, false)
		if err != nil {
			return "", err
		}
		v = strings.TrimSpace(v)
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= len(candidates) {
			return candidates[n-1], nil
		}
		if v == "" {
			fmt.Fprintln(h.IO().Stderr(), "protocol is required")
			continue
		}
		// accept names of compiled-in drivers and any scheme alias
		if _, err := dburl.Parse(v + "://usql"); err == nil {
			return v, nil
		}
		fmt.Fprintln(h.IO().Stderr(), "unknown protocol:", v)
	}
}

// askField prompts for a field value. When def is non-empty and the user
// enters nothing, def is returned. masked input hides the typed characters.
func askField(h Handler, label, def string, masked bool) (string, error) {
	typ := "string"
	if masked {
		typ = "password"
	}
	prompt := label + ": "
	if def != "" && !masked {
		prompt = label + " [" + def + "]: "
	}
	v, err := h.ReadVar(typ, prompt)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(v) == "" {
		return def, nil
	}
	return v, nil
}
