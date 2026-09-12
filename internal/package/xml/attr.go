// Attribute parsing: quick-xml 0.41's Attributes iterator state machine
// (XML mode) and attribute-value normalization
// (normalize_xml10_attribute_value with the five predefined XML entities).
package xml

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// attrErrorKind marks a malformed attribute. Error positions only feed log
// lines anydoc drops, so the variants carry no payload.
type attrErrorKind uint8

const (
	attrExpectedEq attrErrorKind = iota + 1
	attrExpectedValue
	attrUnquotedValue
	attrExpectedQuote
	attrDuplicated
)

// attr iteration states (IterState::State).
const (
	attrStateDone = iota
	attrStateNext
	attrStateSkipValue
	attrStateSkipEqValue
)

// attrIter walks one start tag's attribute list. content is the tag content
// between '<' and '>' ("name key='value' ..."); attributes start after the
// element name.
type attrIter struct {
	content   []byte
	offset    int // where the next attribute scan resumes
	state     int
	keys      [][2]int // ranges of previously seen attribute names
	checkDups bool
}

// newAttrIter returns an iterator over the attributes of tag content.
func newAttrIter(content []byte, checkDuplicates bool) *attrIter {
	return &attrIter{content: content, offset: nameLen(content), state: attrStateNext, checkDups: checkDuplicates}
}

// next yields one attribute per call. err != nil marks a malformed attribute
// (recovery positions are already baked into the iterator state, matching
// quick-xml's recover-on-error iteration); ok is false once the list is
// exhausted.
func (it *attrIter) next() (key, value []byte, err attrErrorKind, ok bool) {
	off, ok := it.recover()
	if !ok {
		return nil, nil, 0, false
	}
	s := it.content

	// Index where the key starts.
	i := off
	for i < len(s) && isWhitespace(s[i]) {
		i++
	}
	if i >= len(s) {
		it.state = attrStateDone
		return nil, nil, 0, false
	}
	startKey := i

	// Span of the key: up to '=' or whitespace.
	keyEnd := -1
	afterKey := -1
	for ; i < len(s); i++ {
		if s[i] == '=' {
			keyEnd, afterKey = i, i
			break
		}
		if isWhitespace(s[i]) {
			keyEnd = i
			// The '=' may follow the separating whitespace.
			for i < len(s) && isWhitespace(s[i]) {
				i++
			}
			if i < len(s) && s[i] == '=' {
				afterKey = i
				break
			}
			// Bare attribute without '='.
			if i >= len(s) {
				it.state = attrStateDone
			} else {
				it.state = attrStateNext
				it.offset = i
			}
			return s[startKey:keyEnd], nil, attrExpectedEq, true
		}
	}
	if afterKey < 0 {
		// Input ran out inside the key: `key` alone.
		it.state = attrStateDone
		return s[startKey:], nil, attrExpectedEq, true
	}
	i = afterKey + 1 // past '='

	// Position of the value's opening quote.
	for i < len(s) && isWhitespace(s[i]) {
		i++
	}
	if i >= len(s) {
		it.state = attrStateDone
		return nil, nil, attrExpectedValue, true
	}
	quote := s[i]
	if quote != '\'' && quote != '"' {
		it.state = attrStateSkipValue
		it.offset = i
		return nil, nil, attrUnquotedValue, true
	}
	startValue := i + 1

	// Duplicate names are errors in checked mode (the namespace-binding pass
	// runs unchecked).
	if it.checkDups {
		for _, r := range it.keys {
			if bytesEqual(s[r[0]:r[1]], s[startKey:keyEnd]) {
				it.state = attrStateSkipEqValue
				it.offset = afterKey
				return nil, nil, attrDuplicated, true
			}
		}
		it.keys = append(it.keys, [2]int{startKey, keyEnd})
	}

	end := -1
	for j := startValue; j < len(s); j++ {
		if s[j] == quote {
			end = j
			break
		}
	}
	if end < 0 {
		it.state = attrStateDone
		return nil, nil, attrExpectedQuote, true
	}
	it.state = attrStateNext
	it.offset = end + 1
	return s[startKey:keyEnd], s[startValue:end], 0, true
}

// recover resumes from the previous error's saved position.
func (it *attrIter) recover() (int, bool) {
	switch it.state {
	case attrStateDone:
		return 0, false
	case attrStateNext:
		return it.offset, true
	case attrStateSkipValue:
		for i := it.offset; i < len(it.content); i++ {
			if isWhitespace(it.content[i]) {
				return i, true
			}
		}
		return 0, false
	default: // attrStateSkipEqValue
		s := it.content
		i := it.offset
		for i < len(s) && isWhitespace(s[i]) {
			i++
		}
		if i >= len(s) {
			return 0, false
		}
		if q := s[i]; q == '\'' || q == '"' {
			for j := i + 1; j < len(s); j++ {
				if s[j] == q {
					return j, true
				}
			}
			return 0, false
		}
		for j := i; j < len(s); j++ {
			if isWhitespace(s[j]) {
				return j, true
			}
		}
		return 0, false
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// isNamespaceBindingKey reports whether an attribute key is an xmlns
// declaration: "xmlns" or "xmlns:prefix" (QName::as_namespace_binding).
func isNamespaceBindingKey(key []byte) bool {
	if len(key) < 5 || string(key[:5]) != "xmlns" {
		return false
	}
	return len(key) == 5 || key[5] == ':'
}

// nsBindingPrefix returns the declared prefix for an xmlns key: nil for the
// default namespace, else the bytes after "xmlns:" (possibly empty).
func nsBindingPrefix(key []byte) []byte {
	if len(key) == 5 {
		return nil
	}
	return key[6:]
}

// normalizeAttrValue decodes and normalizes one attribute value the way
// quick-xml's decoded_and_normalized_value does for XML 1.0: strict UTF-8
// decoding, then \t / \r / \r\n / \n become spaces and the five predefined
// entities plus character references expand. ok is false for any escape
// error, mirroring the Err callers answer with the raw lossy value.
func normalizeAttrValue(value []byte) (string, bool) {
	if !utf8.Valid(value) {
		return "", false
	}
	v := string(value)
	i := strings.IndexFunc(v, isNormalizationChar)
	if i < 0 {
		return v, true
	}
	var out strings.Builder
	out.Grow(len(v))
	pos := 0
	for i >= 0 {
		out.WriteString(v[pos:i])
		next, ok := normalizeStep(&out, v, i)
		if !ok {
			return "", false
		}
		pos = next
		i = indexFuncFrom(v, pos, isNormalizationChar)
	}
	out.WriteString(v[pos:])
	return out.String(), true
}

func isNormalizationChar(r rune) bool {
	return r == '\t' || r == '\r' || r == '\n' || r == '&'
}

func indexFuncFrom(s string, from int, f func(rune) bool) int {
	for i, r := range s[from:] {
		if f(r) {
			return from + i
		}
	}
	return -1
}

// normalizeStep handles the normalization character at v[pos]; bytes before
// pos were already written. Returns the next unconsumed index.
func normalizeStep(out *strings.Builder, v string, pos int) (int, bool) {
	switch v[pos] {
	case '&':
		rest := v[pos+1:]
		end := strings.IndexByte(rest, ';')
		if end < 0 {
			return 0, false // UnterminatedEntity
		}
		pat := rest[:end]
		switch {
		case strings.HasPrefix(pat, "#"):
			r, ok := parseCharRef(pat[1:])
			if !ok {
				return 0, false
			}
			out.WriteRune(r)
		case pat == "amp":
			out.WriteByte('&')
		case pat == "lt":
			out.WriteByte('<')
		case pat == "gt":
			out.WriteByte('>')
		case pat == "apos":
			out.WriteByte('\'')
		case pat == "quot":
			out.WriteByte('"')
		default:
			return 0, false // UnrecognizedEntity
		}
		return pos + 1 + end + 1, true
	case '\t':
		out.WriteByte(' ')
		return pos + 1, true
	case '\r':
		out.WriteByte(' ')
		if pos+1 < len(v) && v[pos+1] == '\n' {
			return pos + 2, true
		}
		return pos + 1, true
	default: // '\n'
		out.WriteByte(' ')
		return pos + 1, true
	}
}

// parseCharRef parses a character reference body per quick-xml's
// parse_number: decimal, or hexadecimal after a lowercase 'x'; no sign
// allowed; zero, surrogates, and out-of-range values are errors.
func parseCharRef(num string) (rune, bool) {
	base := 10
	if strings.HasPrefix(num, "x") {
		base, num = 16, num[1:]
	}
	if num == "" {
		return 0, false
	}
	for i := 0; i < len(num); i++ {
		c := num[i]
		digit := c >= '0' && c <= '9' ||
			(base == 16 && ((c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')))
		if !digit {
			return 0, false
		}
	}
	code, err := strconv.ParseUint(num, base, 32)
	if err != nil {
		return 0, false
	}
	if code == 0 || code > 0x10FFFF || (code >= 0xD800 && code <= 0xDFFF) {
		return 0, false
	}
	return rune(code), true
}
