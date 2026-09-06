package charset

import (
	"testing"

	runewidth "github.com/mattn/go-runewidth"
	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestConsolePreviewNilEncoding(t *testing.T) {
	for _, s := range []string{"", "abc", "你好 😀 ±①"} {
		if got := ConsolePreview(s, nil); got != s {
			t.Errorf("ConsolePreview(%q, nil) = %q, want unchanged", s, got)
		}
	}
}

func TestConsolePreviewGBK(t *testing.T) {
	// GBK cannot represent the emoji or the supplementary-plane character,
	// so the console writer substitutes '?' (width 2 -> width 1)
	got := ConsolePreview("你好😀世界ᨏ", simplifiedchinese.GBK)
	if want := "你好?世界?"; got != want {
		t.Errorf("ConsolePreview GBK = %q, want %q", got, want)
	}
	// all-ASCII is unchanged
	if got := ConsolePreview("select * from t1", simplifiedchinese.GBK); got != "select * from t1" {
		t.Errorf("ConsolePreview ASCII = %q, want unchanged", got)
	}
	// CJK is representable and unchanged
	if got := ConsolePreview("用户user表2024_v2", simplifiedchinese.GBK); got != "用户user表2024_v2" {
		t.Errorf("ConsolePreview mixed CJK = %q, want unchanged", got)
	}
	// fullwidth and CJK punctuation are representable
	if got := ConsolePreview("ＡＢＣ，。、", simplifiedchinese.GBK); got != "ＡＢＣ，。、" {
		t.Errorf("ConsolePreview fullwidth = %q, want unchanged", got)
	}
}

func TestConsolePreviewGB18030(t *testing.T) {
	// GB18030 covers all of Unicode: even emoji are representable
	if got := ConsolePreview("你好😀", simplifiedchinese.GB18030); got != "你好😀" {
		t.Errorf("ConsolePreview GB18030 = %q, want unchanged", got)
	}
}

func TestConsolePreviewWidthMatchesTerminal(t *testing.T) {
	// the display width of the preview must equal what the terminal shows:
	// the emoji collapses from two columns to one ('?') under GBK
	s := "a😀中"
	if w := runewidth.StringWidth(ConsolePreview(s, nil)); w != 5 { // 1 + 2 + 2
		t.Errorf("UTF-8 console width of %q = %d, want 5", s, w)
	}
	if w := runewidth.StringWidth(ConsolePreview(s, simplifiedchinese.GBK)); w != 4 { // 1 + 1 + 2
		t.Errorf("GBK console width of %q = %d, want 4", s, w)
	}
}
