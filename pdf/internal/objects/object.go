// Package objects is the PDF object layer pdf-inspector builds on: lopdf's
// role as an internal engine detail. Load from bytes, the object model,
// xref (including incremental/hybrid repair), stream filters the engine
// uses, font encodings, and encryption detection (plus the decrypt path
// LoadMemWithOptions needs when a password is supplied).
package objects

import "strconv"

// ObjectId mirrors lopdf::ObjectId = (u32, u16).
type ObjectId struct {
	Num uint32
	Gen uint16
}

// Kind discriminates the Object sum, mirroring lopdf::Object variants.
type Kind uint8

const (
	KindNull Kind = iota
	KindBoolean
	KindInteger
	KindReal
	KindName
	KindString
	KindArray
	KindDictionary
	KindStream
	KindReference
)

// Object mirrors lopdf::Object as a single struct with a Kind tag:
//
//	Boolean  -> Bool
//	Integer  -> Int (i64)
//	Real     -> Real (f32)
//	Name     -> Name
//	String   -> Str + Hex (StringFormat::Literal/Hexadecimal)
//	Array    -> Array
//	Dictionary -> Dict
//	Stream   -> Stream
//	Reference -> Ref
//	Null     -> (payload free)
//
// The zero value is Null.
type Object struct {
	Kind   Kind
	Bool   bool
	Int    int64
	Real   float32
	Name   []byte
	Str    []byte
	Hex    bool // StringFormat::Hexadecimal when true, Literal when false
	Array  []Object
	Dict   *Dictionary
	Stream *Stream
	Ref    ObjectId
}

// Constructors mirroring lopdf's From impls and helpers.
func Null() Object                       { return Object{Kind: KindNull} }
func Boolean(v bool) Object              { return Object{Kind: KindBoolean, Bool: v} }
func Integer(v int64) Object             { return Object{Kind: KindInteger, Int: v} }
func Real(v float32) Object              { return Object{Kind: KindReal, Real: v} }
func NameObj(name []byte) Object         { return Object{Kind: KindName, Name: name} }
func StringLiteral(s []byte) Object      { return Object{Kind: KindString, Str: s} }
func StringHex(s []byte) Object          { return Object{Kind: KindString, Str: s, Hex: true} }
func ArrayObj(arr []Object) Object       { return Object{Kind: KindArray, Array: arr} }
func DictionaryObj(d *Dictionary) Object { return Object{Kind: KindDictionary, Dict: d} }
func StreamObj(s *Stream) Object         { return Object{Kind: KindStream, Stream: s} }
func Reference(id ObjectId) Object       { return Object{Kind: KindReference, Ref: id} }

// IsNull mirrors Object::is_null.
func (o *Object) IsNull() bool { return o.Kind == KindNull }

// AsBool mirrors Object::as_bool.
func (o *Object) AsBool() (bool, error) {
	if o.Kind == KindBoolean {
		return o.Bool, nil
	}
	return false, errObjectType("Boolean", o.EnumVariant())
}

// AsI64 mirrors Object::as_i64.
func (o *Object) AsI64() (int64, error) {
	if o.Kind == KindInteger {
		return o.Int, nil
	}
	return 0, errObjectType("Integer", o.EnumVariant())
}

// AsF32 mirrors Object::as_f32 (Real only).
func (o *Object) AsF32() (float32, error) {
	if o.Kind == KindReal {
		return o.Real, nil
	}
	return 0, errObjectType("Real", o.EnumVariant())
}

// AsFloat mirrors Object::as_float: Integer or Real, cast to f32.
func (o *Object) AsFloat() (float32, error) {
	switch o.Kind {
	case KindInteger:
		return float32(o.Int), nil
	case KindReal:
		return o.Real, nil
	}
	return 0, errObjectType("Integer or Real", o.EnumVariant())
}

// AsName mirrors Object::as_name.
func (o *Object) AsName() ([]byte, error) {
	if o.Kind == KindName {
		return o.Name, nil
	}
	return nil, errObjectType("Name", o.EnumVariant())
}

// AsStr mirrors Object::as_str (String payload bytes).
func (o *Object) AsStr() ([]byte, error) {
	if o.Kind == KindString {
		return o.Str, nil
	}
	return nil, errObjectType("String", o.EnumVariant())
}

// AsReference mirrors Object::as_reference.
func (o *Object) AsReference() (ObjectId, error) {
	if o.Kind == KindReference {
		return o.Ref, nil
	}
	return ObjectId{}, errObjectType("Reference", o.EnumVariant())
}

// AsArray mirrors Object::as_array.
func (o *Object) AsArray() ([]Object, error) {
	if o.Kind == KindArray {
		return o.Array, nil
	}
	return nil, errObjectType("Array", o.EnumVariant())
}

// AsDict mirrors Object::as_dict.
func (o *Object) AsDict() (*Dictionary, error) {
	if o.Kind == KindDictionary {
		return o.Dict, nil
	}
	return nil, errObjectType("Dictionary", o.EnumVariant())
}

// AsStream mirrors Object::as_stream.
func (o *Object) AsStream() (*Stream, error) {
	if o.Kind == KindStream {
		return o.Stream, nil
	}
	return nil, errObjectType("Stream", o.EnumVariant())
}

// TypeName mirrors Object::type_name (Dictionary/Stream /Type entry).
func (o *Object) TypeName() ([]byte, error) {
	switch o.Kind {
	case KindDictionary:
		return o.Dict.GetType()
	case KindStream:
		return o.Stream.Dict.GetType()
	}
	return nil, errObjectType("Dictionary or Stream", o.EnumVariant())
}

// EnumVariant mirrors Object::enum_variant: the variant name of this object.
func (o *Object) EnumVariant() string {
	switch o.Kind {
	case KindNull:
		return "Null"
	case KindBoolean:
		return "Boolean"
	case KindInteger:
		return "Integer"
	case KindReal:
		return "Real"
	case KindName:
		return "Name"
	case KindString:
		return "String"
	case KindArray:
		return "Array"
	case KindDictionary:
		return "Dictionary"
	case KindStream:
		return "Stream"
	case KindReference:
		return "Reference"
	}
	return "Null"
}

// String renders the object like lopdf's Debug impl. Used for diagnostics only.
func (o *Object) String() string {
	switch o.Kind {
	case KindNull:
		return "Null"
	case KindBoolean:
		if o.Bool {
			return "true"
		}
		return "false"
	case KindInteger:
		return itoa(o.Int)
	case KindReal:
		return f32toa(o.Real)
	case KindName:
		return "/" + string(o.Name)
	case KindString:
		if o.Hex {
			return "<" + hexEncode(o.Str) + ">"
		}
		return "(" + string(o.Str) + ")"
	case KindArray:
		s := "["
		for i, item := range o.Array {
			if i > 0 {
				s += " "
			}
			s += item.String()
		}
		return s + "]"
	case KindDictionary:
		return o.Dict.String()
	case KindStream:
		if o.Stream != nil {
			return o.Stream.Dict.String() + "stream...endstream"
		}
		return "stream...endstream"
	case KindReference:
		return itoa(int64(o.Ref.Num)) + " " + itoa(int64(o.Ref.Gen)) + " R"
	}
	return "Null"
}

func itoa(v int64) string {
	neg := v < 0
	u := uint64(v)
	if neg {
		u = uint64(-v)
	}
	var buf [20]byte
	i := len(buf)
	for {
		i--
		buf[i] = byte('0' + u%10)
		u /= 10
		if u == 0 {
			break
		}
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func f32toa(v float32) string {
	return strconv.FormatFloat(float64(v), 'g', -1, 32)
}

func hexEncode(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&0xf])
	}
	return string(out)
}
