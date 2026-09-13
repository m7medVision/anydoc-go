// Port of src/shared/delta.rs: tri-state style deltas used during cascade
// resolution. A property is either explicitly on, explicitly off, or unset
// (inherit); only after the full cascade is a delta collapsed into the
// model's resolved Style.

package shared

import "github.com/m7medVision/anydoc-go/internal/model"

// StyleDelta is a tri-state overlay of character-style toggles. A nil
// pointer means unset (inherit); a non-nil pointer is an explicit on or off.
type StyleDelta struct {
	Bold   *bool
	Italic *bool
	Strike *bool
	Code   *bool
}

// Merge overlays child on d: an explicit child value (on or off) wins;
// unset inherits.
func (d StyleDelta) Merge(child StyleDelta) StyleDelta {
	return StyleDelta{
		Bold:   orBool(child.Bold, d.Bold),
		Italic: orBool(child.Italic, d.Italic),
		Strike: orBool(child.Strike, d.Strike),
		Code:   orBool(child.Code, d.Code),
	}
}

func orBool(child, parent *bool) *bool {
	if child != nil {
		return child
	}
	return parent
}

// Apply overlays d onto base: unset fields keep the base value.
func (d StyleDelta) Apply(base model.Style) model.Style {
	if d.Bold != nil {
		base.Bold = *d.Bold
	}
	if d.Italic != nil {
		base.Italic = *d.Italic
	}
	if d.Strike != nil {
		base.Strike = *d.Strike
	}
	if d.Code != nil {
		base.Code = *d.Code
	}
	return base
}

// Resolve collapses d against the plain style.
func (d StyleDelta) Resolve() model.Style {
	return d.Apply(model.Style{})
}

// RebaseEmphasis drops from every run the emphasis base already carries. A
// heading style defines its own typography, so its runs should carry only
// what they add beyond it — otherwise the bold in `## **Heading**` is the
// style's, not the author's.
func RebaseEmphasis(inlines []model.Inline, base model.Style) {
	if base == (model.Style{}) {
		return
	}
	for i := range inlines {
		switch v := inlines[i].(type) {
		case model.Run:
			v.Style.Bold = v.Style.Bold && !base.Bold
			v.Style.Italic = v.Style.Italic && !base.Italic
			v.Style.Strike = v.Style.Strike && !base.Strike
			inlines[i] = v
		case model.Link:
			RebaseEmphasis(v.Inlines, base)
			inlines[i] = v
		}
	}
}
