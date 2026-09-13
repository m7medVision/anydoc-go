package objects

import (
	"bytes"
	"compress/flate"
	"compress/zlib"
	"io"
)

// Stream mirrors lopdf::Stream.
type Stream struct {
	Dict    Dictionary
	Content []byte
	// StartPosition mirrors start_position: set (>= 0) when the stream content
	// could not be read at parse time (missing/indirect Length); -1 otherwise.
	StartPosition int
}

// NewStream mirrors Stream::new: sets /Length in the dictionary.
func NewStream(dict *Dictionary, content []byte) *Stream {
	if dict == nil {
		dict = NewDictionary()
	}
	dict.Set([]byte("Length"), Integer(int64(len(content))))
	return &Stream{Dict: *dict, Content: content, StartPosition: -1}
}

// StreamWithPosition mirrors Stream::with_position.
func StreamWithPosition(dict *Dictionary, position int) *Stream {
	if dict == nil {
		dict = NewDictionary()
	}
	return &Stream{Dict: *dict, Content: nil, StartPosition: position}
}

// Filters mirrors Stream::filters.
func (s *Stream) Filters() ([][]byte, error) {
	filter, err := s.Dict.Get([]byte("Filter"))
	if err != nil {
		return nil, err
	}
	if name, err := filter.AsName(); err == nil {
		return [][]byte{name}, nil
	}
	if arr, err := filter.AsArray(); err == nil {
		out := make([][]byte, 0, len(arr))
		for _, item := range arr {
			n, err := item.AsName()
			if err != nil {
				return nil, err
			}
			out = append(out, n)
		}
		return out, nil
	}
	return nil, errObjectType("Name or Array", filter.EnumVariant())
}

// SetContent mirrors Stream::set_content.
func (s *Stream) SetContent(content []byte) {
	s.Content = content
	s.Dict.Set([]byte("Length"), Integer(int64(len(content))))
}

// GetPlainContent mirrors Stream::get_plain_content.
func (s *Stream) GetPlainContent() ([]byte, error) {
	filters, err := s.Filters()
	if err == nil && len(filters) > 0 {
		return s.DecompressedContent()
	}
	return append([]byte(nil), s.Content...), nil
}

// Decompress mirrors Stream::decompress: decode and strip the filter entries.
func (s *Stream) Decompress() error {
	data, err := s.DecompressedContent()
	if err != nil {
		return err
	}
	s.Dict.Remove([]byte("DecodeParms"))
	s.Dict.Remove([]byte("Filter"))
	s.SetContent(data)
	return nil
}

// IsCompressed mirrors Stream::is_compressed.
func (s *Stream) IsCompressed() bool {
	_, err := s.Dict.Get([]byte("Filter"))
	return err == nil
}

// DecompressedContent mirrors Stream::decompressed_content. Filters are
// applied in decoding order; unknown filters error. Zlib errors fall back to
// raw deflate (skipping the 2-byte header) exactly like lopdf.
func (s *Stream) DecompressedContent() ([]byte, error) {
	params, _ := s.Dict.Get([]byte("DecodeParms"))
	var paramsDict *Dictionary
	if params != nil {
		paramsDict, _ = params.AsDict()
	}
	filters, err := s.Filters()
	if err != nil {
		// No /Filter key (or malformed): the stream is uncompressed.
		return append([]byte(nil), s.Content...), nil
	}

	input := s.Content
	var output []byte
	for _, filter := range filters {
		switch string(filter) {
		case "FlateDecode":
			output = decompressZlib(input)
			var perr error
			output, perr = decompressPredictor(output, paramsDict)
			if perr != nil {
				return nil, perr
			}
		case "LZWDecode":
			var err2 error
			output, err2 = decompressLZW(input, paramsDict)
			if err2 != nil {
				return nil, err2
			}
			output, err2 = decompressPredictor(output, paramsDict)
			if err2 != nil {
				return nil, err2
			}
		case "ASCII85Decode":
			var err2 error
			output, err2 = decodeASCII85(input)
			if err2 != nil {
				return nil, err2
			}
		default:
			return nil, &Error{Kind: KindUnimplemented, UnimplementedMsg: "decompression algorithms"}
		}
		input = output
	}
	return output, nil
}

// DecodeContent mirrors Stream::decode_content: parse the (already
// filter-decoded) stream bytes as a content stream.
func (s *Stream) DecodeContent() (*Content, error) {
	return DecodeContent(s.Content)
}

// newRawDeflateReader wraps compress/flate for the raw-deflate retry path.
func newRawDeflateReader(input []byte) io.Reader {
	return flate.NewReader(bytes.NewReader(input))
}

// decompressZlib mirrors lopdf's decompress_zlib: partial output is kept even
// when the stream is corrupt; a failed zlib read falls back to raw deflate.
func decompressZlib(input []byte) []byte {
	var output []byte
	if len(input) != 0 {
		if r, err := zlib.NewReader(bytes.NewReader(input)); err == nil {
			out, rerr := io.ReadAll(r)
			output = out
			if rerr != nil {
				// Zlib decompression failed (e.g. corrupt adler32 checksum in
				// encrypted PDFs). Retry with raw deflate.
				if len(output) == 0 && len(input) > 2 {
					raw := newRawDeflateReader(input[2:])
					out2, rerr2 := io.ReadAll(raw)
					if rerr2 == nil || len(out2) > 0 {
						if len(out2) > 0 {
							output = out2
						}
					}
				}
			}
		} else {
			// zlib.NewReader itself failed (bad header). lopdf's
			// ZlibDecoder::read_to_end returns an error with empty output;
			// the raw-deflate retry then applies.
			if len(input) > 2 {
				raw := newRawDeflateReader(input[2:])
				out2, _ := io.ReadAll(raw)
				if len(out2) > 0 {
					output = out2
				}
			}
		}
	}
	return output
}

// decompressPredictor mirrors lopdf's decompress_predictor.
func decompressPredictor(data []byte, params *Dictionary) ([]byte, error) {
	if params == nil {
		return data, nil
	}
	predictor := int64(1)
	if o, err := params.Get([]byte("Predictor")); err == nil {
		if v, err2 := o.AsI64(); err2 == nil {
			predictor = v
		}
	}
	if predictor < 10 || predictor > 15 {
		return data, nil
	}
	pixelsPerRow := int64(1)
	if o, err := params.Get([]byte("Columns")); err == nil {
		if v, err2 := o.AsI64(); err2 == nil && v > 1 {
			pixelsPerRow = v
		}
	}
	colors := int64(1)
	if o, err := params.Get([]byte("Colors")); err == nil {
		if v, err2 := o.AsI64(); err2 == nil && v > 1 {
			colors = v
		}
	}
	bits := int64(8)
	if o, err := params.Get([]byte("BitsPerComponent")); err == nil {
		if v, err2 := o.AsI64(); err2 == nil && v > 8 {
			bits = v
		}
	}
	bytesPerPixel := int(colors * bits / 8)
	return decodePNGFrame(data, bytesPerPixel, int(pixelsPerRow))
}

// decodeASCII85 mirrors lopdf's decode_ascii85.
func decodeASCII85(input []byte) ([]byte, error) {
	var output []byte
	var buffer uint32
	count := 0
	inputNoEod := input
	if len(input) >= 2 && input[len(input)-2] == '~' && input[len(input)-1] == '>' {
		inputNoEod = input[:len(input)-2]
	}
	for _, ch := range inputNoEod {
		if ch == 'z' {
			if count != 0 {
				return nil, &Error{Kind: KindDecompress, Inner: &DecompressError{Detail: "z character is not allowed in the middle of a group"}}
			}
			output = append(output, 0, 0, 0, 0)
			continue
		}
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' || ch == 0x0C {
			continue
		}
		if ch < '!' || ch > 'u' {
			break
		}
		if buffer > 0xFFFFFFFF/85 {
			return nil, &Error{Kind: KindDecompress, Inner: &DecompressError{Detail: "multiplication overflow"}}
		}
		buffer = buffer*85 + uint32(ch-'!')
		count++
		if count == 5 {
			output = append(output, byte(buffer>>24), byte(buffer>>16), byte(buffer>>8), byte(buffer))
			buffer = 0
			count = 0
		}
	}
	if count > 0 {
		n := count
		for ; count < 5; count++ {
			if buffer > 0xFFFFFFFF/85 {
				return nil, &Error{Kind: KindDecompress, Inner: &DecompressError{Detail: "multiplication overflow"}}
			}
			buffer = buffer*85 + 84
		}
		bytes4 := [4]byte{byte(buffer >> 24), byte(buffer >> 16), byte(buffer >> 8), byte(buffer)}
		output = append(output, bytes4[:n-1]...)
	}
	return output, nil
}
