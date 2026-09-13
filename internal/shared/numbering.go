// Port of src/shared/numbering.rs: shared list-number pattern IR.
//
// A level's number text is a sequence of literal pieces and level
// references, rendered against the live counter values. Every frontend
// resolves its native syntax onto this one form: WordprocessingML
// w:lvlText (`%1.%2)`), the DOC xst placeholder string, RTF
// \leveltext/\levelnumbers, and ODF prefix/suffix/display-levels.
// Rendering returns ok=false when the pattern reproduces the default label
// the renderer builds from marker + value alone, so native Markdown list
// numbering is used whenever it is faithful.

package shared

import (
	"strings"

	"github.com/m7medVision/anydoc-go/internal/model"
)

// NumberText is one piece of a level's number text: literal characters, or
// the current number of a (zero-based) level.
type NumberText struct {
	// Literal holds characters when IsLevel is false.
	Literal string
	// Level is the zero-based referenced level when IsLevel is true.
	Level uint8
	// IsLevel reports a level reference rather than a literal.
	IsLevel bool
}

// NumberPattern is a level's resolved number pattern.
type NumberPattern struct {
	// Text is empty when no pattern is known; the marker kind's default
	// label applies.
	Text []NumberText
	// Legal numbering (w:isLgl, LVLF fLegal): referenced levels render as
	// decimal regardless of their own format.
	Legal bool
}

// ParsePercentPattern parses WordprocessingML-style percent patterns
// (`%1`–`%9` level references, everything else literal) into pattern tokens.
func ParsePercentPattern(text string) []NumberText {
	var out []NumberText
	rs := []rune(text)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		if c == '%' && i+1 < len(rs) {
			d := rs[i+1]
			if d >= '1' && d <= '9' {
				out = append(out, NumberText{IsLevel: true, Level: uint8(d - '1')})
				i++
				continue
			}
		}
		if n := len(out); n > 0 && !out[n-1].IsLevel {
			out[n-1].Literal += string(c)
		} else {
			out = append(out, NumberText{Literal: string(c)})
		}
	}
	return out
}

// CompositeLabel renders a pattern against the current sequence values.
// levelMarker and levelValue supply each referenced level's marker kind
// and current ordinal (the level's start value when it has not begun
// counting). ok is false when the result matches the default label
// produced from the own level's marker and value.
func CompositeLabel(
	pattern NumberPattern,
	ownMarker model.MarkerKind,
	ownValue uint64,
	levelMarker func(int) model.MarkerKind,
	levelValue func(int) uint64,
) (string, bool) {
	if len(pattern.Text) == 0 {
		return "", false
	}
	var out strings.Builder
	for _, piece := range pattern.Text {
		if !piece.IsLevel {
			out.WriteString(piece.Literal)
			continue
		}
		l := int(piece.Level)
		kind := levelMarker(l)
		if pattern.Legal {
			kind = model.MarkerDecimal
		}
		out.WriteString(kind.Ordinal(levelValue(l)))
	}
	s := out.String()
	if s == ownMarker.Label(ownValue) {
		return "", false
	}
	return s, true
}
