package objects

// MSB-first LZW decoder mirroring weezl 0.1.12 (BitOrder::Msb) with the TIFF
// "early change" size switch lopdf uses for /LZWDecode streams. Behavioral
// parity with weezl's Decoder::with_tiff_size_switch / decode_all:
//   - clear code resets the table, end code stops decoding (trailing input is
//     ignored), truncated input yields the partial output without error,
//   - the table stops growing at 4096 entries,
//   - a code greater than next_code is an error (partial output is returned
//     alongside it by decodeLZWAll).
const (
	lzwMinCodeSize = 8  // lopdf passes MIN_BITS-1
	lzwMaxCodeSize = 12 // weezl MAX_CODESIZE
	lzwMaxEntries  = 1 << lzwMaxCodeSize
)

type lzwLink struct {
	prev  uint16 // predecessor code (unused for literal codes)
	ch    byte
	first byte
}

type lzwDecoder struct {
	table     [lzwMaxEntries]lzwLink
	depth     [lzwMaxEntries]uint16
	entries   int // number of valid table entries
	nextCode  uint16
	codeSize  uint8
	clearCode uint16
	endCode   uint16
	tiff      bool
	// bit reader (MSB-first)
	bitBuf uint64
	bitCnt uint8
}

func newLZWDecoder(tiff bool) *lzwDecoder {
	d := &lzwDecoder{
		clearCode: 1 << lzwMinCodeSize,
		endCode:   1<<lzwMinCodeSize + 1,
		tiff:      tiff,
	}
	d.resetTable()
	return d
}

func (d *lzwDecoder) resetTable() {
	d.codeSize = lzwMinCodeSize + 1
	d.nextCode = 1<<lzwMinCodeSize + 2
	d.entries = int(d.nextCode)
	for i := 0; i < 1<<lzwMinCodeSize; i++ {
		d.table[i] = lzwLink{prev: 0, ch: byte(i), first: byte(i)}
		d.depth[i] = 1
	}
	d.table[d.clearCode] = lzwLink{}
	d.table[d.endCode] = lzwLink{}
	d.depth[d.clearCode] = 0
	d.depth[d.endCode] = 0
}

func (d *lzwDecoder) maxCode() uint16 { return 1 << d.codeSize }

// readCode reads the next MSB-first code; ok=false when input is exhausted.
func (d *lzwDecoder) readCode(input []byte, pos *int) (uint16, bool) {
	for d.bitCnt < d.codeSize {
		if *pos >= len(input) {
			return 0, false
		}
		d.bitBuf = d.bitBuf<<8 | uint64(input[*pos])
		*pos++
		d.bitCnt += 8
	}
	shift := d.bitCnt - d.codeSize
	code := uint16((d.bitBuf >> shift) & uint64(d.maxCode()-1))
	d.bitCnt = shift
	d.bitBuf &= (uint64(1) << shift) - 1
	return code, true
}

// reconstruct writes the word for code into out (len(out) == depth[code]) and
// returns its first byte.
func (d *lzwDecoder) reconstruct(code uint16, out []byte) byte {
	c := code
	for i := len(out) - 1; i >= 0; i-- {
		out[i] = d.table[c].ch
		c = d.table[c].prev
	}
	return d.table[code].first
}

func (d *lzwDecoder) word(code uint16) []byte {
	out := make([]byte, d.depth[code])
	d.reconstruct(code, out)
	return out
}

// decodeAll mirrors weezl's decode_all: partial output plus error status.
func (d *lzwDecoder) decodeAll(input []byte) ([]byte, error) {
	var output []byte
	pos := 0
	started := false // a first data code has been seen
	var lastCode uint16
	var lastWord []byte

	for {
		code, ok := d.readCode(input, &pos)
		if !ok {
			// Truncated or cleanly ended input: not an error.
			return output, nil
		}
		if !started {
			if code == d.clearCode {
				d.resetTable()
				continue
			}
			if code == d.endCode {
				return output, nil
			}
			if code > d.nextCode {
				return output, errLzwInvalidCode()
			}
			if code == d.nextCode {
				return output, errLzwInvalidCode()
			}
			lastWord = d.word(code)
			output = append(output, lastWord...)
			lastCode = code
			started = true
			continue
		}
		switch {
		case code == d.clearCode:
			d.resetTable()
			started = false
		case code == d.endCode:
			return output, nil
		case code > d.nextCode:
			return output, errLzwInvalidCode()
		default:
			var word []byte
			var first byte
			if code == d.nextCode {
				// cScSc special case: last word + its own first byte.
				word = make([]byte, 0, len(lastWord)+1)
				word = append(word, lastWord...)
				first = lastWord[0]
				word = append(word, first)
			} else {
				word = d.word(code)
				first = word[0]
			}
			output = append(output, word...)
			if d.entries < lzwMaxEntries {
				d.table[d.nextCode] = lzwLink{prev: lastCode, ch: first, first: d.table[lastCode].first}
				d.depth[d.nextCode] = d.depth[lastCode] + 1
				d.entries++
				bumpAt := d.maxCode()
				if d.tiff {
					bumpAt--
				}
				if d.nextCode >= bumpAt && d.codeSize < lzwMaxCodeSize {
					d.codeSize++
				}
				d.nextCode++
			}
			lastCode = code
			lastWord = word
		}
	}
}

func errLzwInvalidCode() error {
	// weezl's LzwError::InvalidCode; only the warn log consumes this message.
	return &Error{Kind: KindSyntax, Detail: "invalid code in lzw stream"}
}

// decompressLZW mirrors lopdf's decompress_lzw: decode with EarlyChange
// (default true), always applying the predictor afterwards. A decode error is
// logged (warn) upstream and the partial output is used; here the partial
// output is returned with the error filtered to the predictor stage only.
func decompressLZW(input []byte, params *Dictionary) ([]byte, error) {
	earlyChange := true
	if params != nil {
		if o, err := params.Get([]byte("EarlyChange")); err == nil {
			if v, err2 := o.AsI64(); err2 == nil {
				earlyChange = v != 0
			}
		}
	}
	d := newLZWDecoder(earlyChange)
	output, _ := d.decodeAll(input)
	return output, nil
}
