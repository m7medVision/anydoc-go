// Package formats hosts one frontend per input format: the canonical
// Format selection type, content and extension detection, and the parse
// dispatch into the document model.
package formats

import (
	"bytes"

	"github.com/m7medVision/anydoc-go/internal/cerr"
	"github.com/m7medVision/anydoc-go/internal/model"
)

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
