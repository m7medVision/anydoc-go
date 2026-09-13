package model

import (
	"strconv"
	"strings"
)

// MarkerKind is the marker family a list level uses in the source document.
type MarkerKind int

const (
	// MarkerBullet: unordered.
	MarkerBullet MarkerKind = iota
	// MarkerDecimal: 1, 2, 3.
	MarkerDecimal
	// MarkerLowerAlpha: a, b, c.
	MarkerLowerAlpha
	// MarkerUpperAlpha: A, B, C.
	MarkerUpperAlpha
	// MarkerLowerRoman: i, ii, iii.
	MarkerLowerRoman
	// MarkerUpperRoman: I, II, III.
	MarkerUpperRoman
)

// Ordered reports whether the kind is a numbered one: every kind but
// MarkerBullet.
func (m MarkerKind) Ordered() bool { return m != MarkerBullet }

// Label is the marker text for ordinal n (1-based), without trailing
// space: "3.", "c.", "iv."; bullets have no ordinal text.
func (m MarkerKind) Label(n uint64) string {
	if m == MarkerBullet {
		return "-"
	}
	return m.Ordinal(n) + "."
}

// Ordinal is the bare ordinal text for n without punctuation: "3", "c",
// "iv". Bullets render as their marker.
func (m MarkerKind) Ordinal(n uint64) string {
	switch m {
	case MarkerBullet:
		return "-"
	case MarkerDecimal:
		return strconv.FormatUint(n, 10)
	case MarkerLowerAlpha:
		return alpha(n)
	case MarkerUpperAlpha:
		return strings.ToUpper(alpha(n))
	case MarkerLowerRoman:
		return roman(n)
	case MarkerUpperRoman:
		return strings.ToUpper(roman(n))
	}
	return strconv.FormatUint(n, 10)
}

// alpha maps 1 -> a, 26 -> z, 27 -> aa (bijective base 26).
func alpha(n uint64) string {
	if n == 0 {
		return "0"
	}
	var out []byte
	for n > 0 {
		n--
		out = append(out, byte('a'+n%26))
		n /= 26
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}

// roman renders lowercase numerals; 0 and anything past 3999 fall back to
// the plain number, as upstream does.
func roman(n uint64) string {
	if n == 0 || n > 3999 {
		return strconv.FormatUint(n, 10)
	}
	numerals := [13]struct {
		value   uint64
		numeral string
	}{
		{1000, "m"}, {900, "cm"}, {500, "d"}, {400, "cd"},
		{100, "c"}, {90, "xc"}, {50, "l"}, {40, "xl"},
		{10, "x"}, {9, "ix"}, {5, "v"}, {4, "iv"}, {1, "i"},
	}
	var out []byte
	for _, num := range numerals {
		for n >= num.value {
			out = append(out, num.numeral...)
			n -= num.value
		}
	}
	return string(out)
}

// List is a fully resolved list: numbering identity and marker resolution
// happen in the frontends, which split runs whenever the list instance or
// marker kind changes.
type List struct {
	// Marker is the marker family every item in this run uses.
	Marker MarkerKind
	// Start is the ordinal of the first item, from the source's own
	// numbering.
	Start uint64
	// Items holds the items, in order.
	Items []ListItem
}

// Ordered reports whether this list's marker is a numbered one.
func (l List) Ordered() bool { return l.Marker.Ordered() }

// ListItem is one item of a List, which may hold nested blocks including
// further lists.
type ListItem struct {
	// Blocks is the item's content.
	Blocks []Block
	// MarkerLabel is literal marker text that overrides the level marker
	// when the source number text cannot be reproduced from Marker +
	// position alone (composite number text such as "1-a)"); "" when the
	// computed marker applies.
	MarkerLabel string
}
