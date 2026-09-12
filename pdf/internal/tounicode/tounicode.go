// Package tounicode parses ToUnicode CMaps for PDF text extraction,
// converting CID-encoded text to Unicode.
//
// Port of pdf-inspector v1.14.2 src/tounicode.rs.
package tounicode

import (
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// CidRange is one bfrange mapping: CIDs start..=end map linearly onto Unicode
// code points starting at Base. Upstream: (u16, u16, u32) tuples.
type CidRange struct {
	Start uint16
	End   uint16
	Base  uint32
}

// ToUnicodeCMap is a parsed ToUnicode CMap mapping CIDs to Unicode strings.
type ToUnicodeCMap struct {
	// CharMap holds direct character mappings (CID → Unicode string).
	CharMap map[uint16]string
	// Ranges holds range mappings (start CID, end CID) → base Unicode.
	Ranges []CidRange
	// CodeByteLength is the byte width of source codes (1 or 2), determined
	// from the codespace and CMap entries.
	CodeByteLength uint8
	// CIDPassthrough makes unmapped CIDs be interpreted as Unicode code
	// points directly. Last resort for Identity-H fonts without
	// ToUnicode/cmap/glyph names.
	CIDPassthrough bool
}

// NewCMap creates a new empty CMap.
func NewCMap() ToUnicodeCMap {
	return ToUnicodeCMap{CharMap: map[uint16]string{}}
}

// Parse parses a ToUnicode CMap from its decompressed content. Reports false
// when no mappings could be extracted.
//
// Upstream: ToUnicodeCMap::parse.
func Parse(content []byte) (ToUnicodeCMap, bool) {
	text := lossyUTF8(content)
	cmap := NewCMap()
	var srcHexLengths []int
	var useCmapName string

	// Parse begincodespacerange ... endcodespacerange to determine byte width.
	var codespaceByteLen uint8
	var haveCodespace bool
	if csStart := strings.Index(text, "begincodespacerange"); csStart >= 0 {
		sectionStart := csStart + len("begincodespacerange")
		if csEnd := strings.Index(text[sectionStart:], "endcodespacerange"); csEnd >= 0 {
			section := text[sectionStart : sectionStart+csEnd]
			inHex, hexLen := false, 0
			for _, c := range section {
				switch {
				case c == '<':
					inHex, hexLen = true, 0
				case c == '>':
					if inHex && hexLen > 0 {
						codespaceByteLen = uint8((hexLen + 1) / 2) // 2 hex digits = 1 byte
						haveCodespace = true
					}
					inHex = false
				case inHex && isASCIIHexDigit(byte(c)):
					hexLen++
				}
			}
		}
	}

	// Parse "usecmap" if present.
	if name, ok := findUsecmapName(text); ok {
		useCmapName = name
	}

	// Parse beginbfchar ... endbfchar sections.
	pos := 0
	for {
		start := strings.Index(text[pos:], "beginbfchar")
		if start < 0 {
			break
		}
		sectionStart := pos + start + len("beginbfchar")
		end := strings.Index(text[sectionStart:], "endbfchar")
		if end < 0 {
			break
		}
		section := text[sectionStart : sectionStart+end]
		parseBfcharSection(&cmap, section, &srcHexLengths)
		pos = sectionStart + end
	}

	// Parse beginbfrange ... endbfrange sections.
	pos = 0
	for {
		start := strings.Index(text[pos:], "beginbfrange")
		if start < 0 {
			break
		}
		sectionStart := pos + start + len("beginbfrange")
		end := strings.Index(text[sectionStart:], "endbfrange")
		if end < 0 {
			break
		}
		section := text[sectionStart : sectionStart+end]
		parseBfrangeSection(&cmap, section, &srcHexLengths)
		pos = sectionStart + end
	}

	if len(cmap.CharMap) == 0 && len(cmap.Ranges) == 0 {
		return ToUnicodeCMap{}, false
	}

	// Determine byte width: use codespace if available, otherwise infer from
	// entries.
	if haveCodespace {
		// If codespace says 2-byte but ALL entries use 1-byte source codes
		// (hex length <= 2), treat as 1-byte. This handles the common case
		// where codespace is <0000><FFFF> but entries are <20>, <41>, etc.
		allShort := len(srcHexLengths) > 0
		for _, l := range srcHexLengths {
			if l > 2 {
				allShort = false
				break
			}
		}
		if codespaceByteLen == 2 && allShort {
			cmap.CodeByteLength = 1
		} else {
			cmap.CodeByteLength = codespaceByteLen
		}
	} else if len(srcHexLengths) > 0 {
		// No codespace declaration: infer from entry hex lengths.
		maxHexLen := 4
		for _, l := range srcHexLengths {
			if l > maxHexLen {
				maxHexLen = l
			}
		}
		if maxHexLen <= 2 {
			cmap.CodeByteLength = 1
		} else {
			cmap.CodeByteLength = 2
		}
	} else {
		cmap.CodeByteLength = 2 // Default to 2-byte
	}

	// Sort ranges by start CID for binary search in Lookup.
	sort.Slice(cmap.Ranges, func(i, j int) bool {
		return cmap.Ranges[i].Start < cmap.Ranges[j].Start
	})

	if useCmapName != "" {
		if base, ok := loadBuiltinCmapByName(useCmapName); ok {
			cmap = mergeCmaps(base, cmap)
		}
	}

	return cmap, true
}

// parseBfcharSection parses a bfchar section: <src> <dst> pairs.
func parseBfcharSection(cmap *ToUnicodeCMap, section string, srcHexLengths *[]int) {
	// Match pairs of hex values: <XXXX> <YYYY>
	r := runeScanner{src: section}

	for {
		// Skip whitespace
		r.skipWhitespace()
		// Look for opening <
		if r.peek() != '<' {
			break
		}
		r.next() // consume <

		// Read source hex
		srcHex := r.readUntil('>')
		r.next() // consume >

		// Track source hex length for byte width detection
		if trimmed := strings.TrimSpace(srcHex); trimmed != "" {
			*srcHexLengths = append(*srcHexLengths, len(trimmed))
		}

		// Skip whitespace
		r.skipWhitespace()

		// Look for opening <
		if r.peek() != '<' {
			continue
		}
		r.next() // consume <

		// Read destination hex
		dstHex := r.readUntil('>')
		r.next() // consume >

		// Parse and store mapping
		src, okSrc := parseHexU16(srcHex)
		dst, okDst := hexToUnicodeString(dstHex)
		if okSrc && okDst {
			cmap.CharMap[src] = dst
		}
	}
}

// parseBfrangeSection parses a bfrange section:
// <start> <end> <base> or <start> <end> [<u1> <u2> ...] triplets.
func parseBfrangeSection(cmap *ToUnicodeCMap, section string, srcHexLengths *[]int) {
	r := runeScanner{src: section}

	for {
		// Skip whitespace
		r.skipWhitespace()
		// Look for opening <
		if r.peek() != '<' {
			break
		}
		r.next() // consume <

		// Read start hex
		startHex := r.readUntil('>')
		r.next() // consume >

		// Track source hex length
		if trimmed := strings.TrimSpace(startHex); trimmed != "" {
			*srcHexLengths = append(*srcHexLengths, len(trimmed))
		}

		// Skip whitespace
		r.skipWhitespace()

		// Read end hex
		if r.peek() != '<' {
			continue
		}
		r.next()
		endHex := r.readUntil('>')
		r.next()

		// Skip whitespace
		r.skipWhitespace()

		// Read base - could be <hex> or [array]
		if r.peek() == '<' {
			r.next()
			baseHex := r.readUntil('>')
			r.next()

			// Store range mapping
			start, okStart := parseHexU16(startHex)
			end, okEnd := parseHexU16(endHex)
			base, okBase := hexToUnicodeScalar(baseHex)
			if okStart && okEnd && okBase {
				cmap.Ranges = append(cmap.Ranges, CidRange{Start: start, End: end, Base: base})
			}
		} else if r.peek() == '[' {
			// Array format: [<unicode1> <unicode2> ...]
			// Each entry maps to start_cid + index
			r.next() // consume [
			start, okStart := parseHexU16(startHex)
			end, okEnd := parseHexU16(endHex)
			if okStart && okEnd {
				cid := start
				for {
					// Skip whitespace
					r.skipWhitespace()
					if r.peek() == ']' {
						r.next()
						break
					}
					if r.peek() != '<' {
						break
					}
					r.next() // consume <
					hex := r.readUntil('>')
					r.next() // consume >
					if unicodeStr, ok := hexToUnicodeString(hex); ok {
						cmap.CharMap[cid] = unicodeStr
					}
					if cid >= end {
						// Skip remaining entries and closing bracket
						r.skipUntil(']')
						if r.peek() == ']' {
							r.next()
						}
						break
					}
					cid = saturatingAdd1(cid)
				}
			} else {
				// Couldn't parse start/end, skip the array
				r.skipUntil(']')
				if r.peek() == ']' {
					r.next()
				}
			}
		}
	}
}

// Lookup looks up a CID and returns the Unicode string.
func (c *ToUnicodeCMap) Lookup(cid uint16) (string, bool) {
	// First check direct mappings
	if s, ok := c.CharMap[cid]; ok {
		return s, true
	}

	// Binary search through sorted ranges: the insertion index of the first
	// range whose start is >= cid (Rust binary_search_by unwrap_or_else).
	idx := sort.Search(len(c.Ranges), func(i int) bool {
		return c.Ranges[i].Start >= cid
	})

	// Check the range at idx (where start == cid)
	if idx < len(c.Ranges) {
		rg := c.Ranges[idx]
		if cid >= rg.Start && cid <= rg.End {
			unicodeCP := rg.Base + uint32(cid-rg.Start)
			if r, ok := charFromU32(unicodeCP); ok {
				return string(r), true
			}
		}
	}

	// Check the range before idx (cid may fall within a range that starts
	// before it)
	if idx > 0 {
		rg := c.Ranges[idx-1]
		if cid >= rg.Start && cid <= rg.End {
			unicodeCP := rg.Base + uint32(cid-rg.Start)
			if r, ok := charFromU32(unicodeCP); ok {
				return string(r), true
			}
		}
	}

	return "", false
}

// LookupBytes performs a per-byte CMap lookup without Latin-1 fallback.
// Returns (raw byte, cmap result) for each byte. Only meaningful for
// single-byte (CodeByteLength==1) CMaps.
func (c *ToUnicodeCMap) LookupBytes(bytes []byte) []ByteLookup {
	out := make([]ByteLookup, len(bytes))
	for i, b := range bytes {
		s, ok := c.Lookup(uint16(b))
		if ok && strings.ContainsRune(s, 0xFFFD) {
			ok = false
		}
		out[i] = ByteLookup{Byte: b, Text: s, OK: ok}
	}
	return out
}

// ByteLookup is one (raw byte, optional cmap result) pair from LookupBytes.
type ByteLookup struct {
	Byte byte
	Text string
	OK   bool
}

// DecodeCIDs decodes a byte slice to a Unicode string, respecting the CMap's
// code byte width. When too many codes were unmapped, an empty string signals
// failure so the caller can fall through to other decoding methods.
func (c *ToUnicodeCMap) DecodeCIDs(bytes []byte) string {
	var result strings.Builder
	unmappedCount := 0

	if c.CodeByteLength == 1 {
		// Single-byte codes: each byte is a code
		for _, b := range bytes {
			if s, ok := c.Lookup(uint16(b)); ok && !strings.ContainsRune(s, 0xFFFD) {
				result.WriteString(s)
			} else {
				// For single-byte unmapped codes, try as Latin-1
				// (the byte IS the character code in most legacy encodings)
				if b >= 0x20 {
					result.WriteRune(rune(b))
				}
				unmappedCount++
			}
		}
	} else {
		// Two-byte codes: CIDs are 2 bytes each (big-endian)
		for i := 0; i+2 <= len(bytes); i += 2 {
			cid := uint16(bytes[i])<<8 | uint16(bytes[i+1])
			if s, ok := c.Lookup(cid); ok && !strings.ContainsRune(s, 0xFFFD) {
				result.WriteString(s)
			} else if c.CIDPassthrough {
				// Last-resort: treat CID as Unicode codepoint.
				// Valid for Identity-H fonts where the PDF generator used
				// Unicode values as CIDs but stripped the cmap.
				if ch, ok := charFromU32(uint32(cid)); ok {
					if !isControlRune(ch) || ch == '\t' || ch == '\n' {
						result.WriteRune(ch)
					} else {
						unmappedCount++
					}
				} else {
					unmappedCount++
				}
			} else {
				// CIDs are font-internal indices, not Unicode values.
				// Unmapped 2-byte CIDs are skipped to avoid CJK garbage.
				unmappedCount++
			}
		}
	}

	// If too many codes were unmapped, signal failure by returning empty.
	total := len(bytes)
	if c.CodeByteLength != 1 {
		total = len(bytes) / 2
	}
	if total > 0 && unmappedCount > total/2 {
		return ""
	}

	return result.String()
}

// MinSourceCID returns the minimum source CID across all mappings.
func (c *ToUnicodeCMap) MinSourceCID() (uint16, bool) {
	charMin, haveChar := uint16(0), false
	for k := range c.CharMap {
		if !haveChar || k < charMin {
			charMin, haveChar = k, true
		}
	}
	rangeMin, haveRange := uint16(0), false
	for _, rg := range c.Ranges {
		if !haveRange || rg.Start < rangeMin {
			rangeMin, haveRange = rg.Start, true
		}
	}
	switch {
	case haveChar && haveRange:
		return minU16(charMin, rangeMin), true
	case haveChar:
		return charMin, true
	case haveRange:
		return rangeMin, true
	}
	return 0, false
}

// MaxSourceCID returns the maximum source CID across all mappings.
func (c *ToUnicodeCMap) MaxSourceCID() (uint16, bool) {
	charMax, haveChar := uint16(0), false
	for k := range c.CharMap {
		if !haveChar || k > charMax {
			charMax, haveChar = k, true
		}
	}
	rangeMax, haveRange := uint16(0), false
	for _, rg := range c.Ranges {
		if !haveRange || rg.End > rangeMax {
			rangeMax, haveRange = rg.End, true
		}
	}
	switch {
	case haveChar && haveRange:
		return maxU16(charMax, rangeMax), true
	case haveChar:
		return charMax, true
	case haveRange:
		return rangeMax, true
	}
	return 0, false
}

// RemapToSequential remaps a CMap that references pre-subsetting GIDs to
// sequential post-subsetting GIDs: all source CIDs are collected, sorted, and
// reassigned to 1, 2, 3, ...
//
// Range expansion stops after maxCIDWExpansion CID visits, counting
// overwrites, so repeated full-width bfranges cannot re-expand the 16-bit
// domain. Later overlapping ranges that would have introduced new CIDs after
// that many visits are truncated.
func (c *ToUnicodeCMap) RemapToSequential() ToUnicodeCMap {
	cidToUnicode := map[uint16]string{}
	expandBfrangesForRemap(c.Ranges, cidToUnicode, maxCIDWExpansion)

	// CharMap entries override range entries
	for cid, unicode := range c.CharMap {
		cidToUnicode[cid] = unicode
	}

	// Sort old CIDs ascending
	oldCids := make([]uint16, 0, len(cidToUnicode))
	for cid := range cidToUnicode {
		oldCids = append(oldCids, cid)
	}
	sort.Slice(oldCids, func(i, j int) bool { return oldCids[i] < oldCids[j] })

	// Build new CMap with sequential CIDs starting at 1
	newCmap := NewCMap()
	for i, oldCid := range oldCids {
		newCid := uint16(i + 1) // GID 0 is .notdef, content CIDs start at 1
		if unicode, ok := cidToUnicode[oldCid]; ok {
			newCmap.CharMap[newCid] = unicode
		}
	}
	newCmap.CodeByteLength = c.CodeByteLength

	return newCmap
}

// expandBfrangesForRemap expands bfrange entries into individual CID→Unicode
// inserts. Returns how many CIDs were visited. Counts overwrites so a repeated
// full-width range cannot keep working after maxAssignments.
func expandBfrangesForRemap(ranges []CidRange, cidToUnicode map[uint16]string, maxAssignments int) int {
	assigned := 0
ranges:
	for _, rg := range ranges {
		if rg.Start > rg.End {
			continue
		}
		for cid := rg.Start; ; cid = saturatingAdd1(cid) {
			if assigned >= maxAssignments {
				break ranges
			}
			assigned++
			unicodeCP := rg.Base + uint32(cid-rg.Start)
			if ch, ok := charFromU32(unicodeCP); ok {
				cidToUnicode[cid] = string(ch)
			}
			if cid == rg.End {
				break
			}
		}
	}
	return assigned
}

// maxCIDWExpansion is the shared 16-bit CID expansion cap (65,536). Encoding
// begincidrange, /W width assignment, and ToUnicode sequential remap count
// every insert, including overwrites, so a repeated full-width range cannot
// keep working after the domain is filled. The /W unicode heuristic caps
// unique CIDs with the same number.
const maxCIDWExpansion = 65_536

// parseHexU16 parses a hex string to u16 (Rust u16::from_str_radix).
func parseHexU16(hex string) (uint16, bool) {
	v, ok := rustParseUint(strings.TrimSpace(hex), 16, 16)
	if !ok {
		return 0, false
	}
	return uint16(v), true
}

// hexToUnicodeString converts a ToUnicode destination hex string to Unicode.
//
// PDF ToUnicode destinations are UTF-16BE strings. Supplementary-plane
// characters are encoded as surrogate pairs, so treating each 4-hex chunk as
// a scalar drops emoji like D83CDF1F.
func hexToUnicodeString(hex string) (string, bool) {
	var filtered strings.Builder
	filtered.Grow(len(hex))
	for _, ch := range hex {
		// Drop ASCII whitespace only (Rust filter(!is_ascii_whitespace));
		// non-ASCII runes survive filtering but fail the radix parse below.
		if ch < 0x80 && isASCIIWhitespace(byte(ch)) {
			continue
		}
		filtered.WriteRune(ch)
	}
	h := filtered.String()
	if h == "" || len(h)%2 != 0 {
		return "", false
	}

	bytes := make([]byte, 0, len(h)/2)
	for i := 0; i+2 <= len(h); i += 2 {
		v, err := strconv.ParseUint(h[i:i+2], 16, 8)
		if err != nil {
			return "", false
		}
		bytes = append(bytes, byte(v))
	}

	if len(bytes)%2 == 0 {
		units := make([]uint16, 0, len(bytes)/2)
		for i := 0; i < len(bytes); i += 2 {
			units = append(units, uint16(bytes[i])<<8|uint16(bytes[i+1]))
		}
		if result, ok := stringFromUTF16(units); ok && result != "" {
			return normalizeTounicodeDestination(result), true
		}
	}

	// Be permissive for non-standard one-byte destinations.
	if len(bytes) == 1 {
		ch := rune(bytes[0])
		if !isControlRune(ch) || ch == '\t' || ch == '\n' {
			return string(ch), true
		}
	}

	return "", false
}

// normalizeTounicodeDestination collapses malformed multi-codepoint
// destinations some producers emit into one canonical codepoint.
func normalizeTounicodeDestination(text string) string {
	runes := []rune(text)
	isMultiChar := len(runes) > 1

	// Some malformed producer CMaps put a list of alternative whitespace or
	// hyphen codepoints into one destination. Keep ordinary multi-character
	// mappings intact unless that malformed signature is present.
	if isMultiChar && allWhitespaceRunes(runes) && anyTabNewlineCR(runes) {
		if strings.ContainsRune(text, '\t') {
			return "\t"
		}
		return " "
	}

	if isMultiChar && strings.ContainsRune(text, '\u00AD') && allHyphenLikeRunes(runes) {
		return "-"
	}

	return text
}

func allWhitespaceRunes(runes []rune) bool {
	for _, r := range runes {
		if !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func anyTabNewlineCR(runes []rune) bool {
	for _, r := range runes {
		if r == '\t' || r == '\n' || r == '\r' {
			return true
		}
	}
	return false
}

func allHyphenLikeRunes(runes []rune) bool {
	for _, r := range runes {
		switch r {
		case '-', 0x00AD, 0x2010, 0x2011, 0x2012, 0x2013, 0x2212:
		default:
			return false
		}
	}
	return true
}

func hexToUnicodeScalar(hex string) (uint32, bool) {
	text, ok := hexToUnicodeString(hex)
	if !ok {
		return 0, false
	}
	runes := []rune(text)
	if len(runes) != 1 {
		return 0, false
	}
	return uint32(runes[0]), true
}

func findUsecmapName(text string) (string, bool) {
	for _, line := range strings.Split(text, "\n") {
		if !strings.Contains(line, "usecmap") {
			continue
		}
		parts := strings.Fields(line)
		for i := 1; i < len(parts); i++ {
			if parts[i] == "usecmap" {
				name := strings.TrimSpace(parts[i-1])
				if stripped, ok := strings.CutPrefix(name, "/"); ok {
					return stripped, true
				}
			}
		}
	}
	return "", false
}

// ─── rune scanner over the lossy-decoded text ─────────────────────────────────

// runeScanner iterates runes with one-rune lookahead, mirroring the Rust
// chars().peekable() loops.
type runeScanner struct {
	src string
	pos int // byte offset
	eof bool
}

func (r *runeScanner) peek() rune {
	if r.pos >= len(r.src) {
		return -1
	}
	c, _ := utf8.DecodeRuneInString(r.src[r.pos:])
	return c
}

func (r *runeScanner) next() rune {
	if r.pos >= len(r.src) {
		r.eof = true
		return -1
	}
	c, size := utf8.DecodeRuneInString(r.src[r.pos:])
	r.pos += size
	return c
}

func (r *runeScanner) skipWhitespace() {
	for {
		c := r.peek()
		if c == -1 || !unicode.IsSpace(c) {
			return
		}
		r.next()
	}
}

// readUntil collects runes until stop (not consumed; a missing stop consumes
// the rest, like the Rust loop over chars.peek()).
func (r *runeScanner) readUntil(stop rune) string {
	var b strings.Builder
	for {
		c := r.peek()
		if c == -1 || c == stop {
			break
		}
		b.WriteRune(r.next())
	}
	return b.String()
}

func (r *runeScanner) skipUntil(stop rune) {
	for {
		c := r.peek()
		if c == -1 || c == stop {
			return
		}
		r.next()
	}
}

// ─── shared helpers ───────────────────────────────────────────────────────────

// rustParseUint parses like Rust's from_str_radix: an optional leading '+' is
// accepted, no other sign, no underscores, no base prefix. Digits outside the
// base or overflow report false.
func rustParseUint(s string, base, bits int) (uint32, bool) {
	s = strings.TrimPrefix(s, "+")
	v, err := strconv.ParseUint(s, base, bits)
	if err != nil {
		return 0, false
	}
	return uint32(v), true
}

// charFromU32 mirrors Rust's char::from_u32: false for surrogates and
// anything above the Unicode maximum.
func charFromU32(cp uint32) (rune, bool) {
	if cp > 0x10FFFF || (cp >= 0xD800 && cp <= 0xDFFF) {
		return 0, false
	}
	return rune(cp), true
}

// stringFromUTF16 mirrors Rust's String::from_utf16: unpaired surrogates are
// an error rather than a replacement character.
func stringFromUTF16(units []uint16) (string, bool) {
	for i := 0; i < len(units); i++ {
		if units[i] >= 0xD800 && units[i] <= 0xDBFF {
			if i+1 >= len(units) || !(units[i+1] >= 0xDC00 && units[i+1] <= 0xDFFF) {
				return "", false
			}
			i++
		} else if units[i] >= 0xDC00 && units[i] <= 0xDFFF {
			return "", false
		}
	}
	return string(utf16.Decode(units)), true
}

// lossyUTF8 mirrors Rust's String::from_utf8_lossy: each maximal invalid
// subpart of the input becomes one U+FFFD.
func lossyUTF8(data []byte) string {
	valid := utf8.Valid(data)
	if valid {
		return string(data)
	}
	var b strings.Builder
	b.Grow(len(data))
	i := 0
	for i < len(data) {
		r, size := utf8.DecodeRune(data[i:])
		if r != utf8.RuneError || size != 1 {
			b.WriteRune(r)
			i += size
			continue
		}
		// Invalid sequence: determine the maximal subpart (the lead byte plus
		// however many continuation bytes follow it, capped at the expected
		// length). One U+FFFD replaces the whole subpart.
		c := data[i]
		var need int
		switch {
		case c&0xE0 == 0xC0:
			need = 2
		case c&0xF0 == 0xE0:
			need = 3
		case c&0xF8 == 0xF0:
			need = 4
		default:
			b.WriteRune(0xFFFD)
			i++
			continue
		}
		j := i + 1
		for j < len(data) && j < i+need && data[j]&0xC0 == 0x80 {
			j++
		}
		b.WriteRune(0xFFFD)
		i = j
	}
	return b.String()
}

func isASCIIHexDigit(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F'
}

func isASCIIWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == 0x0C
}

func isControlRune(r rune) bool { return unicode.IsControl(r) }

func saturatingAdd1(v uint16) uint16 {
	if v == 0xFFFF {
		return 0xFFFF
	}
	return v + 1
}

func minU16(a, b uint16) uint16 {
	if a < b {
		return a
	}
	return b
}

func maxU16(a, b uint16) uint16 {
	if a > b {
		return a
	}
	return b
}
