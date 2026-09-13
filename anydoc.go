// Package anydoc converts documents to GitHub-Flavored Markdown.
//
// These are Go bindings for the anydoc 0.2.4 Rust crate (the same engine
// as the Python and Node packages). PDF conversion uses pdf-inspector
// 1.14.2 inside that crate. Building requires cgo and a Rust toolchain.
package anydoc

import "fmt"

// Format names the parser, using the extension that identifies it.
// Container variants that share a parser (.docm, .xlsm, .ppsx, ...) map
// onto these via FormatFromBytes or FormatFromExtension.
type Format string

const (
	FormatDoc  Format = "doc"
	FormatDocx Format = "docx"
	FormatOdt  Format = "odt"
	FormatPDF  Format = "pdf"
	FormatPPT  Format = "ppt"
	FormatPPTX Format = "pptx"
	FormatRTF  Format = "rtf"
	FormatEPUB Format = "epub"
	FormatXLSX Format = "xlsx"
	FormatODS  Format = "ods"
	FormatODP  Format = "odp"
	FormatCSV  Format = "csv"
)

var formats = []Format{
	FormatDoc, FormatDocx, FormatOdt, FormatPDF, FormatPPT, FormatPPTX,
	FormatRTF, FormatEPUB, FormatXLSX, FormatODS, FormatODP, FormatCSV,
}

func parseFormat(name string) (Format, error) {
	for _, f := range formats {
		if f == Format(name) {
			return f, nil
		}
	}
	return "", fmt.Errorf("unknown format %q; expected one of %s", name, joinFormats())
}

func joinFormats() string {
	out := ""
	for i, f := range formats {
		if i > 0 {
			out += ", "
		}
		out += string(f)
	}
	return out
}

// FormatFromBytes detects the format from the content itself: the signature
// and identity each container specification designates (PDF header, RTF open
// group, OLE stream names, ZIP package mimetype/content types). Plain-text
// formats (CSV) carry no signature and return false; so does anything
// unrecognized.
func FormatFromBytes(data []byte) (Format, bool) {
	return formatFromBytes(data)
}

// FormatFromExtension is the format an extension names, with or without a
// leading dot.
func FormatFromExtension(extension string) (Format, bool) {
	return formatFromExtension(extension)
}

// FormatFromPath is the format a path's extension names.
func FormatFromPath(path string) (Format, bool) {
	return formatFromPath(path)
}

// ToMarkdown converts a document file to Markdown. The format is detected
// from the file content; the extension is the fallback for signature-less
// formats (CSV) and unrecognizable containers.
func ToMarkdown(path string) (string, error) {
	return toMarkdown(path)
}

// ToMarkdownBytes converts an in-memory document to Markdown. Without a
// format (the empty string), it is detected from the content, which
// signature-less formats (CSV) have to name explicitly.
func ToMarkdownBytes(data []byte, format Format) (string, error) {
	if format != "" {
		if _, err := parseFormat(string(format)); err != nil {
			return "", err
		}
	}
	return toMarkdownBytes(data, format)
}

// ToDocument parses an in-memory document into the document model, which
// also carries the embedded assets. Without a format (the empty string), it
// is detected from the content.
//
// Unsupported for PDF: PDF conversion produces Markdown directly and has no
// document-model form; use ToMarkdownBytes.
func ToDocument(data []byte, format Format) (Document, error) {
	if format != "" {
		if _, err := parseFormat(string(format)); err != nil {
			return Document{}, err
		}
	}
	return toDocument(data, format)
}

//go:generate sh native/build.sh
