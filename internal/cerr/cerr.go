// Package cerr holds anydoc's typed conversion errors (src/error.rs).
//
// An error means a complete conversion was impossible: the input was
// unreadable or structurally unusable, encrypted, or crossed a fixed
// safety/resource limit. Recoverable producer quirks never surface here -
// they are recovered or skipped while conversion continues.
package cerr

import (
	"fmt"
	"strconv"
	"strings"
)

// ErrorKind names why a conversion could not produce a useful result. The
// kind is both the sentinel and the machine-readable code: callers branch
// with errors.Is(err, cerr.KindEncrypted) and publish Code() as error.code.
type ErrorKind string

const (
	// KindUnsupported: the format is unknown or cannot be converted at all.
	KindUnsupported ErrorKind = "unsupported"
	// KindNeedsOcr: some pages of a PDF are scanned or image-only and need
	// OCR, which anydoc does not do. Nothing is returned for the document,
	// so output missing those pages never passes as complete.
	KindNeedsOcr ErrorKind = "needsOcr"
	// KindMalformed: the document is structurally unusable - no meaningful
	// content could be extracted.
	KindMalformed ErrorKind = "malformed"
	// KindEncrypted: the document is encrypted or password-protected.
	KindEncrypted ErrorKind = "encrypted"
	// KindResourceLimit: a fixed safety limit was exceeded (decompression,
	// nesting depth, node count, repeat expansion, retained asset bytes).
	// These are hard errors in every case.
	KindResourceLimit ErrorKind = "resourceLimit"
	// KindMissingPart: a part required for any meaningful output is missing.
	KindMissingPart ErrorKind = "missingPart"
	// KindIO: the input could not be read. Only the path-based entry
	// produces it.
	KindIO ErrorKind = "io"
)

// Error makes a kind usable directly as a sentinel value.
func (k ErrorKind) Error() string { return string(k) }

// Error is a typed conversion failure. Only the fields its Kind names carry
// meaning; the others stay zero.
type Error struct {
	Kind ErrorKind
	// Detail is the Unsupported, Malformed, and ResourceLimit payload.
	Detail string
	// Part names the package part or stream for Malformed ("" = none) and
	// MissingPart.
	Part string
	// Limit names the ResourceLimit that was hit.
	Limit string
	// Pages are the 1-indexed pages that need OCR, ascending.
	Pages []int
	// PageCount is the number of pages in the document (NeedsOcr).
	PageCount int
	// Err wraps the underlying IO failure (*fs.PathError, ...).
	Err error
}

// Code is the stable, machine-readable name for the kind: what a caller
// branches on, where Error carries the detail.
func (e *Error) Code() string { return string(e.Kind) }

// Error renders the message exactly as anydoc's ConvertError Display does.
func (e *Error) Error() string {
	switch e.Kind {
	case KindUnsupported:
		return "unsupported input: " + e.Detail
	case KindNeedsOcr:
		switch {
		case len(e.Pages) == 1:
			return fmt.Sprintf("page %d of %d needs OCR", e.Pages[0], e.PageCount)
		case len(e.Pages) >= e.PageCount:
			return fmt.Sprintf("all %d pages need OCR", e.PageCount)
		default:
			return fmt.Sprintf("pages %s of %d need OCR", pageRanges(e.Pages), e.PageCount)
		}
	case KindMalformed:
		if e.Part != "" {
			return fmt.Sprintf("malformed document (%s): %s", e.Part, e.Detail)
		}
		return "malformed document: " + e.Detail
	case KindEncrypted:
		return "document is encrypted"
	case KindResourceLimit:
		return fmt.Sprintf("resource limit exceeded (%s): %s", e.Limit, e.Detail)
	case KindMissingPart:
		return "missing required part: " + e.Part
	case KindIO:
		return "io error: " + e.Err.Error()
	}
	return string(e.Kind)
}

// Is matches ErrorKind targets so errors.Is(err, cerr.KindEncrypted) works;
// the wrapped IO chain stays reachable through Unwrap.
func (e *Error) Is(target error) bool {
	if kind, ok := target.(ErrorKind); ok {
		return kind == e.Kind
	}
	return false
}

// Unwrap exposes the underlying IO error, if any.
func (e *Error) Unwrap() error { return e.Err }

// IsFatal reports whether recovery must not swallow this error: fixed
// safety limits hard-fail in every context, including optional parts.
func (e *Error) IsFatal() bool { return e.Kind == KindResourceLimit }

// pageRanges renders "2, 5-7, 12" from ascending page numbers.
func pageRanges(pages []int) string {
	ranges := make([]string, 0, len(pages))
	for i := 0; i < len(pages); {
		start := pages[i]
		end := start
		for i+1 < len(pages) && pages[i+1] == end+1 {
			i++
			end++
		}
		i++
		if end > start {
			ranges = append(ranges, strconv.Itoa(start)+"-"+strconv.Itoa(end))
		} else {
			ranges = append(ranges, strconv.Itoa(start))
		}
	}
	return strings.Join(ranges, ", ")
}
