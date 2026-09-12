// Package text is anydoc's charset decoding layer: the code-page and UTF-16
// operations the RTF, Word, PowerPoint and Excel frontends perform, ported
// from upstream's encoding_rs usage onto golang.org/x/text.
//
// Upstream decodes with encoding_rs (WHATWG Encoding Standard). The Go port
// keeps those exact semantics:
//
//   - Decode            = encoding_rs Encoding::decode (BOM sniffing + lossy)
//   - DecodeWithoutBOMHandling = encoding_rs decode_without_bom_handling
//   - ForLabel          = encoding_rs Encoding::for_label (WHATWG labels)
//   - DecodeUTF16/LE    = Rust char::decode_utf16(..).unwrap_or(U+FFFD) /
//     String::from_utf16_lossy over utf16le_units
//   - DecodeUTF8        = Rust String::from_utf8_lossy (maximal-subpart
//     replacement, per the WHATWG UTF-8 decoder)
package text

import (
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/transform"
)

// Encoding is a character encoding (legacy code page or Unicode
// transformation format). Values are comparable: frontends hold them in font
// tables and compare them against this package's exported encodings, exactly
// like upstream's `&'static encoding_rs::Encoding`.
type Encoding = encoding.Encoding

// Decode decodes b with enc per encoding_rs Encoding::decode: every invalid
// byte sequence is replaced with U+FFFD, and a UTF-8, UTF-16LE or UTF-16BE
// byte order mark at the start of b overrides enc and is stripped.
//
// Upstream call sites: rtf TextDecoder::take_pending (mod.rs), rtf build_pattern
// and stylesheet names (tables.rs), csv fallback decoding, and xml.rs to_utf8
// transcoding.
func Decode(enc Encoding, b []byte) string {
	switch {
	case len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF:
		return DecodeUTF8(b[3:])
	case len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE:
		return decodeBytes(UTF16LE, b[2:])
	case len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF:
		return decodeBytes(UTF16BE, b[2:])
	default:
		return decodeBytes(enc, b)
	}
}

// DecodeWithoutBOMHandling decodes b with enc per encoding_rs
// decode_without_bom_handling: lossy like Decode, but a byte order mark is
// decoded as a character like any other input and never overrides enc.
//
// Upstream call sites: doc compressed-piece text (1-2 bytes at a time, chosen
// by is-lead-byte probing) and the BIFF5 byte strings of the legacy .xls
// reader.
func DecodeWithoutBOMHandling(enc Encoding, b []byte) string {
	return decodeBytes(enc, b)
}

// decodeBytes runs one full-buffer, at-EOF decode. The x/text decoders
// replace invalid input with U+FFFD and keep going (errors surface only for
// streaming truncation, which a complete buffer never triggers), so the
// result is the whole input decoded lossily.
func decodeBytes(enc Encoding, b []byte) string {
	out, _, _ := transform.Bytes(enc.NewDecoder(), b)
	return string(out)
}

// ForLabel resolves one encoding label from the WHATWG Encoding Standard
// (encoding_rs Encoding::for_label): matched ASCII case-insensitively after
// trimming ASCII whitespace. ok is false for unknown labels.
//
// Upstream call site: xml.rs to_utf8, for the XML prolog's encoding
// declaration.
func ForLabel(label string) (enc Encoding, ok bool) {
	enc, err := htmlindex.Get(label)
	if err != nil {
		return nil, false
	}
	return enc, true
}

// DecodeUTF16 decodes UTF-16 code units to a string, replacing every
// unpaired surrogate with U+FFFD (Rust String::from_utf16_lossy and
// char::decode_utf16(..).unwrap_or(REPLACEMENT_CHARACTER): one replacement
// per unpaired unit).
//
// Upstream call sites: xls BIFF8 string and TXO text (units accumulated
// across CONTINUE records), doc STSH style names.
func DecodeUTF16(units []uint16) string {
	return string(utf16.Decode(units))
}

// DecodeUTF16LE decodes b as UTF-16LE: byte pairs become code units (a
// trailing odd byte is dropped) and unpaired surrogates become U+FFFD.
//
// Upstream call sites: ppt TextCharsAtom, xlsb XLWideString /
// XLNullableWideString, doc uncompressed piece text.
func DecodeUTF16LE(b []byte) string {
	return string(utf16.Decode(UTF16LEUnits(b)))
}

// UTF16LEUnits returns the UTF-16LE code units of b; a trailing odd byte is
// dropped (shared/binary.rs utf16le_units).
func UTF16LEUnits(b []byte) []uint16 {
	units := make([]uint16, len(b)/2)
	for i := range units {
		units[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	return units
}

// DecodeUTF8 decodes b as UTF-8 with replacement, per Rust
// String::from_utf8_lossy and the WHATWG UTF-8 decoder: each maximal
// ill-formed subpart becomes a single U+FFFD. A byte order mark is not
// special (it decodes to U+FEFF).
func DecodeUTF8(b []byte) string {
	var out strings.Builder
	out.Grow(len(b))
	i := 0
	for i < len(b) {
		if c := b[i]; c < utf8.RuneSelf {
			j := i + 1
			for j < len(b) && b[j] < utf8.RuneSelf {
				j++
			}
			out.Write(b[i:j])
			i = j
			continue
		}
		// Lead byte of a multi-byte sequence: need continuation bytes, the
		// first of which may be restricted (WHATWG UTF-8 decoder states).
		var need int
		var cp rune
		lower, upper := byte(0x80), byte(0xBF)
		switch c := b[i]; {
		case c >= 0xC2 && c <= 0xDF:
			need, cp = 1, rune(c&0x1F)
		case c >= 0xE0 && c <= 0xEF:
			need, cp = 2, rune(c&0x0F)
			switch c {
			case 0xE0:
				lower = 0xA0
			case 0xED:
				upper = 0x9F
			}
		case c >= 0xF0 && c <= 0xF4:
			need, cp = 3, rune(c&0x07)
			switch c {
			case 0xF0:
				lower = 0x90
			case 0xF4:
				upper = 0x8F
			}
		default:
			out.WriteRune(utf8.RuneError)
			i++
			continue
		}
		seen := 0
		j := i + 1
		for seen < need && j < len(b) {
			if c := b[j]; c < lower || c > upper {
				break
			}
			lower, upper = 0x80, 0xBF
			cp = cp<<6 | rune(b[j]&0x3F)
			seen++
			j++
		}
		if seen < need {
			// Ill-formed: one replacement for the maximal subpart consumed
			// so far; the offending byte is reprocessed as a fresh start.
			out.WriteRune(utf8.RuneError)
			i = j
			continue
		}
		out.WriteRune(cp)
		i = j
	}
	return out.String()
}
