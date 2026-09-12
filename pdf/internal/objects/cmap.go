package objects

import "sort"

// ToUnicodeCMap port of lopdf src/encodings/cmap.rs (plus the RangeInclusiveMap
// semantics of the rangemap crate: later inserts replace overlapping parts of
// existing ranges and split them).

type bfRangeTargetType uint8

const (
	bfTargetHexString bfRangeTargetType = iota
	bfTargetUTF16CodePoint
	bfTargetArrayOfHexStrings
)

type bfRangeTarget struct {
	Type   bfRangeTargetType
	Units  []uint16   // HexString
	Offset uint32     // UTF16CodePoint
	Array  [][]uint16 // ArrayOfHexStrings
}

type bfRange struct {
	Lo, Hi uint32
	Target bfRangeTarget
}

type bfRangeMap struct {
	ranges []bfRange // sorted by Lo, non-overlapping
}

func (m *bfRangeMap) insert(lo, hi uint32, target bfRangeTarget) {
	// rangemap::RangeInclusiveMap::insert: remove overlapping parts of existing
	// ranges, then insert the new range.
	var kept []bfRange
	newBefore := []bfRange{}
	newAfter := []bfRange{}
	for _, r := range m.ranges {
		if r.Hi < lo || r.Lo > hi {
			kept = append(kept, r)
			continue
		}
		if r.Lo < lo {
			newBefore = append(newBefore, bfRange{Lo: r.Lo, Hi: lo - 1, Target: r.Target})
		}
		if r.Hi > hi {
			newAfter = append(newAfter, bfRange{Lo: hi + 1, Hi: r.Hi, Target: r.Target})
		}
	}
	out := make([]bfRange, 0, len(kept)+len(newBefore)+1+len(newAfter))
	out = append(out, kept...)
	out = append(out, newBefore...)
	out = append(out, bfRange{Lo: lo, Hi: hi, Target: target})
	out = append(out, newAfter...)
	sort.Slice(out, func(i, j int) bool { return out[i].Lo < out[j].Lo })
	m.ranges = out
}

// getKeyValue finds the range containing code.
func (m *bfRangeMap) getKeyValue(code uint32) (bfRange, bool) {
	i := sort.Search(len(m.ranges), func(i int) bool { return m.ranges[i].Hi >= code })
	if i < len(m.ranges) && m.ranges[i].Lo <= code {
		return m.ranges[i], true
	}
	return bfRange{}, false
}

// ReverseCMapEntry mirrors encodings::cmap::ReverseCMapEntry.
type ReverseCMapEntry struct {
	SourceCode uint32
	CodeLen    uint8
}

// ToUnicodeCMap mirrors lopdf::ToUnicodeCMap.
type ToUnicodeCMap struct {
	BfRanges   [4]bfRangeMap
	reverseMap map[string][]ReverseCMapEntry // key: UTF-16BE bytes of the unit sequence
}

const cmapReplacementChar uint16 = 0xfffd

func newToUnicodeCMap() *ToUnicodeCMap {
	return &ToUnicodeCMap{reverseMap: nil}
}

func unitsKey(units []uint16) string {
	b := make([]byte, 0, len(units)*2)
	for _, u := range units {
		b = append(b, byte(u>>8), byte(u))
	}
	return string(b)
}

func (c *ToUnicodeCMap) put(srcLo, srcHi uint32, codeLen uint8, target bfRangeTarget) {
	if codeLen > 4 || codeLen == 0 {
		return
	}
	c.BfRanges[codeLen-1].insert(srcLo, srcHi, target)
}

func (c *ToUnicodeCMap) putChar(code uint32, codeLen uint8, dst []uint16) {
	var target bfRangeTarget
	if len(dst) == 1 {
		target = bfRangeTarget{Type: bfTargetUTF16CodePoint, Offset: uint32(dst[0]) - code}
	} else {
		target = bfRangeTarget{Type: bfTargetHexString, Units: append([]uint16(nil), dst...)}
	}
	c.put(code, code, codeLen, target)
}

// Get mirrors ToUnicodeCMap::get.
func (c *ToUnicodeCMap) Get(code uint32, codeLen uint8) []uint16 {
	if codeLen > 4 || codeLen == 0 {
		return nil
	}
	if r, ok := c.BfRanges[codeLen-1].getKeyValue(code); ok {
		switch r.Target.Type {
		case bfTargetHexString:
			ret := append([]uint16(nil), r.Target.Units...)
			ret[len(ret)-1] += uint16(code - r.Lo)
			return ret
		case bfTargetUTF16CodePoint:
			return []uint16{uint16(code + r.Target.Offset)}
		case bfTargetArrayOfHexStrings:
			idx := int(code - r.Lo)
			if idx < len(r.Target.Array) {
				return append([]uint16(nil), r.Target.Array[idx]...)
			}
			return []uint16{cmapReplacementChar}
		}
	}
	return nil
}

func (c *ToUnicodeCMap) getOrReplacementChar(code uint32, codeLen uint8) []uint16 {
	if v := c.Get(code, codeLen); v != nil {
		return v
	}
	return []uint16{cmapReplacementChar}
}

// GetSourceCodesForUnicode mirrors ToUnicodeCMap::get_source_codes_for_unicode.
func (c *ToUnicodeCMap) GetSourceCodesForUnicode(unicodeSequence []uint16) []ReverseCMapEntry {
	if c.reverseMap == nil {
		return nil
	}
	return c.reverseMap[unitsKey(unicodeSequence)]
}

// expandRangeTarget computes the unicode sequence a range maps a source code
// to (used while building the reverse map).
func expandRangeTarget(r bfRange, srcCode uint32) []uint16 {
	switch r.Target.Type {
	case bfTargetUTF16CodePoint:
		return []uint16{uint16(srcCode + r.Target.Offset)}
	case bfTargetHexString:
		if srcCode == r.Lo {
			return r.Target.Units
		}
		if len(r.Target.Units) == 1 {
			return []uint16{r.Target.Units[0] + uint16(srcCode-r.Lo)}
		}
		if len(r.Target.Units) > 0 {
			out := append([]uint16(nil), r.Target.Units...)
			out[len(out)-1] += uint16(srcCode - r.Lo)
			return out
		}
		return nil
	case bfTargetArrayOfHexStrings:
		idx := int(srcCode - r.Lo)
		if idx < len(r.Target.Array) {
			return r.Target.Array[idx]
		}
		return nil
	}
	return nil
}

// fromCMapSections mirrors ToUnicodeCMap::from_sections.
func fromCMapSections(sections []cmapSection) (*ToUnicodeCMap, error) {
	cmap := newToUnicodeCMap()
	for _, section := range sections {
		switch section.kind {
		case cmapSectionCsRange:
			// no additional validation implemented upstream
		case cmapSectionBfChar:
			for _, m := range section.charMappings {
				cmap.putChar(m.code, m.codeLen, m.dst)
			}
		case cmapSectionBfRange:
			for _, m := range section.rangeMappings {
				if m.end < m.start {
					return nil, &UnicodeCMapError{invalidCodeRange: true}
				}
				switch {
				case len(m.dst) == 1 && len(m.dst[0]) == 1:
					cmap.put(m.start, m.end, m.codeLen, bfRangeTarget{
						Type:   bfTargetUTF16CodePoint,
						Offset: uint32(m.dst[0][0]) - m.start,
					})
				case len(m.dst) == 1:
					cmap.put(m.start, m.end, m.codeLen, bfRangeTarget{
						Type:  bfTargetHexString,
						Units: append([]uint16(nil), m.dst[0]...),
					})
				case len(m.dst) == 0:
					return nil, &UnicodeCMapError{invalidCodeRange: true}
				default:
					arr := make([][]uint16, len(m.dst))
					for i := range m.dst {
						arr[i] = append([]uint16(nil), m.dst[i]...)
					}
					cmap.put(m.start, m.end, m.codeLen, bfRangeTarget{
						Type:  bfTargetArrayOfHexStrings,
						Array: arr,
					})
				}
			}
		}
	}

	rev := map[string][]ReverseCMapEntry{}
	for codeLenIdx := 0; codeLenIdx < 4; codeLenIdx++ {
		codeLen := uint8(codeLenIdx + 1)
		for _, r := range cmap.BfRanges[codeLenIdx].ranges {
			for srcCode := r.Lo; srcCode <= r.Hi; srcCode++ {
				uniSeq := expandRangeTarget(r, srcCode)
				if uniSeq != nil && len(uniSeq) > 0 {
					key := unitsKey(uniSeq)
					rev[key] = append(rev[key], ReverseCMapEntry{SourceCode: srcCode, CodeLen: codeLen})
				}
				if srcCode == 0xFFFFFFFF {
					break // avoid overflow in the loop
				}
			}
		}
	}
	cmap.reverseMap = rev
	return cmap, nil
}

// UnicodeCMapError mirrors encodings::cmap::UnicodeCMapError.
type UnicodeCMapError struct {
	parseErr         bool
	invalidCodeRange bool
}

func (e *UnicodeCMapError) Error() string {
	if e.invalidCodeRange {
		return "invalid code range"
	}
	return "could not parse ToUnicode CMap: CMapParseError"
}
