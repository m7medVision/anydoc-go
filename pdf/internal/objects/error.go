package objects

import "fmt"

// ErrKind mirrors the variants of lopdf::Error. Engine code (the lib.rs port)
// matches on these to build pdf.Error values.
type ErrKind int

const (
	KindUnimplemented ErrKind = iota
	KindObjectType
	KindDictType
	KindAlreadyEncrypted
	KindCharacterEncoding
	KindDecompress
	KindParse
	KindDecryption
	KindDictKey
	KindInvalidInlineImage
	KindInvalidOutline
	KindInvalidStream
	KindInvalidObjectStream
	KindInvalidOffset
	KindIO
	KindNoOutline
	KindNotEncrypted
	KindInvalidPassword
	KindMissingXrefEntry
	KindObjectNotFound
	KindReferenceCycle
	KindPageNumberNotFound
	KindNumericCast
	KindReferenceLimit
	KindTextStringDecode
	KindXref
	KindIndirectObject
	KindObjectIdMismatch
	KindSyntax
	KindToUnicodeCMap
	KindTryFromInt
	KindUnsupportedSecurityHandler
	KindInvalidEncodingDifferenceCode
	KindInvalidEncodingDifferenceGlyph
)

// ParseError mirrors lopdf::error::ParseError; Display strings are part of the
// engine's public error surface and must match verbatim.
type ParseError int

const (
	ParseEndOfInput ParseError = iota
	ParseInvalidContentStream
	ParseInvalidFileHeader
	ParseInvalidTrailer
	ParseInvalidXref
)

func (e ParseError) Error() string {
	switch e {
	case ParseEndOfInput:
		return "unexpected end of input"
	case ParseInvalidContentStream:
		return "invalid content stream"
	case ParseInvalidFileHeader:
		return "invalid file header"
	case ParseInvalidTrailer:
		return "invalid file trailer"
	case ParseInvalidXref:
		return "invalid cross reference table"
	}
	return "invalid cross reference table"
}

// XrefError mirrors lopdf::error::XrefError.
type XrefError int

const (
	XrefErrStart XrefError = iota
	XrefErrPrevStart
	XrefErrStreamStart
)

func (e XrefError) Error() string {
	switch e {
	case XrefErrStart:
		return "invalid start value"
	case XrefErrPrevStart:
		return "invalid start value in Prev field"
	case XrefErrStreamStart:
		return "invalid start value of XRefStm"
	}
	return "invalid start value"
}

// DecompressError mirrors lopdf::error::DecompressError.
type DecompressError struct {
	Detail string
}

func (e *DecompressError) Error() string { return "decoding ASCII85 failed: " + e.Detail }

// Error mirrors lopdf::Error. Error() strings are byte-pinned: they surface
// through pdf-inspector's PdfError::Parse(e.to_string()) at the public seam.
type Error struct {
	Kind ErrKind
	// Unimplemented message.
	UnimplementedMsg string
	// ObjectType expected/found.
	Expected, Found string
	// DictType expected/found.
	DictFound string
	// Wrapped errors: *DecompressError, ParseError, *DecryptionError, io error,
	// XrefError, *UnicodeCMapError.
	Inner error
	// DictKey name.
	Key string
	// Free-form detail for InvalidStream / InvalidObjectStream / InvalidOutline /
	// InvalidInlineImage / NumericCast / Syntax / TryFromInt.
	Detail string
	// InvalidOffset / IndirectObject offset.
	Offset int
	// ObjectNotFound / ReferenceCycle id.
	ID ObjectId
	// PageNumberNotFound page.
	Page uint32
	// UnsupportedSecurityHandler filter name.
	FilterName []byte
	// InvalidEncodingDifferenceCode code.
	Code int64
	// InvalidEncodingDifferenceGlyph name.
	GlyphName string
}

func (e *Error) Error() string {
	switch e.Kind {
	case KindUnimplemented:
		return fmt.Sprintf("missing feature of lopdf: %s; please open an issue at https://github.com/J-F-Liu/lopdf/ to let the developers know of your usecase", e.UnimplementedMsg)
	case KindObjectType:
		return fmt.Sprintf("object has wrong type; expected type %s but found type %s", e.Expected, e.Found)
	case KindDictType:
		return "dictionary has wrong type: "
	case KindAlreadyEncrypted:
		return "PDF document is already encrypted"
	case KindCharacterEncoding:
		return "invalid character encoding"
	case KindDecompress:
		return fmt.Sprintf("couldn't decompress stream %v", e.Inner)
	case KindParse:
		return fmt.Sprintf("couldn't parse input: %v", e.Inner)
	case KindDecryption:
		return fmt.Sprintf("decryption error: %v", e.Inner)
	case KindDictKey:
		return fmt.Sprintf("missing required dictionary key %q", e.Key)
	case KindInvalidInlineImage:
		return fmt.Sprintf("invalid inline image: %s", e.Detail)
	case KindInvalidOutline:
		return fmt.Sprintf("invalid document outline: %s", e.Detail)
	case KindInvalidStream:
		return fmt.Sprintf("invalid stream: %s", e.Detail)
	case KindInvalidObjectStream:
		return fmt.Sprintf("invalid object stream: %s", e.Detail)
	case KindInvalidOffset:
		return "invalid byte offset"
	case KindIO:
		return fmt.Sprintf("IO error: %v", e.Inner)
	case KindNoOutline:
		return "PDF document does not have an outline"
	case KindNotEncrypted:
		return "PDF document is not encrypted"
	case KindInvalidPassword:
		return "invalid password for encrypted PDF"
	case KindMissingXrefEntry:
		return "missing xref entry"
	case KindObjectNotFound:
		return fmt.Sprintf("object ID %d %d not found", e.ID.Num, e.ID.Gen)
	case KindReferenceCycle:
		return fmt.Sprintf("reference cycle with object ID %d %d", e.ID.Num, e.ID.Gen)
	case KindPageNumberNotFound:
		return "page number not found"
	case KindNumericCast:
		return fmt.Sprintf("numberic type cast failed: %s", e.Detail)
	case KindReferenceLimit:
		return "dereferencing object reached limit, may indicate a reference cycle"
	case KindTextStringDecode:
		return "decoding text string failed"
	case KindXref:
		return fmt.Sprintf("failed parsing cross reference table: %v", e.Inner)
	case KindIndirectObject:
		return fmt.Sprintf("invalid indirect object at byte offset %d", e.Offset)
	case KindObjectIdMismatch:
		return "found object ID does not match expected object ID"
	case KindSyntax:
		return fmt.Sprintf("syntax error in content stream: %s", e.Detail)
	case KindToUnicodeCMap:
		return fmt.Sprintf("failed parsing ToUnicode CMap: %v", e.Inner)
	case KindTryFromInt:
		return fmt.Sprintf("converting integer: %s", e.Detail)
	case KindUnsupportedSecurityHandler:
		return "unsupported security handler"
	case KindInvalidEncodingDifferenceCode:
		return fmt.Sprintf("invalid encoding difference code: %d", e.Code)
	case KindInvalidEncodingDifferenceGlyph:
		return fmt.Sprintf("invalid encoding difference glyph name: %s", e.GlyphName)
	}
	return "lopdf error"
}

// Unwrap exposes the wrapped inner error (io errors, decryption errors, ...).
func (e *Error) Unwrap() error { return e.Inner }

// Sentinel error values for the variants the engine matches by identity.
var (
	ErrAlreadyEncrypted = &Error{Kind: KindAlreadyEncrypted}
	ErrInvalidPassword  = &Error{Kind: KindInvalidPassword}
	ErrNotEncrypted     = &Error{Kind: KindNotEncrypted}
	ErrMissingXrefEntry = &Error{Kind: KindMissingXrefEntry}
	ErrReferenceLimit   = &Error{Kind: KindReferenceLimit}
	ErrObjectIdMismatch = &Error{Kind: KindObjectIdMismatch}
)

func errObjectType(expected, found string) *Error {
	return &Error{Kind: KindObjectType, Expected: expected, Found: found}
}

func errDictKey(key []byte) *Error {
	return &Error{Kind: KindDictKey, Key: string(key)}
}

func errParse(pe ParseError) *Error { return &Error{Kind: KindParse, Inner: pe} }

func errIO(err error) *Error { return &Error{Kind: KindIO, Inner: err} }

// IsIO reports whether err is an lopdf IO error.
func IsIO(err error) bool { return hasKind(err, KindIO) }

// IsEncryptedError mirrors pdf-inspector's is_encrypted_lopdf_error: whether
// err represents an encryption-related load failure.
func IsEncryptedError(err error) bool {
	e, ok := err.(*Error)
	if !ok {
		return false
	}
	switch e.Kind {
	case KindDecryption, KindInvalidPassword, KindAlreadyEncrypted, KindUnsupportedSecurityHandler:
		return true
	case KindUnimplemented:
		return contains(e.UnimplementedMsg, "encrypted")
	}
	return false
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func hasKind(err error, kind ErrKind) bool {
	switch e := err.(type) {
	case *Error:
		return e.Kind == kind
	case nil:
		return false
	default:
		return false
	}
}
