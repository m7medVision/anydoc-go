// Port of src/shared/blockstyle.rs: paragraph styles that name a block
// container. Word, Pandoc and LibreOffice all mark quotations and
// preformatted text with a built-in paragraph style name rather than
// dedicated markup, so the name is the only signal a frontend has.

package shared

import (
	"strings"

	"github.com/m7medVision/anydoc-go/internal/model"
)

// BlockStyle is the block container a paragraph style designates.
type BlockStyle int

const (
	// BlockStyleQuote is a block quotation.
	BlockStyleQuote BlockStyle = iota
	// BlockStyleCode is a preformatted code container.
	BlockStyleCode
)

// FromStyleName returns the container a paragraph style name designates.
// ODF encodes spaces in internal style names as `_20_`.
func FromStyleName(name string) (BlockStyle, bool) {
	name = strings.ReplaceAll(name, "_20_", " ")
	switch asciiLower(strings.TrimSpace(name)) {
	case "quote", "intense quote", "block text", "quotations":
		return BlockStyleQuote, true
	case "html preformatted", "source code", "preformatted text":
		return BlockStyleCode, true
	default:
		return 0, false
	}
}

// StyledRun folds consecutive paragraphs sharing one styled container into
// a single block: producers write a multi-paragraph quote, and a code block
// one line per paragraph, as a run of separately styled paragraphs.
type StyledRun struct {
	kind   styledKind
	blocks []model.Block
	lines  []string
}

type styledKind int

const (
	styledEmpty styledKind = iota
	styledQuote
	styledCode
)

func (r *StyledRun) style() (BlockStyle, bool) {
	switch r.kind {
	case styledQuote:
		return BlockStyleQuote, true
	case styledCode:
		return BlockStyleCode, true
	default:
		return 0, false
	}
}

// Push adds one styled paragraph, closing the open container first when the
// style changes.
func (r *StyledRun) Push(style BlockStyle, inlines []model.Inline, out *[]model.Block) {
	if cur, ok := r.style(); !ok || cur != style {
		r.Flush(out)
		switch style {
		case BlockStyleQuote:
			*r = StyledRun{kind: styledQuote}
		case BlockStyleCode:
			*r = StyledRun{kind: styledCode}
		}
	}
	switch r.kind {
	case styledCode:
		// Code is literal text; character styling is presentation the
		// source applied to its syntax, never content.
		r.lines = append(r.lines, model.InlinesToPlainText(inlines))
	case styledQuote:
		if !model.InlinesAreEmpty(inlines) {
			r.blocks = append(r.blocks, model.Paragraph{Inlines: inlines})
		}
	}
}

// Flush closes the open container, if any.
func (r *StyledRun) Flush(out *[]model.Block) {
	kind, blocks, lines := r.kind, r.blocks, r.lines
	*r = StyledRun{}
	switch kind {
	case styledQuote:
		if len(blocks) > 0 {
			*out = append(*out, model.Quote{Blocks: blocks})
		}
	case styledCode:
		// Blank lines count only between the code's own lines.
		first, last := -1, -1
		for i, l := range lines {
			if strings.TrimSpace(l) != "" {
				if first < 0 {
					first = i
				}
				last = i
			}
		}
		if first >= 0 {
			*out = append(*out, model.CodeBlock{Code: strings.Join(lines[first:last+1], "\n")})
		}
	}
}
