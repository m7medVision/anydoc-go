// ToUnicode CMap construction from embedded TrueType fonts.
//
// Port of pdf-inspector v1.14.2 src/tounicode.rs (build_cmap_from_truetype,
// build_simple_cmap_from_truetype, build_gid_to_unicode, glyph name fallbacks).
package tounicode

import (
	"strings"

	"github.com/m7medVision/anydoc-go/pdf/internal/glyphs"
	"github.com/m7medVision/anydoc-go/pdf/internal/truetype"
)

// BuildCmapFromTrueType builds a ToUnicode CMap from an embedded TrueType
// font's cmap table.
//
// For Identity-H CID fonts, CID == GID. The TrueType cmap maps Unicode→GID,
// so we reverse it to get GID→Unicode (i.e. CID→Unicode).
//
// Upstream: pub fn build_cmap_from_truetype.
func BuildCmapFromTrueType(fontData []byte) (ToUnicodeCMap, bool) {
	face, err := truetype.Parse(fontData, 0)
	if err != nil {
		return ToUnicodeCMap{}, false
	}
	gidToUnicode, ok := buildGidToUnicode(face)
	if !ok {
		return ToUnicodeCMap{}, false
	}

	cmap := NewCMap()
	for gid, ch := range gidToUnicode {
		cmap.CharMap[gid] = string(ch)
	}
	cmap.CodeByteLength = 2 // Identity-H uses 2-byte CIDs

	return cmap, true
}

// buildSimpleCmapFromTrueType builds a single-byte CMap for simple fonts by
// treating the character code as a glyph id (best-effort fallback when no
// usable ToUnicode exists).
func buildSimpleCmapFromTrueType(fontData []byte) (ToUnicodeCMap, bool) {
	face, err := truetype.Parse(fontData, 0)
	if err != nil {
		return ToUnicodeCMap{}, false
	}
	gidToUnicode, ok := buildGidToUnicode(face)
	if !ok {
		return ToUnicodeCMap{}, false
	}

	cmap := NewCMap()

	// Use the font's encoding cmap subtable for proper code→GID→Unicode
	// mapping. In subsetted TrueType fonts, GID ≠ character code, so we need
	// the cmap table to translate byte codes (as used in the PDF content
	// stream) to GIDs.
	usedEncodingCmap := false
	if c := face.Cmap(); c != nil {
		// Prefer Mac Roman (1,0): maps byte codes 0–255 directly to GIDs.
		for _, subtable := range c.Subtables() {
			if subtable.PlatformId == truetype.PlatformMacintosh && subtable.EncodingId == 0 {
				for code := uint32(0x20); code <= 0xFF; code++ {
					encodeSimpleCode(cmap, gidToUnicode, subtable, code, code)
				}
				usedEncodingCmap = true
				break
			}
		}
		// Fallback: Windows Symbol (3,0) — maps F000+byte to GIDs.
		if !usedEncodingCmap {
			for _, subtable := range c.Subtables() {
				if subtable.PlatformId == truetype.PlatformWindows && subtable.EncodingId == 0 {
					for code := uint32(0x20); code <= 0xFF; code++ {
						encodeSimpleCode(cmap, gidToUnicode, subtable, code, code+0xF000)
					}
					usedEncodingCmap = true
					break
				}
			}
		}
		// Fallback: Windows Unicode BMP (3,1) — maps Unicode codepoints to
		// GIDs. For single-byte fonts, try each byte value as a Unicode
		// codepoint. Common in OCR-generated PDFs where byte values
		// correspond to Unicode codepoints but the declared encoding
		// (WinAnsiEncoding) is wrong.
		if !usedEncodingCmap {
			for _, subtable := range c.Subtables() {
				if subtable.PlatformId == truetype.PlatformWindows && subtable.EncodingId == 1 {
					for code := uint32(0x20); code <= 0xFF; code++ {
						encodeSimpleCode(cmap, gidToUnicode, subtable, code, code)
					}
					usedEncodingCmap = true
					break
				}
			}
		}
	}

	if !usedEncodingCmap {
		// No encoding cmap found — fall back to treating GID as code.
		for gid, ch := range gidToUnicode {
			if gid <= 0xFF {
				cmap.CharMap[gid] = string(ch)
			}
		}
		// Fill missing single-byte codes from glyph names (helps with
		// ligatures like "t_i").
		for gidIdx := range uint32(face.NumberOfGlyphs()) {
			gidVal := uint16(gidIdx)
			if gidVal > 0xFF {
				continue
			}
			if _, ok := cmap.CharMap[gidVal]; ok {
				continue
			}
			if name, ok := face.GlyphName(gidVal); ok {
				if s, ok := glyphNameToString(name); ok {
					cmap.CharMap[gidVal] = s
				}
			}
		}
	}

	if len(cmap.CharMap) == 0 {
		return ToUnicodeCMap{}, false
	}
	cmap.CodeByteLength = 1
	return cmap, true
}

// encodeSimpleCode maps one byte code through a cmap subtable lookup at
// lookupCode and records code → Unicode (first mapping wins, like the
// upstream entry().or_insert()).
func encodeSimpleCode(cmap ToUnicodeCMap, gidToUnicode map[uint16]rune, subtable truetype.CmapSubtable, code, lookupCode uint32) {
	gid, ok := subtable.GlyphIndex(lookupCode)
	if !ok {
		return
	}
	ch, ok := gidToUnicode[gid]
	if !ok {
		return
	}
	ch = stripPUAChar(ch)
	if _, exists := cmap.CharMap[uint16(code)]; !exists {
		cmap.CharMap[uint16(code)] = string(ch)
	}
}

// stripPUAChar strips the Private Use Area F000 offset (Windows Symbol
// encoding convention).
func stripPUAChar(ch rune) rune {
	cp := uint32(ch)
	if cp >= 0xF000 && cp <= 0xF0FF {
		if r, ok := charFromU32(cp - 0xF000); ok {
			return r
		}
	}
	return ch
}

// glyphNameToString converts a glyph name (with optional ligature parts) to a
// Unicode string.
func glyphNameToString(name string) (string, bool) {
	base := name
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	if ch, ok := glyphs.GlyphToChar(base); ok {
		return string(ch), true
	}
	if strings.ContainsRune(base, '_') {
		var out strings.Builder
		for _, part := range strings.Split(base, "_") {
			if part == "" {
				return "", false
			}
			if ch, ok := glyphs.GlyphToChar(part); ok {
				out.WriteRune(ch)
			} else if len(part) == 1 {
				out.WriteString(part)
			} else {
				return "", false
			}
		}
		if out.Len() > 0 {
			return out.String(), true
		}
	}
	switch base {
	case "ti", "tt", "tz":
		return base, true
	}
	return "", false
}

// buildCmapFromGlyphNames builds a ToUnicode CMap from a font's glyph names
// (post table), using the Adobe Glyph List to map names to Unicode.
func buildCmapFromGlyphNames(face *truetype.Face) (ToUnicodeCMap, bool) {
	cmap := NewCMap()

	for gid := range uint32(face.NumberOfGlyphs()) {
		if name, ok := face.GlyphName(uint16(gid)); ok {
			if ch, ok := glyphs.GlyphToChar(name); ok {
				cmap.CharMap[uint16(gid)] = string(ch)
			}
		}
	}

	if len(cmap.CharMap) == 0 {
		return ToUnicodeCMap{}, false
	}

	cmap.CodeByteLength = 2
	return cmap, true
}

// buildGidToUnicode reverses the font's cmap (Unicode→GID) into a GID→Unicode
// map, preferring the first (lowest) codepoint for each GID. Falls back to
// glyph names when no Unicode cmap entries exist.
func buildGidToUnicode(face *truetype.Face) (map[uint16]rune, bool) {
	gidToUnicode := map[uint16]rune{}

	// Iterate all Unicode codepoints that have a glyph mapping. For each
	// codepoint, the face gives us a glyph id; reverse that to GID→Unicode.
	if c := face.Cmap(); c != nil {
		for _, subtable := range c.Subtables() {
			isSymbol := subtable.PlatformId == truetype.PlatformWindows && subtable.EncodingId == 0
			if !subtable.IsUnicode() && !isSymbol {
				continue
			}
			subtable.Codepoints(func(cp uint32) {
				if ch, ok := charFromU32(cp); ok {
					if gid, ok := subtable.GlyphIndex(cp); ok {
						if _, exists := gidToUnicode[gid]; !exists {
							gidToUnicode[gid] = ch
						}
					}
				}
			})
		}
	}

	if len(gidToUnicode) == 0 {
		if cmap, ok := buildCmapFromGlyphNames(face); ok {
			m := map[uint16]rune{}
			for gid, s := range cmap.CharMap {
				for _, ch := range s {
					m[gid] = ch
					break // first char only
				}
			}
			return m, true
		}
		return nil, false
	}

	return gidToUnicode, true
}
