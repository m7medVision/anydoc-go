// Encoding CMap fallback construction: builds ToUnicode CMaps from font
// /Encoding entries and CIDSystemInfo orderings.
//
// Port of pdf-inspector v1.14.2 src/tounicode.rs (EncodingCMap machinery).
package tounicode

import (
	"strings"
	"unicode"

	"github.com/m7medVision/anydoc-go/pdf/internal/objects"
)

// encodingCMap is a parsed encoding (code→CID) CMap.
type encodingCMap struct {
	Map            map[uint16]uint16
	CodeByteLength uint8
	IsIdentity     bool
}

// buildFallbackTounicodeFromEncoding builds a ToUnicode CMap from the font's
// /Encoding entry combined with the CIDSystemInfo ordering's UCS2 map.
func buildFallbackTounicodeFromEncoding(fontDict *objects.Dictionary, doc *objects.Document) (ToUnicodeCMap, bool) {
	encoding, ok := buildEncodingCmapFromFont(fontDict, doc)
	if !ok {
		return ToUnicodeCMap{}, false
	}
	ordering, ok := getCidSystemInfoOrdering(fontDict, doc)
	if !ok {
		return ToUnicodeCMap{}, false
	}
	ucs2, ok := buildCmapFromBuiltinCmap(ordering)
	if !ok {
		return ToUnicodeCMap{}, false
	}

	if encoding.IsIdentity {
		// Identity mapping: charcode == CID
		return ucs2, true
	}

	cmap := NewCMap()
	for charcode, cid := range encoding.Map {
		if s, ok := ucs2.Lookup(cid); ok {
			cmap.CharMap[charcode] = s
		}
	}
	if len(cmap.CharMap) == 0 {
		return ToUnicodeCMap{}, false
	}
	cmap.CodeByteLength = encoding.CodeByteLength
	return cmap, true
}

// getCidSystemInfoOrdering reads the /Ordering string from the descendant
// CIDFont's /CIDSystemInfo.
func getCidSystemInfoOrdering(fontDict *objects.Dictionary, doc *objects.Document) (string, bool) {
	cidFontDict, ok := getDescendantCidFont(fontDict, doc)
	if !ok {
		return "", false
	}
	csiObj, err := cidFontDict.Get([]byte("CIDSystemInfo"))
	if err != nil {
		return "", false
	}
	var csiDict *objects.Dictionary
	switch {
	case csiObj.Kind == objects.KindReference:
		d, err := doc.GetDictionary(csiObj.Ref)
		if err != nil {
			return "", false
		}
		csiDict = d
	case csiObj.Kind == objects.KindDictionary:
		csiDict = csiObj.Dict
	default:
		return "", false
	}
	orderingObj, err := csiDict.Get([]byte("Ordering"))
	if err != nil || orderingObj.Kind != objects.KindString {
		return "", false
	}
	return lossyUTF8(orderingObj.Str), true
}

// buildEncodingCmapFromFont reads the /Encoding entry of a font dict.
func buildEncodingCmapFromFont(fontDict *objects.Dictionary, doc *objects.Document) (encodingCMap, bool) {
	encodingObj, err := fontDict.Get([]byte("Encoding"))
	if err != nil {
		return encodingCMap{}, false
	}
	switch encodingObj.Kind {
	case objects.KindName:
		enc := encodingObj.Name
		if string(enc) == "Identity-H" || string(enc) == "Identity-V" {
			return encodingCMap{Map: map[uint16]uint16{}, CodeByteLength: 2, IsIdentity: true}, true
		}
		return loadBuiltinEncodingCmap(lossyUTF8(enc))
	case objects.KindReference:
		obj, err := doc.GetObject(encodingObj.Ref)
		if err != nil {
			return encodingCMap{}, false
		}
		return parseEncodingCmapObject(obj, doc)
	case objects.KindStream:
		data, err := encodingObj.Stream.DecompressedContent()
		if err != nil {
			return encodingCMap{}, false
		}
		return parseEncodingCmapStream(data)
	}
	return encodingCMap{}, false
}

func parseEncodingCmapObject(obj *objects.Object, doc *objects.Document) (encodingCMap, bool) {
	switch obj.Kind {
	case objects.KindStream:
		data, err := obj.Stream.DecompressedContent()
		if err != nil {
			return encodingCMap{}, false
		}
		return parseEncodingCmapStream(data)
	case objects.KindReference:
		next, err := doc.GetObject(obj.Ref)
		if err != nil {
			return encodingCMap{}, false
		}
		return parseEncodingCmapObject(next, doc)
	}
	return encodingCMap{}, false
}

// loadBuiltinEncodingCmap loads a named encoding from the built-in bcmaps.
func loadBuiltinEncodingCmap(name string) (encodingCMap, bool) {
	data, ok := readBuiltinCmapFile(name + ".bcmap")
	if !ok {
		return encodingCMap{}, false
	}
	enc, err := parseBinaryCmapEncoding(data)
	if err != nil {
		return encodingCMap{}, false
	}
	return enc, true
}

// parseEncodingCmapStream parses a text CMap stream into an encoding map
// (begincidchar / begincidrange).
func parseEncodingCmapStream(data []byte) (encodingCMap, bool) {
	text := lossyUTF8(data)
	var srcHexLengths []int
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
						codespaceByteLen = uint8((hexLen + 1) / 2)
						haveCodespace = true
					}
					inHex = false
				case inHex && isASCIIHexDigit(byte(c)):
					hexLen++
				}
			}
		}
	}

	m := map[uint16]uint16{}
	assigned := 0
	pos := 0
	for {
		start := strings.Index(text[pos:], "begincidchar")
		if start < 0 {
			break
		}
		sectionStart := pos + start + len("begincidchar")
		end := strings.Index(text[sectionStart:], "endcidchar")
		if end < 0 {
			break
		}
		section := text[sectionStart : sectionStart+end]
		if !parseCidcharSection(section, m, &srcHexLengths, &assigned) {
			break
		}
		pos = sectionStart + end
	}
	pos = 0
	for assigned < maxCIDWExpansion {
		start := strings.Index(text[pos:], "begincidrange")
		if start < 0 {
			break
		}
		sectionStart := pos + start + len("begincidrange")
		end := strings.Index(text[sectionStart:], "endcidrange")
		if end < 0 {
			break
		}
		section := text[sectionStart : sectionStart+end]
		if !parseCidrangeSection(section, m, &srcHexLengths, &assigned) {
			break
		}
		pos = sectionStart + end
	}

	if len(m) == 0 {
		return encodingCMap{}, false
	}

	var codeByteLength uint8
	if haveCodespace {
		codeByteLength = codespaceByteLen
	} else if len(srcHexLengths) > 0 {
		maxHexLen := 4
		for _, l := range srcHexLengths {
			if l > maxHexLen {
				maxHexLen = l
			}
		}
		if maxHexLen <= 2 {
			codeByteLength = 1
		} else {
			codeByteLength = 2
		}
	} else {
		codeByteLength = 2
	}

	return encodingCMap{Map: m, CodeByteLength: codeByteLength, IsIdentity: false}, true
}

// parseCidcharSection parses a begincidchar section: <code> cid pairs.
func parseCidcharSection(section string, m map[uint16]uint16, srcHexLengths *[]int, assigned *int) bool {
	r := runeScanner{src: section}
	for {
		r.skipWhitespace()
		if r.peek() != '<' {
			break
		}
		r.next()
		srcHex := r.readUntil('>')
		r.next()
		if trimmed := strings.TrimSpace(srcHex); trimmed != "" {
			*srcHexLengths = append(*srcHexLengths, len(trimmed))
		}
		r.skipWhitespace()
		var cidStr strings.Builder
		for {
			c := r.peek()
			if c == -1 || unicode.IsSpace(c) {
				break
			}
			cidStr.WriteRune(r.next())
		}
		code, okCode := parseHexU16(srcHex)
		cid, okCid := rustParseUint16(cidStr.String(), 10)
		if okCode && okCid {
			if !assignEncodingCid(m, code, cid, assigned) {
				return false
			}
		}
	}
	return true
}

// parseCidrangeSection parses a begincidrange section:
// <start> <end> startCid triplets.
func parseCidrangeSection(section string, m map[uint16]uint16, srcHexLengths *[]int, assigned *int) bool {
	r := runeScanner{src: section}
	for {
		r.skipWhitespace()
		if r.peek() != '<' {
			break
		}
		r.next()
		startHex := r.readUntil('>')
		r.next()
		if trimmed := strings.TrimSpace(startHex); trimmed != "" {
			*srcHexLengths = append(*srcHexLengths, len(trimmed))
		}
		r.skipWhitespace()
		if r.peek() != '<' {
			continue
		}
		r.next()
		endHex := r.readUntil('>')
		r.next()
		r.skipWhitespace()
		var cidStr strings.Builder
		for {
			c := r.peek()
			if c == -1 || unicode.IsSpace(c) {
				break
			}
			cidStr.WriteRune(r.next())
		}
		start, okStart := parseHexU16(startHex)
		end, okEnd := parseHexU16(endHex)
		startCid, okCid := rustParseUint16(cidStr.String(), 10)
		if !okStart || !okEnd || !okCid {
			continue
		}
		if start > end {
			continue
		}
		cid := startCid
		for code := start; ; code = saturatingAdd1(code) {
			if !assignEncodingCid(m, code, cid, assigned) {
				return false
			}
			if code == end {
				break
			}
			cid = saturatingAdd1(cid)
		}
	}
	return true
}

func assignEncodingCid(m map[uint16]uint16, code, cid uint16, assigned *int) bool {
	// Count overwrites: unique-key coverage alone would not stop a repeated
	// full-width range from re-inserting all 65,536 codes.
	if *assigned >= maxCIDWExpansion {
		return false
	}
	m[code] = cid
	*assigned++
	return true
}

// rustParseUint16 parses a decimal u16 like Rust's str::parse::<u16>()
// (optional leading '+' allowed).
func rustParseUint16(s string, base int) (uint16, bool) {
	v, ok := rustParseUint(s, base, 16)
	if !ok {
		return 0, false
	}
	return uint16(v), true
}

// parseBinaryCmapEncoding parses a pdf.js binary CMap into an encoding map
// (cidchar/cidrange records).
func parseBinaryCmapEncoding(data []byte) (encodingCMap, error) {
	stream := newBinaryCMapStream(data)
	if _, ok := stream.readByte(); !ok {
		return encodingCMap{}, errEOFbcmap
	}
	m := map[uint16]uint16{}
	maxCodeSize := uint8(1)
	var useCmap string

	for {
		b, ok := stream.readByte()
		if !ok {
			break
		}
		typ := b >> 5
		if typ == 7 {
			switch b & 0x1f {
			case 0:
				if _, err := stream.readString(); err != nil {
					return encodingCMap{}, err
				}
			case 1:
				name, err := stream.readString()
				if err != nil {
					return encodingCMap{}, err
				}
				useCmap = name
			}
			continue
		}
		sequence := b&0x10 != 0
		dataSize := int(b & 0x0f)
		if dataSize+1 > 16 {
			return encodingCMap{}, errInvalidDataSize
		}
		if max := uint8(dataSize + 1); max > maxCodeSize {
			maxCodeSize = max
		}
		subitems, err := stream.readNumber()
		if err != nil {
			return encodingCMap{}, err
		}
		switch typ {
		case 2:
			// cidchar
			var prevCode uint32
			for i := range subitems {
				codeBytes, err := stream.readHexNumber(dataSize)
				if err != nil {
					return encodingCMap{}, err
				}
				code := hexToU32(codeBytes)
				cid, err := stream.readNumber()
				if err != nil {
					return encodingCMap{}, err
				}
				if i == 0 {
					prevCode = code
					m[uint16(code)] = uint16(cid)
					continue
				}
				if sequence {
					prevCode = saturatingAdd1u32(prevCode)
					m[uint16(prevCode)] = uint16(cid)
				} else {
					m[uint16(code)] = uint16(cid)
					prevCode = code
				}
			}
		case 3:
			// cidrange
			for range subitems {
				start, err := stream.readHexNumber(dataSize)
				if err != nil {
					return encodingCMap{}, err
				}
				endDelta, err := stream.readHexNumber(dataSize)
				if err != nil {
					return encodingCMap{}, err
				}
				end := append([]byte(nil), start...)
				addHex(end, endDelta)
				cidStart, err := stream.readNumber()
				if err != nil {
					return encodingCMap{}, err
				}
				startCode := uint16(hexToU32(start))
				endCode := uint16(hexToU32(end))
				if startCode > endCode {
					continue
				}
				cid := uint16(cidStart)
				for code := startCode; ; code = saturatingAdd1(code) {
					m[code] = cid
					if code == endCode {
						break
					}
					cid = saturatingAdd1(cid)
				}
			}
		default:
			// Skip other types
			for range subitems {
				_, _ = stream.readHexNumber(dataSize)
				_, _ = stream.readHexNumber(dataSize)
				_, _ = stream.readNumber()
			}
		}
	}

	if useCmap != "" {
		if base, ok := loadBuiltinEncodingCmap(useCmap); ok {
			merged := base.Map
			for k, v := range m {
				merged[k] = v
			}
			codeByteLength := base.CodeByteLength
			if maxCodeSize > codeByteLength {
				codeByteLength = maxCodeSize
			}
			return encodingCMap{Map: merged, CodeByteLength: codeByteLength, IsIdentity: false}, nil
		}
	}

	return encodingCMap{Map: m, CodeByteLength: maxCodeSize, IsIdentity: false}, nil
}

func saturatingAdd1u32(v uint32) uint32 {
	if v == 0xFFFFFFFF {
		return 0xFFFFFFFF
	}
	return v + 1
}
