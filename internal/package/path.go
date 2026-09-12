// Package target resolution per OPC/EPUB URI semantics, applied before any
// archive lookup.
//
// This file ports src/package/path.rs.
//
// The reference is treated as a URI: fragment and query split off first,
// then each path segment is percent-decoded *after* segmentation. A segment
// whose decoded form would introduce structure (`/`, `\`, `.`, `..`) is
// rejected - encoded traversal never becomes path structure.
package pkg

import (
	"fmt"
	"strings"

	"github.com/m7medVision/anydoc-go/internal/cerr"
	"github.com/m7medVision/anydoc-go/internal/text"
)

// Target is a resolved package reference.
type Target struct {
	// Path is the normalized archive path (no leading slash).
	Path string
	// Fragment is the reference's #fragment, percent-decoded. HasFragment
	// distinguishes an empty fragment ("doc.xml#") from none ("doc.xml").
	Fragment    string
	HasFragment bool
}

// Resolve resolves a relative or package-absolute reference against the
// part it appears in.
func Resolve(basePart, reference string) (Target, error) {
	ref := reference
	var target Target
	if r, fragment, found := strings.Cut(ref, "#"); found {
		ref = r
		target.Fragment = DecodeFragment(fragment)
		target.HasFragment = true
	}
	if r, _, found := strings.Cut(ref, "?"); found {
		ref = r
	}
	if ref == "" {
		// Fragment-only reference: the target is the base part itself.
		return Target{Path: basePart, Fragment: target.Fragment, HasFragment: target.HasFragment}, nil
	}

	var segments []string
	if !strings.HasPrefix(ref, "/") {
		// Start from the base part's directory.
		if idx := strings.LastIndex(basePart, "/"); idx >= 0 {
			for _, s := range strings.Split(basePart[:idx], "/") {
				if s != "" {
					segments = append(segments, s)
				}
			}
		}
	}
	for _, raw := range strings.Split(ref, "/") {
		switch raw {
		case "", ".":
		case "..":
			// Dot segments resolve against the base, clamped at the package
			// root (a traversal above root is producer sloppiness in the
			// wild; clamping preserves OPC behavior).
			if len(segments) > 0 {
				segments = segments[:len(segments)-1]
			}
		default:
			decoded := decodeComponent(raw)
			if strings.ContainsAny(decoded, `/\`) {
				return Target{}, &cerr.Error{
					Kind: cerr.KindMalformed,
					Detail: fmt.Sprintf(
						"percent-encoded separator in package reference segment %q", raw),
				}
			}
			if decoded == "." || decoded == ".." {
				return Target{}, &cerr.Error{
					Kind: cerr.KindMalformed,
					Detail: fmt.Sprintf(
						"percent-encoded traversal in package reference segment %q", raw),
				}
			}
			segments = append(segments, decoded)
		}
	}
	target.Path = strings.Join(segments, "/")
	return target, nil
}

// decodeComponent percent-decodes one URI component. Infallible by design:
// a `%` not followed by two hex digits passes through literally (producers
// emit such names), and non-UTF-8 decoded bytes degrade lossily - the
// result is only ever matched against archive entry names, where a
// near-miss simply fails the lookup.
func decodeComponent(component string) string {
	if !strings.Contains(component, "%") {
		return component
	}
	out := make([]byte, 0, len(component))
	for i := 0; i < len(component); i++ {
		if component[i] == '%' && i+2 < len(component) && isHexDigit(component[i+1]) && isHexDigit(component[i+2]) {
			out = append(out, hexVal(component[i+1])<<4|hexVal(component[i+2]))
			i += 2
			continue
		}
		out = append(out, component[i])
	}
	return text.DecodeUTF8(out)
}

// DecodeFragment percent-decodes a URI fragment.
func DecodeFragment(fragment string) string {
	return decodeComponent(fragment)
}

func isHexDigit(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func hexVal(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}
