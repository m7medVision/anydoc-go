package objects

import "strconv"

// Recursive-descent port of lopdf src/parser/mod.rs (nom combinator set).
// Every function mirrors its nom counterpart's backtracking behavior:
// matching functions return the remaining input and an ok flag.

const (
	maxBracket    = 100 // reader::MAX_BRACKET
	maxNestingDep = 100 // reader::MAX_NESTING_DEPTH
)

func isWhitespaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == 0 || c == 0x0C
}

func isDelimiterByte(c byte) bool {
	switch c {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

func isRegularByte(c byte) bool { return !isWhitespaceByte(c) && !isDelimiterByte(c) }

func isDirectLiteralStringByte(c byte) bool {
	return c != '(' && c != ')' && c != '\\' && c != '\r' && c != '\n'
}

func hasPrefix(in []byte, p string) bool {
	return len(in) >= len(p) && string(in[:len(p)]) == p
}

// pEol: "\r\n" | "\n" | "\r".
func pEol(in []byte) ([]byte, bool) {
	switch {
	case hasPrefix(in, "\r\n"):
		return in[2:], true
	case len(in) > 0 && (in[0] == '\n' || in[0] == '\r'):
		return in[1:], true
	}
	return in, false
}

// pComment: "%" [^\r\n]* eol (eol required).
func pComment(in []byte) ([]byte, bool) {
	if len(in) == 0 || in[0] != '%' {
		return in, false
	}
	i := 1
	for i < len(in) && in[i] != '\r' && in[i] != '\n' {
		i++
	}
	rest, ok := pEol(in[i:])
	if !ok {
		return in, false
	}
	return rest, true
}

// pSpace: many0(whitespace-run | comment).
func pSpace(in []byte) []byte {
	for {
		i := 0
		for i < len(in) && isWhitespaceByte(in[i]) {
			i++
		}
		if i > 0 {
			in = in[i:]
			continue
		}
		if rest, ok := pComment(in); ok {
			in = rest
			continue
		}
		return in
	}
}

func parseDigits(in []byte) ([]byte, bool) {
	i := 0
	for i < len(in) && in[i] >= '0' && in[i] <= '9' {
		i++
	}
	if i == 0 {
		return in, false
	}
	return in[:i], true
}

// pInteger: [+-]? digit+ parsed as i64 (overflow fails, like i64::from_str).
func pInteger(in []byte) ([]byte, int64, bool) {
	i := 0
	if i < len(in) && (in[i] == '+' || in[i] == '-') {
		i++
	}
	digits, ok := parseDigits(in[i:])
	if !ok {
		return in, 0, false
	}
	text := string(in[:i+len(digits)])
	v, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return in, 0, false
	}
	return in[i+len(digits):], v, true
}

// pReal: [+-]? (digit+ '.' digit* | '.' digit+) parsed as f32.
func pReal(in []byte) ([]byte, float32, bool) {
	i := 0
	if i < len(in) && (in[i] == '+' || in[i] == '-') {
		i++
	}
	j := i
	d1, ok1 := parseDigits(in[j:])
	j += len(d1)
	if ok1 && j < len(in) && in[j] == '.' {
		j++
		d2, _ := parseDigits(in[j:])
		j += len(d2)
	} else if !ok1 && j < len(in) && in[j] == '.' {
		j++
		d2, ok2 := parseDigits(in[j:])
		if !ok2 {
			return in, 0, false
		}
		j += len(d2)
	} else {
		return in, 0, false
	}
	v, err := strconv.ParseFloat(string(in[:j]), 32)
	if err != nil {
		return in, 0, false
	}
	return in[j:], float32(v), true
}

func hexDigitValue(c byte) (uint8, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// pHexChar: exactly two hex digits.
func pHexChar(in []byte) ([]byte, uint8, bool) {
	if len(in) < 2 {
		return in, 0, false
	}
	h1, ok1 := hexDigitValue(in[0])
	h2, ok2 := hexDigitValue(in[1])
	if !ok1 || !ok2 {
		return in, 0, false
	}
	return in[2:], h1<<4 | h2, true
}

// pOctChar: 1-3 octal digits, parsed as u16 then truncated to u8.
func pOctChar(in []byte) ([]byte, uint8, bool) {
	i := 0
	for i < len(in) && i < 3 && in[i] >= '0' && in[i] <= '7' {
		i++
	}
	if i == 0 {
		return in, 0, false
	}
	v, err := strconv.ParseUint(string(in[:i]), 8, 16)
	if err != nil {
		return in, 0, false
	}
	return in[i:], uint8(v), true
}

// pName: '/' ( '#' hex hex | regular-non-'#' )*.
func pName(in []byte) ([]byte, []byte, bool) {
	if len(in) == 0 || in[0] != '/' {
		return in, nil, false
	}
	out := make([]byte, 0, 8)
	i := 1
	for i < len(in) {
		if in[i] == '#' {
			rest, v, ok := pHexChar(in[i+1:])
			if !ok {
				break
			}
			out = append(out, v)
			i = len(in) - len(rest)
			continue
		}
		if !isRegularByte(in[i]) || in[i] == '#' {
			break
		}
		out = append(out, in[i])
		i++
	}
	return in[i:], out, true
}

// pEscapeSequence: '\\' ( oct | eol | n | r | t | b | f | any1 ).
func pEscapeSequence(in []byte) ([]byte, []byte, bool) {
	if len(in) == 0 || in[0] != '\\' {
		return in, nil, false
	}
	if rest, v, ok := pOctChar(in[1:]); ok {
		return rest, []byte{v}, true
	}
	if rest, ok := pEol(in[1:]); ok {
		return rest, nil, true // line continuation: nothing emitted
	}
	rest := in[1:]
	if len(rest) == 0 {
		return in, nil, false
	}
	switch rest[0] {
	case 'n':
		return rest[1:], []byte{'\n'}, true
	case 'r':
		return rest[1:], []byte{'\r'}, true
	case 't':
		return rest[1:], []byte{'\t'}, true
	case 'b':
		return rest[1:], []byte{0x08}, true
	case 'f':
		return rest[1:], []byte{0x0C}, true
	}
	return rest[1:], []byte{rest[0]}, true
}

func pInnerLiteralString(in []byte, depth int) ([]byte, []byte, bool) {
	var out []byte
	for {
		// Direct run.
		i := 0
		for i < len(in) && isDirectLiteralStringByte(in[i]) {
			i++
		}
		if i > 0 {
			out = append(out, in[:i]...)
			in = in[i:]
			continue
		}
		if rest, esc, ok := pEscapeSequence(in); ok {
			out = append(out, esc...)
			in = rest
			continue
		}
		if rest, ok := pEol(in); ok {
			out = append(out, in[:len(in)-len(rest)]...)
			in = rest
			continue
		}
		if rest, nested, ok := pNestedLiteralString(in, depth); ok {
			out = append(out, nested...)
			in = rest
			continue
		}
		return in, out, true
	}
}

func pNestedLiteralString(in []byte, depth int) ([]byte, []byte, bool) {
	if depth == 0 {
		return in, nil, false
	}
	if len(in) == 0 || in[0] != '(' {
		return in, nil, false
	}
	rest, inner, ok := pInnerLiteralString(in[1:], depth-1)
	if !ok || len(rest) == 0 || rest[0] != ')' {
		return in, nil, false
	}
	return rest[1:], append([]byte{'('}, append(inner, ')')...), true
}

func pLiteralString(in []byte) ([]byte, []byte, bool) {
	if len(in) == 0 || in[0] != '(' {
		return in, nil, false
	}
	rest, inner, ok := pInnerLiteralString(in[1:], maxBracket)
	if !ok || len(rest) == 0 || rest[0] != ')' {
		return in, nil, false
	}
	return rest[1:], inner, true
}

// pHexString: '<' (white_space hex_digit)* white_space '>' with nibble pairing.
func pHexString(in []byte) ([]byte, Object, bool) {
	if !hasPrefix(in, "<") {
		return in, Object{}, false
	}
	rest := in[1:]
	var out []byte
	hi := false
	for {
		for len(rest) > 0 && isWhitespaceByte(rest[0]) {
			rest = rest[1:]
		}
		if len(rest) == 0 || rest[0] == '>' {
			break
		}
		d, ok := hexDigitValue(rest[0])
		if !ok {
			break
		}
		rest = rest[1:]
		if !hi {
			out = append(out, d<<4)
			hi = true
		} else {
			out[len(out)-1] |= d
			hi = false
		}
	}
	// trailing white_space then '>'
	for len(rest) > 0 && isWhitespaceByte(rest[0]) {
		rest = rest[1:]
	}
	if len(rest) == 0 || rest[0] != '>' {
		return in, Object{}, false
	}
	return rest[1:], StringHex(out), true
}

func pBoolean(in []byte) ([]byte, Object, bool) {
	if hasPrefix(in, "true") {
		return in[4:], Boolean(true), true
	}
	if hasPrefix(in, "false") {
		return in[5:], Boolean(false), true
	}
	return in, nil, false
}

func pNull(in []byte) ([]byte, Object, bool) {
	if hasPrefix(in, "null") {
		return in[4:], Null(), true
	}
	return in, nil, false
}

func pArray(in []byte, depth int) ([]byte, []Object, bool) {
	if len(in) == 0 || in[0] != '[' {
		return in, nil, false
	}
	rest := pSpace(in[1:])
	var out []Object
	for {
		r2, item, ok := pDirectObject(rest, depth)
		if !ok {
			break
		}
		out = append(out, item)
		rest = r2
	}
	if len(rest) == 0 || rest[0] != ']' {
		return in, nil, false
	}
	return rest[1:], out, true
}

func pDictionaryRaw(in []byte, depth int) ([]byte, *Dictionary, bool) {
	if !hasPrefix(in, "<<") {
		return in, nil, false
	}
	rest := pSpace(in[2:])
	dict := NewDictionary()
	for {
		keyRest, key, ok := pName(rest)
		if !ok {
			break
		}
		keyRest = pSpace(keyRest)
		vr, val, vok := pDirectObject(keyRest, depth)
		if !vok {
			break
		}
		dict.Set(key, val)
		rest = vr
	}
	if !hasPrefix(rest, ">>") {
		return in, nil, false
	}
	return rest[2:], dict, true
}

// pObjectId: uint space uint space (space = the full whitespace/comment skip).
func pObjectId(in []byte) ([]byte, ObjectId, bool) {
	first, ok := parseDigits(in)
	if !ok {
		return in, ObjectId{}, false
	}
	num, err := strconv.ParseUint(string(first), 10, 32)
	if err != nil {
		return in, ObjectId{}, false
	}
	rest := pSpace(in[len(first):])
	second, ok := parseDigits(rest)
	if !ok {
		return in, ObjectId{}, false
	}
	gen, err := strconv.ParseUint(string(second), 10, 16)
	if err != nil {
		return in, ObjectId{}, false
	}
	rest = pSpace(rest[len(second):])
	return rest, ObjectId{Num: uint32(num), Gen: uint16(gen)}, true
}

func pReference(in []byte) ([]byte, Object, bool) {
	rest, id, ok := pObjectId(in)
	if !ok || !hasPrefix(rest, "R") {
		return in, nil, false
	}
	return rest[1:], Reference(id), true
}

// pDirectObjects mirrors _direct_objects: the ordered alt list.
func pDirectObjects(in []byte, depth int) ([]byte, Object, bool) {
	if rest, o, ok := pNull(in); ok {
		return rest, o, true
	}
	if rest, o, ok := pBoolean(in); ok {
		return rest, o, true
	}
	if rest, o, ok := pReference(in); ok {
		return rest, o, true
	}
	if rest, f, ok := pReal(in); ok {
		return rest, Real(f), true
	}
	if rest, v, ok := pInteger(in); ok {
		return rest, Integer(v), true
	}
	if rest, n, ok := pName(in); ok {
		return rest, NameObj(n), true
	}
	if rest, s, ok := pLiteralString(in); ok {
		return rest, StringLiteral(s), true
	}
	if rest, o, ok := pHexString(in); ok {
		return rest, o, true
	}
	if rest, arr, ok := pArray(in, depth); ok {
		return rest, ArrayObj(arr), true
	}
	if rest, dict, ok := pDictionaryRaw(in, depth); ok {
		return rest, DictionaryObj(dict), true
	}
	return in, Object{}, false
}

// pDirectObject mirrors _direct_object: depth guard + trailing space.
func pDirectObject(in []byte, depth int) ([]byte, Object, bool) {
	if depth == 0 {
		return in, nil, false
	}
	rest, o, ok := pDirectObjects(in, depth-1)
	if !ok {
		return in, Object{}, false
	}
	return pSpace(rest), o, true
}

// directObject is the pub entry (depth = MAX_NESTING_DEPTH), used by the
// object-stream parser.
func directObject(in []byte) (Object, bool) {
	_, o, ok := pDirectObject(in, maxNestingDep)
	if !ok {
		return Object{}, false
	}
	return o, true
}

// pStreamObject mirrors parser::stream.
func pStreamObject(in []byte, reader *reader, alreadySeen map[ObjectId]bool) ([]byte, Object, bool) {
	rest, dict, ok := pDictionaryRaw(in, maxNestingDep)
	if !ok {
		return in, Object{}, false
	}
	// space, "stream", space0 (literal spaces), eol
	afterDict := pSpace(rest)
	if !hasPrefix(afterDict, "stream") {
		return in, Object{}, false
	}
	p := afterDict[len("stream"):]
	for len(p) > 0 && p[0] == ' ' {
		p = p[1:]
	}
	p, ok = pEol(p)
	if !ok {
		return in, Object{}, false
	}
	// Resolve Length (direct integer or indirect reference).
	var length int64
	lengthOK := false
	if lo, err := dict.Get([]byte("Length")); err == nil {
		if ref, rerr := lo.AsReference(); rerr == nil {
			if obj, gerr := reader.getObject(ref, alreadySeen); gerr == nil {
				if v, verr := obj.AsI64(); verr == nil {
					length = v
					lengthOK = true
				}
			}
		} else if v, verr := lo.AsI64(); verr == nil {
			length = v
			lengthOK = true
		}
	}
	if !lengthOK {
		// Stream with deferred content: position relative to stream start.
		pos := len(in) - len(p)
		return p, StreamObj(StreamWithPosition(dict, pos)), true
	}
	if length < 0 {
		return in, Object{}, false
	}
	if int(length) > len(p) {
		return in, Object{}, false
	}
	data := p[:length]
	q := p[length:]
	if r2, ok2 := pEol(q); ok2 {
		q = r2
	}
	if !hasPrefix(q, "endstream") {
		return in, Object{}, false
	}
	q = q[len("endstream"):]
	return q, StreamObj(NewStream(dict, append([]byte(nil), data...))), true
}

// pObject mirrors parser::object: stream or direct object, trailing space.
func pObject(in []byte, reader *reader, alreadySeen map[ObjectId]bool) ([]byte, Object, bool) {
	if rest, o, ok := pStreamObject(in, reader, alreadySeen); ok {
		return pSpace(rest), o, true
	}
	rest, o, ok := pDirectObjects(in, maxNestingDep)
	if !ok {
		return in, Object{}, false
	}
	return pSpace(rest), o, true
}

// parseIndirectObject mirrors parser::indirect_object + _indirect_object.
func parseIndirectObject(
	in []byte, offset int, expectedID *ObjectId, reader *reader, alreadySeen map[ObjectId]bool,
) (ObjectId, Object, error) {
	id, obj, err := parseIndirectObjectRaw(in, offset, expectedID, reader, alreadySeen)
	if err != nil {
		return id, nil, err
	}
	offsetStream(&obj, offset)
	return id, obj, nil
}

func offsetStream(obj *Object, offset int) {
	if obj.Kind == KindStream && obj.Stream.StartPosition >= 0 {
		obj.Stream.StartPosition += offset
	}
}

func parseIndirectObjectRaw(
	in []byte, offset int, expectedID *ObjectId, reader *reader, alreadySeen map[ObjectId]bool,
) (ObjectId, Object, error) {
	rest := pSpace(in)
	idRest, id, ok := pObjectId(rest)
	if !ok {
		return ObjectId{}, nil, &Error{Kind: KindIndirectObject, Offset: offset}
	}
	if !hasPrefix(idRest, "obj") {
		return ObjectId{}, nil, &Error{Kind: KindIndirectObject, Offset: offset}
	}
	afterObj := pSpace(idRest[len("obj"):])
	if expectedID != nil && id != *expectedID {
		return ObjectId{}, nil, ErrObjectIdMismatch
	}
	objRest, obj, ok := pObject(afterObj, reader, alreadySeen)
	if !ok {
		return ObjectId{}, nil, &Error{Kind: KindIndirectObject, Offset: offset}
	}
	// (space, opt("endobj"), space) — both many0-spaces, never fail.
	if hasPrefix(objRest, "endobj") {
		objRest = pSpace(objRest[len("endobj"):])
	}
	offsetStream(&obj, len(in)-len(afterObj))
	return id, obj, nil
}

// parseHeader mirrors parser::header.
func parseHeader(in []byte, strict bool) (string, bool) {
	if !hasPrefix(in, "%PDF-") {
		return "", false
	}
	i := len("%PDF-")
	vStart := i
	for i < len(in) && (in[i] >= '0' && in[i] <= '9' || in[i] == '.') {
		i++
	}
	version := string(in[vStart:i])
	trailStart := i
	for i < len(in) && in[i] != '\r' && in[i] != '\n' {
		i++
	}
	trailing := in[trailStart:i]
	rest, ok := pEol(in[i:])
	if !ok {
		return "", false
	}
	for {
		r2, ok2 := pComment(rest)
		if !ok2 {
			break
		}
		rest = r2
	}
	if strict && len(trailing) > 0 {
		return "", false
	}
	return version, true
}

// parseBinaryMark mirrors parser::binary_mark.
func parseBinaryMark(in []byte) ([]byte, bool) {
	if len(in) == 0 || in[0] != '%' {
		return nil, false
	}
	i := 1
	for i < len(in) && in[i] != '\r' && in[i] != '\n' {
		i++
	}
	mark := in[1:i]
	rest, ok := pEol(in[i:])
	if !ok {
		return nil, false
	}
	for {
		r2, ok2 := pComment(rest)
		if !ok2 {
			break
		}
		rest = r2
	}
	return mark, true
}

// parseXref mirrors parser::xref (classic cross-reference table).
func parseXref(in []byte) ([]byte, *Xref, bool) {
	if !hasPrefix(in, "xref") {
		return in, nil, false
	}
	rest := in[len("xref"):]
	if hasPrefix(rest, " ") {
		rest = rest[1:]
	}
	if r2, ok := pEol(rest); ok {
		rest = r2
	} else {
		return in, nil, false
	}
	xref := NewXref(0, XrefTypeCrossReferenceTable)
	sections := 0
sections:
	for {
		// Section header: start count (opt ' ' eol).
		startDigits, ok := parseDigits(rest)
		if !ok {
			break
		}
		start, err := strconv.ParseUint(string(startDigits), 10, 64)
		if err != nil {
			break
		}
		afterStart := rest[len(startDigits):]
		if !hasPrefix(afterStart, " ") {
			break
		}
		countDigits, ok2 := parseDigits(afterStart[1:])
		if !ok2 {
			break
		}
		count, err := strconv.ParseUint(string(countDigits), 10, 32)
		if err != nil {
			break
		}
		afterCount := afterStart[1+len(countDigits):]
		if hasPrefix(afterCount, " ") {
			afterCount = afterCount[1:]
		}
		if r3, ok3 := pEol(afterCount); ok3 {
			afterCount = r3
		} else {
			break
		}
		rest = afterCount
		sections++
		for idx := uint64(0); idx < count; idx++ {
			entryRest, entry, ok4 := parseXrefEntry(rest)
			if !ok4 {
				// many0 semantics: the section ends at the first malformed
				// entry; the fold still tries the next section header.
				continue sections
			}
			rest = entryRest
			if entry.isNormal && entry.generation <= 65535 {
				xref.Insert(uint32(start+idx), XrefEntry{
					Type:       XrefEntryNormal,
					Offset:     entry.offset,
					Generation: uint16(entry.generation),
				})
			}
		}
	}
	if sections == 0 {
		return in, nil, false
	}
	rest = pSpace(rest)
	return rest, xref, true
}

type xrefEntryRaw struct {
	offset     uint32
	generation uint32
	isNormal   bool
}

func parseXrefEntry(in []byte) ([]byte, xrefEntryRaw, bool) {
	// offset ' ' generation ' ' ('n'|'f') xref_eol
	d1, ok := parseDigits(in)
	if !ok {
		return in, xrefEntryRaw{}, false
	}
	offset, err := strconv.ParseUint(string(d1), 10, 32)
	if err != nil {
		return in, xrefEntryRaw{}, false
	}
	rest := in[len(d1):]
	if !hasPrefix(rest, " ") {
		return in, xrefEntryRaw{}, false
	}
	d2, ok := parseDigits(rest[1:])
	if !ok {
		return in, xrefEntryRaw{}, false
	}
	generation, err := strconv.ParseUint(string(d2), 10, 32)
	if err != nil {
		return in, xrefEntryRaw{}, false
	}
	rest = rest[1+len(d2):]
	if !hasPrefix(rest, " ") {
		return in, xrefEntryRaw{}, false
	}
	rest = rest[1:]
	if len(rest) == 0 || (rest[0] != 'n' && rest[0] != 'f') {
		return in, xrefEntryRaw{}, false
	}
	isNormal := rest[0] == 'n'
	rest = rest[1:]
	switch {
	case hasPrefix(rest, " \r"):
		rest = rest[2:]
	case hasPrefix(rest, " \n"):
		rest = rest[2:]
	case hasPrefix(rest, "\r\n"):
		rest = rest[2:]
	default:
		return in, xrefEntryRaw{}, false
	}
	return rest, xrefEntryRaw{offset: uint32(offset), generation: uint32(generation), isNormal: isNormal}, true
}

func parseTrailerDict(in []byte) ([]byte, *Dictionary, bool) {
	if !hasPrefix(in, "trailer") {
		return in, nil, false
	}
	rest := pSpace(in[len("trailer"):])
	rest, dict, ok := pDictionaryRaw(rest, maxNestingDep)
	if !ok {
		return in, nil, false
	}
	return pSpace(rest), dict, true
}

// parseXrefAndTrailer mirrors parser::xref_and_trailer.
func parseXrefAndTrailer(in []byte, reader *reader) (*Xref, *Dictionary, error) {
	if rest, xref, ok := parseXref(in); ok {
		if rest2, trailer, ok2 := parseTrailerDict(rest); ok2 {
			rest2 = pSpace(rest2)
			_ = rest2
			sizeObj, err := trailer.Get([]byte("Size"))
			if err != nil {
				return nil, nil, errParse(ParseInvalidTrailer)
			}
			size, err2 := sizeObj.AsI64()
			if err2 != nil {
				return nil, nil, errParse(ParseInvalidTrailer)
			}
			xref.Size = uint32(size)
			return xref, trailer, nil
		}
	}
	// Fall back: indirect object holding a cross-reference stream.
	_, obj, err := parseIndirectObjectRaw(in, 0, nil, reader, map[ObjectId]bool{})
	if err != nil {
		return nil, nil, errParse(ParseInvalidTrailer)
	}
	if obj.Kind != KindStream {
		return nil, nil, errParse(ParseInvalidXref)
	}
	xref, dict, err := decodeXrefStream(obj.Stream)
	if err != nil {
		return nil, nil, err
	}
	return xref, dict, nil
}

// parseXrefStart mirrors parser::xref_start.
func parseXrefStart(in []byte) (int64, bool) {
	if !hasPrefix(in, "startxref") {
		return 0, false
	}
	rest := in[len("startxref"):]
	if hasPrefix(rest, " ") {
		rest = rest[1:]
	}
	if r2, ok := pEol(rest); ok {
		rest = r2
	} else {
		return 0, false
	}
	for hasPrefix(rest, " ") {
		rest = rest[1:]
	}
	rest, v, ok := pInteger(rest)
	if !ok {
		return 0, false
	}
	for hasPrefix(rest, " ") {
		rest = rest[1:]
	}
	if r2, ok2 := pEol(rest); ok2 {
		rest = r2
	} else {
		return 0, false
	}
	if !hasPrefix(rest, "%%EOF") {
		return 0, false
	}
	rest = rest[len("%%EOF"):]
	pSpace(rest)
	return v, true
}

// ── Content stream parsing ───────────────────────────────────────────

func pContentSpace(in []byte) []byte {
	i := 0
	for i < len(in) && (in[i] == ' ' || in[i] == '\t' || in[i] == '\r' || in[i] == '\n') {
		i++
	}
	return in[i:]
}

func pOperator(in []byte) ([]byte, string, bool) {
	i := 0
	for i < len(in) {
		c := in[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '*' || c == '\'' || c == '"' {
			i++
			continue
		}
		break
	}
	if i == 0 {
		return in, "", false
	}
	return in[i:], string(in[:i]), true
}

func pOperand(in []byte) ([]byte, Object, bool) {
	if rest, o, ok := pNull(in); ok {
		return pContentSpace(rest), o, true
	}
	if rest, o, ok := pBoolean(in); ok {
		return pContentSpace(rest), o, true
	}
	if rest, f, ok := pReal(in); ok {
		return pContentSpace(rest), Real(f), true
	}
	if rest, v, ok := pInteger(in); ok {
		return pContentSpace(rest), Integer(v), true
	}
	if rest, n, ok := pName(in); ok {
		return pContentSpace(rest), NameObj(n), true
	}
	if rest, s, ok := pLiteralString(in); ok {
		return pContentSpace(rest), StringLiteral(s), true
	}
	if rest, o, ok := pHexString(in); ok {
		return pContentSpace(rest), o, true
	}
	if rest, arr, ok := pArray(in, maxNestingDep); ok {
		return pContentSpace(rest), ArrayObj(arr), true
	}
	if rest, dict, ok := pDictionaryRaw(in, maxNestingDep); ok {
		return pContentSpace(rest), DictionaryObj(dict), true
	}
	return in, Object{}, false
}

func pOperation(in []byte) ([]byte, Operation, bool) {
	// many0(comment)
	for {
		rest, ok := pComment(in)
		if !ok {
			in = rest
			break
		}
		in = rest
	}
	// alt(inline_image, operands+operator)
	if rest, op, ok := pInlineImage(in); ok {
		return rest, op, true
	}
	var operands []Object
	rest := in
	for {
		r2, o, ok := pOperand(rest)
		if !ok {
			break
		}
		operands = append(operands, o)
		rest = r2
	}
	r3, operator, ok := pOperator(rest)
	if !ok {
		return in, Operation{}, false
	}
	return pContentSpace(r3), Operation{Operator: operator, Operands: operands}, true
}

// pInlineImage mirrors parser::inline_image (BI … ID … EI).
func pInlineImage(in []byte) ([]byte, Operation, bool) {
	if !hasPrefix(in, "BI") {
		return in, Operation{}, false
	}
	rest := pContentSpace(in[len("BI"):])
	// cut: failures past BI abort the operation.
	out, operands, ok := pInlineImageImpl(rest)
	if !ok {
		return in, Operation{}, false
	}
	return out, Operation{Operator: "BI", Operands: operands}, true
}

func pInlineImageImpl(in []byte) ([]byte, []Object, bool) {
	rest, dict, ok := pDictionaryRaw(in, maxNestingDep)
	if !ok {
		return in, nil, false
	}
	if !hasPrefix(rest, "ID") {
		return in, nil, false
	}
	rest = pContentSpace(rest[len("ID"):])
	dataRest, stream, err := parseInlineDataStream(rest, dict)
	if err == nil {
		after := pContentSpace(dataRest)
		if !hasPrefix(after, "EI") {
			return in, nil, false
		}
		return pContentSpace(after[len("EI"):]), []Object{StreamObj(stream)}, true
	}
	// Skip to EI marker so the rest of the content stream can still be parsed.
	eiPos := -1
	for i := 0; i+4 <= len(rest); i++ {
		w := rest[i : i+4]
		if (w[0] == ' ' || w[0] == '\n' || w[0] == '\r') &&
			w[1] == 'E' && w[2] == 'I' &&
			(w[3] == ' ' || w[3] == '\n' || w[3] == '\r') {
			eiPos = i
			break
		}
	}
	if eiPos < 0 {
		return in, nil, false
	}
	return pContentSpace(rest[eiPos+3:]), []Object{}, true
}

func parseInlineDataStream(in []byte, dict *Dictionary) ([]byte, *Stream, error) {
	getAbbr := func(abbr, key string) (*Object, error) {
		if o, err := dict.Get([]byte(abbr)); err == nil {
			return o, nil
		}
		return dict.Get([]byte(key))
	}
	widthObj, err := getAbbr("W", "Width")
	if err != nil {
		return nil, nil, err
	}
	width, err := widthObj.AsI64()
	if err != nil {
		return nil, nil, err
	}
	heightObj, err := getAbbr("H", "Height")
	if err != nil {
		return nil, nil, err
	}
	height, err := heightObj.AsI64()
	if err != nil {
		return nil, nil, err
	}
	bpcObj, err := getAbbr("BPC", "BitsPerComponent")
	if err != nil {
		return nil, nil, err
	}
	bpc, err := bpcObj.AsI64()
	if err != nil {
		return nil, nil, err
	}
	var numColors int64
	imTrue := false
	if im, err := getAbbr("IM", "ImageMask"); err == nil {
		if v, err2 := im.AsBool(); err2 == nil && v {
			imTrue = true
		}
	}
	if imTrue {
		numColors = 1
	} else {
		csObj, err := getAbbr("CS", "ColorSpace")
		if err != nil {
			// Upstream panics here (unwrap on the missing key); recover with
			// the typed error instead so the EI-skip path still applies.
			return nil, nil, err
		}
		cs, err2 := inlineImageColorCount(csObj)
		if err2 != nil {
			return nil, nil, err2
		}
		numColors = cs
	}
	stride := (width*(numColors*bpc) + 7) / 8
	length := height * stride
	filterObj, ferr := getAbbr("F", "Filter")
	if ferr != nil {
		if int(length) > len(in) {
			return nil, nil, errParse(ParseEndOfInput)
		}
		return in[length:], NewStream(dict, append([]byte(nil), in[:length]...)), nil
	}
	switch filterObj.Kind {
	case KindName, KindArray:
		return nil, nil, &Error{Kind: KindUnimplemented, UnimplementedMsg: "filters for inline images"}
	default:
		return nil, nil, errObjectType("Name or Array", filterObj.EnumVariant())
	}
}

func inlineImageColorCount(o *Object) (int64, error) {
	name, err := o.AsName()
	if err != nil {
		return 0, err
	}
	switch string(name) {
	case "DeviceGray", "Gray":
		return 1, nil
	case "DeviceRGB", "RGB":
		return 3, nil
	case "DeviceRGBA", "RGBA":
		return 4, nil
	case "DeviceCMYK", "CMYK":
		return 4, nil
	case "Pattern":
		return 0, &Error{Kind: KindInvalidInlineImage, Detail: "Pattern colorspace is not allowed in inline images"}
	}
	return 0, &Error{Kind: KindUnimplemented, UnimplementedMsg: "inline image colorspaces"}
}

// parseContent mirrors parser::content (lenient: trailing garbage ignored).
func parseContent(in []byte) *Content {
	in = pContentSpace(in)
	var operations []Operation
	for {
		rest, op, ok := pOperation(in)
		if !ok {
			break
		}
		operations = append(operations, op)
		in = rest
	}
	for {
		rest, ok := pComment(in)
		if !ok {
			break
		}
		in = pContentSpace(rest)
	}
	return &Content{Operations: operations}
}

// parseContentStrict mirrors parser::content_strict.
func parseContentStrict(in []byte) (*Content, error) {
	rest := pContentSpace(in)
	var operations []Operation
	for {
		r2, op, ok := pOperation(rest)
		if !ok {
			break
		}
		operations = append(operations, op)
		rest = r2
	}
	for {
		r2, ok := pComment(rest)
		if !ok {
			break
		}
		rest = pContentSpace(r2)
	}
	if len(rest) != 0 {
		return nil, ParseInvalidContentStream
	}
	return &Content{Operations: operations}, nil
}
