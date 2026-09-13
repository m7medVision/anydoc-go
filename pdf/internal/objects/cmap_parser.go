package objects

// ToUnicode CMap parser, port of lopdf src/parser/cmap_parser.rs. The grammar
// uses its own space rules: space = ' '|'\t'; multispace = space | eol | '%'
// comment.

type cmapSectionKind uint8

const (
	cmapSectionCsRange cmapSectionKind = iota
	cmapSectionBfChar
	cmapSectionBfRange
)

type cmapSection struct {
	kind cmapSectionKind
	// CsRange
	ranges [][3]any // (start, end, codeLen) — see sourceRangeTuple
	// BfChar
	charMappings []cmapCharMapping
	// BfRange
	rangeMappings []cmapRangeMapping
}

type sourceRangeTuple struct {
	start, end uint32
	codeLen    uint8
}

type cmapCharMapping struct {
	code    uint32
	codeLen uint8
	dst     []uint16
}

type cmapRangeMapping struct {
	start, end uint32
	codeLen    uint8
	dst        [][]uint16
}

func cmapSpace0(in []byte) []byte {
	for len(in) > 0 && (in[0] == ' ' || in[0] == '\t') {
		in = in[1:]
	}
	return in
}

func cmapSpace1(in []byte) ([]byte, bool) {
	if len(in) == 0 || (in[0] != ' ' && in[0] != '\t') {
		return in, false
	}
	return cmapSpace0(in), true
}

func cmapMultispace(in []byte) []byte {
	for {
		i := 0
		for i < len(in) && (in[i] == ' ' || in[i] == '\t') {
			i++
		}
		if i > 0 {
			in = in[i:]
			continue
		}
		if rest, ok := pEol(in); ok {
			in = rest
			continue
		}
		if rest, ok := pComment(in); ok {
			in = rest
			continue
		}
		return in
	}
}

func cmapMultispace1(in []byte) ([]byte, bool) {
	if len(in) == 0 {
		return in, false
	}
	c := in[0]
	if c != ' ' && c != '\t' && c != '\n' && c != '\r' && c != '%' {
		return in, false
	}
	return cmapMultispace(in), true
}

func cmapTag(in []byte, tag string) bool { return hasPrefix(in, tag) }

// parseCMapSections mirrors cmap_parser::parse.
func parseCMapSections(streamContent []byte) ([]cmapSection, error) {
	// cidinit_procset
	if cmapTag(streamContent, "\xEF\xBB\xBF") {
		streamContent = streamContent[3:]
	}
	in := cmapMultispace(streamContent)
	if !cmapTag(in, "/CIDInit") {
		return nil, errCmapParse()
	}
	in = cmapSpace0(in[len("/CIDInit"):])
	if cmapTag(in, "/ProcSet") {
		in = in[len("/ProcSet"):]
	} else if cmapTag(in, "/Procset") {
		in = in[len("/Procset"):]
	} else {
		return nil, errCmapParse()
	}
	var ok bool
	if in, ok = cmapSpace1(in); !ok {
		return nil, errCmapParse()
	}
	if !cmapTag(in, "findresource") {
		return nil, errCmapParse()
	}
	in = in[len("findresource"):]
	if in, ok = cmapSpace1(in); !ok {
		return nil, errCmapParse()
	}
	if !cmapTag(in, "begin") {
		return nil, errCmapParse()
	}
	in = in[len("begin"):]
	if in, ok = cmapMultispace1(in); !ok {
		return nil, errCmapParse()
	}

	// cmap_resource_dictionary: digit1 space1 "dict" space1 "begin" multispace1
	digits, ok2 := parseDigits(in)
	if !ok2 {
		return nil, errCmapParse()
	}
	in = in[len(digits):]
	if in, ok = cmapSpace1(in); !ok {
		return nil, errCmapParse()
	}
	if !cmapTag(in, "dict") {
		return nil, errCmapParse()
	}
	in = in[len("dict"):]
	if in, ok = cmapSpace1(in); !ok {
		return nil, errCmapParse()
	}
	if !cmapTag(in, "begin") {
		return nil, errCmapParse()
	}
	in = in[len("begin"):]
	if in, ok = cmapMultispace1(in); !ok {
		return nil, errCmapParse()
	}

	// cmap_data
	if !cmapTag(in, "begincmap") {
		return nil, errCmapParse()
	}
	in = cmapMultispace(in[len("begincmap"):])
	in, ok = cmapMetadata(in)
	if !ok {
		return nil, errCmapParse()
	}
	sections, rest, ok := cmapCodespaceAndMappings(in)
	if !ok {
		return nil, errCmapParse()
	}
	in = rest
	for _, tag := range []string{"endcmap", "CMapName", "currentdict", "/CMap", "defineresource", "pop"} {
		if !cmapTag(in, tag) {
			return nil, errCmapParse()
		}
		in = in[len(tag):]
		switch tag {
		case "endcmap", "pop":
			if in, ok = cmapMultispace1(in); !ok {
				return nil, errCmapParse()
			}
		case "CMapName", "currentdict", "/CMap", "defineresource":
			if in, ok = cmapSpace1(in); !ok {
				return nil, errCmapParse()
			}
		}
	}

	// resource dictionary end: "end" multispace0 (multispace1 in
	// cmap_resource_dictionary's end_parser)
	if !cmapTag(in, "end") {
		return nil, errCmapParse()
	}
	in = cmapMultispace(in[len("end"):])
	_ = in
	return sections, nil
}

func errCmapParse() error {
	return &Error{Kind: KindToUnicodeCMap, Inner: &UnicodeCMapError{parseErr: true}}
}

// cmapMetadata mirrors fold_many_m_n(1, 7, metadata_parser).
func cmapMetadata(in []byte) ([]byte, bool) {
	count := 0
	for count < 7 {
		rest, ok := cmapMetadataEntry(in)
		if !ok {
			break
		}
		in = rest
		count++
	}
	if count < 1 {
		return in, false
	}
	return in, true
}

func cmapMetadataEntry(in []byte) ([]byte, bool) {
	// cid_system_info: "/CIDSystemInfo" multispace0 (dictionary | dict_dup) multispace1 "def" multispace1
	if cmapTag(in, "/CIDSystemInfo") {
		rest := cmapMultispace(in[len("/CIDSystemInfo"):])
		if r2, _, ok := pDictionaryRaw(rest, maxNestingDep); ok {
			rest = r2
		} else if r2, ok = cmapDictDup(rest); ok {
			rest = r2
		} else {
			return in, false
		}
		rest = cmapMultispace(rest)
		if !cmapTag(rest, "def") {
			return in, false
		}
		rest = cmapMultispace(rest[len("def"):])
		var ok bool
		if rest, ok = cmapMultispace1(rest); !ok {
			return in, false
		}
		return rest, true
	}
	oneWord := func(key string, value func([]byte) ([]byte, bool)) ([]byte, bool) {
		if !cmapTag(in, key) {
			return in, false
		}
		rest := cmapSpace0(in[len(key):])
		rest, ok := value(rest)
		if !ok {
			return in, false
		}
		rest = cmapMultispace(rest)
		if !cmapTag(rest, "def") {
			return in, false
		}
		rest = cmapMultispace(rest[len("def"):])
		if rest, ok = cmapMultispace1(rest); !ok {
			return in, false
		}
		return rest, true
	}
	// cmap_name: "/CMapName" space0 name space1 "def" multispace1
	if cmapTag(in, "/CMapName") {
		return oneWord("/CMapName", func(b []byte) ([]byte, bool) {
			rest, _, ok := pName(b)
			if !ok {
				return b, false
			}
			rest, ok = cmapSpace1(rest)
			return rest, ok
		})
	}
	// cmap_type / wmode / uidoffset: digit1 value
	for _, key := range []string{"/CMapType", "/WMode", "/UIDOffset"} {
		if cmapTag(in, key) {
			return oneWord(key, func(b []byte) ([]byte, bool) {
				if _, ok := cmapSpace1(b); !ok {
					return b, false
				}
				digits, ok := parseDigits(b)
				if !ok {
					return b, false
				}
				return b[len(digits):], true
			})
		}
	}
	// cmap_version: digit1 ('.' digit1)?
	if cmapTag(in, "/CMapVersion") {
		return oneWord("/CMapVersion", func(b []byte) ([]byte, bool) {
			if _, ok := cmapSpace1(b); !ok {
				return b, false
			}
			digits, ok := parseDigits(b)
			if !ok {
				return b, false
			}
			b = b[len(digits):]
			if cmapTag(b, ".") {
				b = b[1:]
				d2, ok2 := parseDigits(b)
				if !ok2 {
					return b, false
				}
				b = b[len(d2):]
			}
			return b, true
		})
	}
	// xuid: '[' digits... ']'
	if cmapTag(in, "/XUID") {
		return oneWord("/XUID", func(b []byte) ([]byte, bool) {
			if _, ok := cmapSpace1(b); !ok {
				return b, false
			}
			if !cmapTag(b, "[") {
				return b, false
			}
			b = b[1:]
			for {
				digits, ok := parseDigits(b)
				if !ok {
					break
				}
				b = b[len(digits):]
				if _, ok = cmapSpace1(b); !ok {
					break
				}
			}
			if !cmapTag(b, "]") {
				return b, false
			}
			return b[1:], true
		})
	}
	return in, false
}

// cmapDictDup mirrors parser::dict_dup:
// digit1 space1 "dict" space1 "dup" space1 "begin" multispace1
// ( name space1 direct_object (tag("def") multispace1) )* "end".
func cmapDictDup(in []byte) ([]byte, bool) {
	d1, ok := parseDigits(in)
	if !ok {
		return in, false
	}
	rest := in[len(d1):]
	steps := []struct {
		tag   string
		space bool // space1 (true) or multispace1 (false)
	}{
		{"dict", true}, {"dup", true}, {"begin", false},
	}
	for _, st := range steps {
		if st.space {
			if rest, ok = cmapSpace1(rest); !ok {
				return in, false
			}
		}
		if !cmapTag(rest, st.tag) {
			return in, false
		}
		rest = rest[len(st.tag):]
		if !st.space {
			if rest, ok = cmapMultispace1(rest); !ok {
				return in, false
			}
		}
	}
	for {
		keyRest, _, ok2 := pName(rest)
		if !ok2 {
			break
		}
		keyRest = cmapMultispace(keyRest)
		valRest, _, ok3 := pDirectObject(keyRest, maxNestingDep)
		if !ok3 {
			break
		}
		defRest := cmapMultispace(valRest)
		if !cmapTag(defRest, "def") {
			break
		}
		defRest = cmapMultispace(defRest[len("def"):])
		if defRest, ok4 := cmapMultispace1(defRest); ok4 {
			rest = defRest
		} else {
			break
		}
	}
	if !cmapTag(rest, "end") {
		return in, false
	}
	return rest[len("end"):], true
}

// cmapCodespaceAndMappings mirrors many1(alt(codespace_range, bf_char, bf_range)).
func cmapCodespaceAndMappings(in []byte) ([]cmapSection, []byte, bool) {
	var sections []cmapSection
	for {
		if rest, sec, ok := cmapCodespaceRangeSection(in); ok {
			sections = append(sections, sec)
			in = rest
			continue
		}
		if rest, sec, ok := cmapBfCharSection(in); ok {
			sections = append(sections, sec)
			in = rest
			continue
		}
		if rest, sec, ok := cmapBfRangeSection(in); ok {
			sections = append(sections, sec)
			in = rest
			continue
		}
		break
	}
	if len(sections) == 0 {
		return nil, in, false
	}
	return sections, in, true
}

func cmapSectionBegin(in []byte, keyword string) ([]byte, bool) {
	digits, ok := parseDigits(in)
	if !ok {
		return in, false
	}
	rest := in[len(digits):]
	if rest, ok = cmapSpace1(rest); !ok {
		return in, false
	}
	if !cmapTag(rest, keyword) {
		return in, false
	}
	rest = rest[len(keyword):]
	if rest, ok = cmapMultispace1(rest); !ok {
		return in, false
	}
	return rest, true
}

func cmapSectionEnd(in []byte, keyword string) ([]byte, bool) {
	if !cmapTag(in, keyword) {
		return in, false
	}
	rest, ok := cmapMultispace1(in[len(keyword):])
	if !ok {
		return in, false
	}
	return rest, true
}

func cmapCodespaceRangeSection(in []byte) ([]byte, cmapSection, bool) {
	rest, ok := cmapSectionBegin(in, "begincodespacerange")
	if !ok {
		return in, cmapSection{}, false
	}
	sec := cmapSection{kind: cmapSectionCsRange}
	for {
		pairRest := cmapSpace0(rest)
		pr, pair, ok2 := cmapCodeRangePair(pairRest)
		if !ok2 {
			break
		}
		pr, ok3 := cmapMultispace1(pr)
		if !ok3 {
			break
		}
		rest = pr
		sec.ranges = append(sec.ranges, [3]any{pair.start, pair.end, pair.codeLen})
	}
	if len(sec.ranges) == 0 {
		return in, cmapSection{}, false // many1
	}
	rest, ok = cmapSectionEnd(rest, "endcodespacerange")
	if !ok {
		return in, cmapSection{}, false
	}
	return rest, sec, true
}

func cmapSourceCode(in []byte) ([]byte, uint32, uint8, bool) {
	if !cmapTag(in, "<") {
		return in, 0, 0, false
	}
	rest := in[1:]
	var code uint32
	n := 0
	for n < 4 {
		r, v, ok := pHexChar(rest)
		if !ok {
			break
		}
		rest = r
		code = code<<8 | uint32(v)
		n++
	}
	if n == 0 || !cmapTag(rest, ">") {
		return in, 0, 0, false
	}
	return rest[len(">"):], code, uint8(n), true
}

func cmapCodeRangePair(in []byte) ([]byte, sourceRangeTuple, bool) {
	rest, start, lenBeg, ok := cmapSourceCode(in)
	if !ok {
		return in, sourceRangeTuple{}, false
	}
	rest = cmapSpace0(rest)
	rest2, end, lenEnd, ok2 := cmapSourceCode(rest)
	if !ok2 {
		return in, sourceRangeTuple{}, false
	}
	if lenBeg != lenEnd {
		return in, sourceRangeTuple{}, false
	}
	return rest2, sourceRangeTuple{start: start, end: end, codeLen: lenBeg}, true
}

func cmapTargetString(in []byte) ([]byte, []uint16, bool) {
	if !cmapTag(in, "<") {
		return in, nil, false
	}
	rest := in[1:]
	var units []uint16
	for len(units) < 256 {
		r, h1, ok := pHexChar(rest)
		if !ok {
			break
		}
		r2, h2, ok2 := pHexChar(r)
		if !ok2 {
			break
		}
		units = append(units, uint16(h1)<<8|uint16(h2))
		rest = cmapMultispace(r2)
	}
	if len(units) == 0 || !cmapTag(rest, ">") {
		return in, nil, false
	}
	return rest[len(">"):], units, true
}

func cmapBfCharSection(in []byte) ([]byte, cmapSection, bool) {
	rest, ok := cmapSectionBegin(in, "beginbfchar")
	if !ok {
		return in, cmapSection{}, false
	}
	sec := cmapSection{kind: cmapSectionBfChar}
	for {
		lineRest := cmapSpace0(rest)
		r, code, codeLen, ok2 := cmapSourceCode(lineRest)
		if !ok2 {
			break
		}
		r = cmapSpace0(r)
		r2, dst, ok3 := cmapTargetString(r)
		if !ok3 {
			break
		}
		r2, ok4 := cmapMultispace1(r2)
		if !ok4 {
			break
		}
		rest = r2
		sec.charMappings = append(sec.charMappings, cmapCharMapping{code: code, codeLen: codeLen, dst: dst})
	}
	rest, ok = cmapSectionEnd(rest, "endbfchar")
	if !ok {
		return in, cmapSection{}, false
	}
	return rest, sec, true
}

func cmapBfRangeSection(in []byte) ([]byte, cmapSection, bool) {
	rest, ok := cmapSectionBegin(in, "beginbfrange")
	if !ok {
		return in, cmapSection{}, false
	}
	sec := cmapSection{kind: cmapSectionBfRange}
	for {
		lineRest := cmapSpace0(rest)
		r, pair, ok2 := cmapCodeRangePair(lineRest)
		if !ok2 {
			break
		}
		r = cmapSpace0(r)
		var dst [][]uint16
		if r2, ts, ok3 := cmapTargetString(r); ok3 {
			dst = [][]uint16{ts}
			r = r2
		} else if r2, arr, ok4 := cmapRangeTargetArray(r); ok4 {
			dst = arr
			r = r2
		} else {
			break
		}
		r, ok5 := cmapMultispace1(r)
		if !ok5 {
			break
		}
		rest = r
		sec.rangeMappings = append(sec.rangeMappings, cmapRangeMapping{
			start: pair.start, end: pair.end, codeLen: pair.codeLen, dst: dst,
		})
	}
	rest, ok = cmapSectionEnd(rest, "endbfrange")
	if !ok {
		return in, cmapSection{}, false
	}
	return rest, sec, true
}

func cmapRangeTargetArray(in []byte) ([]byte, [][]uint16, bool) {
	if !cmapTag(in, "[") {
		return in, nil, false
	}
	rest := cmapSpace0(in[1:])
	var arr [][]uint16
	for {
		r, ts, ok := cmapTargetString(rest)
		if !ok {
			break
		}
		arr = append(arr, ts)
		r, ok = cmapSpace1(r)
		if !ok {
			break
		}
		rest = r
	}
	if len(arr) == 0 {
		return in, nil, false
	}
	rest = cmapSpace0(rest)
	if !cmapTag(rest, "]") {
		return in, nil, false
	}
	return rest[len("]"):], arr, true
}

// parseToUnicodeCMap mirrors ToUnicodeCMap::parse.
func parseToUnicodeCMap(streamContent []byte) (*ToUnicodeCMap, error) {
	sections, err := parseCMapSections(streamContent)
	if err != nil {
		return nil, err
	}
	return fromCMapSections(sections)
}
