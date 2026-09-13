package text

import (
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/encoding/unicode"
)

// The encodings anydoc's frontends decode with. They are the x/text
// implementations of the same WHATWG encodings upstream names in encoding_rs,
// so decoding matches byte for byte. All the code-page mappings a frontend
// resolves to (ansicpg, fcharset, CODEPAGE record, Word LID) come from this
// package so every frontend shares one table.
var (
	// Windows874 is the Windows-874 (Thai) encoding (encoding_rs WINDOWS_874).
	Windows874 = charmap.Windows874
	// Windows1250 is the Windows-1250 (Central European) encoding.
	Windows1250 = charmap.Windows1250
	// Windows1251 is the Windows-1251 (Cyrillic) encoding.
	Windows1251 = charmap.Windows1251
	// Windows1252 is the Windows-1252 (Western) encoding; anydoc's default
	// whenever no code page is declared.
	Windows1252 = charmap.Windows1252
	// Windows1253 is the Windows-1253 (Greek) encoding.
	Windows1253 = charmap.Windows1253
	// Windows1254 is the Windows-1254 (Turkish) encoding.
	Windows1254 = charmap.Windows1254
	// Windows1255 is the Windows-1255 (Hebrew) encoding.
	Windows1255 = charmap.Windows1255
	// Windows1256 is the Windows-1256 (Arabic) encoding.
	Windows1256 = charmap.Windows1256
	// Windows1257 is the Windows-1257 (Baltic) encoding.
	Windows1257 = charmap.Windows1257
	// Windows1258 is the Windows-1258 (Vietnamese) encoding.
	Windows1258 = charmap.Windows1258

	// ShiftJIS is the Shift-JIS encoding (code page 932).
	ShiftJIS = japanese.ShiftJIS
	// GBK is the GBK encoding (code page 936, WHATWG "gbk" = CP936).
	GBK = simplifiedchinese.GBK
	// EUCKR is the EUC-KR encoding (code page 949).
	EUCKR = korean.EUCKR
	// Big5 is the Big5 encoding (code page 950).
	Big5 = traditionalchinese.Big5

	// UTF8 is UTF-8; ForLabel resolves every UTF-8 label to this value, so
	// `enc != UTF8` detects a part that needs transcoding (xml.rs to_utf8).
	UTF8 = unicode.UTF8
	// UTF16LE is UTF-16LE without BOM handling; Decode sniffs and strips a
	// BOM in front of it, like encoding_rs UTF_16LE.decode.
	UTF16LE = unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM)
	// UTF16BE is UTF-16BE without BOM handling; Decode sniffs and strips a
	// BOM in front of it, like encoding_rs UTF_16BE.decode.
	UTF16BE = unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM)
)

// CodepageEncoding maps an ANSI code page number to its encoding, defaulting
// to Windows-1252 for everything unmapped. It backs both the RTF reader's
// \ansicpgN scan (rtf/mod.rs scan_codepage via tables.rs codepage_encoding)
// and the .xls reader's CODEPAGE record for BIFF5 byte strings
// (sheet/xls.rs codepage_encoding) — the two upstream tables are identical.
func CodepageEncoding(cp uint32) Encoding {
	switch cp {
	case 932:
		return ShiftJIS
	case 936:
		return GBK
	case 949:
		return EUCKR
	case 950:
		return Big5
	case 1250:
		return Windows1250
	case 1251:
		return Windows1251
	case 1253:
		return Windows1253
	case 1254:
		return Windows1254
	case 1255:
		return Windows1255
	case 1256:
		return Windows1256
	case 1257:
		return Windows1257
	case 1258:
		return Windows1258
	case 874:
		return Windows874
	default:
		return Windows1252
	}
}

// CharsetEncoding maps an RTF \fcharsetN value to its encoding
// (rtf/tables.rs charset_encoding). 0 (ANSI charset) and 1 (default) keep the
// document's default encoding, as does every value without an entry.
func CharsetEncoding(charset int32, defaultEncoding Encoding) Encoding {
	switch charset {
	case 0, 1:
		return defaultEncoding
	case 128:
		return ShiftJIS
	case 129:
		return EUCKR
	case 134:
		return GBK
	case 136:
		return Big5
	case 161:
		return Windows1253
	case 162:
		return Windows1254
	case 163:
		return Windows1258
	case 177:
		return Windows1255
	case 178, 179, 180:
		return Windows1256
	case 186:
		return Windows1257
	case 204:
		return Windows1251
	case 222:
		return Windows874
	case 238:
		return Windows1250
	default:
		return defaultEncoding
	}
}

// LIDEncoding maps a Word language id (FibBase.lid, or FibRgW97.lidFE when
// fFarEast is set) to the ANSI code page for its compressed piece text
// (doc/mod.rs lid_encoding). The match is on the primary language (the low
// ten bits); Chinese needs the full LID to pick Simplified vs Traditional.
func LIDEncoding(lid uint16) Encoding {
	switch lid & 0x03FF {
	case 0x11:
		return ShiftJIS // 932
	case 0x12:
		return EUCKR // 949
	case 0x04:
		switch lid {
		case 0x0404, 0x0C04, 0x1404, 0x7C04:
			return Big5 // 950
		default:
			return GBK // 936
		}
	case 0x01, 0x20, 0x29:
		return Windows1256 // Arabic script
	case 0x02, 0x19, 0x22, 0x23:
		return Windows1251 // Cyrillic
	case 0x05, 0x0E, 0x15, 0x18, 0x1A, 0x1B, 0x24:
		return Windows1250
	case 0x08:
		return Windows1253 // Greek
	case 0x0D:
		return Windows1255 // Hebrew
	case 0x1E:
		return Windows874 // Thai
	case 0x1F, 0x2C:
		return Windows1254 // Turkic
	case 0x25, 0x26, 0x27:
		return Windows1257 // Baltic
	case 0x2A:
		return Windows1258 // Vietnamese
	default:
		return Windows1252
	}
}

// IsLeadByte reports whether b starts a two-byte sequence in enc
// (doc/mod.rs is_lead_byte). Only the double-byte code pages ever lead with
// a byte above ASCII: Shift-JIS in its two bands, GBK, Big5 and EUC-KR from
// 0x81 through 0xFE.
func IsLeadByte(enc Encoding, b byte) bool {
	switch enc {
	case ShiftJIS:
		return 0x81 <= b && b <= 0x9F || 0xE0 <= b && b <= 0xFC
	case GBK, Big5, EUCKR:
		return 0x81 <= b && b <= 0xFE
	default:
		return false
	}
}
