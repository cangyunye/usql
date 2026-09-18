package completer

import (
	"reflect"
	"testing"
)

// TestMetaPolicyTable pins the declarative meta-command argument policy:
// known commands complete per the table (with display kinds), display
// variants resolve to the base command, and unknown commands never produce
// candidates.
func TestMetaPolicyTable(t *testing.T) {
	c := NewDefaultCompleter(
		WithReader(candMockReader{}),
		WithLogger(discardLogger()),
	).(*completer)

	do := func(line string) []string {
		cands, _, _ := c.DoRepl([]rune(line), len([]rune(line)))
		if cands == nil {
			return nil
		}
		out := make([]string, 0, len(cands))
		for _, c := range cands {
			out = append(out, c.Text)
		}
		return out
	}

	// unknown command: no rule, never SQL keywords
	if got := do(`\encoding `); got != nil {
		t.Errorf(`\encoding: got %v, want nil`, got)
	}
	// \e is files; \encoding must not inherit it via a prefix match
	if got := do(`\e `); len(got) == 0 {
		t.Error(`\e: want file candidates, got none`)
	}
	// display variants share the base command's policy
	for _, cmd := range []string{`\dt`, `\dt+`, `\dtS`, `\dtS+`} {
		got := do(cmd + " ")
		if !reflect.DeepEqual(got, []string{"other", "public"}) {
			t.Errorf("%s: got %v, want [other public]", cmd, got)
		}
	}
	// \pset: setting names (fuzzy-ranked), then per-setting values
	if got, want := do(`\pset `), psetSettings; len(got) != len(want) {
		t.Errorf(`\pset: got %d settings, want %d`, len(got), len(want))
	}
	if got := do(`\pset format al`); !reflect.DeepEqual(got, []string{"aligned", "latex-longtable", "vertical", "unaligned"}) {
		t.Errorf(`\pset format al: got %v`, got)
	}
	if got := do(`\pset format `); len(got) != 11 {
		t.Errorf(`\pset format: got %d values, want 11`, len(got))
	}
	// \pset with a setting that has no value list: nothing
	if got := do(`\pset title `); got != nil {
		t.Errorf(`\pset title: got %v, want nil`, got)
	}
	// \copy takes exactly one argument
	if got := do(`\copy x `); got != nil {
		t.Errorf(`\copy x: got %v, want nil`, got)
	}
}

// TestKindsAttached checks that context and listing candidates carry the
// display kinds the menu badges render.
func TestKindsAttached(t *testing.T) {
	c := completer{reader: candMockReader{}, logger: discardLogger(), schemaKind: "schema"}
	WithContextCompletion()(&c)
	waitForSnapshot(t, c.snap)

	cands, _ := c.Do([]rune("SELECT * FROM fi"), 16)
	if len(cands) != 2 {
		t.Fatalf("FROM fi = %v, want 2 candidates", cands)
	}
	if cands[0].Kind != "table" || cands[1].Kind != "view" {
		t.Errorf("FROM fi kinds = %q,%q, want table,view", cands[0].Kind, cands[1].Kind)
	}

	cands, _ = c.Do([]rune("SELECT * FROM "), 14)
	if len(cands) == 0 || cands[0].Kind != "schema" {
		t.Errorf("FROM schema candidates: first = %+v, want kind schema", cands[0])
	}

	cands, _ = c.Do([]rune("SELECT * FROM film WHERE "), 25)
	for _, c := range cands {
		if c.Text == "id" && c.Kind != "column" {
			t.Errorf("column candidate kind = %q, want column", c.Kind)
		}
		if c.Text == "AND" && c.Kind != "" {
			t.Errorf("keyword candidate kind = %q, want empty", c.Kind)
		}
	}
}
