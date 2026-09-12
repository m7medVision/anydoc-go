// Package formats hosts one frontend per input format: the canonical
// Format selection type, content and extension detection, and the parse
// dispatch into the document model.
package formats

import (
	"bytes"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/m7medVision/anydoc-go/internal/cerr"
	"github.com/m7medVision/anydoc-go/internal/model"
)

// Format is the input format; it selects the parser. Container variants
// that share a parser (docm, xlsm, ...) map onto these through content
// detection or the extension maps.
type Format int

const (
	// Auto asks for detection from the content itself. It is an
	// argument-only value: never a detection result, and never a parser
	// selection.
	Auto Format = iota
	// Doc: binary Word 97-2003 (.doc).
	Doc
	// Docx: WordprocessingML (.docx, .docm), both Transitional and Strict.
	Docx
	// Odt: OpenDocument Text (.odt).
	Odt
	// PDF converts via the pdf engine, which emits Markdown directly:
	// parsing into the document model is unsupported for PDFs. Scanned or
	// image-only pages need OCR, which anydoc does not do: the conversion
	// errors naming them.
	PDF
	// Ppt: binary PowerPoint 97-2003 (.ppt, .pps, .pot).
	Ppt
	// Pptx: PresentationML (.pptx, .pptm, .ppsx, .ppsm).
	Pptx
	// RTF: Rich Text Format (.rtf).
	RTF
	// Epub: EPUB 2 and 3 (.epub).
	Epub
	// Excel workbooks: .xlsx, .xlsm, binary .xlsb, and legacy OLE-based
	// .xls.
	Excel
	// Ods: OpenDocument Spreadsheet (.ods).
	Ods
	// Odp: OpenDocument Presentation (.odp).
	Odp
	// CSV: delimiter-separated text (.csv). Carries no signature, so it
	// has to be named rather than detected.
	CSV
)

var formatNames = [...]string{
	Auto:  "Auto",
	Doc:   "Doc",
	Docx:  "Docx",
	Odt:   "Odt",
	PDF:   "Pdf",
	Ppt:   "Ppt",
	Pptx:  "Pptx",
	RTF:   "Rtf",
	Epub:  "Epub",
	Excel: "Excel",
	Ods:   "Ods",
	Odp:   "Odp",
	CSV:   "Csv",
}

// String names the format as anydoc's Format Debug output does.
func (f Format) String() string {
	if f < 0 || int(f) >= len(formatNames) {
		return "Format(" + strconv.Itoa(int(f)) + ")"
	}
	return formatNames[f]
}

// FromBytes detects the format from the content itself: the signature and
// identity each container specification designates (PDF header, RTF open
// group, OLE stream names, ZIP package mimetype/content types). Plain-text
// formats (CSV) carry no signature and return false; so does anything
// unrecognized.
//
// Placeholder until the detection frontend (the detect.rs port in
// detect.go) lands: nothing is recognized from content alone yet, so
// callers fall back to the extension maps.
func FromBytes(data []byte) (Format, bool) {
	return Auto, false
}

// FromExtension names the format an extension names (leading dot
// optional), matched case-insensitively. False for anything unrecognized.
func FromExtension(ext string) (Format, bool) {
	ext = strings.ToLower(strings.TrimPrefix(ext, "."))
	switch ext {
	case "doc":
		return Doc, true
	case "docx", "docm":
		return Docx, true
	case "odt":
		return Odt, true
	case "pdf":
		return PDF, true
	case "pptx", "pptm", "ppsx", "ppsm":
		return Pptx, true
	case "ppt", "pps", "pot":
		return Ppt, true
	case "rtf":
		return RTF, true
	case "epub":
		return Epub, true
	case "xlsx", "xlsm", "xlsb", "xls":
		return Excel, true
	case "ods":
		return Ods, true
	case "odp":
		return Odp, true
	case "csv":
		return CSV, true
	}
	return Auto, false
}

// FromPath names the format a path's extension names. False when the path
// has no extension or names nothing recognized.
func FromPath(path string) (Format, bool) {
	ext := filepath.Ext(path)
	if ext == "" {
		return Auto, false
	}
	return FromExtension(ext)
}

// parseFunc parses one input format's bytes into the document model.
type parseFunc func(data []byte) (model.Document, error)

// parseFuncs dispatches each format to its frontend; frontends register
// here as their tickets land.
var parseFuncs = map[Format]parseFunc{}

// Parse parses bytes into the document model with the named frontend.
func Parse(data []byte, format Format) (model.Document, error) {
	if format == PDF {
		// pdf-inspector produces Markdown directly; there is no document
		// model for PDFs. ToMarkdownBytes routes them to PDFToMarkdown.
		return model.Document{}, &cerr.Error{
			Kind:   cerr.KindUnsupported,
			Detail: "PDF converts directly to Markdown; use to_markdown or to_markdown_bytes",
		}
	}
	// RTF files wearing a .doc extension are common in the wild.
	if format == Doc && bytes.HasPrefix(data, []byte("{\\rtf")) {
		if parse, ok := parseFuncs[RTF]; ok {
			return parse(data)
		}
	}
	if parse, ok := parseFuncs[format]; ok {
		return parse(data)
	}
	return model.Document{}, &cerr.Error{
		Kind:   cerr.KindUnsupported,
		Detail: "no parser linked for format " + format.String(),
	}
}

// PDFToMarkdown converts PDF bytes to Markdown through the pdf engine,
// bypassing the document model.
//
// Placeholder until the pdf track lands: PDFs are unsupported meanwhile.
func PDFToMarkdown(data []byte) (string, error) {
	return "", &cerr.Error{
		Kind:   cerr.KindUnsupported,
		Detail: "PDF conversion is not available in this build",
	}
}
