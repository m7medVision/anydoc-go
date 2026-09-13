// cmap table: character-to-glyph index mapping.
//
// Port of ttf-parser 0.25.1 tables/cmap (formats 0, 2, 4, 6, 10, 12, 13, 14).
package truetype

import "errors"

// PlatformId is a cmap subtable platform.
type PlatformId uint8

// Platform ids (name table spec).
const (
	PlatformUnicode PlatformId = iota
	PlatformMacintosh
	PlatformIso
	PlatformWindows
	PlatformCustom
)

// parsePlatformId mirrors PlatformId::parse: unknown ids are invalid, which
// invalidates the whole encoding record.
func parsePlatformId(v uint16) (PlatformId, bool) {
	switch v {
	case 0:
		return PlatformUnicode, true
	case 1:
		return PlatformMacintosh, true
	case 2:
		return PlatformIso, true
	case 3:
		return PlatformWindows, true
	case 4:
		return PlatformCustom, true
	}
	return 0, false
}

// Cmap is a parsed cmap table.
type Cmap struct {
	data    []byte // whole cmap table
	records []encodingRecord
}

type encodingRecord struct {
	platformId PlatformId
	encodingId uint16
	offset     uint32
}

// ParseCmap parses a cmap table from raw data. Mirrors cmap::Table::parse:
// the version is skipped; a too-short record array fails the whole table.
func ParseCmap(data []byte) (*Cmap, error) {
	count, ok := beU16(data, 2)
	if !ok {
		return nil, errors.New("cmap: truncated header")
	}
	if int(count) < 0 || 4+int(count)*8 > len(data) {
		return nil, errors.New("cmap: truncated records")
	}
	records := make([]encodingRecord, 0, count)
	for i := range int(count) {
		at := 4 + i*8
		platform, ok := beU16(data, at)
		if !ok {
			return nil, errors.New("cmap: truncated record")
		}
		pid, ok := parsePlatformId(platform)
		if !ok {
			return nil, errors.New("cmap: invalid platform id")
		}
		encoding, _ := beU16(data, at+2)
		offset, _ := beU32(data, at+4)
		records = append(records, encodingRecord{platformId: pid, encodingId: encoding, offset: offset})
	}
	return &Cmap{data: data, records: records}, nil
}

// CmapSubtable is one encoding subtable of a cmap.
type CmapSubtable struct {
	// PlatformId is the subtable platform.
	PlatformId PlatformId
	// EncodingId is the subtable encoding.
	EncodingId uint16
	// Format is the subtable format number (0, 2, 4, 6, 8, 10, 12, 13, 14).
	Format uint16

	// Parsed subtable payload, keyed by format. Exactly one is non-nil for
	// supported formats; formats 8 (mixed coverage) and 14 (variation
	// sequences) carry no payload here and match nothing.
	sub0  *subtable0
	sub2  *subtable2
	sub4  *subtable4
	sub6  *subtable6
	sub10 *subtable10
	sub12 *subtable12
	sub13 *subtable13
}

// subtableBytes returns the raw bytes of record i's subtable, running to the
// END of the cmap table (ttf-parser slices self.data.get(offset..)), plus its
// format.
func (c *Cmap) subtableBytes(i int) ([]byte, uint16, bool) {
	if i < 0 || i >= len(c.records) {
		return nil, 0, false
	}
	off := int(c.records[i].offset)
	if off < 0 || off >= len(c.data) {
		return nil, 0, false
	}
	format, ok := beU16(c.data, off)
	if !ok {
		return nil, 0, false
	}
	return c.data[off:], format, true
}

// get mirrors Subtables::get: the record plus a successfully parsed subtable,
// or not-ok when anything is invalid or the format is unknown.
func (c *Cmap) get(i int) (CmapSubtable, bool) {
	if i < 0 || i >= len(c.records) {
		return CmapSubtable{}, false
	}
	data, format, ok := c.subtableBytes(i)
	if !ok {
		return CmapSubtable{}, false
	}
	sub := CmapSubtable{
		PlatformId: c.records[i].platformId,
		EncodingId: c.records[i].encodingId,
		Format:     format,
	}
	switch format {
	case 0:
		s, ok := parseSubtable0(data)
		if !ok {
			return CmapSubtable{}, false
		}
		sub.sub0 = s
	case 2:
		s, ok := parseSubtable2(data)
		if !ok {
			return CmapSubtable{}, false
		}
		sub.sub2 = s
	case 4:
		s, ok := parseSubtable4(data)
		if !ok {
			return CmapSubtable{}, false
		}
		sub.sub4 = s
	case 6:
		s, ok := parseSubtable6(data)
		if !ok {
			return CmapSubtable{}, false
		}
		sub.sub6 = s
	case 8:
		// Mixed coverage: unsupported, matches nothing.
	case 10:
		s, ok := parseSubtable10(data)
		if !ok {
			return CmapSubtable{}, false
		}
		sub.sub10 = s
	case 12:
		s, ok := parseSubtable12(data)
		if !ok {
			return CmapSubtable{}, false
		}
		sub.sub12 = s
	case 13:
		s, ok := parseSubtable13(data)
		if !ok {
			return CmapSubtable{}, false
		}
		sub.sub13 = s
	case 14:
		// Unicode variation sequences: counts as a valid subtable, but the
		// payload is not consumed and it matches nothing.
	default:
		return CmapSubtable{}, false
	}
	return sub, true
}

// Subtables returns the subtables in file order. Like ttf-parser's
// SubtablesIter, iteration STOPS at the first record whose subtable is
// invalid (bad platform, bad offset, unsupported/unknown format).
func (c *Cmap) Subtables() []CmapSubtable {
	var out []CmapSubtable
	for i := range c.records {
		sub, ok := c.get(i)
		if !ok {
			break
		}
		out = append(out, sub)
	}
	return out
}

// IsUnicode checks that the current encoding is Unicode compatible.
//
// Windows BMP (encoding 1) always qualifies; Windows full repertoire
// (encoding 10) qualifies only for format 12/13 subtables.
func (s *CmapSubtable) IsUnicode() bool {
	const windowsUnicodeBmp = 1
	const windowsUnicodeFull = 10
	switch s.PlatformId {
	case PlatformUnicode:
		return true
	case PlatformWindows:
		if s.EncodingId == windowsUnicodeBmp {
			return true
		}
		if s.EncodingId == windowsUnicodeFull {
			// "Fonts that support Unicode supplementary-plane characters ...
			// on the Windows platform must have a format 12 subtable for
			// platform ID 3, encoding ID 10."
			return s.Format == 12 || s.Format == 13
		}
		return false
	}
	return false
}

// GlyphIndex maps a code point to a glyph id. It reports false when the glyph
// is not found (or is glyph 0), when the format is unsupported (8), or for
// format 14 subtables.
func (s *CmapSubtable) GlyphIndex(codePoint uint32) (uint16, bool) {
	switch {
	case s.sub0 != nil:
		return s.sub0.glyphIndex(codePoint)
	case s.sub2 != nil:
		return s.sub2.glyphIndex(codePoint)
	case s.sub4 != nil:
		return s.sub4.glyphIndex(codePoint)
	case s.sub6 != nil:
		return s.sub6.glyphIndex(codePoint)
	case s.sub10 != nil:
		return s.sub10.glyphIndex(codePoint)
	case s.sub12 != nil:
		return s.sub12.glyphIndex(codePoint)
	case s.sub13 != nil:
		return s.sub13.glyphIndex(codePoint)
	}
	return 0, false
}

// Codepoints calls f for all code points defined in this subtable (in table
// order). Code points that map to glyph 0 may still be reported.
func (s *CmapSubtable) Codepoints(f func(cp uint32)) {
	switch {
	case s.sub0 != nil:
		s.sub0.codepoints(f)
	case s.sub2 != nil:
		s.sub2.codepoints(f)
	case s.sub4 != nil:
		s.sub4.codepoints(f)
	case s.sub6 != nil:
		s.sub6.codepoints(f)
	case s.sub10 != nil:
		s.sub10.codepoints(f)
	case s.sub12 != nil:
		s.sub12.codepoints(f)
	case s.sub13 != nil:
		s.sub13.codepoints(f)
	}
}

// ─── format 0: byte encoding table ────────────────────────────────────────────

type subtable0 struct {
	glyphIds []byte // 256 entries
}

func parseSubtable0(data []byte) (*subtable0, bool) {
	if len(data) < 6+256 {
		return nil, false
	}
	return &subtable0{glyphIds: data[6 : 6+256]}, true
}

func (s *subtable0) glyphIndex(codePoint uint32) (uint16, bool) {
	if codePoint >= 256 {
		return 0, false
	}
	glyphId := s.glyphIds[codePoint]
	if glyphId == 0 {
		return 0, false
	}
	return uint16(glyphId), true
}

func (s *subtable0) codepoints(f func(cp uint32)) {
	for i, gid := range s.glyphIds {
		// Unlike other formats, check the glyph id: the array always has 256
		// entries even when the face has fewer glyphs.
		if gid != 0 {
			f(uint32(i))
		}
	}
}

// ─── format 2: high-byte mapping through table ────────────────────────────────

type subHeaderRecord struct {
	firstCode     uint16
	entryCount    uint16
	idDelta       int16
	idRangeOffset uint16
}

type subtable2 struct {
	subHeaderKeys    []uint16 // 256 entries
	subHeadersOffset int
	subHeaders       []subHeaderRecord
	data             []byte
}

func parseSubtable2(data []byte) (*subtable2, bool) {
	if len(data) < 6+256*2 {
		return nil, false
	}
	keys := make([]uint16, 256)
	for i := range keys {
		keys[i] = beU16must(data, 6+i*2)
	}
	// The maximum index in sub_header_keys is the sub_headers count.
	maxIdx := uint16(0)
	for _, k := range keys {
		if k/8 > maxIdx {
			maxIdx = k / 8
		}
	}
	subHeadersOffset := 6 + 256*2
	n := int(maxIdx) + 1
	if n < 0 || subHeadersOffset+n*8 > len(data) {
		return nil, false
	}
	headers := make([]subHeaderRecord, n)
	for i := range headers {
		at := subHeadersOffset + i*8
		headers[i] = subHeaderRecord{
			firstCode:     beU16must(data, at),
			entryCount:    beU16must(data, at+2),
			idDelta:       int16(beU16must(data, at+4)),
			idRangeOffset: beU16must(data, at+6),
		}
	}
	return &subtable2{subHeaderKeys: keys, subHeadersOffset: subHeadersOffset, subHeaders: headers, data: data}, true
}

func (s *subtable2) glyphIndex(codePoint uint32) (uint16, bool) {
	// This subtable supports code points only in a u16 range.
	if codePoint > 0xFFFF {
		return 0, false
	}
	cp := uint16(codePoint)
	highByte := cp >> 8
	lowByte := cp & 0x00FF

	var i uint16
	if cp < 0xff {
		// SubHeader 0 is special: it is used for single-byte character codes.
		i = 0
	} else {
		i = s.subHeaderKeys[highByte] / 8
	}
	if int(i) >= len(s.subHeaders) {
		return 0, false
	}
	subHeader := s.subHeaders[i]

	firstCode := subHeader.firstCode
	if subHeader.entryCount != 0 && firstCode+subHeader.entryCount < firstCode {
		return 0, false // checked_add overflow
	}
	rangeEnd := firstCode + subHeader.entryCount
	if lowByte < firstCode || lowByte >= rangeEnd {
		return 0, false
	}

	// idRangeOffset points to first_code in the glyphIndexArray; advance to
	// our code point.
	indexOffset := uint(lowByte-firstCode) * 2

	// "The value of the idRangeOffset is the number of bytes past the actual
	// location of the idRangeOffset".
	offset := uint(s.subHeadersOffset) +
		8*uint(uint16(i+1)) - // advance to required subheader
		2 + // move back to idRangeOffset start
		uint(subHeader.idRangeOffset) +
		indexOffset

	glyph, ok := beU16(s.data, int(offset))
	if !ok {
		return 0, false
	}
	if glyph == 0 {
		return 0, false
	}
	sum := int32(glyph) + int32(subHeader.idDelta)
	sum %= 65536
	if sum < 0 {
		// Rust: (i32 % 65536) keeps the sign; u16::try_from then fails.
		return 0, false
	}
	return uint16(sum), true
}

func (s *subtable2) codepoints(f func(cp uint32)) {
	// Mirrors codepoints_inner: the first failed lookup (out-of-range key or
	// header, or an overflowing range) terminates the whole enumeration.
	for firstByte := range uint16(256) {
		i := s.subHeaderKeys[firstByte] / 8
		if int(i) >= len(s.subHeaders) {
			return
		}
		subHeader := s.subHeaders[i]
		firstCode := subHeader.firstCode
		if i == 0 {
			// Single byte code.
			if subHeader.entryCount != 0 && firstCode+subHeader.entryCount < firstCode {
				return
			}
			rangeEnd := firstCode + subHeader.entryCount
			if firstByte >= firstCode && firstByte < rangeEnd {
				f(uint32(firstByte))
			}
		} else {
			// Two byte code.
			base := firstCode + firstByte<<8
			if base < firstCode {
				return // checked_add overflow
			}
			for k := range uint32(subHeader.entryCount) {
				cp := base + uint16(k)
				if uint32(k) > 0 && cp < base {
					return // checked_add overflow
				}
				f(uint32(cp))
			}
		}
	}
}

// ─── format 4: segment mapping to delta values ────────────────────────────────

type subtable4 struct {
	startCodes       []uint16
	endCodes         []uint16
	idDeltas         []int16
	idRangeOffsets   []uint16
	idRangeOffsetPos int
	data             []byte
}

func parseSubtable4(data []byte) (*subtable4, bool) {
	// format + length + language
	segCountX2, ok := beU16(data, 6)
	if !ok || segCountX2 < 2 {
		return nil, false
	}
	segCount := int(segCountX2 / 2)
	// searchRange + entrySelector + rangeShift
	at := 8 + 6
	end := at + segCount*2 + 2 // + reservedPad
	if segCount < 0 || end > len(data) {
		return nil, false
	}
	endCodes := make([]uint16, segCount)
	for i := range endCodes {
		endCodes[i] = beU16must(data, at+i*2)
	}
	at = end
	end = at + segCount*2
	if end > len(data) {
		return nil, false
	}
	startCodes := make([]uint16, segCount)
	for i := range startCodes {
		startCodes[i] = beU16must(data, at+i*2)
	}
	at = end
	end = at + segCount*2
	if end > len(data) {
		return nil, false
	}
	idDeltas := make([]int16, segCount)
	for i := range idDeltas {
		idDeltas[i] = int16(beU16must(data, at+i*2))
	}
	idRangeOffsetPos := at
	at = end
	end = at + segCount*2
	if end > len(data) {
		return nil, false
	}
	idRangeOffsets := make([]uint16, segCount)
	for i := range idRangeOffsets {
		idRangeOffsets[i] = beU16must(data, at+i*2)
	}
	return &subtable4{
		startCodes:       startCodes,
		endCodes:         endCodes,
		idDeltas:         idDeltas,
		idRangeOffsets:   idRangeOffsets,
		idRangeOffsetPos: idRangeOffsetPos,
		data:             data,
	}, true
}

func (s *subtable4) glyphIndex(codePoint uint32) (uint16, bool) {
	// This subtable supports code points only in a u16 range.
	if codePoint > 0xFFFF {
		return 0, false
	}
	cp := uint16(codePoint)

	// A custom binary search.
	start, end := 0, len(s.startCodes)
	for end > start {
		index := (start + end) / 2
		endValue := s.endCodes[index]
		if endValue >= cp {
			startValue := s.startCodes[index]
			if startValue > cp {
				end = index
				continue
			}
			idRangeOffset := s.idRangeOffsets[index]
			idDelta := s.idDeltas[index]
			if idRangeOffset == 0 {
				return cp + uint16(idDelta), true
			} else if idRangeOffset == 0xFFFF {
				// Some malformed fonts have 0xFFFF as the last offset, which
				// is invalid and should be ignored.
				return 0, false
			}

			delta := (uint32(cp) - uint32(startValue)) * 2
			if delta > 0xFFFF {
				return 0, false
			}

			idRangeOffsetPos := uint16(s.idRangeOffsetPos + index*2)
			pos := idRangeOffsetPos + uint16(delta)
			pos += idRangeOffset // wrapping u16 adds, like upstream

			glyphArrayValue, ok := beU16(s.data, int(pos))
			if !ok {
				return 0, false
			}

			// 0 indicates missing glyph.
			if glyphArrayValue == 0 {
				return 0, false
			}

			glyphId := int16(glyphArrayValue) + idDelta
			if glyphId < 0 {
				// u16::try_from(negative) fails upstream.
				return 0, false
			}
			return uint16(glyphId), true
		}
		start = index + 1
	}
	return 0, false
}

func (s *subtable4) codepoints(f func(cp uint32)) {
	for i := range s.startCodes {
		start, end := s.startCodes[i], s.endCodes[i]
		// 0xFFFF value is special and indicates codes end.
		if start == end && start == 0xFFFF {
			break
		}
		if start > end {
			continue // empty range: start..=end yields nothing
		}
		for cp := start; ; cp++ {
			f(uint32(cp))
			if cp == end {
				break
			}
		}
	}
}

// ─── format 6: trimmed table mapping ──────────────────────────────────────────

type subtable6 struct {
	firstCodePoint uint16
	glyphs         []uint16
}

func parseSubtable6(data []byte) (*subtable6, bool) {
	firstCodePoint, ok1 := beU16(data, 6)
	count, ok2 := beU16(data, 8)
	if !ok1 || !ok2 {
		return nil, false
	}
	end := 10 + int(count)*2
	if int(count) < 0 || end > len(data) {
		return nil, false
	}
	glyphs := make([]uint16, count)
	for i := range glyphs {
		glyphs[i] = beU16must(data, 10+i*2)
	}
	return &subtable6{firstCodePoint: firstCodePoint, glyphs: glyphs}, true
}

func (s *subtable6) glyphIndex(codePoint uint32) (uint16, bool) {
	// This subtable supports code points only in a u16 range.
	if codePoint > 0xFFFF {
		return 0, false
	}
	cp := uint16(codePoint)
	if cp < s.firstCodePoint {
		return 0, false
	}
	idx := int(cp - s.firstCodePoint)
	if idx >= len(s.glyphs) {
		return 0, false
	}
	return s.glyphs[idx], true
}

func (s *subtable6) codepoints(f func(cp uint32)) {
	for i := range s.glyphs {
		// checked_add(i): an overflowing addition ends the enumeration.
		cp := s.firstCodePoint + uint16(i)
		if uint16(i) != 0 && cp < s.firstCodePoint {
			return
		}
		f(uint32(cp))
	}
}

// ─── format 10: trimmed array ─────────────────────────────────────────────────

type subtable10 struct {
	firstCodePoint uint32
	glyphs         []uint16
}

func parseSubtable10(data []byte) (*subtable10, bool) {
	firstCodePoint, ok1 := beU32(data, 12)
	count, ok2 := beU32(data, 16)
	if !ok1 || !ok2 {
		return nil, false
	}
	if count > (1<<32)/2 || 20+uint64(count)*2 > uint64(len(data)) {
		return nil, false
	}
	glyphs := make([]uint16, count)
	for i := range glyphs {
		glyphs[i] = beU16must(data, 20+i*2)
	}
	return &subtable10{firstCodePoint: firstCodePoint, glyphs: glyphs}, true
}

func (s *subtable10) glyphIndex(codePoint uint32) (uint16, bool) {
	if codePoint < s.firstCodePoint {
		return 0, false
	}
	idx := codePoint - s.firstCodePoint
	if idx >= uint32(len(s.glyphs)) {
		return 0, false
	}
	return s.glyphs[idx], true
}

func (s *subtable10) codepoints(f func(cp uint32)) {
	for i := range s.glyphs {
		// checked_add(i): an overflowing addition ends the enumeration.
		cp := s.firstCodePoint + uint32(i)
		if uint32(i) != 0 && cp < s.firstCodePoint {
			return
		}
		f(cp)
	}
}

// ─── formats 12/13: segmented coverage ────────────────────────────────────────

type sequentialMapGroup struct {
	startCharCode uint32
	endCharCode   uint32
	startGlyphId  uint32
}

type subtable12 struct {
	groups []sequentialMapGroup
}

func parseSubtable12(data []byte) (*subtable12, bool) {
	count, ok := beU32(data, 12)
	if !ok {
		return nil, false
	}
	if count > (1<<32)/12 || 16+uint64(count)*12 > uint64(len(data)) {
		return nil, false
	}
	groups := make([]sequentialMapGroup, count)
	for i := range groups {
		at := 16 + i*12
		groups[i] = sequentialMapGroup{
			startCharCode: beU32must(data, at),
			endCharCode:   beU32must(data, at+4),
			startGlyphId:  beU32must(data, at+8),
		}
	}
	return &subtable12{groups: groups}, true
}

func (s *subtable12) glyphIndex(codePoint uint32) (uint16, bool) {
	// binary_search_by: Greater when the group starts after the code point,
	// Less when it ends before it, Equal when it contains it. Like Rust's
	// binary_search_by, the scan stops at the first Equal hit.
	lo, hi := 0, len(s.groups)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		g := s.groups[mid]
		switch {
		case g.startCharCode > codePoint:
			hi = mid
		case g.endCharCode < codePoint:
			lo = mid + 1
		default:
			id := g.startGlyphId + codePoint
			if id < g.startGlyphId {
				return 0, false // checked_add overflow
			}
			id -= g.startCharCode
			if id > 0xFFFF {
				return 0, false
			}
			return uint16(id), true
		}
	}
	return 0, false
}

func (s *subtable12) codepoints(f func(cp uint32)) {
	for _, g := range s.groups {
		if g.startCharCode > g.endCharCode {
			continue // empty range: start..=end yields nothing
		}
		for cp := g.startCharCode; ; cp++ {
			f(cp)
			if cp == g.endCharCode {
				break
			}
		}
	}
}

type subtable13 struct {
	groups []sequentialMapGroup
}

func parseSubtable13(data []byte) (*subtable13, bool) {
	s, ok := parseSubtable12(data)
	if !ok {
		return nil, false
	}
	return &subtable13{groups: s.groups}, true
}

func (s *subtable13) glyphIndex(codePoint uint32) (uint16, bool) {
	for _, g := range s.groups {
		if codePoint >= g.startCharCode && codePoint <= g.endCharCode {
			if g.startGlyphId > 0xFFFF {
				return 0, false // u16::try_from fails upstream
			}
			return uint16(g.startGlyphId), true
		}
	}
	return 0, false
}

func (s *subtable13) codepoints(f func(cp uint32)) {
	for _, g := range s.groups {
		if g.startCharCode > g.endCharCode {
			continue // empty range: start..=end yields nothing
		}
		for cp := g.startCharCode; ; cp++ {
			f(cp)
			if cp == g.endCharCode {
				break
			}
		}
	}
}

func beU16must(data []byte, at int) uint16 {
	return uint16(data[at])<<8 | uint16(data[at+1])
}

func beU32must(data []byte, at int) uint32 {
	return uint32(data[at])<<24 | uint32(data[at+1])<<16 | uint32(data[at+2])<<8 | uint32(data[at+3])
}
