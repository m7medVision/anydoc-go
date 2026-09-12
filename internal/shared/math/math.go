// Package math converts formulas to LaTeX, the form model.InlineMath and
// model.MathBlock carry.
package math

import (
	"strings"

	"github.com/m7medVision/anydoc-go/internal/model"
)

// MathLines returns the equations of a paragraph that holds nothing else,
// for formats whose math paragraphs arrive as inline content: such a
// paragraph is displayed math, one block per equation. ok is false when the
// paragraph holds any other visible content or no equations at all.
func MathLines(inlines []model.Inline) (lines []string, ok bool) {
	for _, i := range inlines {
		switch v := i.(type) {
		case model.InlineMath:
			lines = append(lines, v.TeX)
		case model.LineBreak:
			// Layout, not content.
		case model.Run:
			if strings.TrimSpace(v.Text) != "" {
				return nil, false
			}
		default:
			return nil, false
		}
	}
	if len(lines) == 0 {
		return nil, false
	}
	return lines, true
}
