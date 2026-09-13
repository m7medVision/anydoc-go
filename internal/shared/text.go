// Port of src/shared/text.rs: shared text normalization.

package shared

import (
	"strings"
	"unicode"
)

// CleanText drops control characters and layout-only invisibles, converts
// NBSP to a regular space, and strips soft hyphens. Line breaks become
// single spaces; a CRLF pair is one break. U+200C (ZWNJ) and U+200D (ZWJ)
// are preserved: they carry meaning in Arabic/Indic shaping and emoji
// sequences.
func CleanText(text string) string {
	var out strings.Builder
	out.Grow(len(text))
	rs := []rune(text)
	for i := 0; i < len(rs); i++ {
		switch c := rs[i]; c {
		case '\u00a0':
			out.WriteRune(' ')
		case '\u00ad', '\u200b', '\ufeff':
			// Soft hyphen, zero-width space, BOM: layout-only invisibles.
		case '\t':
			out.WriteRune('\t')
		case '\r':
			if i+1 < len(rs) && rs[i+1] == '\n' {
				i++
			}
			out.WriteRune(' ')
		case '\n':
			out.WriteRune(' ')
		default:
			if !unicode.IsControl(c) {
				out.WriteRune(c)
			}
		}
	}
	return out.String()
}

// CollapseWS collapses whitespace runs to single spaces.
func CollapseWS(text string) string {
	var out strings.Builder
	out.Grow(len(text))
	prevSpace := false
	for _, c := range text {
		if unicode.IsSpace(c) {
			if !prevSpace {
				out.WriteRune(' ')
			}
			prevSpace = true
		} else {
			out.WriteRune(c)
			prevSpace = false
		}
	}
	return out.String()
}

// asciiLower lowercases ASCII letters only, mirroring Rust's
// str::to_ascii_lowercase; non-ASCII characters are left untouched.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// asciiLowerRune is asciiLower for a single rune; non-ASCII runes pass
// through unchanged.
func asciiLowerRune(c rune) rune {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}

// asciiEqualFold reports ASCII case-insensitive equality, mirroring Rust's
// eq_ignore_ascii_case: non-ASCII characters compare exactly.
func asciiEqualFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		if asciiLowerByte(a[i]) != asciiLowerByte(b[i]) {
			return false
		}
	}
	return true
}

func asciiLowerByte(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}
