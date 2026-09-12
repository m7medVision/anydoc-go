// Package model is anydoc's information-preserving document model
// (src/model), ported type for type.
//
// Only fully resolved content lives here: format frontends resolve style
// cascades, numbering, and references before constructing these types. A
// Document is self-contained - embedded assets carry their bytes, so it
// stays usable after the source archive is gone.
package model

// Document is a parsed document: its body, its notes, and the bytes of
// everything it embedded.
type Document struct {
	// Blocks is the body content in reading order.
	Blocks []Block
	// Notes holds the note bodies, in the order the document defines them.
	// Text refers to them by id through NoteRef.
	Notes []Note
	// Assets holds every embedded asset, indexed by AssetID.
	Assets []Asset
}

// Note is a footnote or endnote body, referenced from text by NoteRef.
type Note struct {
	// ID is the document-scoped id the referencing NoteRef carries.
	ID string
	// Kind is where the source placed this note.
	Kind NoteKind
	// Blocks is the note's own content.
	Blocks []Block
}

// NoteKind is where the source document places a note.
type NoteKind int

const (
	// NoteFootnote: placed at the foot of the page that references it.
	NoteFootnote NoteKind = iota
	// NoteEndnote: collected at the end of the document or section.
	NoteEndnote
)
