package completer

import (
	"strings"
	"testing"

	"github.com/xo/usql/rline"
)

// oracleHook mirrors drivers.oracleQuoteIdent: Oracle-family identifiers
// that do not survive unquoted upper-folding must be inserted quoted.
func oracleHook(name string) string {
	if name != strings.ToUpper(name) {
		return `"` + name + `"`
	}
	return name
}

func TestQuote(t *testing.T) {
	c := completer{quoteIdent: oracleHook}
	for _, tt := range []struct{ in, want string }{
		{"dept", `"dept"`},         // quoted-lowercase creation: must quote
		{"MIG_MYSQL", "MIG_MYSQL"}, // upper-normal: folds fine unquoted
		{`"dept"`, `"dept"`},       // already quoted: never double-quote
		{"deptno", `"deptno"`},     // quoted-lowercase column
	} {
		if got := c.quote(tt.in); got != tt.want {
			t.Errorf("quote(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	// nil hook is a no-op
	var nilC completer
	if got := nilC.quote("dept"); got != "dept" {
		t.Errorf("nil hook: quote(%q) = %q", "dept", got)
	}
}

func TestCandsObjsQuotes(t *testing.T) {
	c := completer{quoteIdent: oracleHook}
	got := c.candsObjs([]obj{{name: "dept", kind: "table"}, {name: "MIG_MYSQL", kind: "user"}})
	if got[0].Text != `"dept"` || got[1].Text != "MIG_MYSQL" {
		t.Fatalf("candsObjs = %q, %q", got[0].Text, got[1].Text)
	}
}

// TestCompletePrefixFullQuoted covers matching and case handling for quoted
// candidates: they match by their bare name, and the quoted form survives
// the lowercase pattern heuristic verbatim.
func TestCompletePrefixFullQuoted(t *testing.T) {
	options := []rline.Cand{
		{Text: `"dept"`, Kind: "table"},
		{Text: "MIG_MYSQL", Kind: "user"},
		{Text: "DEPT_TEMP", Kind: "table"},
	}
	// lower-case pattern: quoted candidate keeps its quotes and case, the
	// upper-normal candidates are lower-cased (they fold unquoted)
	got := completePrefixFull("de", options)
	if len(got) != 2 {
		t.Fatalf("got %d candidates for %q: %+v", len(got), "de", got)
	}
	if got[0].Text != `"dept"` {
		t.Errorf("quoted candidate = %q, want %q (quotes must survive)", got[0].Text, `"dept"`)
	}
	// catalog identifiers keep their stored case (fold-safe here, and it
	// matters on case-sensitive engines)
	if got[1].Text != "DEPT_TEMP" {
		t.Errorf("unquoted candidate = %q, want %q (stored case preserved)", got[1].Text, "DEPT_TEMP")
	}
	// a half-typed quoted pattern ("de) matches bare names too — the user
	// is mid-quoted-identifier; the shortest (quoted) candidate ranks first
	// and the upper-normal one keeps its case (the pattern is not a letter)
	got = completePrefixFull(`"de`, options)
	if len(got) != 2 || got[0].Text != `"dept"` || got[1].Text != "DEPT_TEMP" {
		t.Fatalf("quoted pattern match: got %+v", got)
	}
	// upper-case pattern: nothing is lower-cased
	got = completePrefixFull("MIG", options)
	if len(got) != 1 || got[0].Text != "MIG_MYSQL" {
		t.Fatalf("upper pattern match: got %+v", got)
	}
	// built-in functions still follow the keyword-case heuristic (they are
	// fold-safe on every engine)
	fn := completePrefixFull("subs", []rline.Cand{{Text: "SUBSTRING(", Kind: "function"}})
	if len(fn) != 1 || fn[0].Text != "substring(" {
		t.Errorf("function candidate = %+v, want lower-cased substring(", fn)
	}
}
