// post table: PostScript names and metrics.
//
// Port of ttf-parser 0.25.1 tables/post.rs, including the 258-entry
// Macintosh standard glyph name order (TrueType Reference Manual).
package truetype

import (
	"errors"
	"unicode/utf8"
)

// macintoshNames is the standard Macintosh glyph name order (258 entries).
var macintoshNames = [...]string{
	".notdef",
	".null",
	"nonmarkingreturn",
	"space",
	"exclam",
	"quotedbl",
	"numbersign",
	"dollar",
	"percent",
	"ampersand",
	"quotesingle",
	"parenleft",
	"parenright",
	"asterisk",
	"plus",
	"comma",
	"hyphen",
	"period",
	"slash",
	"zero",
	"one",
	"two",
	"three",
	"four",
	"five",
	"six",
	"seven",
	"eight",
	"nine",
	"colon",
	"semicolon",
	"less",
	"equal",
	"greater",
	"question",
	"at",
	"A",
	"B",
	"C",
	"D",
	"E",
	"F",
	"G",
	"H",
	"I",
	"J",
	"K",
	"L",
	"M",
	"N",
	"O",
	"P",
	"Q",
	"R",
	"S",
	"T",
	"U",
	"V",
	"W",
	"X",
	"Y",
	"Z",
	"bracketleft",
	"backslash",
	"bracketright",
	"asciicircum",
	"underscore",
	"grave",
	"a",
	"b",
	"c",
	"d",
	"e",
	"f",
	"g",
	"h",
	"i",
	"j",
	"k",
	"l",
	"m",
	"n",
	"o",
	"p",
	"q",
	"r",
	"s",
	"t",
	"u",
	"v",
	"w",
	"x",
	"y",
	"z",
	"braceleft",
	"bar",
	"braceright",
	"asciitilde",
	"Adieresis",
	"Aring",
	"Ccedilla",
	"Eacute",
	"Ntilde",
	"Odieresis",
	"Udieresis",
	"aacute",
	"agrave",
	"acircumflex",
	"adieresis",
	"atilde",
	"aring",
	"ccedilla",
	"eacute",
	"egrave",
	"ecircumflex",
	"edieresis",
	"iacute",
	"igrave",
	"icircumflex",
	"idieresis",
	"ntilde",
	"oacute",
	"ograve",
	"ocircumflex",
	"odieresis",
	"otilde",
	"uacute",
	"ugrave",
	"ucircumflex",
	"udieresis",
	"dagger",
	"degree",
	"cent",
	"sterling",
	"section",
	"bullet",
	"paragraph",
	"germandbls",
	"registered",
	"copyright",
	"trademark",
	"acute",
	"dieresis",
	"notequal",
	"AE",
	"Oslash",
	"infinity",
	"plusminus",
	"lessequal",
	"greaterequal",
	"yen",
	"mu",
	"partialdiff",
	"summation",
	"product",
	"pi",
	"integral",
	"ordfeminine",
	"ordmasculine",
	"Omega",
	"ae",
	"oslash",
	"questiondown",
	"exclamdown",
	"logicalnot",
	"radical",
	"florin",
	"approxequal",
	"Delta",
	"guillemotleft",
	"guillemotright",
	"ellipsis",
	"nonbreakingspace",
	"Agrave",
	"Atilde",
	"Otilde",
	"OE",
	"oe",
	"endash",
	"emdash",
	"quotedblleft",
	"quotedblright",
	"quoteleft",
	"quoteright",
	"divide",
	"lozenge",
	"ydieresis",
	"Ydieresis",
	"fraction",
	"currency",
	"guilsinglleft",
	"guilsinglright",
	"fi",
	"fl",
	"daggerdbl",
	"periodcentered",
	"quotesinglbase",
	"quotedblbase",
	"perthousand",
	"Acircumflex",
	"Ecircumflex",
	"Aacute",
	"Edieresis",
	"Egrave",
	"Iacute",
	"Icircumflex",
	"Idieresis",
	"Igrave",
	"Oacute",
	"Ocircumflex",
	"apple",
	"Ograve",
	"Uacute",
	"Ucircumflex",
	"Ugrave",
	"dotlessi",
	"circumflex",
	"tilde",
	"macron",
	"breve",
	"dotaccent",
	"ring",
	"cedilla",
	"hungarumlaut",
	"ogonek",
	"caron",
	"Lslash",
	"lslash",
	"Scaron",
	"scaron",
	"Zcaron",
	"zcaron",
	"brokenbar",
	"Eth",
	"eth",
	"Yacute",
	"yacute",
	"Thorn",
	"thorn",
	"minus",
	"multiply",
	"onesuperior",
	"twosuperior",
	"threesuperior",
	"onehalf",
	"onequarter",
	"threequarters",
	"franc",
	"Gbreve",
	"gbreve",
	"Idotaccent",
	"Scedilla",
	"scedilla",
	"Cacute",
	"cacute",
	"Ccaron",
	"ccaron",
	"dcroat",
}

// postTable is a parsed post table.
type postTable struct {
	italicAngle  float32
	isMonospaced bool
	namesData    []byte // Pascal strings (version 2.0 only)
	glyphIndexes []uint16
}

// parsePost parses the post table.
func parsePost(data []byte) (*postTable, error) {
	// Do not check the exact length, because some fonts include padding in
	// table's length in table records, which is incorrect.
	if len(data) < 32 {
		return nil, errors.New("post: too short")
	}
	version := beU32must(data, 0)
	if version != 0x00010000 && version != 0x00020000 && version != 0x00025000 &&
		version != 0x00030000 && version != 0x00040000 {
		return nil, errors.New("post: invalid version")
	}

	// italicAngle is a Fixed 16.16 at offset 4.
	raw := beU32must(data, 4)
	italicAngle := float32(int32(raw)) / 65536.0
	isMonospaced := beU32must(data, 12) != 0

	var namesData []byte
	var glyphIndexes []uint16
	// Only version 2.0 of the table has data at the end.
	if version == 0x00020000 {
		if len(data) < 34 {
			return nil, errors.New("post: truncated v2 header")
		}
		indexesCount := int(beU16must(data, 32))
		end := 34 + indexesCount*2
		if indexesCount < 0 || end > len(data) {
			return nil, errors.New("post: truncated glyph indexes")
		}
		glyphIndexes = make([]uint16, indexesCount)
		for i := range glyphIndexes {
			glyphIndexes[i] = beU16must(data, 34+i*2)
		}
		namesData = data[end:]
	}

	return &postTable{
		italicAngle:  italicAngle,
		isMonospaced: isMonospaced,
		namesData:    namesData,
		glyphIndexes: glyphIndexes,
	}, nil
}

// glyphName returns the glyph name by ID.
func (p *postTable) glyphName(gid uint16) (string, bool) {
	if int(gid) >= len(p.glyphIndexes) {
		return "", false
	}
	index := p.glyphIndexes[gid]

	// "If the name index is between 0 and 257, treat the name index as a
	// glyph index in the Macintosh standard order."
	if int(index) < len(macintoshNames) {
		return macintoshNames[index], true
	}
	// "If the name index is between 258 and 65535, then subtract 258 and use
	// that to index into the list of Pascal strings at the end of the table."
	index -= uint16(len(macintoshNames))
	name, ok := p.nameAt(int(index))
	return name, ok
}

// nameAt returns the i-th Pascal-string custom name. Mirrors the Names
// iterator: an empty length or invalid UTF-8 terminates iteration.
func (p *postTable) nameAt(i int) (string, bool) {
	offset := 0
	for n := 0; n <= i; n++ {
		if offset >= len(p.namesData) {
			return "", false
		}
		length := int(p.namesData[offset])
		offset++
		// An empty name is an error.
		if length == 0 {
			return "", false
		}
		if offset+length > len(p.namesData) {
			return "", false
		}
		if n == i {
			return stringFromUtf8(p.namesData[offset : offset+length])
		}
		offset += length
	}
	return "", false
}

// stringFromUtf8 mirrors core::str::from_utf8: only valid UTF-8 passes.
func stringFromUtf8(b []byte) (string, bool) {
	if !utf8.Valid(b) {
		return "", false
	}
	return string(b), true
}
