// Package dupcheck ranks a user's existing entities against a prospective
// purchase (name plus optional ASIN / JAN / model number) to warn about
// likely duplicates before buying.
//
// The package is pure (no database access) so ranking can be unit tested in
// isolation; the repository layer supplies Candidates and the API layer
// resolves location paths for the returned matches.
package dupcheck

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// NormalizeName folds a product name into a canonical comparison form:
//
//   - Unicode NFKC (full-width ASCII -> half-width, half-width katakana ->
//     full-width katakana, compatibility characters decomposed)
//   - lowercase
//   - katakana folded to hiragana (so "ケーブル" == "けーぶる")
//   - punctuation and symbols replaced by spaces, whitespace collapsed
func NormalizeName(s string) string {
	s = norm.NFKC.String(s)
	var b strings.Builder
	b.Grow(len(s))
	lastSpace := true
	for _, r := range s {
		r = unicode.ToLower(r)
		r = foldKana(r)
		switch {
		case unicode.IsLetter(r) || unicode.IsNumber(r) || r == 'ー' || unicode.Is(unicode.Mn, r):
			b.WriteRune(r)
			lastSpace = false
		default: // punctuation, symbols, whitespace
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// NormalizeID canonicalizes an identifier (ASIN, JAN/EAN, model number):
// NFKC, uppercase, and every non-alphanumeric character removed, so
// "ab-123 x" and "ＡＢ１２３Ｘ" compare equal.
func NormalizeID(s string) string {
	s = norm.NFKC.String(s)
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToUpper(r))
		}
	}
	return b.String()
}

// Tokens splits a name into normalized tokens. Runs of CJK characters are
// additionally split into character bigrams, since Japanese product titles
// rarely contain spaces.
func Tokens(s string) []string {
	fields := strings.Fields(NormalizeName(s))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, splitScripts(f)...)
	}
	return out
}

// foldKana maps katakana (U+30A1..U+30F6) to the matching hiragana.
func foldKana(r rune) rune {
	if r >= 0x30A1 && r <= 0x30F6 {
		return r - 0x60
	}
	return r
}

func isCJK(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana) || r == 'ー'
}

// splitScripts breaks a token at script boundaries (e.g. "usbけーぶる" ->
// "usb", then kana bigrams) and emits bigrams for CJK runs.
func splitScripts(tok string) []string {
	var out []string
	runes := []rune(tok)
	start := 0
	flush := func(end int) {
		if end <= start {
			return
		}
		seg := runes[start:end]
		if !isCJK(seg[0]) {
			out = append(out, string(seg))
			return
		}
		if len(seg) == 1 {
			out = append(out, string(seg))
			return
		}
		for i := 0; i+1 < len(seg); i++ {
			out = append(out, string(seg[i:i+2]))
		}
	}
	for i := 1; i <= len(runes); i++ {
		if i == len(runes) || isCJK(runes[i]) != isCJK(runes[i-1]) {
			flush(i)
			start = i
		}
	}
	return out
}
