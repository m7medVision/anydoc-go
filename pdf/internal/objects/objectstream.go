package objects

import (
	"errors"
	"sort"
	"strconv"
	"strings"
)

// ObjectStream port of lopdf src/object_stream.rs (decode side).
type ObjectStream struct {
	Objects map[ObjectId]Object
	order   []ObjectId // sorted by object number (BTreeMap parity)
}

// NewObjectStream mirrors ObjectStream::new: parses an existing object stream.
func NewObjectStream(stream *Stream) (*ObjectStream, error) {
	_ = stream.Decompress()

	if len(stream.Content) == 0 {
		return &ObjectStream{Objects: map[ObjectId]Object{}}, nil
	}

	firstObj, err := stream.Dict.Get([]byte("First"))
	if err != nil {
		return nil, err
	}
	firstOffset, err := firstObj.AsI64()
	if err != nil {
		return nil, err
	}
	if firstOffset < 0 {
		return nil, &Error{Kind: KindNumericCast, Detail: "out of range integral type conversion attempted"}
	}
	if int(firstOffset) > len(stream.Content) {
		return nil, &Error{Kind: KindInvalidOffset, Offset: int(firstOffset)}
	}
	indexBlock := stream.Content[:firstOffset]

	numbersStr := string(indexBlock)
	fields := strings.Fields(numbersStr)
	numbers := make([]*uint32, 0, len(fields))
	for _, f := range fields {
		v, err := strconv.ParseUint(f, 10, 32)
		if err != nil {
			numbers = append(numbers, nil)
			continue
		}
		u := uint32(v)
		numbers = append(numbers, &u)
	}
	pairs := len(numbers) / 2 * 2 // ensure only pairs

	nObj, err := stream.Dict.Get([]byte("N"))
	if err != nil {
		return nil, err
	}
	n, err := nObj.AsI64()
	if err != nil {
		return nil, err
	}
	// Wrong count is only warned about upstream.
	_ = n

	os := &ObjectStream{Objects: map[ObjectId]Object{}}
	for i := 0; i+1 < pairs; i += 2 {
		idPtr, offPtr := numbers[i], numbers[i+1]
		if idPtr == nil || offPtr == nil {
			continue
		}
		offset := int(firstOffset) + int(*offPtr)
		if offset >= len(stream.Content) {
			continue // out-of-bounds offset in object stream (warned upstream)
		}
		start := offset
		for start < len(stream.Content) && isASCIISpace(stream.Content[start]) {
			start++
		}
		if start >= len(stream.Content) {
			continue // only whitespace after offset (warned upstream)
		}
		obj, ok := directObject(stream.Content[start:])
		if !ok {
			continue
		}
		id := ObjectId{Num: *idPtr, Gen: 0}
		os.put(id, obj)
	}
	sort.Slice(os.order, func(i, j int) bool { return os.order[i].Num < os.order[j].Num })
	return os, nil
}

func (o *ObjectStream) put(id ObjectId, obj Object) {
	if _, ok := o.Objects[id]; !ok {
		o.order = append(o.order, id)
	}
	o.Objects[id] = obj
}

// SortedIDs returns contained object IDs in ascending object-number order.
func (o *ObjectStream) SortedIDs() []ObjectId { return o.order }

func isASCIISpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == 0x0C
}

// decodeXrefStream mirrors parser_aux::decode_xref_stream.
func decodeXrefStream(stream *Stream) (*Xref, *Dictionary, error) {
	if stream.IsCompressed() {
		if err := stream.Decompress(); err != nil {
			return nil, nil, err
		}
	}
	dict := &stream.Dict
	content := stream.Content

	sizeObj, err := dict.Get([]byte("Size"))
	if err != nil {
		return nil, nil, errParse(ParseInvalidXref)
	}
	sizeI, err := sizeObj.AsI64()
	if err != nil {
		return nil, nil, errParse(ParseInvalidXref)
	}
	xref := NewXref(uint32(sizeI), XrefTypeCrossReferenceStream)

	sectionIndices := []int64{0, sizeI}
	if idxObj, err := dict.Get([]byte("Index")); err == nil {
		indices, err2 := parseIntegerArray(idxObj)
		if err2 == nil {
			sectionIndices = indices
		}
	}
	widthObj, err := dict.Get([]byte("W"))
	if err != nil {
		return nil, nil, errParse(ParseInvalidXref)
	}
	widths, err := parseIntegerArray(widthObj)
	if err != nil {
		return nil, nil, errParse(ParseInvalidXref)
	}
	if len(widths) < 3 || widths[0] < 0 || widths[1] < 0 || widths[2] < 0 {
		return nil, nil, errParse(ParseInvalidXref)
	}

	pos := 0
	readBE := func(nbytes int64) (uint32, bool) {
		if nbytes == 0 {
			return 0, true
		}
		if pos+int(nbytes) > len(content) {
			return 0, false
		}
		var value uint32
		for _, b := range content[pos : pos+int(nbytes)] {
			value = value<<8 + uint32(b)
		}
		pos += int(nbytes)
		return value, true
	}

	for i := 0; i+1 < len(sectionIndices); i += 2 {
		start := sectionIndices[i]
		count := sectionIndices[i+1]
		for j := int64(0); j < count; j++ {
			entryType := uint32(1)
			if widths[0] > 0 {
				v, ok := readBE(widths[0])
				if !ok {
					return nil, nil, errIO(errShortRead())
				}
				entryType = v
			}
			switch entryType {
			case 0:
				if _, ok := readBE(widths[1]); !ok {
					return nil, nil, errIO(errShortRead())
				}
				if _, ok := readBE(widths[2]); !ok {
					return nil, nil, errIO(errShortRead())
				}
			case 1:
				offset, ok := readBE(widths[1])
				if !ok {
					return nil, nil, errIO(errShortRead())
				}
				generation := uint32(0)
				if widths[2] > 0 {
					g, ok2 := readBE(widths[2])
					if !ok2 {
						return nil, nil, errIO(errShortRead())
					}
					generation = g
				}
				xref.Insert(uint32(start+j), XrefEntry{
					Type:       XrefEntryNormal,
					Offset:     offset,
					Generation: uint16(generation),
				})
			case 2:
				container, ok := readBE(widths[1])
				if !ok {
					return nil, nil, errIO(errShortRead())
				}
				index, ok2 := readBE(widths[2])
				if !ok2 {
					return nil, nil, errIO(errShortRead())
				}
				xref.Insert(uint32(start+j), XrefEntry{
					Type:      XrefEntryCompressed,
					Container: container,
					Index:     uint16(index),
				})
			}
		}
	}

	dict = dict.Clone()
	dict.Remove([]byte("Length"))
	dict.Remove([]byte("W"))
	dict.Remove([]byte("Index"))
	return xref, dict, nil
}

func parseIntegerArray(array *Object) ([]int64, error) {
	arr, err := array.AsArray()
	if err != nil {
		return nil, err
	}
	out := make([]int64, 0, len(arr))
	for i := range arr {
		v, err := arr[i].AsI64()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

var errShortReadErr = errors.New("failed to fill whole buffer")

func errShortRead() error { return errShortReadErr }
