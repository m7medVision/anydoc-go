// Port of src/shared/uri.rs: URI classification shared across frontends
// (EPUB hrefs, ODF hrefs and image references, Word field targets).

package shared

import "strings"

// hasScheme reports whether s starts with an RFC 3986 scheme followed by
// ':' (scheme = ALPHA *( ALPHA / DIGIT / "+" / "-" / "." )). One-character
// schemes are valid.
func hasScheme(s string) bool {
	colon := strings.IndexByte(s, ':')
	if colon < 0 {
		return false
	}
	prefix := s[:colon]
	if prefix == "" {
		return false
	}
	for i, c := range prefix {
		ok := isASCIILetter(c) && i == 0 || i > 0 && (isASCIILetter(c) || isASCIIDigit(c) ||
			c == '+' || c == '-' || c == '.')
		if !ok {
			return false
		}
	}
	return true
}

// isDrivePath reports whether s is a Windows drive-letter path (C:\...,
// C:/...). They parse as a one-letter scheme under RFC 3986, but in
// documents they are local file paths (legacy Word HYPERLINK fields),
// never URIs.
func isDrivePath(s string) bool {
	return len(s) >= 3 && isASCIILetter(rune(s[0])) && s[1] == ':' &&
		(s[2] == '\\' || s[2] == '/')
}

// IsAbsoluteURI reports whether s should be treated as an absolute URI
// with a scheme rather than a relative/package reference.
func IsAbsoluteURI(s string) bool {
	return hasScheme(s) && !isDrivePath(s)
}

func isASCIILetter(c rune) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isASCIIDigit(c rune) bool {
	return c >= '0' && c <= '9'
}
