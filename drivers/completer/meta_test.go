package completer

import (
	"reflect"
	"testing"

	"github.com/xo/usql/rline"
)

// TestMetaCommandCompletion pins the semantics of meta-command completion:
// each meta command completes only with objects of its own kind, fully
// qualified, and meta commands without a completion rule never fall back to
// SQL keywords.
func TestMetaCommandCompletion(t *testing.T) {
	c := NewDefaultCompleter(
		WithReader(candMockReader{}),
		WithLogger(discardLogger()),
		WithConnStrings([]string{"mysql_root_localhost_3306", "opengauss_ogadmin_6432"}),
	).(*completer)

	// doRepl drives the replace-style path the real readline fork uses.
	doRepl := func(line string) ([]rline.Cand, int, bool) {
		return c.DoRepl([]rune(line), len([]rune(line)))
	}

	cases := []struct {
		name     string
		line     string
		want     []string
		wantLen  int
		wantFull bool // DoRepl replace semantics expected
	}{
		// \dt — schemas first (the accessible namespaces), then that
		// namespace's tables when qualified, fully qualified names
		{"dt all", `\dt `, []string{"other", "public"}, 0, true},
		{"dt prefix", `\dt fi`, []string{"public.film"}, 2, true},
		{"dt schema-qualified", `\dt public.`, []string{"public.film", "public.actor"}, 7, true},
		{"dt cross-schema", `\dt other.o`, []string{"other.orders"}, 7, true},
		{"dv views only", `\dv `, []string{"other", "public"}, 0, true},
		{"dv views qualified", `\dv public.`, []string{"public.film_view"}, 7, true},
		// \ds — sequences only once qualified
		{"ds sequences only", `\ds `, []string{"other", "public"}, 0, true},
		{"ds sequences qualified", `\ds public.`, []string{"public.actor_id_seq"}, 7, true},
		// \df — functions only once qualified
		{"df functions only", `\df `, []string{"other", "public"}, 0, true},
		{"df functions qualified", `\df public.`, []string{"public.now"}, 7, true},
		// \di — no index reader: nothing, not the SQL fallback
		{"di unsupported reader", `\di `, nil, 0, false},
		// \dn — schemas are the object of this command
		{"dn schemas only", `\dn `, []string{"other", "public"}, 0, true},
		// \l — no catalog reader: nothing
		{"l unsupported reader", `\l `, nil, 0, false},
		// \d — schemas first, then the selectables of a named namespace
		{"d selectables", `\d `, []string{"other", "public"}, 0, true},
		{"d selectables qualified", `\d public.`, []string{"public.now", "public.film", "public.actor", "public.film_view", "public.actor_id_seq"}, 7, true},
		// \c — connection names only
		{"c connection names", `\c `, []string{"opengauss_ogadmin_6432", "mysql_root_localhost_3306"}, 0, true},
		{"c prefix", `\c op`, []string{"opengauss_ogadmin_6432"}, 2, true},
		// \pset — its value rules keep working as full words
		{"pset format", `\pset format al`, []string{"aligned", "latex-longtable", "vertical", "unaligned"}, 2, true},
		// meta commands WITHOUT a completion rule: no SQL keywords, ever
		{"encoding no rule", `\encoding `, nil, 0, false},
		{"timing no rule", `\timing `, nil, 0, false},
		{"x no rule", `\x `, nil, 0, false},
		{"password no rule", `\password `, nil, 0, false},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, length, full := doRepl(test.line)
			if full != test.wantFull {
				t.Errorf("DoRepl(%q) full=%v, want %v", test.line, full, test.wantFull)
			}
			if length != test.wantLen {
				t.Errorf("DoRepl(%q) length=%d, want %d", test.line, length, test.wantLen)
			}
			if test.want == nil {
				if got != nil {
					t.Errorf("DoRepl(%q) = %q, want nil", test.line, got)
				}
				return
			}
			gs := make([]string, len(got))
			for i := range got {
				gs[i] = got[i].Text
			}
			if !reflect.DeepEqual(gs, test.want) {
				t.Errorf("DoRepl(%q):\n got  %q\n want %q", test.line, gs, test.want)
			}
		})
	}
}

// TestMetaCommandNoSQLFallback guards the heuristic path: after a meta
// command with no completion rule, the plain Do path must not suggest SQL
// keywords either.
func TestMetaCommandNoSQLFallback(t *testing.T) {
	c := NewDefaultCompleter(
		WithReader(candMockReader{}),
		WithLogger(discardLogger()),
	).(*completer)

	for _, line := range []string{`\encoding `, `\timing `, `\x `} {
		// previousWords = the meta command, text = "" — as Do computes it
		got := c.complete([]string{line[:len(line)-1]}, nil)
		if got != nil {
			t.Errorf("complete(%q) = %q, want nil (no SQL keyword fallback)", line, got)
		}
	}
}
