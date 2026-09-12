package objects

import (
	"strings"

	"golang.org/x/text/unicode/bidi"
	"golang.org/x/text/unicode/norm"
)

// saslprep ports stringprep 0.1.5's saslprep (RFC 4013) as used by lopdf for
// revision 5/6 password sanitization.
func saslprep(s string) (string, error) {
	// Fast path for ASCII text without control characters.
	asciiClean := true
	for _, c := range []byte(s) {
		if c >= 0x80 || c <= 0x1F || c == 0x7F {
			asciiClean = false
			break
		}
	}
	if asciiClean {
		return s, nil
	}

	// 2.1 Mapping
	var mapped strings.Builder
	for _, c := range s {
		if nonASCIISpaceCharacter(c) {
			mapped.WriteRune(' ')
			continue
		}
		if commonlyMappedToNothing(c) {
			continue
		}
		mapped.WriteRune(c)
	}

	// 2.2 Normalization
	normalized := norm.NFKC.String(mapped.String())

	// 2.3 Prohibited Output
	for _, c := range normalized {
		if nonASCIISpaceCharacter(c) || // C.1.2
			asciiControlCharacter(c) || // C.2.1
			nonASCIIControlCharacter(c) || // C.2.2
			privateUse(c) || // C.3
			nonCharacterCodePoint(c) || // C.4
			surrogateCode(c) || // C.5
			inappropriateForPlainText(c) || // C.6
			inappropriateForCanonicalRepresentation(c) || // C.7
			changeDisplayPropertiesOrDeprecated(c) || // C.8
			taggingCharacter(c) { // C.9
			return "", &DecryptionError{Kind: DecStringPrep}
		}
	}

	// 2.4 Bidirectional Characters
	if isProhibitedBidirectionalText(normalized) {
		return "", &DecryptionError{Kind: DecStringPrep}
	}

	// 2.5 Unassigned Code Points
	for _, c := range normalized {
		if unassignedCodePoint(c) {
			return "", &DecryptionError{Kind: DecStringPrep}
		}
	}

	return normalized, nil
}

func isProhibitedBidirectionalText(s string) bool {
	hasRandAL := false
	hasL := false
	first, last := rune(-1), rune(-1)
	for i, c := range s {
		if i == 0 {
			first = c
		}
		last = c
		if bidiRorAL(c) {
			hasRandAL = true
		}
		if bidiL(c) {
			hasL = true
		}
	}
	if hasRandAL {
		if hasL {
			return true
		}
		if !bidiRorAL(first) || !bidiRorAL(last) {
			return true
		}
	}
	return false
}

func bidiRorAL(c rune) bool {
	p, _ := bidi.LookupRune(c)
	return p.Class() == bidi.R || p.Class() == bidi.AL
}

func bidiL(c rune) bool {
	p, _ := bidi.LookupRune(c)
	return p.Class() == bidi.L
}

// B.1 Commonly mapped to nothing.
func commonlyMappedToNothing(c rune) bool {
	switch c {
	case 0x00AD, 0x034F, 0x1806, 0x180B, 0x180C, 0x180D, 0x200B, 0x200C,
		0x200D, 0x2060, 0xFE00, 0xFE01, 0xFE02, 0xFE03, 0xFE04, 0xFE05,
		0xFE06, 0xFE07, 0xFE08, 0xFE09, 0xFE0A, 0xFE0B, 0xFE0C, 0xFE0D,
		0xFE0E, 0xFE0F, 0xFEFF:
		return true
	}
	return false
}

// C.1.2 Non-ASCII space characters.
func nonASCIISpaceCharacter(c rune) bool {
	switch c {
	case 0x00A0, 0x1680, 0x2000, 0x2001, 0x2002, 0x2003, 0x2004, 0x2005,
		0x2006, 0x2007, 0x2008, 0x2009, 0x200A, 0x200B, 0x202F, 0x205F, 0x3000:
		return true
	}
	return false
}

// C.2.1 ASCII control characters.
func asciiControlCharacter(c rune) bool {
	return c <= 0x1F || c == 0x7F
}

// C.2.2 Non-ASCII control characters.
func nonASCIIControlCharacter(c rune) bool {
	if c >= 0x0080 && c <= 0x009F {
		return true
	}
	switch c {
	case 0x06DD, 0x070F, 0x180E, 0x200C, 0x200D, 0x2028, 0x2029, 0x2060,
		0x2061, 0x2062, 0x2063, 0xFEFF, 0xFFF9, 0xFFFA, 0xFFFB, 0xFFFC:
		return true
	}
	return c >= 0x206A && c <= 0x206F || c >= 0x1D173 && c <= 0x1D17A
}

// C.3 Private use.
func privateUse(c rune) bool {
	return c >= 0xE000 && c <= 0xF8FF ||
		c >= 0xF0000 && c <= 0xFFFFD ||
		c >= 0x100000 && c <= 0x10FFFD
}

// C.4 Non-character code points.
func nonCharacterCodePoint(c rune) bool {
	if c >= 0xFDD0 && c <= 0xFDEF {
		return true
	}
	switch {
	case c >= 0xFFFE && c <= 0xFFFF,
		c >= 0x1FFFE && c <= 0x1FFFF,
		c >= 0x2FFFE && c <= 0x2FFFF,
		c >= 0x3FFFE && c <= 0x3FFFF,
		c >= 0x4FFFE && c <= 0x4FFFF,
		c >= 0x5FFFE && c <= 0x5FFFF,
		c >= 0x6FFFE && c <= 0x6FFFF,
		c >= 0x7FFFE && c <= 0x7FFFF,
		c >= 0x8FFFE && c <= 0x8FFFF,
		c >= 0x9FFFE && c <= 0x9FFFF,
		c >= 0xAFFFE && c <= 0xAFFFF,
		c >= 0xBFFFE && c <= 0xBFFFF,
		c >= 0xCFFFE && c <= 0xCFFFF,
		c >= 0xDFFFE && c <= 0xDFFFF,
		c >= 0xEFFFE && c <= 0xEFFFF,
		c >= 0xFFFFE && c <= 0xFFFFF,
		c >= 0x10FFFE && c <= 0x10FFFF:
		return true
	}
	return false
}

// C.5 Surrogate codes. Rust chars cannot represent surrogates, so the upstream
// table always returns false; Go strings decode invalid bytes to U+FFFD, and
// isolated surrogates cannot occur in a well-formed string iteration either.
func surrogateCode(c rune) bool { return false }

// C.6 Inappropriate for plain text.
func inappropriateForPlainText(c rune) bool {
	switch c {
	case 0xFFF9, 0xFFFA, 0xFFFB, 0xFFFC, 0xFFFD:
		return true
	}
	return false
}

// C.7 Inappropriate for canonical representation.
func inappropriateForCanonicalRepresentation(c rune) bool {
	return c >= 0x2FF0 && c <= 0x2FFB
}

// C.8 Change display properties or are deprecated.
func changeDisplayPropertiesOrDeprecated(c rune) bool {
	switch c {
	case 0x0340, 0x0341, 0x200E, 0x200F, 0x202A, 0x202B, 0x202C, 0x202D,
		0x202E, 0x206A, 0x206B, 0x206C, 0x206D, 0x206E, 0x206F:
		return true
	}
	return false
}

// C.9 Tagging characters.
func taggingCharacter(c rune) bool {
	return c == 0xE0001 || c >= 0xE0020 && c <= 0xE007F
}

// A.1 Unassigned code points in Unicode 3.2 (generated table).
func unassignedCodePoint(c rune) bool {
	lo, hi := 0, len(unassignedCodePoints)-1
	for lo <= hi {
		mid := (lo + hi) / 2
		switch r := unassignedCodePoints[mid]; {
		case c < r[0]:
			hi = mid - 1
		case c > r[1]:
			lo = mid + 1
		default:
			return true
		}
	}
	return false
}
