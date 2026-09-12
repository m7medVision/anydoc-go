// Package anydoc converts documents to GitHub-Flavored Markdown.
//
// Recovery and skipped-content events never change conversion behavior.
// An error means conversion was impossible; the typed kinds live in
// internal/cerr, aliased here.
package anydoc

import (
	"os"

	"github.com/m7medVision/anydoc-go/internal/cerr"
	"github.com/m7medVision/anydoc-go/internal/formats"
	"github.com/m7medVision/anydoc-go/internal/model"
	"github.com/m7medVision/anydoc-go/internal/render/markdown"
)

// Format is the input format; it selects the parser.
type Format = formats.Format

// The selectable formats. Auto is the zero value: it asks for detection
// from the content and is never a detection result.
const (
	Auto  = formats.Auto
	Doc   = formats.Doc
	Docx  = formats.Docx
	Odt   = formats.Odt
	PDF   = formats.PDF
	Ppt   = formats.Ppt
	Pptx  = formats.Pptx
	RTF   = formats.RTF
	Epub  = formats.Epub
	Excel = formats.Excel
	Ods   = formats.Ods
	Odp   = formats.Odp
	CSV   = formats.CSV
)

// Error and ErrorKind are the typed conversion errors. The kinds double as
// errors.Is sentinels and Error.Code values.
type (
	Error     = cerr.Error
	ErrorKind = cerr.ErrorKind
)

// The error kinds.
const (
	KindUnsupported   = cerr.KindUnsupported
	KindNeedsOcr      = cerr.KindNeedsOcr
	KindMalformed     = cerr.KindMalformed
	KindEncrypted     = cerr.KindEncrypted
	KindResourceLimit = cerr.KindResourceLimit
	KindMissingPart   = cerr.KindMissingPart
	KindIO            = cerr.KindIO
)

// The document model (internal/model), aliased wholesale as upstream's
// public model module.
type (
	AnchorID         = model.AnchorID
	Asset            = model.Asset
	AssetID          = model.AssetID
	Block            = model.Block
	Cell             = model.Cell
	Inline           = model.Inline
	CellSlot         = model.CellSlot
	Checkbox         = model.Checkbox
	CodeBlock        = model.CodeBlock
	CoveredCell      = model.CoveredCell
	Document         = model.Document
	ExternalImage    = model.ExternalImage
	ExternalLink     = model.ExternalLink
	Heading          = model.Heading
	Image            = model.Image
	ImageSource      = model.ImageSource
	InlineMath       = model.InlineMath
	LineBreak        = model.LineBreak
	Link             = model.Link
	LinkTarget       = model.LinkTarget
	List             = model.List
	ListBlock        = model.ListBlock
	ListItem         = model.ListItem
	MarkerKind       = model.MarkerKind
	MathBlock        = model.MathBlock
	Note             = model.Note
	NoteKind         = model.NoteKind
	NoteRef          = model.NoteRef
	OriginCell       = model.OriginCell
	Paragraph        = model.Paragraph
	Quote            = model.Quote
	RelativeLink     = model.RelativeLink
	Run              = model.Run
	Rule             = model.Rule
	Style            = model.Style
	Table            = model.Table
	TableBlock       = model.TableBlock
	TableKind        = model.TableKind
	UnavailableImage = model.UnavailableImage
)

// The model's enum kinds.
const (
	NoteFootnote     = model.NoteFootnote
	NoteEndnote      = model.NoteEndnote
	MarkerBullet     = model.MarkerBullet
	MarkerDecimal    = model.MarkerDecimal
	MarkerLowerAlpha = model.MarkerLowerAlpha
	MarkerUpperAlpha = model.MarkerUpperAlpha
	MarkerLowerRoman = model.MarkerLowerRoman
	MarkerUpperRoman = model.MarkerUpperRoman
	TableData        = model.TableData
	TableLayout      = model.TableLayout
)

// Plain builds unstyled text.
func Plain(text string) Run { return model.Plain(text) }

// CheckboxText is the Markdown task-list token for a checkbox state.
func CheckboxText(checked bool) string { return model.CheckboxText(checked) }

// InlinesToPlainText flattens inlines to their text, dropping styling and
// links but keeping link text, image alt text, and formula source.
func InlinesToPlainText(inlines []Inline) string {
	return model.InlinesToPlainText(inlines)
}

// InlinesAreEmpty reports whether nothing in inlines would render as
// visible content.
func InlinesAreEmpty(inlines []Inline) bool {
	return model.InlinesAreEmpty(inlines)
}

// NewHeading builds a heading with no anchor.
func NewHeading(level uint8, inlines []Inline) Heading {
	return model.NewHeading(level, inlines)
}

// NewCell builds a table cell spanning one position.
func NewCell(blocks []Block) Cell { return model.NewCell(blocks) }

// CellFromInlines builds a one-paragraph table cell spanning one position.
func CellFromInlines(inlines []Inline) Cell {
	return model.CellFromInlines(inlines)
}

// SpanningCell builds a table cell covering colSpan by rowSpan positions.
func SpanningCell(blocks []Block, colSpan, rowSpan uint32) Cell {
	return model.SpanningCell(blocks, colSpan, rowSpan)
}

// TableFromRows builds a plain span-less table from rows of cells.
func TableFromRows(rows [][]Cell, headerRows int, kind TableKind) Table {
	return model.TableFromRows(rows, headerRows, kind)
}

// FormatFromBytes detects the format from the content itself: the
// signature and identity each container specification designates (PDF
// header, RTF open group, OLE stream names, ZIP package mimetype/content
// types). Plain-text formats (CSV) carry no signature and return false; so
// does anything unrecognized.
func FormatFromBytes(data []byte) (Format, bool) {
	return formats.FromBytes(data)
}

// FormatFromExtension names the format an extension names (leading dot
// optional), matched case-insensitively. False for anything unrecognized.
func FormatFromExtension(ext string) (Format, bool) {
	return formats.FromExtension(ext)
}

// FormatFromPath names the format a path's extension names. False when the
// path has no extension or names nothing recognized.
func FormatFromPath(path string) (Format, bool) {
	return formats.FromPath(path)
}

// ToMarkdown converts a document file to Markdown. The format is detected
// from the file content; the extension is the fallback for signature-less
// formats (CSV) and unrecognizable containers.
func ToMarkdown(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", &cerr.Error{Kind: cerr.KindIO, Err: err}
	}
	format, ok := FormatFromBytes(data)
	if !ok {
		format, ok = FormatFromPath(path)
	}
	if !ok {
		return "", &cerr.Error{
			Kind:   cerr.KindUnsupported,
			Detail: "unrecognized file content and extension: " + path,
		}
	}
	return ToMarkdownBytes(data, format)
}

// ToMarkdownBytes converts an in-memory document to Markdown. Pass a
// Format to select the parser, or Auto to detect it from the content,
// which signature-less formats (CSV) have to name explicitly.
func ToMarkdownBytes(data []byte, format Format) (string, error) {
	format, err := resolveFormat(data, format)
	if err != nil {
		return "", err
	}
	// PDFs convert to Markdown directly (the pdf engine) without passing
	// through the document model.
	if format == PDF {
		return formats.PDFToMarkdown(data)
	}
	doc, err := ToDocument(data, format)
	if err != nil {
		return "", err
	}
	return markdown.Document(doc), nil
}

// ToDocument parses an in-memory document into the document model. Pass a
// Format to select the parser, or Auto to detect it from the content.
//
// Unsupported for PDF: PDF conversion produces Markdown directly and has
// no document-model form; use ToMarkdownBytes.
func ToDocument(data []byte, format Format) (Document, error) {
	format, err := resolveFormat(data, format)
	if err != nil {
		return Document{}, err
	}
	return formats.Parse(data, format)
}

// resolveFormat trusts an explicit format even when the content disagrees;
// Auto asks the content, and unrecognized content is Unsupported.
func resolveFormat(data []byte, format Format) (Format, error) {
	if format != Auto {
		return format, nil
	}
	if detected, ok := FormatFromBytes(data); ok {
		return detected, nil
	}
	return Auto, &cerr.Error{
		Kind:   cerr.KindUnsupported,
		Detail: "unrecognized file content: name the format explicitly",
	}
}
