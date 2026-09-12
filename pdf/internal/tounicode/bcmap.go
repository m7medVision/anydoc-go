// Binary CMap (bcmap) parsing and built-in CMap loading.
//
// Port of pdf-inspector v1.14.2 src/tounicode.rs (parse_binary_cmap,
// parse_binary_cmap_encoding, BinaryCMapStream, builtin cmap loading).
//
// The pdf.js bcmaps shipped in pdf-inspector's external/bcmaps are embedded
// via go:embed (bcmaps/ directory, vendored by tools/tablegen). The Rust
// build embeds them only for wasm32 targets and reads a directory relative to
// CARGO_MANIFEST_DIR otherwise; in Go the embedded copy is always available,
// and the PDF_INSPECTOR_BCMAPS_DIR environment override is still honored.
package tounicode

import (
	"embed"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

//go:embed bcmaps
var builtinCmaps embed.FS

// readBuiltinCmapFile loads a bcmap by file name.
func readBuiltinCmapFile(name string) ([]byte, bool) {
	if dir := os.Getenv("PDF_INSPECTOR_BCMAPS_DIR"); dir != "" {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			if b, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
				return b, true
			}
		}
	}
	b, err := builtinCmaps.ReadFile("bcmaps/" + name)
	if err != nil {
		return nil, false
	}
	return b, true
}

// buildCmapFromBuiltinCmap builds a ToUnicodeCMap from pdf.js built-in binary
// CMaps (Adobe-<Ordering>-UCS2.bcmap).
func buildCmapFromBuiltinCmap(ordering string) (ToUnicodeCMap, bool) {
	name := "Adobe-" + ordering + "-UCS2.bcmap"
	data, ok := readBuiltinCmapFile(name)
	if !ok {
		return ToUnicodeCMap{}, false
	}
	cmap, err := parseBinaryCmap(data)
	if err != nil {
		return ToUnicodeCMap{}, false
	}
	if len(cmap.CharMap) == 0 && len(cmap.Ranges) == 0 {
		return ToUnicodeCMap{}, false
	}
	cmap.CodeByteLength = 2
	return cmap, true
}

// loadBuiltinCmapByName loads a usecmap-referenced built-in CMap (only
// *UCS2 maps are usable as ToUnicode sources).
func loadBuiltinCmapByName(name string) (ToUnicodeCMap, bool) {
	if !strings.HasSuffix(name, "UCS2") {
		return ToUnicodeCMap{}, false
	}
	data, ok := readBuiltinCmapFile(name + ".bcmap")
	if !ok {
		return ToUnicodeCMap{}, false
	}
	cmap, err := parseBinaryCmap(data)
	if err != nil {
		return ToUnicodeCMap{}, false
	}
	if len(cmap.CharMap) == 0 && len(cmap.Ranges) == 0 {
		return ToUnicodeCMap{}, false
	}
	cmap.CodeByteLength = 2
	return cmap, true
}

// mergeCmaps overlays the entries of `overlay` on top of `base`.
func mergeCmaps(base, overlay ToUnicodeCMap) ToUnicodeCMap {
	for cid, s := range overlay.CharMap {
		base.CharMap[cid] = s
	}
	base.Ranges = append(base.Ranges, overlay.Ranges...)
	sort.Slice(base.Ranges, func(i, j int) bool {
		return base.Ranges[i].Start < base.Ranges[j].Start
	})
	if overlay.CodeByteLength > base.CodeByteLength {
		base.CodeByteLength = overlay.CodeByteLength
	}
	return base
}

// ─── binary CMap parsing ──────────────────────────────────────────────────────

// parseBinaryCmap parses a pdf.js binary CMap into a ToUnicodeCMap.
func parseBinaryCmap(data []byte) (ToUnicodeCMap, error) {
	stream := newBinaryCMapStream(data)
	if _, ok := stream.readByte(); !ok {
		return ToUnicodeCMap{}, errEOFbcmap
	}

	cmap := NewCMap()
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
					return ToUnicodeCMap{}, err
				}
			case 1:
				name, err := stream.readString()
				if err != nil {
					return ToUnicodeCMap{}, err
				}
				useCmap = name
			}
			continue
		}
		sequence := b&0x10 != 0
		dataSize := int(b & 0x0f)
		if dataSize+1 > 16 {
			return ToUnicodeCMap{}, errInvalidDataSize
		}
		subitems, err := stream.readNumber()
		if err != nil {
			return ToUnicodeCMap{}, err
		}
		switch typ {
		case 4:
			// bfchar
			for i := 0; i < subitems; i++ {
				src, err := stream.readHexNumber(1)
				if err != nil {
					return ToUnicodeCMap{}, err
				}
				dst, err := stream.readHexBytes(dataSize + 1)
				if err != nil {
					return ToUnicodeCMap{}, err
				}
				srcCode := uint16(hexToU32(src))
				if s, ok := bytesToUnicodeString(dst); ok {
					cmap.CharMap[srcCode] = s
				}
				if i+1 < subitems && sequence {
					// sequence handled by encoded data, nothing to do
				}
			}
		case 5:
			// bfrange
			for range subitems {
				start, err := stream.readHexNumber(1)
				if err != nil {
					return ToUnicodeCMap{}, err
				}
				endDelta, err := stream.readHexNumber(1)
				if err != nil {
					return ToUnicodeCMap{}, err
				}
				end := append([]byte(nil), start...)
				addHex(end, endDelta)
				dst, err := stream.readHexBytes(dataSize + 1)
				if err != nil {
					return ToUnicodeCMap{}, err
				}
				startCode := uint16(hexToU32(start))
				endCode := uint16(hexToU32(end))
				if s, ok := bytesToUnicodeString(dst); ok {
					if len([]rune(s)) == 1 {
						base := uint32([]rune(s)[0])
						cmap.Ranges = append(cmap.Ranges, CidRange{Start: startCode, End: endCode, Base: base})
					} else {
						// Expand multi char sequences
						cid := startCode
						for _, ch := range s {
							cmap.CharMap[cid] = string(ch)
							if cid == endCode {
								break
							}
							cid = saturatingAdd1(cid)
						}
					}
				}
			}
		default:
			// Skip unsupported types by consuming their payload.
			// We only implement bfchar/bfrange for UCS2 maps.
			for range subitems {
				// Best-effort skip: read a few fields based on type.
				if typ <= 3 {
					_, _ = stream.readHexNumber(dataSize)
					_, _ = stream.readHexNumber(dataSize)
					if typ >= 1 {
						_, _ = stream.readNumber()
					}
				}
			}
		}
	}

	sort.Slice(cmap.Ranges, func(i, j int) bool {
		return cmap.Ranges[i].Start < cmap.Ranges[j].Start
	})
	if useCmap != "" {
		if base, ok := loadBuiltinCmapByName(useCmap); ok {
			cmap = mergeCmaps(base, cmap)
		}
	}
	return cmap, nil
}

// binaryCMapStream reads the bcmap wire format.
type binaryCMapStream struct {
	data []byte
	pos  int
}

func newBinaryCMapStream(data []byte) *binaryCMapStream {
	return &binaryCMapStream{data: data}
}

func (s *binaryCMapStream) readByte() (byte, bool) {
	if s.pos >= len(s.data) {
		return 0, false
	}
	b := s.data[s.pos]
	s.pos++
	return b, true
}

func (s *binaryCMapStream) readNumber() (int, error) {
	var n uint32
	for {
		b, ok := s.readByte()
		if !ok {
			return 0, errors.New("unexpected EOF in bcmap")
		}
		last := b&0x80 == 0
		n = n<<7 | uint32(b&0x7f)
		if last {
			break
		}
	}
	return int(n), nil
}

func (s *binaryCMapStream) readHexNumber(size int) ([]byte, error) {
	// encoded 7-bit number into size+1 bytes
	var stack []byte
	for {
		b, ok := s.readByte()
		if !ok {
			return nil, errors.New("unexpected EOF in bcmap")
		}
		last := b&0x80 == 0
		stack = append(stack, b&0x7f)
		if last {
			break
		}
	}
	out := make([]byte, size+1)
	buffer := uint32(0)
	bufferSize := uint32(0)
	for i := size; i >= 0; i-- {
		for bufferSize < 8 && len(stack) > 0 {
			buffer |= uint32(stack[len(stack)-1]) << bufferSize
			stack = stack[:len(stack)-1]
			bufferSize += 7
		}
		out[i] = byte(buffer & 0xff)
		buffer >>= 8
		if bufferSize >= 8 {
			bufferSize -= 8
		} else {
			bufferSize = 0 // saturating_sub(8)
		}
	}
	return out, nil
}

func (s *binaryCMapStream) readHexBytes(length int) ([]byte, error) {
	if s.pos+length > len(s.data) {
		return nil, errors.New("unexpected EOF in bcmap")
	}
	out := s.data[s.pos : s.pos+length]
	s.pos += length
	return out, nil
}

func (s *binaryCMapStream) readString() (string, error) {
	length, err := s.readNumber()
	if err != nil {
		return "", err
	}
	buf := make([]byte, 0, length)
	for range length {
		v, err := s.readNumber()
		if err != nil {
			return "", err
		}
		buf = append(buf, byte(v))
	}
	if !utf8.Valid(buf) {
		return "", errors.New("invalid UTF-8 in bcmap string")
	}
	return string(buf), nil
}

func hexToU32(bytes []byte) uint32 {
	var n uint32
	for _, b := range bytes {
		n = n<<8 | uint32(b)
	}
	return n
}

func addHex(a, b []byte) {
	var c uint16
	for i := len(a) - 1; i >= 0; i-- {
		c += uint16(a[i]) + uint16(b[i])
		a[i] = byte(c & 0xff)
		c >>= 8
	}
}

func bytesToUnicodeString(bytes []byte) (string, bool) {
	if len(bytes) == 0 {
		return "", false
	}
	if len(bytes)%2 != 0 {
		// Treat as latin-1 bytes
		var b strings.Builder
		for _, by := range bytes {
			b.WriteRune(rune(by))
		}
		return b.String(), true
	}
	var out strings.Builder
	for i := 0; i < len(bytes); i += 2 {
		cp := uint32(uint16(bytes[i])<<8 | uint16(bytes[i+1]))
		if ch, ok := charFromU32(cp); ok {
			out.WriteRune(ch)
		}
	}
	if out.Len() == 0 {
		return "", false
	}
	return out.String(), true
}

// Shared bcmap parse errors.
var (
	errEOFbcmap        = errors.New("unexpected EOF in bcmap")
	errInvalidDataSize = errors.New("invalid dataSize in bcmap")
)
