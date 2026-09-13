package anydoc

/*
#cgo CFLAGS: -I${SRCDIR}/native
#cgo linux,amd64 LDFLAGS: ${SRCDIR}/native/prebuilt/linux_amd64/libanydoc_ffi.a -ldl -lm -lpthread -lstdc++
#cgo linux,arm64 LDFLAGS: ${SRCDIR}/native/prebuilt/linux_arm64/libanydoc_ffi.a -ldl -lm -lpthread -lstdc++
#cgo darwin,amd64 LDFLAGS: ${SRCDIR}/native/prebuilt/darwin_amd64/libanydoc_ffi.a -framework Security -framework SystemConfiguration -ldl -lm -lpthread
#cgo darwin,arm64 LDFLAGS: ${SRCDIR}/native/prebuilt/darwin_arm64/libanydoc_ffi.a -framework Security -framework SystemConfiguration -ldl -lm -lpthread
#include "anydoc.h"
#include <stdlib.h>
*/
import "C"

import (
	"encoding/json"
	"unsafe"
)

func cBytes(data []byte) (*C.uint8_t, C.size_t) {
	if len(data) == 0 {
		return nil, 0
	}
	return (*C.uint8_t)(unsafe.Pointer(&data[0])), C.size_t(len(data))
}

func goString(p *C.char) string {
	if p == nil {
		return ""
	}
	return C.GoString(p)
}

func takeString(p *C.char) string {
	if p == nil {
		return ""
	}
	s := C.GoString(p)
	C.anydoc_string_free(p)
	return s
}

func takeError(err *C.anydoc_error) error {
	if err == nil || err.code == nil {
		return &ConvertError{Code: CodeMalformed, Message: "anydoc: empty error"}
	}
	ce := &ConvertError{
		Code:      goString(err.code),
		Message:   goString(err.message),
		PageCount: uint32(err.page_count),
		Part:      goString(err.part),
		Limit:     goString(err.limit),
	}
	if err.pages != nil && err.pages_len > 0 {
		n := int(err.pages_len)
		ce.Pages = make([]uint32, n)
		src := unsafe.Slice((*uint32)(unsafe.Pointer(err.pages)), n)
		copy(ce.Pages, src)
	}
	C.anydoc_error_free(err)
	return ce
}

func formatFromC(ok C.int, out *C.char) (Format, bool) {
	if ok != 1 {
		if out != nil {
			C.anydoc_string_free(out)
		}
		return "", false
	}
	if out == nil {
		return "", false
	}
	return Format(takeString(out)), true
}

func formatFromBytes(data []byte) (Format, bool) {
	ptr, n := cBytes(data)
	var out *C.char
	ok := C.anydoc_format_from_bytes(ptr, n, &out)
	return formatFromC(ok, out)
}

func formatFromExtension(extension string) (Format, bool) {
	c := C.CString(extension)
	defer C.free(unsafe.Pointer(c))
	var out *C.char
	ok := C.anydoc_format_from_extension(c, &out)
	return formatFromC(ok, out)
}

func formatFromPath(path string) (Format, bool) {
	c := C.CString(path)
	defer C.free(unsafe.Pointer(c))
	var out *C.char
	ok := C.anydoc_format_from_path(c, &out)
	return formatFromC(ok, out)
}

func toMarkdown(path string) (string, error) {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	var out *C.char
	var err C.anydoc_error
	if C.anydoc_to_markdown(cpath, &out, &err) != 1 {
		return "", takeError(&err)
	}
	return takeString(out), nil
}

func toMarkdownBytes(data []byte, format Format) (string, error) {
	ptr, n := cBytes(data)
	var cformat *C.char
	if format != "" {
		cformat = C.CString(string(format))
		defer C.free(unsafe.Pointer(cformat))
	}
	var out *C.char
	var err C.anydoc_error
	if C.anydoc_to_markdown_bytes(ptr, n, cformat, &out, &err) != 1 {
		return "", takeError(&err)
	}
	return takeString(out), nil
}

func toDocument(data []byte, format Format) (Document, error) {
	ptr, n := cBytes(data)
	var cformat *C.char
	if format != "" {
		cformat = C.CString(string(format))
		defer C.free(unsafe.Pointer(cformat))
	}
	var out *C.char
	var err C.anydoc_error
	if C.anydoc_to_document_json(ptr, n, cformat, &out, &err) != 1 {
		return Document{}, takeError(&err)
	}
	raw := takeString(out)
	var doc Document
	if e := json.Unmarshal([]byte(raw), &doc); e != nil {
		return Document{}, &ConvertError{Code: CodeMalformed, Message: "document json: " + e.Error()}
	}
	return doc, nil
}
