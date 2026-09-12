// Package truetype is a minimal TrueType/OpenType font parser covering exactly
// the ttf-parser 0.25 surface used by pdf-inspector v1.14.2:
//
//	Face::parse(data, 0), face.tables().cmap (subtables: platform/encoding ids,
//	is_unicode, glyph_index, codepoints), face.number_of_glyphs(),
//	face.glyph_name(gid) (post table + Macintosh standard names),
//	face.is_italic(), face.is_bold(), face.italic_angle().
//
// Parsing semantics (accept/reject behavior, truncation handling, subtable
// iteration order) mirror ttf-parser 0.25.1. The `glyph-names` crate feature
// is always on here (pdf-inspector relies on it).
package truetype

import (
	"encoding/binary"
	"errors"
	"math"
)

// Face is a parsed font face.
type Face struct {
	data          []byte
	head          *headTable
	numberOfGlyph uint16 // maxp number_of_glyphs; never zero
	cmap          *Cmap  // nil when absent or malformed
	post          *postTable
	os2           *os2Table
}

// Parse creates a new Face from raw data. index selects the face in a font
// collection; use 0 if unsure.
//
// Upstream: ttf_parser::Face::parse. Required tables: head, hhea, maxp —
// anything else is optional and silently skipped when malformed.
func Parse(data []byte, index uint32) (*Face, error) {
	records, err := parseTableRecords(data, index)
	if err != nil {
		return nil, err
	}

	f := &Face{data: data}
	for _, r := range records {
		table, ok := sliceAt(data, r.offset, r.length)
		if !ok {
			continue // out-of-bounds record: skipped
		}
		switch string(r.tag[:]) {
		case "head":
			if h, err := parseHead(table); err == nil {
				f.head = h
			}
		case "hhea":
			// Required for Face::parse to succeed, but its contents are
			// unused here; only its parseability matters.
			if len(table) < 36 {
				return nil, errors.New("the hhea table is missing or malformed")
			}
		case "maxp":
			n, err := parseMaxp(table)
			if err != nil {
				return nil, errors.New("the maxp table is missing or malformed")
			}
			f.numberOfGlyph = n
		case "cmap":
			if c, err := ParseCmap(table); err == nil {
				f.cmap = c
			}
		case "post":
			if p, err := parsePost(table); err == nil {
				f.post = p
			}
		case "OS/2":
			if o, err := parseOs2(table); err == nil {
				f.os2 = o
			}
		}
	}
	if f.head == nil {
		return nil, errors.New("the head table is missing or malformed")
	}
	if f.numberOfGlyph == 0 {
		return nil, errors.New("the maxp table is missing or malformed")
	}
	return f, nil
}

type tableRecord struct {
	tag    [4]byte
	offset uint32
	length uint32
}

// parseTableRecords mirrors RawFace::parse: font magic, collection handling,
// then the table directory read as a bounds-checked record array.
func parseTableRecords(data []byte, index uint32) ([]tableRecord, error) {
	if len(data) < 4 {
		return nil, errors.New("unknown magic")
	}
	magic := binary.BigEndian.Uint32(data)
	pos := 0
	switch magic {
	case 0x00010000, 0x74727565: // 0x00010000, 'true'
		// A regular font is treated as a one-element collection.
		if index != 0 {
			return nil, errors.New("face index is out of bounds")
		}
	case 0x4F54544F: // 'OTTO'
		// Same rule for bare OpenType/CFF containers.
		if index != 0 {
			return nil, errors.New("face index is out of bounds")
		}
	case 0x74746366: // 'ttcf' collection
		if len(data) < 12 {
			return nil, errors.New("malformed font")
		}
		number := binary.BigEndian.Uint32(data[8:])
		offsetsEnd := 12 + int(number)*4
		if number > math.MaxUint32/4 || offsetsEnd > len(data) {
			return nil, errors.New("malformed font")
		}
		if index >= number {
			return nil, errors.New("face index is out of bounds")
		}
		faceOffset := binary.BigEndian.Uint32(data[12+int(index)*4:])
		// Face offset is from the start of the font data, adjusted to the
		// current parser offset (after the offsets array).
		if faceOffset < uint32(offsetsEnd) {
			return nil, errors.New("malformed font")
		}
		pos = int(faceOffset)
		if pos+4 > len(data) {
			return nil, errors.New("malformed font")
		}
		magic = binary.BigEndian.Uint32(data[pos:])
		if magic == 0x74746366 {
			return nil, errors.New("unknown magic") // collection inside a collection
		}
	default:
		return nil, errors.New("unknown magic")
	}
	if magic != 0x00010000 && magic != 0x74727565 && magic != 0x4F54544F {
		return nil, errors.New("unknown magic")
	}

	if pos+12 > len(data) {
		return nil, errors.New("malformed font")
	}
	numTables := int(binary.BigEndian.Uint16(data[pos+4:]))
	recordsStart := pos + 12
	end := recordsStart + numTables*16
	if numTables < 0 || end > len(data) {
		return nil, errors.New("malformed font")
	}
	records := make([]tableRecord, 0, numTables)
	for i := range numTables {
		at := recordsStart + i*16
		var r tableRecord
		copy(r.tag[:], data[at:at+4])
		r.offset = binary.BigEndian.Uint32(data[at+8:])
		r.length = binary.BigEndian.Uint32(data[at+12:])
		records = append(records, r)
	}
	return records, nil
}

// ─── head ─────────────────────────────────────────────────────────────────────

// headTable carries the fields pdf-inspector needs from the head table
// (currently only its parseability gates Face::parse).
type headTable struct{}

func parseHead(data []byte) (*headTable, error) {
	// Do not check the exact length, because some fonts include padding in
	// table's length in table records, which is incorrect.
	if len(data) < 54 {
		return nil, errors.New("head: too short")
	}
	unitsPerEm := binary.BigEndian.Uint16(data[18:])
	if unitsPerEm < 16 || unitsPerEm > 16384 {
		return nil, errors.New("head: invalid unitsPerEm")
	}
	if f := binary.BigEndian.Uint16(data[50:]); f > 1 {
		return nil, errors.New("head: invalid indexToLocFormat")
	}
	return &headTable{}, nil
}

// ─── maxp ─────────────────────────────────────────────────────────────────────

func parseMaxp(data []byte) (uint16, error) {
	if len(data) < 6 {
		return 0, errors.New("maxp: too short")
	}
	version := binary.BigEndian.Uint32(data)
	if version != 0x00005000 && version != 0x00010000 {
		return 0, errors.New("maxp: invalid version")
	}
	n := binary.BigEndian.Uint16(data[4:])
	if n == 0 {
		return 0, errors.New("maxp: zero glyphs")
	}
	return n, nil
}

// ─── OS/2 ─────────────────────────────────────────────────────────────────────

type os2Table struct {
	version uint16
	data    []byte
}

func parseOs2(data []byte) (*os2Table, error) {
	if len(data) < 2 {
		return nil, errors.New("os2: too short")
	}
	version := binary.BigEndian.Uint16(data)
	var tableLen int
	switch version {
	case 0:
		tableLen = 78
	case 1:
		tableLen = 86
	case 2, 3, 4:
		tableLen = 96
	case 5:
		tableLen = 100
	default:
		return nil, errors.New("os2: invalid version")
	}
	if len(data) < tableLen {
		return nil, errors.New("os2: too short")
	}
	return &os2Table{version: version, data: data}, nil
}

// fsSelection bits (OS/2 spec).
const (
	fsSelectionItalic  = 1 << 0
	fsSelectionBold    = 1 << 5
	fsSelectionOblique = 1 << 9
)

func (o *os2Table) fsSelection() uint16 {
	if o == nil || len(o.data) < 64 {
		return 0
	}
	return binary.BigEndian.Uint16(o.data[62:])
}

// ─── style helpers ────────────────────────────────────────────────────────────

// NumberOfGlyphs returns the total number of glyphs in the face (never zero).
func (f *Face) NumberOfGlyphs() uint16 { return f.numberOfGlyph }

// Cmap returns the character-to-glyph mapping table, or nil when the font has
// no usable cmap table.
func (f *Face) Cmap() *Cmap { return f.cmap }

// IsItalic reports whether the face is marked italic. A face can have a Normal
// style and a non-zero italic angle, which also makes it italic.
func (f *Face) IsItalic() bool { return f.style() == styleItalic || f.ItalicAngle() != 0 }

// IsBold reports whether the face is marked bold. False when the OS/2 table is
// not present.
func (f *Face) IsBold() bool {
	if f.os2 == nil {
		return false
	}
	return f.os2.fsSelection()&fsSelectionBold != 0
}

// ItalicAngle returns the face's italic angle (0.0 when the post table is not
// present).
func (f *Face) ItalicAngle() float32 {
	if f.post == nil {
		return 0
	}
	return f.post.italicAngle
}

type style uint8

const (
	styleNormal style = iota
	styleItalic
	styleOblique
)

func (f *Face) style() style {
	if f.os2 == nil {
		return styleNormal
	}
	flags := f.os2.fsSelection()
	if flags&fsSelectionItalic != 0 {
		return styleItalic
	}
	if f.os2.version >= 4 && flags&fsSelectionOblique != 0 {
		return styleOblique
	}
	return styleNormal
}

// GlyphName resolves a glyph name from the post table's glyph names (and the
// Macintosh standard order). Reports false when no name is associated.
func (f *Face) GlyphName(gid uint16) (string, bool) {
	if f.post != nil {
		if name, ok := f.post.glyphName(gid); ok {
			return name, true
		}
	}
	return "", false
}

// ─── shared byte helpers ──────────────────────────────────────────────────────

func sliceAt(data []byte, offset, length uint32) ([]byte, bool) {
	end64 := uint64(offset) + uint64(length)
	if end64 > uint64(len(data)) {
		return nil, false
	}
	return data[offset:end64], true
}

func beU16(data []byte, at int) (uint16, bool) {
	if at < 0 || at+2 > len(data) {
		return 0, false
	}
	return binary.BigEndian.Uint16(data[at:]), true
}

func beU32(data []byte, at int) (uint32, bool) {
	if at < 0 || at+4 > len(data) {
		return 0, false
	}
	return binary.BigEndian.Uint32(data[at:]), true
}
