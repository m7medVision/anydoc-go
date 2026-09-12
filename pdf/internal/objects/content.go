package objects

// Operation / Content port of lopdf src/content.rs (decode side).
type Operation struct {
	Operator string
	Operands []Object
}

type Content struct {
	Operations []Operation
}

// DecodeContent mirrors Content::decode (lenient).
func DecodeContent(data []byte) (*Content, error) {
	return parseContent(data), nil
}

// DecodeContentStrict mirrors Content::decode_strict.
func DecodeContentStrict(data []byte) (*Content, error) {
	return parseContentStrict(data)
}
