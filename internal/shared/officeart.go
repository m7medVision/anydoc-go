// Port of src/shared/officeart.rs: OfficeArt (MS-ODRAW) blip extraction
// shared by the legacy binary formats: DOC picture data (PICF + OfficeArt
// records in the Data stream) and the PPT Pictures stream (a sequence of
// BStore file blocks). Only the picture payloads are extracted; drawing
// geometry is out of scope.

package shared

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"io"
)

// RecordAt is the (verAndInstance, recType, body) of the OfficeArt record
// at off — the same 8-byte header the PPT record stream uses.
func RecordAt(data []byte, off int) (verInst, recType uint16, body []byte, ok bool) {
	if off < 0 || off > len(data) {
		return 0, 0, nil, false
	}
	rest := data[off:]
	if len(rest) < 8 {
		return 0, 0, nil, false
	}
	verInst = binary.LittleEndian.Uint16(rest[0:2])
	recType = binary.LittleEndian.Uint16(rest[2:4])
	length := int(binary.LittleEndian.Uint32(rest[4:8]))
	if length < 0 || 8+length > len(rest) {
		return 0, 0, nil, false
	}
	return verInst, recType, rest[8 : 8+length], true
}

// Blip is an extracted picture payload.
type Blip struct {
	MediaType string
	Extension string
	Bytes     []byte
}

// DecodeBlip decodes one blip record (recType 0xF01A–0xF01F). Metafile
// blips may be deflate-compressed; the output is bounded by the declared
// uncompressed size, capped at maxBytes.
func DecodeBlip(verInst, recType uint16, body []byte, maxBytes int) (Blip, bool) {
	instance := verInst >> 4
	switch recType {
	// Bitmap blips: rgbUid1 (16), + rgbUid2 (16) for the doubled
	// instance, then the picture bytes, with one tag byte first.
	case 0xF01D, 0xF01E:
		doubled := instance == 0x46B || instance == 0x6E3 || instance == 0x6E1
		start := 16 + 1
		if doubled {
			start = 32 + 1
		}
		if start > len(body) {
			return Blip{}, false
		}
		media, ext := "image/png", "png"
		if recType == 0xF01D {
			media, ext = "image/jpeg", "jpg"
		}
		return Blip{MediaType: media, Extension: ext, Bytes: body[start:]}, true
	// Metafile blips: rgbUid (16/32), then a 34-byte metafile header
	// (cbSize, bounds, ptSize, cbSave, compression, filter).
	case 0xF01A, 0xF01B:
		doubled := instance == 0x3D5 || instance == 0x217
		header := 16
		if doubled {
			header = 32
		}
		if header > len(body) {
			return Blip{}, false
		}
		headerAndData := body[header:]
		if len(headerAndData) < 4 {
			return Blip{}, false
		}
		cbSize := binary.LittleEndian.Uint32(headerAndData[:4])
		if len(headerAndData) < 33 {
			return Blip{}, false
		}
		compression := headerAndData[32]
		if len(headerAndData) < 34 {
			return Blip{}, false
		}
		data := headerAndData[34:]
		media, ext := "image/wmf", "wmf"
		if recType == 0xF01A {
			media, ext = "image/emf", "emf"
		}
		var payload []byte
		switch compression {
		case 0x00:
			// 0x00 = deflate-compressed; 0xFE = uncompressed.
			limit := int(cbSize)
			if limit > maxBytes {
				limit = maxBytes
			}
			if limit < 0 {
				return Blip{}, false
			}
			r := flate.NewReader(bytes.NewReader(data))
			out, err := io.ReadAll(io.LimitReader(r, int64(limit)))
			_ = r.Close()
			if err != nil {
				return Blip{}, false
			}
			payload = out
		default:
			payload = data
		}
		return Blip{MediaType: media, Extension: ext, Bytes: payload}, true
	default:
		return Blip{}, false
	}
}

// FirstBlip finds and decodes the first blip in a run of OfficeArt records
// (a Pictures stream block sequence, or an inline shape container),
// descending into containers. Bounded traversal: record counts and nesting
// beyond any real drawing abort the search.
func FirstBlip(data []byte, maxBytes int) (Blip, bool) {
	type rng struct{ cursor, end int }
	stack := []rng{{0, len(data)}}
	visited := uint32(0)
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		if top.cursor >= top.end {
			stack = stack[:len(stack)-1]
			continue
		}
		verInst, recType, body, ok := RecordAt(data[:top.end], top.cursor)
		if !ok {
			stack = stack[:len(stack)-1]
			continue
		}
		bodyStart := top.cursor + 8
		if bodyStart < top.cursor {
			return Blip{}, false
		}
		bodyEnd := bodyStart + len(body)
		if bodyEnd < bodyStart {
			return Blip{}, false
		}
		top.cursor = bodyEnd
		visited++
		if visited > 10_000 || len(stack) > 16 {
			return Blip{}, false
		}
		if blip, ok := DecodeBlip(verInst, recType, body, maxBytes); ok {
			return blip, true
		}
		if recType == 0xF007 {
			off, ok := fbseBlipOffset(body)
			innerStart := bodyStart + off
			if !ok {
				innerStart = bodyStart + len(body)
			}
			if innerStart < bodyEnd {
				stack = append(stack, rng{innerStart, bodyEnd})
			}
			continue
		}
		if verInst&0xF == 0xF {
			stack = append(stack, rng{bodyStart, bodyEnd})
		}
	}
	return Blip{}, false
}

// Offset of the embedded blip record inside an FBSE (0xF007) body: the
// 36-byte header plus the entry's name.
func fbseBlipOffset(body []byte) (int, bool) {
	if len(body) < 34 {
		return 0, false
	}
	cbName := int(body[33])
	off := 36 + cbName
	if off < 36 {
		return 0, false
	}
	return off, true
}

// FBSEBlip decodes the blip embedded in an FBSE (0xF007) record body, if
// present.
func FBSEBlip(body []byte, maxBytes int) (Blip, bool) {
	offset, ok := fbseBlipOffset(body)
	if !ok {
		return Blip{}, false
	}
	verInst, recType, blipBody, ok := RecordAt(body, offset)
	if !ok {
		return Blip{}, false
	}
	return DecodeBlip(verInst, recType, blipBody, maxBytes)
}
