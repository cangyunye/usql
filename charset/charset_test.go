package charset

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/mattn/go-runewidth"
	"github.com/xo/tblfmt"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestParseEncoding(t *testing.T) {
	tests := []struct {
		name  string
		want  encoding.Encoding
		isErr bool
	}{
		{"", nil, false},
		{"utf-8", nil, false},
		{"utf8", nil, false},
		{"UTF-8", nil, false},
		{"gbk", simplifiedchinese.GBK, false},
		{"GBK", simplifiedchinese.GBK, false},
		{"cp936", simplifiedchinese.GBK, false},
		{"gb2312", simplifiedchinese.GBK, false},
		{"euccn", simplifiedchinese.GBK, false},
		{"gb18030", simplifiedchinese.GB18030, false},
		{"GB18030", simplifiedchinese.GB18030, false},
		{" latin1 ", nil, true},
		{"big5", nil, true},
	}
	for _, test := range tests {
		enc, err := ParseEncoding(test.name)
		switch {
		case test.isErr && err == nil:
			t.Errorf("ParseEncoding(%q): expected error, got nil", test.name)
		case !test.isErr && err != nil:
			t.Errorf("ParseEncoding(%q): unexpected error: %v", test.name, err)
		case !test.isErr && enc != test.want:
			t.Errorf("ParseEncoding(%q): expected %v, got %v", test.name, test.want, enc)
		}
	}
}

func TestToUTF8(t *testing.T) {
	tests := []struct {
		name string
		in   string
		enc  encoding.Encoding
		want string
	}{
		// nil enc: passthrough
		{"nil enc passthrough", "\xc4\xe3\xba\xc3", nil, "\xc4\xe3\xba\xc3"},
		// C4E3 BAC3 = 你好 in GBK
		{"gbk decode", "\xc4\xe3\xba\xc3", simplifiedchinese.GBK, "你好"},
		// D5C5 C8FD = 张三 in GBK, decodable by the GB18030 decoder
		{"gb18030 decodes gbk", "\xd5\xc5\xc8\xfd", simplifiedchinese.GB18030, "张三"},
		// pure ASCII is identity under GBK
		{"ascii identity", "SELECT 1", simplifiedchinese.GBK, "SELECT 1"},
		// binary junk is preserved, not turned into U+FFFD
		{"binary preserved", "\xff\xfe\xff\xd8", simplifiedchinese.GBK, "\xff\xfe\xff\xd8"},
		{"jpeg magic preserved", "\xff\xd8\xff\xe0\x00\x10", simplifiedchinese.GBK, "\xff\xd8\xff\xe0\x00\x10"},
		// mixed text+junk: all-or-nothing, original preserved
		{"mixed preserved", "\xc4\xe3\x41\xff\x42", simplifiedchinese.GBK, "\xc4\xe3\x41\xff\x42"},
		// a genuine U+FFFD in the input is not treated as a decoder artifact
		{"input fffd kept", "a\ufffd", simplifiedchinese.GBK, "a\ufffd"},
	}
	for _, test := range tests {
		if got := ToUTF8(test.in, test.enc); got != test.want {
			t.Errorf("ToUTF8(%q, %v): got %q, want %q", test.in, test.enc, got, test.want)
		}
	}
}

func TestOutputEncoding(t *testing.T) {
	tests := []struct {
		lang      string
		lcAll     string
		lcCtype   string
		wantName  string
		wantIsGBK bool
		want18030 bool
	}{
		{"zh_CN.UTF-8", "", "", "UTF-8", false, false},
		{"zh_CN.utf8", "", "", "UTF-8", false, false},
		{"C", "", "", "UTF-8", false, false},
		{"POSIX", "", "", "UTF-8", false, false},
		{"en_US.UTF-8", "", "", "UTF-8", false, false},
		{"zh_CN.GBK", "", "", "GBK", true, false},
		{"zh_CN.GB2312", "", "", "GBK", true, false},
		{"zh_CN.eucCN", "", "", "GBK", true, false},
		{"zh_CN.cp936", "", "", "GBK", true, false},
		{"zh_CN.GB18030", "", "", "GB18030", false, true},
		{"zh_CN.UTF-8@pinyin", "", "", "UTF-8", false, false},
		{"fr_FR.ISO-8859-1", "", "", "UTF-8", false, false},
		{"zh_CN.UTF-8", "zh_CN.GBK", "", "GBK", true, false},
		{"zh_CN.UTF-8", "", "zh_CN.GBK", "GBK", true, false},
		{"", "", "", "UTF-8", false, false},
	}
	for _, test := range tests {
		t.Setenv("LANG", test.lang)
		t.Setenv("LC_CTYPE", test.lcCtype)
		t.Setenv("LC_ALL", test.lcAll)
		enc := OutputEncoding()
		switch {
		case test.wantIsGBK && enc != simplifiedchinese.GBK:
			t.Errorf("OutputEncoding(%q/%q/%q): got %v, want GBK", test.lcAll, test.lcCtype, test.lang, enc)
		case test.want18030 && enc != simplifiedchinese.GB18030:
			t.Errorf("OutputEncoding(%q/%q/%q): got %v, want GB18030", test.lcAll, test.lcCtype, test.lang, enc)
		case !test.wantIsGBK && !test.want18030 && enc != nil:
			t.Errorf("OutputEncoding(%q/%q/%q): got %v, want nil", test.lcAll, test.lcCtype, test.lang, enc)
		}
		if name := ConsoleEncodingName(); name != test.wantName {
			t.Errorf("ConsoleEncodingName(%q/%q/%q): got %q, want %q", test.lcAll, test.lcCtype, test.lang, name, test.wantName)
		}
	}
}

// TestRunewidthEastAsianWidthGB18030 exercises the init() compensation: after
// applyRunewidthFix under a GB18030 locale, ambiguous-width characters must
// measure as two columns.
func TestRunewidthEastAsianWidthGB18030(t *testing.T) {
	t.Setenv("LC_ALL", "zh_CN.GB18030")
	t.Setenv("LC_CTYPE", "")
	t.Setenv("LANG", "")
	old := runewidth.DefaultCondition.EastAsianWidth
	defer func() { runewidth.DefaultCondition.EastAsianWidth = old }()
	runewidth.DefaultCondition.EastAsianWidth = false
	applyRunewidthFix()
	if !runewidth.DefaultCondition.EastAsianWidth {
		t.Error("applyRunewidthFix did not set EastAsianWidth under zh_CN.GB18030")
	}
	if w := runewidth.RuneWidth('±'); w != 2 {
		t.Errorf("RuneWidth('±') = %d, want 2", w)
	}
	// a non-gb18030 locale must not touch the flag
	t.Setenv("LC_ALL", "zh_CN.UTF-8")
	applyRunewidthFix()
	if !runewidth.DefaultCondition.EastAsianWidth {
		t.Error("applyRunewidthFix must not clear EastAsianWidth under zh_CN.UTF-8")
	}
}

func TestResultSetDecodeAny(t *testing.T) {
	base := &fakeResultSet{
		cols: []string{"id", "\xc3\xfb\xb3\xc6"}, // 名称
		vals: [][]any{
			{"1", "\xc4\xe3\xba\xc3"},       // *any string -> 你好
			{2, []byte("\xd5\xc5\xc8\xfd")}, // *any []byte -> 张三
			{int64(3), "\xff\xfe\xff"},      // binary preserved
		},
	}
	rs := NewResultSet(base, simplifiedchinese.GBK)
	cols, err := rs.Columns()
	if err != nil {
		t.Fatalf("Columns: %v", err)
	}
	if cols[0] != "id" || cols[1] != "名称" {
		t.Errorf("Columns: got %q, want [id 名称]", cols)
	}
	var got [][]any
	for rs.Next() {
		dst := []any{new(any), new(any)}
		if err := rs.Scan(dst...); err != nil {
			t.Fatalf("Scan: %v", err)
		}
		got = append(got, []any{*dst[0].(*any), *dst[1].(*any)})
	}
	if err := rs.Err(); err != nil {
		t.Fatalf("Err: %v", err)
	}
	want := [][]any{
		{"1", "你好"},
		{2, []byte("张三")},
		{int64(3), "\xff\xfe\xff"},
	}
	for i := range want {
		if a, b := got[i][0], want[i][0]; a != b {
			t.Errorf("row %d col 0: got %v (%T), want %v (%T)", i, a, a, b, b)
		}
		if a, b := got[i][1], want[i][1]; !equalCell(a, b) {
			t.Errorf("row %d col 1: got %v (%T), want %v (%T)", i, a, a, b, b)
		}
	}
}

func TestResultSetTypedScan(t *testing.T) {
	rs := NewResultSet(&fakeResultSet{
		cols: []string{"c1", "c2", "c3", "c4"},
		vals: [][]any{{ //nolint
			"\xc4\xe3",                 // -> *string: 你
			[]byte("\xd5\xc5\xc8\xfd"), // -> *[]byte: 张三
			[]byte("\xff\xfe"),         // -> *sql.RawBytes: preserved
			sql.NullString{String: "\xb1\xb8", Valid: true}, // -> *sql.NullString: 备
		}},
	}, simplifiedchinese.GBK)
	if !rs.Next() {
		t.Fatal("expected one row")
	}
	var s string
	var b []byte
	var raw sql.RawBytes
	var ns sql.NullString
	raw = make([]byte, 0, 64)
	if err := rs.Scan(&s, &b, &raw, &ns); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if s != "你" {
		t.Errorf("*string: got %q, want 你", s)
	}
	if string(b) != "张三" {
		t.Errorf("*[]byte: got %q, want 张三", b)
	}
	if string(raw) != "\xff\xfe" {
		t.Errorf("*sql.RawBytes: got %q, want preserved bytes", raw)
	}
	if !ns.Valid || ns.String != "备" {
		t.Errorf("*sql.NullString: got %+v, want {备 true}", ns)
	}
}

func TestResultSetPassthroughWhenNilEnc(t *testing.T) {
	fake := &fakeResultSet{cols: []string{"a"}}
	if rs := NewResultSet(fake, nil); rs != tblfmt.ResultSet(fake) {
		t.Error("NewResultSet with nil enc must return the original result set")
	}
}

func TestResultSetColumnTypes(t *testing.T) {
	// wrapped fake without ColumnTypes reports the tblfmt sentinel
	rs := NewResultSet(&fakeResultSet{}, simplifiedchinese.GBK)
	ct, ok := rs.(interface {
		ColumnTypes() ([]*sql.ColumnType, error)
	})
	if !ok {
		t.Fatal("wrapper must implement ColumnTypes")
	}
	if _, err := ct.ColumnTypes(); !errors.Is(err, tblfmt.ErrResultSetHasNoColumnTypes) {
		t.Errorf("expected ErrResultSetHasNoColumnTypes, got %v", err)
	}
	// wrapped fake with ColumnTypes passes through
	rs2 := NewResultSet(&fakeResultSetWithCT{}, simplifiedchinese.GBK)
	ct2, ok := rs2.(interface {
		ColumnTypes() ([]*sql.ColumnType, error)
	})
	if !ok {
		t.Fatal("wrapper must implement ColumnTypes")
	}
	if _, err := ct2.ColumnTypes(); err == nil || err.Error() != "passthrough called" {
		t.Errorf("expected passthrough error, got %v", err)
	}
}

func equalCell(a, b any) bool {
	ab, aok := a.([]byte)
	bb, bok := b.([]byte)
	if aok || bok {
		if !aok || !bok {
			return false
		}
		return string(ab) == string(bb)
	}
	return a == b
}

// fakeResultSet is a minimal tblfmt.ResultSet whose Scan fills both *any and
// typed destinations.
type fakeResultSet struct {
	pos  int
	cols []string
	vals [][]any
	err  error
}

func (f *fakeResultSet) Columns() ([]string, error) { return f.cols, nil }
func (f *fakeResultSet) Next() bool                 { return f.pos < len(f.vals) }

func (f *fakeResultSet) Scan(dst ...any) error {
	for i := range dst {
		v := f.vals[f.pos][i]
		switch d := dst[i].(type) {
		case *any:
			*d = v
		case *string:
			*d = v.(string)
		case *[]byte:
			*d = append([]byte(nil), v.([]byte)...)
		case *sql.RawBytes:
			*d = append((*d)[:0], v.([]byte)...)
		case *sql.NullString:
			*d = v.(sql.NullString)
		}
	}
	f.pos++
	return nil
}

func (f *fakeResultSet) Close() error        { return nil }
func (f *fakeResultSet) Err() error          { return f.err }
func (f *fakeResultSet) NextResultSet() bool { return false }

// fakeResultSetWithCT reports a sentinel from ColumnTypes to verify the
// wrapper forwards the call to the wrapped result set.
type fakeResultSetWithCT struct {
	fakeResultSet
}

func (f *fakeResultSetWithCT) ColumnTypes() ([]*sql.ColumnType, error) {
	return nil, errors.New("passthrough called")
}
