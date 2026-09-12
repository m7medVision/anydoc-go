package model

// Block is one block-level piece of a document body: a sealed sum mirroring
// anydoc's Block enum. Only the node types in this package implement it.
type Block interface {
	isBlock()
}

// Heading is a section heading.
type Heading struct {
	// Level is the outline depth as the source assigns it, 1-based. Word
	// outline levels reach past 6, so renderers clamp to what their target
	// supports rather than the model doing it here.
	Level uint8
	// Anchor is the stable anchor id when the source document targets this
	// heading (bookmark, chapter fragment, ...); "" when nothing links to
	// it. Renderers map it to the heading's own anchor rather than emitting
	// a separate one.
	Anchor AnchorID
	// Inlines is the heading text.
	Inlines []Inline
}

// NewHeading builds a heading with no anchor, for the common case where
// nothing in the document links to it.
func NewHeading(level uint8, inlines []Inline) Heading {
	return Heading{Level: level, Inlines: inlines}
}

// Paragraph is a run of body text.
type Paragraph struct {
	Inlines []Inline
}

// ListBlock carries a fully resolved list, with its numbering already
// resolved.
type ListBlock struct {
	List List
}

// TableBlock carries a table grid.
type TableBlock struct {
	Table Table
}

// Quote is quoted content, which nests.
type Quote struct {
	Blocks []Block
}

// CodeBlock is preformatted text.
type CodeBlock struct {
	// Language is the language hint when the source names one; "" when it
	// does not.
	Language string
	// Code is the literal text, newlines intact.
	Code string
}

// Rule is a horizontal rule.
type Rule struct{}

// MathBlock is a displayed formula, as LaTeX math without delimiters.
type MathBlock struct {
	TeX string
}

func (Heading) isBlock()    {}
func (Paragraph) isBlock()  {}
func (ListBlock) isBlock()  {}
func (TableBlock) isBlock() {}
func (Quote) isBlock()      {}
func (CodeBlock) isBlock()  {}
func (Rule) isBlock()       {}
func (MathBlock) isBlock()  {}
