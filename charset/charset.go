// Package charset provides client-side character encoding support.
//
// usql processes all text internally as UTF-8. Databases configured with a
// GBK-family encoding (GBK, GB2312, GB18030) return non-UTF-8 bytes on the
// wire, and terminals running under a GBK-family locale expect non-UTF-8
// bytes on output. This package handles both directions:
//
//   - input: [ParseEncoding] resolves an encoding name, and [ToUTF8] decodes
//     database values to UTF-8;
//   - output: [OutputEncoding] reports the terminal encoding implied by the
//     locale (LC_ALL > LC_CTYPE > LANG), for use by rline when wrapping
//     console writers.
package charset

import (
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	runewidth "github.com/mattn/go-runewidth"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// EncodingNames are the encoding names accepted by ParseEncoding, in the
// order they should be shown in error and help messages.
var EncodingNames = []string{"utf-8", "gbk", "gb2312", "gb18030"}

// ParseEncoding resolves an encoding name to a decoder, returning nil (UTF-8
// passthrough) for the empty and UTF-8 names. GB2312 (native EUC-CN) is a
// subset of GBK and is decoded with the GBK decoder.
func ParseEncoding(name string) (encoding.Encoding, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "utf-8", "utf8":
		return nil, nil
	case "gbk", "cp936":
		return simplifiedchinese.GBK, nil
	case "gb2312", "euccn":
		return simplifiedchinese.GBK, nil
	case "gb18030":
		return simplifiedchinese.GB18030, nil
	}
	return nil, fmt.Errorf("unknown encoding %q (valid: %s)", name, strings.Join(EncodingNames, ", "))
}

// ToUTF8 decodes s with enc, returning s unchanged when enc is nil.
//
// The x/text GB-family decoders never fail: undecodable bytes are replaced
// with U+FFFD. Because U+FFFD cannot be encoded in GBK-family encodings, an
// U+FFFD in the decoded value proves the value is not valid text in enc
// (binary data, or bytes in some other encoding); in that case the original
// value is returned, preserving its bytes (tblfmt renders undecodable bytes
// as \xNN escapes).
//
// ContainsRune(dec, utf8.RuneError) is safe here: dec is always valid UTF-8
// (decoder output), so it matches only real U+FFFD code points. It must not
// be used against s: there it also matches invalid input bytes, which is
// exactly the case this check has to catch.
func ToUTF8(s string, enc encoding.Encoding) string {
	if enc == nil {
		return s
	}
	dec, _, err := transform.String(enc.NewDecoder(), s)
	switch {
	case err != nil:
		return s
	case strings.ContainsRune(dec, utf8.RuneError):
		return s
	}
	return dec
}

// OutputEncoding returns the encoding implied by the console locale, or nil
// when the locale is UTF-8/unknown and output should not be transcoded.
//
// The character set is taken from the first of LC_ALL, LC_CTYPE, and LANG
// (e.g. zh_CN.GBK), and recognized values are GBK, GB2312/EUC-CN/CP936
// (decoded as GBK), and GB18030. Everything else (including UTF-8, C, and
// POSIX) yields nil.
func OutputEncoding() encoding.Encoding {
	switch localeCharset() {
	case "gbk", "gb2312", "euccn", "cp936":
		return simplifiedchinese.GBK
	case "gb18030":
		return simplifiedchinese.GB18030
	}
	return nil
}

// ConsoleEncodingName returns a human-readable name for the console encoding
// reported by OutputEncoding.
func ConsoleEncodingName() string {
	switch OutputEncoding() {
	case simplifiedchinese.GB18030:
		return "GB18030"
	case simplifiedchinese.GBK:
		return "GBK"
	}
	return "UTF-8"
}

// init compensates for go-runewidth's locale table, which knows gbk and
// gb2312 but not gb18030: without this, EastAsianWidth would be false under
// a zh_CN.GB18030 locale and ambiguous-width characters would be measured as
// one column wide, breaking table borders.
func init() {
	applyRunewidthFix()
}

// localeName returns the locale from the environment, LC_ALL first.
func localeName() string {
	for _, k := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// localeCharset returns the lowercased character set suffix of the locale,
// with any modifier stripped (e.g. zh_CN.UTF-8@pinyin -> utf-8). The C and
// POSIX locales return "".
func localeCharset() string {
	loc := localeName()
	if loc == "" || loc == "C" || loc == "POSIX" {
		return ""
	}
	if i := strings.IndexByte(loc, '@'); i >= 0 {
		loc = loc[:i]
	}
	if i := strings.IndexByte(loc, '.'); i >= 0 {
		return strings.ToLower(loc[i+1:])
	}
	return ""
}

func applyRunewidthFix() {
	if localeCharset() == "gb18030" {
		runewidth.DefaultCondition.EastAsianWidth = true
	}
}
