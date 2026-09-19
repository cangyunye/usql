package completer_test

import (
	"context"
	"database/sql"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/xo/dburl"

	"github.com/xo/usql/drivers"
	"github.com/xo/usql/drivers/completer"
	"github.com/xo/usql/drivers/metadata"
	"github.com/xo/usql/rline"

	// the default driver set, so the checks can connect to real databases
	_ "github.com/xo/usql/internal"
)

// TestLiveFunctionCompletion verifies the function tier and the VALUES
// field:type hints against real databases: built-ins with signatures in
// expression positions, bare date functions without parens, the call-name
// guard, catalog function signatures from the cached L2, the VALUES hints
// (order and ":type" detail cross-checked against the reader), and hint
// advancement past a nested call value. Enabled when USQL_BENCH_DSN is set
// (the same comma-separated URL list the catalog bench uses).
func TestLiveFunctionCompletion(t *testing.T) {
	dsns := os.Getenv("USQL_BENCH_DSN")
	if dsns == "" {
		t.Skip("USQL_BENCH_DSN not set")
	}
	for _, dsn := range strings.Split(dsns, ",") {
		if dsn = strings.TrimSpace(dsn); dsn != "" {
			t.Run(dsn, func(t *testing.T) { funcLiveCheck(t, dsn) })
		}
	}
}

func funcLiveCheck(t *testing.T, rawDSN string) {
	t.Helper()
	u, err := dburl.Parse(rawDSN)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	db, err := sql.Open(u.Driver, u.DSN)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	real, err := drivers.NewMetadataReader(context.Background(), u, db, io.Discard,
		metadata.WithTimeout(20*time.Second), metadata.WithLimit(100000))
	if err != nil {
		t.Fatalf("metadata reader: %v", err)
	}
	comp := drivers.NewCompleter(ctx, u, db, nil,
		completer.WithContextCompletion(),
		completer.WithConnStrings(nil),
	)
	if comp == nil {
		t.Skip("driver has no completer")
	}
	live := completer.NewLive(comp).(rline.LiveCompleter)

	do := func(line string) []rline.Cand {
		cands, _, _ := live.DoLive([]rune(line), len([]rune(line)))
		return cands
	}
	wait := func(line string, cond func([]rline.Cand) bool) []rline.Cand {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		var cands []rline.Cand
		for time.Now().Before(deadline) {
			cands = do(line)
			if cond(cands) {
				return cands
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("condition not reached for %q (last: %d candidates: %v)", line, len(cands), texts(cands))
		return nil
	}
	find := func(cands []rline.Cand, text string) *rline.Cand {
		for i := range cands {
			if cands[i].Text == text {
				return &cands[i]
			}
		}
		return nil
	}

	// 1. built-ins in an expression position: "count(" inserts the paren,
	// the argument signature rides as dim detail
	cands := wait("SELECT co", func(c []rline.Cand) bool { return find(c, "count(") != nil })
	if c := find(cands, "count("); c.Kind != "function" || c.Detail != "expr)" {
		t.Errorf("count candidate = %+v, want kind function, detail %q", *c, "expr)")
	}
	if c := find(cands, "coalesce("); c == nil || c.Detail != "expr, ..)" {
		t.Errorf("coalesce candidate missing or wrong detail: %+v", c)
	}

	// 2. bare date functions complete without a paren
	cands = wait("SELECT current_", func(c []rline.Cand) bool { return find(c, "current_date") != nil })
	if c := find(cands, "current_date"); strings.HasSuffix(c.Text, "(") {
		t.Errorf("current_date = %q, want no paren", c.Text)
	}

	// 3. an empty word keeps the menu to columns and keywords
	for _, c := range do("SELECT ") {
		if c.Kind == "function" {
			t.Errorf("empty word offered function %q", c.Text)
			break
		}
	}

	// 4. inside a call's argument list functions stand down
	cands = do("SELECT coalesce(x, cou")
	for _, c := range cands {
		if c.Kind == "function" {
			t.Errorf("inside call args offered function %q", c.Text)
			break
		}
	}

	// The completer's "current schema" is the first row of the reader's
	// OnlyVisible schema list — informationschema readers answer with just
	// the current one, oracle-style readers with every visible one. Probe
	// schemas in that order.
	var schemas []string
	if sr, ok := real.(metadata.SchemaReader); ok {
		if s, err := sr.Schemas(metadata.Filter{OnlyVisible: true}); err == nil {
			for s.Next() && len(schemas) < 12 {
				schemas = append(schemas, s.Get().Schema)
			}
			s.Close()
		}
	}

	// 5. catalog function signatures: pick a stored function of the
	// completer's current schema from the reader, then complete it in a
	// SELECT position
	var catalogFn string
	if fr, ok := real.(metadata.FunctionReader); ok && len(schemas) > 0 {
		if set, err := fr.Functions(metadata.Filter{Schema: schemas[0], WithSystem: false}); err == nil {
			for set.Next() {
				f := set.Get()
				if f.Name != "" {
					catalogFn = f.Name
					break
				}
			}
			set.Close()
		}
	}
	if catalogFn != "" {
		prefix := catalogFn
		if len(prefix) > 3 {
			prefix = prefix[:3]
		}
		line := "SELECT " + prefix
		cands = wait(line, func(c []rline.Cand) bool { return find(c, catalogFn+"(") != nil })
		if c := find(cands, catalogFn+"("); c.Kind != "function" || c.Detail == "" || !strings.Contains(c.Detail, ")") {
			t.Errorf("catalog function %s candidate = %+v, want kind function and a signature detail", catalogFn, *c)
		} else {
			t.Logf("catalog signature: %s(%s", catalogFn, c.Detail)
		}
	} else {
		t.Log("current schema has no stored functions; catalog-signature check skipped")
	}

	// 6. VALUES field:type hints: discover a table in one of the visible
	// schemas, read its columns from the reader, and cross-check the hints
	// against them
	var qualified string
	var cols []metadata.Column
	if tr, ok := real.(metadata.TableReader); ok {
		cr, isCR := real.(metadata.ColumnReader)
	schemaLoop:
		for _, schema := range schemas {
			set, err := tr.Tables(metadata.Filter{Schema: schema, WithSystem: false})
			if err != nil {
				break
			}
			for set.Next() {
				row := set.Get()
				if row.Schema == "" || row.Name == "" || !isCR {
					continue
				}
				cset, err := cr.Columns(metadata.Filter{Catalog: row.Catalog, Schema: row.Schema, Parent: row.Name, WithSystem: true})
				if err != nil || cset == nil {
					continue
				}
				var cs []metadata.Column
				for cset.Next() {
					cs = append(cs, *cset.Get())
				}
				cset.Close()
				if len(cs) >= 2 {
					qualified, cols = fullQual(row.Catalog, row.Schema, row.Name), cs
					break schemaLoop
				}
			}
			set.Close()
		}
	}
	if qualified == "" {
		t.Skip("catalog has no table with two columns; VALUES checks skipped")
	}
	t.Logf("VALUES target: %s (%d columns)", qualified, len(cols))

	line := "INSERT INTO " + qualified + " VALUES ("
	cands = wait(line, func(c []rline.Cand) bool {
		return len(c) > 0 && c[0].Kind == "column" && strings.HasPrefix(c[0].Detail, ":")
	})
	for i, want := range cols {
		if i >= len(cands) {
			break
		}
		if cands[i].Text != want.Name || cands[i].Detail != ":"+want.DataType {
			t.Errorf("hint[%d] = %q%s, want %q:%s", i, cands[i].Text, cands[i].Detail, want.Name, want.DataType)
		}
	}

	// 7. a written column list sets the hint order, types still resolved
	written := "INSERT INTO " + qualified + " (" + cols[1].Name + ", " + cols[0].Name + ") VALUES ("
	cands = wait(written, func(c []rline.Cand) bool {
		return len(c) >= 2 && c[0].Text == cols[1].Name
	})
	if cands[0].Detail != ":"+cols[1].DataType || cands[1].Text != cols[0].Name ||
		cands[1].Detail != ":"+cols[0].DataType {
		t.Errorf("written-order hints = %v, want [%s:%s %s:%s]",
			cands, cols[1].Name, cols[1].DataType, cols[0].Name, cols[0].DataType)
	}

	// 8. a nested call value advances the hints to the next field
	after := written + "now(), "
	cands = wait(after, func(c []rline.Cand) bool {
		return len(c) > 0 && c[0].Text == cols[0].Name
	})
	if cands[0].Detail != ":"+cols[0].DataType {
		t.Errorf("hint after now() = %q%s, want %q:%s", cands[0].Text, cands[0].Detail, cols[0].Name, cols[0].DataType)
	}

	// 9. typing a value offers the matching built-ins after the hints
	typed := written + "100, su"
	cands = wait(typed, func(c []rline.Cand) bool { return find(c, "sum(") != nil })
	seenSum := false
	for _, c := range cands {
		if c.Text == "sum(" {
			if c.Kind != "function" {
				t.Errorf("sum candidate kind = %q, want function", c.Kind)
			}
			seenSum = true
			continue
		}
		if seenSum && c.Kind != "function" {
			t.Errorf("candidate %q (%s) after sum( is not a function", c.Text, c.Kind)
		}
	}
	if !seenSum {
		t.Errorf("no sum( among %v", texts(cands))
	}
}

// fullQual renders a possibly catalog-qualified table name for SQL; the
// pseudo-catalog "def" (MySQL's implicit catalog) is ignored.
func fullQual(catalog, schema, name string) string {
	switch {
	case catalog != "" && catalog != "def" && schema != "":
		return catalog + "." + schema + "." + name
	case schema != "":
		return schema + "." + name
	default:
		return name
	}
}

// texts extracts candidates' text for failure messages.
func texts(cands []rline.Cand) []string {
	out := make([]string, 0, len(cands))
	for _, c := range cands {
		out = append(out, c.Text)
	}
	return out
}
