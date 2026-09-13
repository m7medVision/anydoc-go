// Package formats detects the input format from the signatures each
// container specification designates, and maps extensions and paths onto
// parsers: the selector layer of the converter (anydoc's
// src/formats/detect.rs plus the Format accessors of src/lib.rs).
package formats

import (
	"strconv"
	"strings"
)

// Format identifies the input format and selects the parser. Container
// variants that share a parser (docm, xlsm, ...) map onto these via
// FromBytes or FromExtension.
type Format int

// The set of input formats.
const (
	// Auto asks the converter to detect the format from the content; the
	// zero value of Format. It is an argument-only value and never a
	// detection result.
	Auto Format = iota
	// Doc is binary Word 97-2003 (.doc).
	Doc
	// Docx is WordprocessingML (.docx, .docm), both Transitional and
	// Strict.
	Docx
	// Odt is OpenDocument Text (.odt).
	Odt
	// PDF is converted by the PDF engine, which emits Markdown directly;
	// ToDocument is unsupported for PDFs. Scanned or image-only pages
	// need OCR, which the converter does not do: the document errors with
	// a needs-OCR error naming the pages.
	PDF
	// Ppt is binary PowerPoint 97-2003 (.ppt, .pps, .pot).
	Ppt
	// Pptx is PresentationML (.pptx, .pptm, .ppsx, .ppsm).
	Pptx
	// RTF is Rich Text Format (.rtf).
	RTF
	// Epub is EPUB 2 and 3 (.epub).
	Epub
	// Excel is Excel workbooks: .xlsx, .xlsm, binary .xlsb, and legacy
	// OLE-based .xls.
	Excel
	// Ods is OpenDocument Spreadsheet (.ods).
	Ods
	// Odp is OpenDocument Presentation (.odp).
	Odp
	// CSV is delimiter-separated text (.csv). It carries no signature,
	// so it has to be named rather than detected.
	CSV
)

// Names maps each format to its canonical name, the reference
// implementation's variant spelling. Callers must not mutate it.
var Names = map[Format]string{
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

// String returns the format's canonical name, or a numbered form for a
// value outside the set.
func (f Format) String() string {
	if name, ok := Names[f]; ok {
		return name
	}
	return "Format(" + strconv.Itoa(int(f)) + ")"
}

// FromExtension returns the format a bare extension names, matched
// case-insensitively (a single leading dot is tolerated). ok reports
// whether the extension names a recognized format.
func FromExtension(ext string) (format Format, ok bool) {
	ext = strings.TrimPrefix(ext, ".")
	switch asciiLower(ext) {
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

// FromPath returns the format a path's extension names. ok reports
// whether the path has an extension naming a recognized format.
func FromPath(path string) (format Format, ok bool) {
	ext, ok := pathExtension(path)
	if !ok {
		return Auto, false
	}
	return FromExtension(ext)
}

// pathExtension returns the portion of the final file name after its
// last dot, with the reference implementation's path semantics: none
// when there is no file name, no embedded dot, or the file name begins
// with the (only) dot.
func pathExtension(path string) (string, bool) {
	name := fileName(path)
	dot := strings.LastIndexByte(name, '.')
	if dot <= 0 {
		return "", false
	}
	return name[dot+1:], true
}

// fileName returns the last path component, ignoring a trailing
// separator and "." components; ".." and separator-only paths have no
// file name.
func fileName(path string) string {
	comps := strings.Split(path, "/")
	for i := len(comps) - 1; i >= 0; i-- {
		switch comps[i] {
		case "", ".":
			continue
		case "..":
			return ""
		default:
			return comps[i]
		}
	}
	return ""
}

// asciiLower lowercases ASCII letters only, leaving every other byte
// untouched (Rust's to_ascii_lowercase).
func asciiLower(s string) string {
	hasUpper := false
	for i := 0; i < len(s); i++ {
		if 'A' <= s[i] && s[i] <= 'Z' {
			hasUpper = true
			break
		}
	}
	if !hasUpper {
		return s
	}
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}
