package anydoc

// ConvertError is a complete conversion that was impossible. Catch this to
// handle every kind of failure. An unreadable file is still a ConvertError
// with Code IO.
type ConvertError struct {
	Code      string
	Message   string
	Pages     []uint32
	PageCount uint32
	Part      string
	Limit     string
}

func (e *ConvertError) Error() string {
	if e == nil {
		return "convert error"
	}
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

const (
	CodeUnsupported   = "unsupported"
	CodeNeedsOcr      = "needsOcr"
	CodeMalformed     = "malformed"
	CodeEncrypted     = "encrypted"
	CodeResourceLimit = "resourceLimit"
	CodeMissingPart   = "missingPart"
	CodeIO            = "io"
)

func (e *ConvertError) Is(target error) bool {
	t, ok := target.(*ConvertError)
	if !ok || e == nil || t == nil {
		return false
	}
	if t.Code != "" && e.Code != t.Code {
		return false
	}
	return true
}
