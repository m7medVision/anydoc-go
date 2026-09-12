package model

import "strings"

// Inline is one span of inline content: a sealed sum mirroring anydoc's
// Inline enum. Only the node types in this package implement it.
type Inline interface {
	isInline()
}

// Run is styled text. Runs are split wherever the style changes.
type Run struct {
	// Text is the text itself.
	Text string
	// Style is the character style covering all of it.
	Style Style
}

// Plain builds unstyled text.
func Plain(text string) Run { return Run{Text: text} }

// Link is a hyperlink wrapping its own inline content.
type Link struct {
	// Inlines is the link text, which may carry its own styling.
	Inlines []Inline
	// Target is where it points.
	Target LinkTarget
}

// Image is an inline image.
type Image struct {
	// Alt is the alt text, "" when the source gives none. Markdown cannot
	// embed bytes, so this is what an embedded image renders as.
	Alt string
	// Source is where the bytes are, or that they are gone.
	Source ImageSource
}

// Anchor is a zero-width marker naming an internal link target at this
// position (bookmarks on paragraphs, spans, list items, table cells, ...).
type Anchor struct {
	ID AnchorID
}

// NoteRef is a reference to the Note with this id.
type NoteRef struct {
	NoteID string
}

// LineBreak is a line break inside a block, not a new block.
type LineBreak struct{}

// InlineMath is an inline formula, as LaTeX math without delimiters.
type InlineMath struct {
	TeX string
}

// Checkbox is a checkbox control with its state.
type Checkbox struct {
	Checked bool
}

func (Run) isInline()        {}
func (Link) isInline()       {}
func (Image) isInline()      {}
func (Anchor) isInline()     {}
func (NoteRef) isInline()    {}
func (LineBreak) isInline()  {}
func (InlineMath) isInline() {}
func (Checkbox) isInline()   {}

// CheckboxText is the Markdown task-list token for a checkbox state.
func CheckboxText(checked bool) string {
	if checked {
		return "[x]"
	}
	return "[ ]"
}

// InlinesToPlainText flattens inlines to their text, dropping styling and
// links but keeping link text, image alt text, and formula source. Line
// breaks become newlines; anchors and note references contribute nothing.
func InlinesToPlainText(inlines []Inline) string {
	var out strings.Builder
	collectPlainText(&out, inlines)
	return out.String()
}

func collectPlainText(out *strings.Builder, inlines []Inline) {
	for _, inline := range inlines {
		switch inl := inline.(type) {
		case Run:
			out.WriteString(inl.Text)
		case Link:
			collectPlainText(out, inl.Inlines)
		case Image:
			out.WriteString(inl.Alt)
		case InlineMath:
			out.WriteString(inl.TeX)
		case Checkbox:
			out.WriteString(CheckboxText(inl.Checked))
		case LineBreak:
			out.WriteByte('\n')
		}
	}
}

// InlinesAreEmpty reports whether nothing here would render as visible
// content: only whitespace, empty-target links, anchors, and line breaks.
// An image or a note reference always counts as content.
func InlinesAreEmpty(inlines []Inline) bool {
	for _, inline := range inlines {
		switch inl := inline.(type) {
		case Run:
			if strings.TrimSpace(inl.Text) != "" {
				return false
			}
		case Link:
			if !inl.Target.IsEmpty() || !InlinesAreEmpty(inl.Inlines) {
				return false
			}
		case InlineMath:
			if strings.TrimSpace(inl.TeX) != "" {
				return false
			}
		case Image, NoteRef, Checkbox:
			return false
		}
	}
	return true
}
