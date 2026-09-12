package objects

import (
	"bytes"
	"sort"
	"strconv"
)

// Reader port of lopdf src/reader.rs (memory-buffer loading only, the surface
// pdf-inspector uses).

// LoadOptions mirrors lopdf::LoadOptions. The zero value is the default
// (lenient, no password).
type LoadOptions struct {
	// Password for encrypted PDFs. Meaningful only when HasPassword is true
	// (lopdf's Option<String>: Some("") is not the same as None).
	Password string
	// HasPassword is true when a password was supplied, including empty.
	HasPassword bool
	// Strict rejects non-conforming PDFs instead of accepting them.
	Strict bool
}

// WithPassword mirrors LoadOptions::with_password.
func WithPassword(password string) LoadOptions {
	return LoadOptions{Password: password, HasPassword: true}
}

// LoadMem mirrors Document::load_mem.
func LoadMem(buffer []byte) (*Document, error) {
	return LoadMemWithOptions(buffer, LoadOptions{})
}

// LoadMemWithOptions mirrors Document::load_mem_with_options.
func LoadMemWithOptions(buffer []byte, options LoadOptions) (*Document, error) {
	r := &reader{
		buffer:      buffer,
		document:    newDocument(),
		password:    options.Password,
		hasPassword: options.HasPassword,
		strict:      options.Strict,
	}
	return r.read()
}

type reader struct {
	buffer          []byte
	document        *Document
	encryptionState *encryptionState
	rawObjects      map[ObjectId][]byte
	rawOrder        []ObjectId
	password        string
	hasPassword     bool
	strict          bool
}

func (r *reader) putRaw(id ObjectId, raw []byte) {
	if _, ok := r.rawObjects[id]; !ok {
		r.rawOrder = append(r.rawOrder, id)
	}
	r.rawObjects[id] = raw
}

// read mirrors Reader::read.
func (r *reader) read() (*Document, error) {
	offset := bytes.Index(r.buffer, []byte("%PDF-"))
	if offset < 0 {
		offset = 0
	}
	r.buffer = r.buffer[offset:]

	version, ok := parseHeader(r.buffer, r.strict)
	if !ok {
		return nil, errParse(ParseInvalidFileHeader)
	}

	// Binary mark must be on line 2 to be honored.
	if nl := bytes.IndexByte(r.buffer, '\n'); nl >= 0 {
		if mark, ok := parseBinaryMark(r.buffer[nl+1:]); ok {
			allHigh := true
			for _, b := range mark {
				if b < 128 {
					allHigh = false
					break
				}
			}
			_ = allHigh // lopdf stores the mark; nothing downstream reads it
		}
	}

	xrefStart, err := r.getXrefStart()
	if err != nil {
		return nil, err
	}
	if xrefStart > len(r.buffer) {
		return nil, &Error{Kind: KindXref, Inner: XrefErrStart}
	}
	r.document.xrefStart = xrefStart

	xref, trailer, err := parseXrefAndTrailer(r.buffer[xrefStart:], r)
	if err != nil {
		return nil, err
	}

	// Read previous xrefs of linearized or incrementally updated documents.
	alreadySeen := map[int64]bool{}
	prevXrefStart, _ := trailer.Remove([]byte("Prev"))
	for {
		prev, hasPrev := int64(0), false
		if prevXrefStart != nil {
			if v, err := prevXrefStart.AsI64(); err == nil {
				prev, hasPrev = v, true
			}
		}
		if !hasPrev {
			break
		}
		if alreadySeen[prev] {
			break
		}
		alreadySeen[prev] = true
		if prev < 0 || int(prev) > len(r.buffer) {
			return nil, &Error{Kind: KindXref, Inner: XrefErrPrevStart}
		}

		prevXref, prevTrailer, err := parseXrefAndTrailer(r.buffer[prev:], r)
		if err != nil {
			return nil, err
		}
		xref.Merge(prevXref)

		// Read xref stream in hybrid-reference file.
		if stm, ok := trailer.Remove([]byte("XRefStm")); ok && stm != nil {
			if v, err := stm.AsI64(); err == nil {
				if v < 0 || int(v) > len(r.buffer) {
					return nil, &Error{Kind: KindXref, Inner: XrefErrStreamStart}
				}
				if prevXref2, _, err := parseXrefAndTrailer(r.buffer[v:], r); err == nil {
					xref.Merge(prevXref2)
				} else {
					return nil, err
				}
			}
		}

		if obj, err := prevTrailer.Get([]byte("Prev")); err == nil {
			prevXrefStart = obj
		} else {
			prevXrefStart = nil
		}
	}
	xrefEntryCount := xref.MaxID()
	if xrefEntryCount == 0xFFFFFFFF {
		return nil, errParse(ParseInvalidXref)
	}
	xrefEntryCount++
	if xref.Size != xrefEntryCount {
		xref.Size = xrefEntryCount
	}

	r.document.Version = version
	r.document.maxID = xref.Size - 1
	r.document.Trailer = trailer
	r.document.xref = xref

	if _, err := r.document.Trailer.Get([]byte("Encrypt")); err == nil {
		if err := r.loadEncryptedDocument(); err != nil {
			return nil, err
		}
	} else {
		if err := r.loadObjectsRaw(); err != nil {
			return nil, err
		}
	}

	return r.document, nil
}

// loadObjectsRaw mirrors Reader::load_objects_raw.
func (r *reader) loadObjectsRaw() error {
	isEncrypted := false
	if _, err := r.document.Trailer.Get([]byte("Encrypt")); err == nil {
		isEncrypted = true
	}
	var zeroLengthStreams []ObjectId
	objectStreams := map[ObjectId]Object{}

	// Map of which container each compressed object belongs to per the xref.
	compressedObjContainers := map[uint32]uint32{}
	for _, id := range r.document.xref.SortedIDs() {
		entry, _ := r.document.xref.Get(id)
		if entry.Type == XrefEntryCompressed {
			compressedObjContainers[id] = entry.Container
		}
	}

	for _, objNum := range r.document.xref.SortedIDs() {
		entry, _ := r.document.xref.Get(objNum)
		if entry.Type != XrefEntryNormal {
			continue
		}
		id, object, err := r.readObject(int(entry.Offset), nil, map[ObjectId]bool{})
		if err != nil {
			// Object load errors are logged and skipped upstream (warn for
			// encrypted docs, error otherwise); loading continues.
			_ = isEncrypted
			continue
		}
		if object.Kind == KindStream {
			stream := object.Stream
			if stream.Dict.HasType([]byte("ObjStm")) && !isEncrypted {
				if objStream, err := NewObjectStream(stream); err == nil {
					for _, innerID := range objStream.SortedIDs() {
						if c, ok := compressedObjContainers[innerID.Num]; ok && c != id.Num {
							continue
						}
						if _, exists := objectStreams[innerID]; !exists {
							objectStreams[innerID] = objStream.Objects[innerID]
						}
					}
				}
			} else if len(stream.Content) == 0 {
				zeroLengthStreams = append(zeroLengthStreams, id)
			}
		}
		r.document.setObject(id, object)
	}

	// Only add entries, never replace existing ones.
	for _, id := range sortedObjectIDSlice(objectStreams) {
		if !r.document.HasObject(id) {
			r.document.setObject(id, objectStreams[id])
		}
	}

	for _, objectID := range zeroLengthStreams {
		_ = r.readStreamContent(objectID)
	}
	return nil
}

func sortedObjectIDSlice(m map[ObjectId]Object) []ObjectId {
	ids := make([]ObjectId, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sortIDs(ids)
	return ids
}

func sortIDs(ids []ObjectId) {
	sort.Slice(ids, func(i, j int) bool {
		if ids[i].Num != ids[j].Num {
			return ids[i].Num < ids[j].Num
		}
		return ids[i].Gen < ids[j].Gen
	})
}

// readStreamContent mirrors Reader::read_stream_content.
func (r *reader) readStreamContent(objectID ObjectId) error {
	length, err := r.getStreamLength(objectID)
	if err != nil {
		return err
	}
	obj, ok := r.document.objects[objectID]
	if !ok || obj.Kind != KindStream {
		return &Error{Kind: KindObjectNotFound, ID: objectID}
	}
	stream := obj.Stream
	if stream.StartPosition < 0 {
		return &Error{Kind: KindInvalidStream, Detail: "missing start position"}
	}
	if length < 0 {
		return &Error{Kind: KindInvalidStream, Detail: "negative stream length."}
	}
	start := stream.StartPosition
	end := start + int(length)
	if end > len(r.buffer) {
		return &Error{Kind: KindInvalidStream, Detail: "stream extends after document end."}
	}
	stream.SetContent(append([]byte(nil), r.buffer[start:end]...))
	return nil
}

// getStreamLength mirrors Reader::get_stream_length.
func (r *reader) getStreamLength(objectID ObjectId) (int64, error) {
	obj, ok := r.document.objects[objectID]
	if !ok {
		return 0, &Error{Kind: KindObjectNotFound, ID: objectID}
	}
	stream, err := obj.AsStream()
	if err != nil {
		return 0, err
	}
	lengthObj, err := stream.Dict.Get([]byte("Length"))
	if err != nil {
		return 0, err
	}
	_, resolved, err := r.document.Dereference(lengthObj)
	if err != nil {
		return 0, err
	}
	v, err := resolved.AsI64()
	if err != nil {
		return 0, err
	}
	return v, nil
}

// getOffset mirrors Reader::get_offset.
func (r *reader) getOffset(id ObjectId) (uint32, error) {
	entry, ok := r.document.xref.Get(id.Num)
	if !ok {
		return 0, ErrMissingXrefEntry
	}
	if entry.Type == XrefEntryNormal && entry.Generation == id.Gen {
		return entry.Offset, nil
	}
	return 0, ErrMissingXrefEntry
}

// getCompressedObject mirrors Reader::get_compressed_object.
func (r *reader) getCompressedObject(id ObjectId) (Object, error) {
	entry, ok := r.document.xref.Get(id.Num)
	if !ok {
		return Object{}, ErrMissingXrefEntry
	}
	if entry.Type != XrefEntryCompressed {
		return Object{}, ErrMissingXrefEntry
	}
	containerID := ObjectId{Num: entry.Container, Gen: 0}
	containerObj, err := r.getObject(containerID, map[ObjectId]bool{})
	if err != nil {
		return Object{}, err
	}
	stream, err := containerObj.AsStream()
	if err != nil {
		return Object{}, err
	}
	objStream, err := NewObjectStream(stream)
	if err != nil {
		return Object{}, err
	}
	if obj, ok := objStream.Objects[id]; ok {
		return obj, nil
	}
	return Object{}, ErrMissingXrefEntry
}

// getObject mirrors Reader::get_object: parse (and decrypt) on demand.
func (r *reader) getObject(id ObjectId, alreadySeen map[ObjectId]bool) (Object, error) {
	if alreadySeen[id] {
		return Object{}, &Error{Kind: KindReferenceCycle, ID: id}
	}
	alreadySeen[id] = true

	if entry, ok := r.document.xref.Get(id.Num); ok {
		if entry.Type == XrefEntryCompressed {
			return r.getCompressedObject(id)
		}
	}

	offset, err := r.getOffset(id)
	if err != nil {
		return Object{}, err
	}
	_, obj, err := r.readObject(int(offset), &id, alreadySeen)
	if err != nil {
		return Object{}, err
	}

	if r.encryptionState != nil {
		encryptRef := r.encryptRef()
		if encryptRef == nil || id != *encryptRef {
			if err := decryptObject(r.encryptionState, id, &obj); err != nil {
				return Object{}, err
			}
		}
	}
	return obj, nil
}

func (r *reader) encryptRef() *ObjectId {
	encObj, err := r.document.Trailer.Get([]byte("Encrypt"))
	if err != nil {
		return nil
	}
	id, err := encObj.AsReference()
	if err != nil {
		return nil
	}
	return &id
}

// readObject mirrors Reader::read_object.
func (r *reader) readObject(offset int, expectedID *ObjectId, alreadySeen map[ObjectId]bool) (ObjectId, Object, error) {
	if offset > len(r.buffer) {
		return ObjectId{}, Object{}, &Error{Kind: KindInvalidOffset, Offset: offset}
	}
	return parseIndirectObject(r.buffer, offset, expectedID, r, alreadySeen)
}

// loadEncryptedDocument mirrors Reader::load_encrypted_document.
func (r *reader) loadEncryptedDocument() error {
	// First, extract all raw object bytes without parsing.
	type compressedRef struct {
		objNum    uint32
		container uint32
		index     uint16
	}
	var objectStreams []compressedRef
	r.rawObjects = map[ObjectId][]byte{}

	for _, objNum := range r.document.xref.SortedIDs() {
		entry, _ := r.document.xref.Get(objNum)
		switch entry.Type {
		case XrefEntryNormal:
			if id, raw, err := r.extractRawObject(int(entry.Offset)); err == nil {
				r.putRaw(id, raw)
			}
		case XrefEntryCompressed:
			objectStreams = append(objectStreams, compressedRef{objNum: objNum, container: entry.Container, index: entry.Index})
		}
	}

	if err := r.parseEncryptionDictionary(); err != nil {
		return err
	}

	pw, err := r.authenticateAndSetupEncryption(false)
	if err != nil {
		return err
	}
	if pw == nil {
		return nil
	}

	if r.encryptionState != nil {
		encryptRef := r.encryptRef()

		for _, id := range append([]ObjectId(nil), r.rawOrder...) {
			raw := r.rawObjects[id]
			if encryptRef != nil && id == *encryptRef {
				continue
			}
			if oid, obj, err := r.parseRawObject(raw); err == nil {
				_ = decryptObject(r.encryptionState, id, &obj)
				r.document.setObject(oid, obj)
			}
		}

		streamsToProcess := map[uint32][]compressedRef{}
		for _, cr := range objectStreams {
			streamsToProcess[cr.container] = append(streamsToProcess[cr.container], cr)
		}
		containerNums := make([]uint32, 0, len(streamsToProcess))
		for c := range streamsToProcess {
			containerNums = append(containerNums, c)
		}
		sortU32s(containerNums)
		for _, containerNum := range containerNums {
			containerID := ObjectId{Num: containerNum, Gen: 0}
			obj, ok := r.document.objects[containerID]
			if !ok {
				continue
			}
			stream, err := obj.AsStream()
			if err != nil {
				continue
			}
			objStream, err := NewObjectStream(stream)
			if err != nil {
				continue
			}
			for _, cr := range streamsToProcess[containerNum] {
				objID := ObjectId{Num: cr.objNum, Gen: 0}
				if inner, ok := objStream.Objects[objID]; ok {
					r.document.setObject(objID, inner)
				}
			}
		}

		r.document.encryption = r.encryptionState

		if encryptRef != nil {
			delete(r.document.objects, *encryptRef)
			r.removeFromObjectIDs(*encryptRef)
		}
		r.document.Trailer.Remove([]byte("Encrypt"))
	}
	return nil
}

func (r *reader) removeFromObjectIDs(id ObjectId) {
	for i, oid := range r.document.objectIDs {
		if oid == id {
			r.document.objectIDs = append(r.document.objectIDs[:i], r.document.objectIDs[i+1:]...)
			return
		}
	}
}

func sortU32s(v []uint32) {
	sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
}

// parseEncryptionDictionary mirrors Reader::parse_encryption_dictionary.
func (r *reader) parseEncryptionDictionary() error {
	encObj, err := r.document.Trailer.Get([]byte("Encrypt"))
	if err != nil {
		return nil
	}
	encryptRef, err := encObj.AsReference()
	if err != nil {
		return nil
	}
	if len(r.rawObjects) == 0 {
		offset, err := r.getOffset(encryptRef)
		if err != nil {
			return err
		}
		_, encryptObj, err := r.readObject(int(offset), &encryptRef, map[ObjectId]bool{})
		if err != nil {
			return err
		}
		r.document.setObject(encryptRef, encryptObj)
	} else if raw, ok := r.rawObjects[encryptRef]; ok {
		if _, obj, err := r.parseRawObject(raw); err == nil {
			r.document.setObject(encryptRef, obj)
		}
	}
	return nil
}

// authenticateAndSetupEncryption mirrors Reader::authenticate_and_setup_encryption.
// A nil return with nil error means "encrypted but no usable password" (the
// document is returned structurally empty, /Encrypt still in the trailer).
func (r *reader) authenticateAndSetupEncryption(requirePassword bool) (*string, error) {
	var passwordToUse *string
	if r.document.authenticatePassword("") == nil {
		empty := ""
		passwordToUse = &empty
	} else if r.hasPassword {
		if r.document.authenticatePassword(r.password) == nil {
			pw := r.password
			passwordToUse = &pw
		} else if requirePassword {
			return nil, ErrInvalidPassword
		} else {
			return nil, ErrInvalidPassword
		}
	} else if requirePassword {
		return nil, &Error{Kind: KindUnimplemented, UnimplementedMsg: "PDF is encrypted and requires a password. Use Document::load_metadata_with_password() instead."}
	} else {
		// PDF is encrypted and requires a password (warned upstream).
		return nil, nil
	}

	if passwordToUse != nil {
		state, err := decodeEncryptionState(r.document, []byte(*passwordToUse))
		if err != nil {
			return nil, err
		}
		r.encryptionState = state
	}
	return passwordToUse, nil
}

// authenticatePassword mirrors Document::authenticate_password.
func (d *Document) authenticatePassword(password string) error {
	if !d.IsEncrypted() {
		return ErrNotEncrypted
	}
	algorithm, err := passwordAlgorithmFromDoc(d)
	if err != nil {
		return err
	}
	sanitized, err := algorithm.sanitizePassword(password)
	if err != nil {
		return err
	}
	return algorithm.authenticateOwnerOrUser(d, sanitized)
}

// parseRawObject mirrors Reader::parse_raw_object.
func (r *reader) parseRawObject(raw []byte) (ObjectId, Object, error) {
	return parseIndirectObject(raw, 0, nil, r, map[ObjectId]bool{})
}

// extractRawObject mirrors Reader::extract_raw_object.
func (r *reader) extractRawObject(offset int) (ObjectId, []byte, error) {
	if offset > len(r.buffer) {
		return ObjectId{}, nil, &Error{Kind: KindInvalidOffset, Offset: offset}
	}
	slice := r.buffer[offset:]

	pos := 0
	for pos < len(slice) && isASCIISpace(slice[pos]) {
		pos++
	}
	numStart := pos
	for pos < len(slice) && slice[pos] >= '0' && slice[pos] <= '9' {
		pos++
	}
	objNum, err := parseU32(slice[numStart:pos])
	if err != nil {
		return ObjectId{}, nil, errParse(ParseInvalidXref)
	}
	for pos < len(slice) && isASCIISpace(slice[pos]) {
		pos++
	}
	genStart := pos
	for pos < len(slice) && slice[pos] >= '0' && slice[pos] <= '9' {
		pos++
	}
	objGen, err := parseU16(slice[genStart:pos])
	if err != nil {
		return ObjectId{}, nil, errParse(ParseInvalidXref)
	}
	for pos < len(slice) && isASCIISpace(slice[pos]) {
		pos++
	}
	if pos+3 > len(slice) || string(slice[pos:pos+3]) != "obj" {
		return ObjectId{}, nil, errParse(ParseInvalidXref)
	}
	pos += 3

	endPos := pos
	for endPos+6 <= len(slice) {
		if string(slice[endPos:endPos+6]) == "endobj" {
			endPos += 6
			break
		}
		endPos++
	}
	if endPos > len(slice) {
		return ObjectId{}, nil, errParse(ParseInvalidXref)
	}
	return ObjectId{Num: objNum, Gen: objGen}, append([]byte(nil), slice[:endPos]...), nil
}

func parseUintStr(b []byte) (uint64, error) {
	if len(b) == 0 {
		return 0, errParse(ParseInvalidXref)
	}
	v, err := strconv.ParseUint(string(b), 10, 64)
	if err != nil {
		return 0, err
	}
	return v, nil
}

func parseU32(b []byte) (uint32, error) {
	v, err := parseUintStr(b)
	if err != nil {
		return 0, err
	}
	return uint32(v), nil
}

func parseU16(b []byte) (uint16, error) {
	v, err := parseUintStr(b)
	if err != nil {
		return 0, err
	}
	return uint16(v), nil
}

// getXrefStart mirrors Reader::get_xref_start: last %%EOF within the final
// 512 bytes, then startxref within 25 bytes before it.
func (r *reader) getXrefStart() (int, error) {
	seekPos := len(r.buffer) - 512
	if seekPos < 0 {
		seekPos = 0
	}
	eofPos := searchLast(r.buffer, []byte("%%EOF"), seekPos)
	if eofPos < 0 || eofPos <= 25 {
		return 0, &Error{Kind: KindXref, Inner: XrefErrStart}
	}
	xrefPos := searchLast(r.buffer[:eofPos], []byte("startxref"), eofPos-25)
	if xrefPos < 0 {
		return 0, &Error{Kind: KindXref, Inner: XrefErrStart}
	}
	if xrefPos <= len(r.buffer) {
		if start, ok := parseXrefStart(r.buffer[xrefPos:]); ok {
			return int(start), nil
		}
		return 0, &Error{Kind: KindXref, Inner: XrefErrStart}
	}
	return 0, &Error{Kind: KindXref, Inner: XrefErrStart}
}

// searchLast mirrors Reader::search_substring (rposition).
func searchLast(buffer, pattern []byte, startPos int) int {
	if startPos > len(buffer) {
		return -1
	}
	maxStart := len(buffer) - len(pattern)
	for i := maxStart; i >= startPos; i-- {
		if i < 0 {
			break
		}
		if bytes.Equal(buffer[i:i+len(pattern)], pattern) {
			return i
		}
	}
	return -1
}
