package objects

import "unicode/utf16"

// FontEncoding port of lopdf's encodings::Encoding (lopdf::Encoding), the
// value returned by Dictionary.GetFontEncoding and consumed by DecodeText.

type encodingVariant uint8

const (
	encOneByteEncoding encodingVariant = iota
	encSimpleEncoding
	encUnicodeMapEncoding
	encDifferences
)

type FontEncoding struct {
	Variant encodingVariant
	// OneByteEncoding table (nil map entry = unmapped).
	Table *codedCharacterSet
	// SimpleEncoding name.
	Name []byte
	// UnicodeMapEncoding.
	CMap *ToUnicodeCMap
	// Differences.
	Diff *Differences
}

// Differences port of lopdf's encodings::Differences.
type Differences struct {
	Base    *FontEncoding
	Map     map[byte]uint16 // code -> glyph (utf16 code unit)
	Inverse map[uint16]byte // glyph -> code
}

func (d *Differences) bytesToString(bytes []byte, out *[]byte) {
	for _, b := range bytes {
		g, ok := d.Map[b]
		if !ok {
			var tmp []byte
			d.Base.writeToString([]byte{b}, &tmp)
			*out = append(*out, tmp...)
			continue
		}
		pushGlyph(out, g)
	}
}

func (d *Differences) stringToBytes(text string, out *[]byte) {
	for _, c := range text {
		any := false
		for _, cu := range utf16.Encode([]rune{c}) {
			g := cu
			if b, ok := d.Inverse[g]; ok {
				*out = append(*out, b)
				any = true
			}
		}
		if any {
			continue
		}
		d.Base.writeToBytes(string(c), out)
	}
}

// pushGlyph appends the UTF-8 encoding of glyph g; lone surrogates are
// dropped (char::decode_utf16([...]).flatten() parity).
func pushGlyph(out *[]byte, g uint16) {
	if utf16.IsSurrogate(rune(g)) {
		return
	}
	*out = utf8AppendRune(*out, rune(g))
}

func utf8AppendRune(b []byte, r rune) []byte {
	return append(b, []byte(string(r))...)
}

// bytesToStringOneByte mirrors encodings::bytes_to_string.
func bytesToStringOneByte(table *codedCharacterSet, bytes []byte, out *[]byte) {
	for _, b := range bytes {
		g := table[b]
		if g == 0 {
			continue
		}
		pushGlyph(out, g)
	}
}

// stringToBytesOneByte mirrors encodings::write_to_bytes (linear scan).
func stringToBytesOneByte(table *codedCharacterSet, text string, out *[]byte) {
	for _, cu := range utf16.Encode([]rune(text)) {
		var n = -1
		for i, g := range table {
			if g == cu {
				n = i
				break
			}
		}
		if n < 0 {
			continue
		}
		*out = append(*out, byte(n))
	}
}

// stringToBytesPDFDoc encodes a password through the PDFDocEncoding table
// (sanitize_password_r4).
func stringToBytesPDFDoc(text string) []byte {
	var out []byte
	stringToBytesOneByte(&pdf_doc_encoding, text, &out)
	return out
}

// BytesToString mirrors Encoding::bytes_to_string.
func (e *FontEncoding) BytesToString(bytes []byte) (string, error) {
	var out []byte
	if err := e.writeToString(bytes, &out); err != nil {
		return "", err
	}
	return string(out), nil
}

func (e *FontEncoding) writeToString(bytes []byte, out *[]byte) error {
	switch e.Variant {
	case encOneByteEncoding:
		bytesToStringOneByte(e.Table, bytes, out)
		return nil
	case encSimpleEncoding:
		switch string(e.Name) {
		case "UniGB-UCS2-H", "UniGB-UTF16-H":
			*out = append(*out, utf16BEDecode(bytes)...)
			return nil
		case "WinAnsiEncoding":
			bytesToStringOneByte(&win_ansi_encoding, bytes, out)
			return nil
		}
		return &Error{Kind: KindCharacterEncoding}
	case encUnicodeMapEncoding:
		e.cmapWriteToString(bytes, out)
		return nil
	case encDifferences:
		e.Diff.bytesToString(bytes, out)
		return nil
	}
	return nil
}

// cmapWriteToString mirrors Encoding::write_to_string's UnicodeMapEncoding
// arm: 1-4 byte source codes, longest accumulated match first.
func (e *FontEncoding) cmapWriteToString(bytes []byte, out *[]byte) {
	var outputUnits []uint16
	bytesConsidered := 0
	consideredCode := uint32(0)
	for _, b := range bytes {
		if bytesConsidered == 4 {
			v := e.CMap.getOrReplacementChar(consideredCode, 4)
			consideredCode = 0
			bytesConsidered = 0
			outputUnits = append(outputUnits, v...)
		}
		bytesConsidered++
		consideredCode = consideredCode*256 + uint32(b)
		if v := e.CMap.Get(consideredCode, uint8(bytesConsidered)); v != nil {
			consideredCode = 0
			bytesConsidered = 0
			outputUnits = append(outputUnits, v...)
		}
	}
	if bytesConsidered > 0 {
		v := e.CMap.getOrReplacementChar(consideredCode, uint8(bytesConsidered))
		outputUnits = append(outputUnits, v...)
	}
	*out = append(*out, utf16BEDecodeUnits(outputUnits)...)
}

// StringToBytes mirrors Encoding::string_to_bytes.
func (e *FontEncoding) StringToBytes(text string) []byte {
	var out []byte
	e.writeToBytes(text, &out)
	return out
}

func (e *FontEncoding) writeToBytes(text string, out *[]byte) {
	switch e.Variant {
	case encOneByteEncoding:
		stringToBytesOneByte(e.Table, text, out)
	case encSimpleEncoding:
		switch string(e.Name) {
		case "UniGB-UCS2-H", "UniGB-UTF16-H":
			*out = append(*out, 0xFE, 0xFF)
			for _, cu := range utf16.Encode([]rune(text)) {
				*out = append(*out, byte(cu>>8), byte(cu))
			}
		case "WinAnsiEncoding":
			stringToBytesOneByte(&win_ansi_encoding, text, out)
		default:
			*out = append(*out, text...)
		}
	case encUnicodeMapEncoding:
		runes := []rune(text)
		for i := range runes {
			seq := utf16.Encode(runes[i : i+1])
			entries := e.CMap.GetSourceCodesForUnicode(seq)
			if len(entries) > 0 {
				entry := entries[0]
				val := entry.SourceCode
				switch entry.CodeLen {
				case 1:
					*out = append(*out, byte(val))
				case 2:
					*out = append(*out, byte(val>>8), byte(val))
				case 3:
					*out = append(*out, byte(val>>16), byte(val>>8), byte(val))
				case 4:
					*out = append(*out, byte(val>>24), byte(val>>16), byte(val>>8), byte(val))
				}
			}
		}
	case encDifferences:
		e.Diff.stringToBytes(text, out)
	}
}

// utf16BEDecode decodes UTF-16BE bytes lossily (encoding_rs UTF_16BE.decode
// parity: invalid units become U+FFFD).
func utf16BEDecode(b []byte) string {
	units := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		units = append(units, uint16(b[i])<<8|uint16(b[i+1]))
	}
	return utf16BEDecodeUnits(units)
}

func utf16BEDecodeUnits(units []uint16) string {
	return string(utf16.Decode(units))
}

// DecodeText mirrors Document::decode_text.
func DecodeText(encoding *FontEncoding, bytes []byte) (string, error) {
	return encoding.BytesToString(bytes)
}

// GetFontEncoding mirrors Dictionary::get_font_encoding. All parse failures
// inside the closure fall back to the standard encoding (warn + fallback
// upstream); only a non-Font dictionary errors.
func (d *Dictionary) GetFontEncoding(doc *Document) (*FontEncoding, error) {
	if !d.HasType([]byte("Font")) {
		found := "None"
		if tn, err := d.GetType(); err == nil {
			found = string(tn)
		}
		return nil, &Error{Kind: KindDictType, Expected: "Font", DictFound: found}
	}

	enc, err := d.getFontEncodingInner(doc)
	if err != nil {
		return &FontEncoding{
			Variant: encOneByteEncoding,
			Table:   &standard_encoding,
		}, nil
	}
	return enc, nil
}

func (d *Dictionary) getFontEncodingInner(doc *Document) (*FontEncoding, error) {
	if o, err := d.Get([]byte("Encoding")); err == nil {
		return d.getBaseEncoding(o, doc)
	}
	if o, err := d.GetDeref([]byte("ToUnicode"), doc); err == nil {
		if stream, err2 := o.AsStream(); err2 == nil {
			return d.getToUnicodeEncoding(stream)
		}
	}
	return &FontEncoding{Variant: encOneByteEncoding, Table: &standard_encoding}, nil
}

func (d *Dictionary) getToUnicodeEncoding(stream *Stream) (*FontEncoding, error) {
	content, err := stream.GetPlainContent()
	if err != nil {
		return nil, err
	}
	cmap, err := parseToUnicodeCMap(content)
	if err != nil {
		return nil, err
	}
	return &FontEncoding{Variant: encUnicodeMapEncoding, CMap: cmap}, nil
}

func (d *Dictionary) getBaseEncoding(object *Object, doc *Document) (*FontEncoding, error) {
	visited := map[ObjectId]bool{}
	for {
		switch object.Kind {
		case KindName:
			return d.baseEncoding(object.Name, doc)
		case KindReference:
			id := object.Ref
			if visited[id] {
				return nil, &Error{Kind: KindReferenceCycle, ID: id}
			}
			visited[id] = true
			o, err := doc.GetObject(id)
			if err != nil {
				return nil, &Error{Kind: KindObjectNotFound, ID: id}
			}
			object = o
		case KindDictionary:
			dict := object.Dict
			tyObj, err := dict.Get([]byte("Type"))
			if err != nil {
				return nil, err
			}
			ty, err := tyObj.AsName()
			if err != nil {
				return nil, err
			}
			if string(ty) != "Encoding" {
				return nil, errObjectType("Encoding Dictionary", "Dictionary with Type other than /Encoding")
			}
			var base *FontEncoding
			if be, err := dict.Get([]byte("BaseEncoding")); err == nil {
				if name, err2 := be.AsName(); err2 == nil {
					base, err = d.baseEncoding(name, doc)
					if err != nil {
						return nil, err
					}
				}
			}
			if base == nil {
				base = &FontEncoding{Variant: encOneByteEncoding, Table: &standard_encoding}
			}
			diffObj, err := dict.Get([]byte("Differences"))
			if err != nil {
				return nil, err
			}
			arr, err := diffObj.AsArray()
			if err != nil {
				return nil, err
			}
			return buildDifferences(base, arr)
		default:
			return nil, errObjectType("Name or Reference or Dictionary", object.EnumVariant())
		}
	}
}

func (d *Dictionary) baseEncoding(name []byte, doc *Document) (*FontEncoding, error) {
	switch string(name) {
	case "StandardEncoding":
		return &FontEncoding{Variant: encOneByteEncoding, Table: &standard_encoding}, nil
	case "MacRomanEncoding":
		return &FontEncoding{Variant: encOneByteEncoding, Table: &mac_roman_encoding}, nil
	case "MacExpertEncoding":
		return &FontEncoding{Variant: encOneByteEncoding, Table: &mac_expert_encoding}, nil
	case "WinAnsiEncoding":
		return &FontEncoding{Variant: encOneByteEncoding, Table: &win_ansi_encoding}, nil
	case "PDFDocEncoding":
		return &FontEncoding{Variant: encOneByteEncoding, Table: &pdf_doc_encoding}, nil
	case "Identity-H", "Identity-V":
		o, err := d.GetDeref([]byte("ToUnicode"), doc)
		if err != nil {
			return nil, err
		}
		stream, err := o.AsStream()
		if err != nil {
			return nil, err
		}
		return d.getToUnicodeEncoding(stream)
	}
	return &FontEncoding{Variant: encSimpleEncoding, Name: append([]byte(nil), name...)}, nil
}

// buildDifferences mirrors Dictionary::differences.
func buildDifferences(base *FontEncoding, array []Object) (*FontEncoding, error) {
	diff := &Differences{
		Base:    base,
		Map:     map[byte]uint16{},
		Inverse: map[uint16]byte{},
	}
	currentCode := int64(0)
	for i := range array {
		obj := &array[i]
		switch obj.Kind {
		case KindInteger:
			if obj.Int < 0 || obj.Int > 255 {
				return nil, &Error{Kind: KindInvalidEncodingDifferenceCode, Code: obj.Int}
			}
			currentCode = obj.Int
		case KindName:
			g, ok := glyphByName[string(obj.Name)]
			if !ok {
				return nil, &Error{Kind: KindInvalidEncodingDifferenceGlyph, GlyphName: string(obj.Name)}
			}
			code := byte(currentCode)
			diff.Map[code] = g
			diff.Inverse[g] = code
			currentCode = (currentCode + 1) & 0xFF // wrapping_add parity
		default:
			return nil, errObjectType("Integer or Name", obj.EnumVariant())
		}
	}
	return &FontEncoding{Variant: encDifferences, Diff: diff}, nil
}
