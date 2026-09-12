package formats

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/richardlehane/mscfb"
	"golang.org/x/text/encoding/ianaindex"
)

// Container signatures and the identity namespaces detection matches.
var (
	oleMagic = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}
	pdfMagic = []byte("%PDF-")
	rtfMagic = []byte(`{\rtf`)
	zipMagic = []byte("PK\x03\x04")
)

const (
	ctNS       = "http://schemas.openxmlformats.org/package/2006/content-types"
	manifestNS = "urn:oasis:names:tc:opendocument:xmlns:manifest:1.0"
	nsP        = "http://schemas.openxmlformats.org/presentationml/2006/main"
	nsW        = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	pkgRelsNS  = "http://schemas.openxmlformats.org/package/2006/relationships"
	spreadshNS = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"

	// officeDocumentRel is the package-level OPC relationship type that
	// designates the main document part.
	officeDocumentRel = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument"

	// pdfHeaderWindow bounds how far into the file a `%PDF-` header is
	// accepted (ISO 32000; implementations allow leading junk).
	pdfHeaderWindow = 1024
)

// FromBytes detects the format from the content itself: the signature
// and identity each container specification designates, never heuristics
// over document content.
//
//   - PDF: the `%PDF-` header (ISO 32000; implementations accept leading
//     junk, bounded here at 1024 bytes).
//   - RTF: the `{\rtf` group that must open every RTF file.
//   - OLE compound files: the [MS-CFB] signature, then the stream name
//     each binary Office format mandates ([MS-DOC] `WordDocument`,
//     [MS-PPT] `PowerPoint Document`, [MS-XLS] `Workbook`/`Book`).
//   - ZIP packages: the local-file-header signature, then the package's
//     own identity: the `mimetype` part (ODF/EPUB OCF), or for OPC the
//     content type of the part the package-level officeDocument
//     relationship designates as the main document (with the main part's
//     mandated root element as the authority when content types are
//     stale or generic).
//
// Plain-text formats (CSV) carry no signature and are never detected;
// callers fall back to the file extension. Detection never errors: any
// unreadable or ambiguous container goes undetected and the caller's
// fallback (extension, then the frontend's own error) applies.
func FromBytes(data []byte) (Format, bool) {
	if bytes.HasPrefix(data, rtfMagic) {
		return RTF, true
	}
	if bytes.HasPrefix(data, oleMagic) {
		return detectOLE(data)
	}
	if bytes.HasPrefix(data, zipMagic) {
		return detectZip(data)
	}
	header := data
	if len(header) > pdfHeaderWindow {
		header = header[:pdfHeaderWindow]
	}
	if bytes.Contains(header, pdfMagic) {
		return PDF, true
	}
	return Auto, false
}

// detectOLE classifies an OLE compound file by its mandated content
// stream. Encrypted OOXML packages (`EncryptedPackage`) stay
// undetected: the inner format is unknowable, and the frontend reports
// encrypted precisely.
func detectOLE(data []byte) (Format, bool) {
	cfb, err := mscfb.New(bytes.NewReader(data))
	if err != nil {
		return Auto, false
	}
	// Stream-name comparison is case-insensitive, matching CFB's own
	// uppercase name comparisons; producers vary (`WORKBOOK`, `BOOK`).
	for {
		entry, err := cfb.Next()
		if err != nil {
			break
		}
		switch {
		case asciiEqualFold(entry.Name, "WordDocument"):
			return Doc, true
		case asciiEqualFold(entry.Name, "PowerPoint Document"):
			return Ppt, true
		case asciiEqualFold(entry.Name, "Workbook"), asciiEqualFold(entry.Name, "Book"):
			return Excel, true
		}
	}
	return Auto, false
}

// Fixed read limits, identical to the package layer's, so detection
// degrades on the same inputs instead of decompressing without bound.
const (
	maxEntryBytes = 128 << 20
	maxTotalBytes = 512 << 20
	maxEntryCount = 100_000
)

// zipPackage is limited central-directory access over a ZIP archive: the
// subset of the package layer detection goes through.
type zipPackage struct {
	r         *zip.Reader
	totalRead uint64
}

// part reads a part's bytes. ok is false when the part is absent or
// unreadable — the unified optional-parts policy: an unreadable part is
// skipped, and the next authority decides.
func (p *zipPackage) part(name string) ([]byte, bool) {
	// OPC part URIs may carry a leading slash; entries never do.
	name = strings.TrimLeft(name, "/")
	var file *zip.File
	for _, f := range p.r.File {
		if f.Name == name {
			file = f
			break
		}
	}
	if file == nil {
		return nil, false
	}
	if file.UncompressedSize64 > maxEntryBytes {
		return nil, false
	}
	// The declared size can lie; read through a hard-capped reader. The
	// cap is whichever budget has less room: the per-entry limit or what
	// remains of the whole-archive total.
	remaining := maxTotalBytes - p.totalRead
	if remaining > maxEntryBytes {
		remaining = maxEntryBytes
	}
	rc, err := file.Open()
	if err != nil {
		return nil, false
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, int64(remaining)+1))
	if err != nil || uint64(len(data)) > remaining {
		return nil, false
	}
	p.totalRead += uint64(len(data))
	return data, true
}

// optionalXMLPart reads and parses an optional XML part; absent,
// unreadable, or corrupt parts are skipped.
func (p *zipPackage) optionalXMLPart(name string) ([]xmlElem, bool) {
	data, ok := p.part(name)
	if !ok {
		return nil, false
	}
	elems, err := scanXML(data)
	if err != nil {
		return nil, false
	}
	return elems, true
}

// hasPart reports whether a part exists, without reading it.
func (p *zipPackage) hasPart(name string) bool {
	name = strings.TrimLeft(name, "/")
	for _, f := range p.r.File {
		if f.Name == name {
			return true
		}
	}
	return false
}

// detectZip classifies a ZIP package by its own identity: the `mimetype`
// part (ODF/EPUB OCF), or for OPC the content type of the part the
// package-level officeDocument relationship designates as the main
// document, with the main part's mandated root element as the authority
// when content types are stale or generic.
func detectZip(data []byte) (Format, bool) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return Auto, false
	}
	if len(zr.File) > maxEntryCount {
		return Auto, false
	}
	pkg := &zipPackage{r: zr}

	// ODF and EPUB designate the `mimetype` part as the package identity;
	// its verdict is final, even when it names nothing recognized.
	if mime, ok := pkg.part("mimetype"); ok {
		if !utf8.Valid(mime) {
			return Auto, false
		}
		return mimetypeFormat(strings.TrimSpace(string(mime)))
	}

	// OPC: the package-level officeDocument relationship designates the
	// main part; its content type names the document kind.
	if rel, ok := firstOfType(readRels(pkg, "_rels/.rels"), officeDocumentRel); ok {
		if tgt, err := resolveTarget("", rel.target); err == nil {
			if types, ok := pkg.optionalXMLPart("[Content_Types].xml"); ok {
				if ct, ok := contentTypeOf(types, tgt.path); ok {
					if format, ok := opcFormat(ct); ok {
						return format, true
					}
				}
			}
			// Content types can be stale or generic; the main part's
			// mandated root element (`w:document`, `p:presentation`,
			// `workbook`) is the next authority.
			if tree, ok := pkg.optionalXMLPart(tgt.path); ok && len(tree) > 0 {
				if format, ok := rootElementFormat(tree[0]); ok {
					return format, true
				}
			}
			// Binary main parts (`xl/workbook.bin`) and unreadable ones:
			// the conventional locations the frontends also fall back to.
			return opcFormatByPath(tgt.path)
		}
	}

	// Packages with no usable rels: conventional main-part locations.
	for _, candidate := range [...]struct {
		part   string
		format Format
	}{
		{"word/document.xml", Docx},
		{"ppt/presentation.xml", Pptx},
		{"xl/workbook.xml", Excel},
		{"xl/workbook.bin", Excel},
	} {
		if pkg.hasPart(candidate.part) {
			return candidate.format, true
		}
	}

	// ODF without its mandatory `mimetype`: the manifest's root file
	// entry carries the same media type.
	if manifest, ok := pkg.optionalXMLPart("META-INF/manifest.xml"); ok {
		for _, entry := range manifest {
			if !entry.is(manifestNS, "file-entry") {
				continue
			}
			if path, ok := entry.attr(manifestNS, "full-path"); ok && path == "/" {
				if mime, ok := entry.attr(manifestNS, "media-type"); ok {
					return mimetypeFormat(strings.TrimSpace(mime))
				}
				break
			}
		}
	}

	// EPUB without its mandatory `mimetype`: the OCF container descriptor.
	if pkg.hasPart("META-INF/container.xml") {
		return Epub, true
	}

	return Auto, false
}

// mimetypeFormat maps an ODF/EPUB mimetype onto its format. The
// `-template` variants share the base format's parser.
func mimetypeFormat(mime string) (Format, bool) {
	mime = strings.TrimSuffix(mime, "-template")
	switch mime {
	case "application/epub+zip":
		return Epub, true
	case "application/vnd.oasis.opendocument.text":
		return Odt, true
	case "application/vnd.oasis.opendocument.spreadsheet":
		return Ods, true
	case "application/vnd.oasis.opendocument.presentation":
		return Odp, true
	}
	return Auto, false
}

// contentTypeOf resolves a part's content type per OPC: an Override for
// the exact part name wins, else the Default for its extension.
func contentTypeOf(types []xmlElem, part string) (string, bool) {
	partName := "/" + part
	for _, e := range types {
		if !e.is(ctNS, "Override") {
			continue
		}
		if name, ok := e.attrAny("PartName"); ok && asciiEqualFold(name, partName) {
			if ct, ok := e.attrAny("ContentType"); ok {
				return ct, true
			}
			break
		}
	}
	ext := ""
	if dot := strings.LastIndexByte(part, '.'); dot >= 0 {
		ext = part[dot+1:]
	} else {
		return "", false
	}
	for _, e := range types {
		if !e.is(ctNS, "Default") {
			continue
		}
		if x, ok := e.attrAny("Extension"); ok && asciiEqualFold(x, ext) {
			if ct, ok := e.attrAny("ContentType"); ok {
				return ct, true
			}
			return "", false
		}
	}
	return "", false
}

// opcFormat maps a main-part content type onto its parser. The family
// segment covers every variant (document, template, macro-enabled,
// slideshow); `ms-excel` covers the binary `.xlsb` main part.
func opcFormat(contentType string) (Format, bool) {
	ct := asciiLower(contentType)
	switch {
	case strings.Contains(ct, "wordprocessingml"):
		return Docx, true
	case strings.Contains(ct, "presentationml"):
		return Pptx, true
	case strings.Contains(ct, "spreadsheetml"), strings.Contains(ct, "ms-excel"):
		return Excel, true
	}
	return Auto, false
}

// rootElementFormat maps a main part's mandated root element onto its
// format via its namespace (already normalized from Strict to
// Transitional at parse time).
func rootElementFormat(root xmlElem) (Format, bool) {
	switch root.ns {
	case nsW:
		return Docx, true
	case nsP:
		return Pptx, true
	case spreadshNS:
		return Excel, true
	}
	return Auto, false
}

// opcFormatByPath maps a conventional main-part location onto its
// parser: binary main parts (`xl/workbook.bin`) and unreadable ones fall
// back here.
func opcFormatByPath(part string) (Format, bool) {
	switch {
	case strings.HasPrefix(part, "word/"):
		return Docx, true
	case strings.HasPrefix(part, "ppt/"):
		return Pptx, true
	case strings.HasPrefix(part, "xl/"):
		return Excel, true
	}
	return Auto, false
}

// relationship is one OPC relationship, as a relationships part loads it.
type relationship struct {
	id       string
	target   string
	relType  string
	external bool
}

// readRels reads a relationships part; an absent, unreadable, or corrupt
// part yields no relationships. Strict relationship-type URIs are
// normalized onto the Transitional forms.
func readRels(pkg *zipPackage, part string) []relationship {
	elems, ok := pkg.optionalXMLPart(part)
	if !ok {
		return nil
	}
	byID := make(map[string]relationship)
	for _, e := range elems {
		if !e.is(pkgRelsNS, "Relationship") {
			continue
		}
		id, ok := e.attrAny("Id")
		if !ok {
			continue
		}
		target, ok := e.attrAny("Target")
		if !ok {
			continue
		}
		external := false
		if mode, ok := e.attrAny("TargetMode"); ok {
			external = asciiEqualFold(mode, "external")
		}
		relType, _ := e.attrAny("Type")
		if normalized, ok := normalizeOOXMLURI(relType); ok {
			relType = normalized
		}
		byID[id] = relationship{id: id, target: target, relType: relType, external: external}
	}
	rels := make([]relationship, 0, len(byID))
	for _, r := range byID {
		rels = append(rels, r)
	}
	return rels
}

// firstOfType returns the internal-mode relationship of a given type,
// lowest id first so the pick is deterministic when a producer emits
// duplicates.
func firstOfType(rels []relationship, relType string) (relationship, bool) {
	var best relationship
	found := false
	for _, r := range rels {
		if r.relType != relType || r.external {
			continue
		}
		if !found || r.id < best.id {
			best, found = r, true
		}
	}
	return best, found
}

// target is a resolved package reference.
type target struct {
	// path is the normalized archive path (no leading slash).
	path     string
	fragment string
}

// resolveTarget resolves a relative or package-absolute reference against
// the part it appears in, per OPC/EPUB URI semantics, applied before any
// archive lookup. The reference is treated as a URI: fragment and query
// split off first, then each path segment is percent-decoded after
// segmentation. A segment whose decoded form would introduce structure
// (`/`, `\`, `.`, `..`) is rejected — encoded traversal never becomes
// path structure.
func resolveTarget(basePart, reference string) (target, error) {
	var t target
	if i := strings.IndexByte(reference, '#'); i >= 0 {
		t.fragment = decodeComponent(reference[i+1:])
		reference = reference[:i]
	}
	if i := strings.IndexByte(reference, '?'); i >= 0 {
		reference = reference[:i]
	}
	if reference == "" {
		// Fragment-only reference: the target is the base part itself.
		t.path = basePart
		return t, nil
	}
	var segments []string
	if reference[0] != '/' {
		// Start from the base part's directory.
		if dirEnd := strings.LastIndexByte(basePart, '/'); dirEnd >= 0 {
			for _, s := range strings.Split(basePart[:dirEnd], "/") {
				if s != "" {
					segments = append(segments, s)
				}
			}
		}
	}
	for _, raw := range strings.Split(reference, "/") {
		switch raw {
		case "", ".":
		case "..":
			// Dot segments resolve against the base, clamped at the
			// package root (a traversal above root is producer sloppiness
			// in the wild; clamping preserves OPC behavior).
			if len(segments) > 0 {
				segments = segments[:len(segments)-1]
			}
		default:
			decoded := decodeComponent(raw)
			if strings.ContainsAny(decoded, `/\`) {
				return t, fmt.Errorf("percent-encoded separator in package reference segment %q", raw)
			}
			if decoded == "." || decoded == ".." {
				return t, fmt.Errorf("percent-encoded traversal in package reference segment %q", raw)
			}
			segments = append(segments, decoded)
		}
	}
	t.path = strings.Join(segments, "/")
	return t, nil
}

// decodeComponent percent-decodes one URI component. Infallible by
// design: a '%' not followed by two hex digits passes through literally
// (producers emit such names), and non-UTF-8 decoded bytes stay raw —
// the result is only ever matched against archive entry names, where a
// near-miss simply fails the lookup.
func decodeComponent(component string) string {
	if !strings.Contains(component, "%") {
		return component
	}
	out := make([]byte, 0, len(component))
	for i := 0; i < len(component); i++ {
		if component[i] == '%' && i+2 < len(component) {
			hi, okHi := hexVal(component[i+1])
			lo, okLo := hexVal(component[i+2])
			if okHi && okLo {
				out = append(out, hi<<4|lo)
				i += 2
				continue
			}
		}
		out = append(out, component[i])
	}
	return string(out)
}

func hexVal(b byte) (byte, bool) {
	switch {
	case '0' <= b && b <= '9':
		return b - '0', true
	case 'a' <= b && b <= 'f':
		return b - 'a' + 10, true
	case 'A' <= b && b <= 'F':
		return b - 'A' + 10, true
	}
	return 0, false
}

// normalizeOOXMLURI maps ISO/IEC 29500 Strict namespace and
// relationship-type URIs onto their Transitional forms: the Strict
// family is the Transitional one re-rooted under
// http://purl.oclc.org/ooxml/ with the `2006` version segment dropped.
// Normalizing to the Transitional family at parse time lets the rest of
// the package match a single set of constants.
func normalizeOOXMLURI(uri string) (string, bool) {
	rest, ok := strings.CutPrefix(uri, "http://purl.oclc.org/ooxml/")
	if !ok {
		return "", false
	}
	family, tail, ok := strings.Cut(rest, "/")
	if !ok {
		return "", false
	}
	return "http://schemas.openxmlformats.org/" + family + "/2006/" + tail, true
}

// asciiEqualFold compares two strings, folding ASCII case only (Rust's
// eq_ignore_ascii_case).
func asciiEqualFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// xmlElem is one element with its resolved namespace and attributes: the
// subset of the package layer's XML DOM that detection consumes.
type xmlElem struct {
	ns    string // resolved namespace URI, "" when unqualified
	local string
	attrs []xmlAttr
}

// xmlAttr is one attribute with its resolved namespace.
type xmlAttr struct {
	ns    string
	local string
	value string
}

// is reports whether the element has the given namespace and local name.
func (e xmlElem) is(ns, local string) bool {
	return e.local == local && e.ns == ns
}

// attrAny is an attribute lookup by local name regardless of namespace,
// for elements whose vocabulary is unambiguous in context
// (relationships, content types).
func (e xmlElem) attrAny(local string) (string, bool) {
	for i := range e.attrs {
		if e.attrs[i].local == local {
			return e.attrs[i].value, true
		}
	}
	return "", false
}

// attr is a same-vocabulary attribute lookup: the qualified attribute
// wins, and an explicitly unqualified one with the same local name is
// accepted as a deliberate leniency — schemas often leave their own
// attributes unqualified and producers vary.
func (e xmlElem) attr(ns, local string) (string, bool) {
	for i := range e.attrs {
		if a := &e.attrs[i]; a.local == local && a.ns == ns {
			return a.value, true
		}
	}
	for i := range e.attrs {
		if a := &e.attrs[i]; a.local == local && a.ns == "" {
			return a.value, true
		}
	}
	return "", false
}

// errUnparseableXML marks a part that cannot be tokenized at all.
var errUnparseableXML = errors.New("formats: unparseable xml")

var (
	cdataOpen    = []byte("![CDATA[")
	cdataClose   = []byte("]]>")
	commentOpen  = []byte("!--")
	commentClose = []byte("-->")
	piClose      = []byte("?>")
	xmlnsName    = []byte("xmlns")
	xmlnsPrefix  = []byte("xmlns:")
)

// scanXML tokenizes an XML part into its elements, in document order,
// with namespaces resolved per XML scoping (Strict OOXML URIs normalized
// onto their Transitional forms, as the package layer's parse does). It
// mirrors that parse's deliberate leniency: CDATA, comments, and
// processing instructions are skipped, unclosed and mismatched tags are
// repaired rather than rejected, and malformed attributes degrade to
// absent. A part that cannot be tokenized at all is an error, which
// callers treat as an absent part.
func scanXML(data []byte) ([]xmlElem, error) {
	data = toUTF8(data)
	var elems []xmlElem
	var stack []nsScope
	for i := 0; i < len(data); {
		off := bytes.IndexByte(data[i:], '<')
		if off < 0 {
			break
		}
		i += off
		if i+1 >= len(data) {
			return nil, errUnparseableXML
		}
		switch c := data[i+1]; {
		case c == '!':
			if bytes.HasPrefix(data[i+1:], commentOpen) {
				if end := bytes.Index(data[i:], commentClose); end >= 0 {
					i += end + len(commentClose)
					continue
				}
				return nil, errUnparseableXML
			}
			if bytes.HasPrefix(data[i+1:], cdataOpen) {
				if end := bytes.Index(data[i:], cdataClose); end >= 0 {
					i += end + len(cdataClose)
					continue
				}
				return nil, errUnparseableXML
			}
			if end := bytes.IndexByte(data[i:], '>'); end >= 0 {
				i += end + 1
				continue
			}
			return nil, errUnparseableXML
		case c == '?':
			if end := bytes.Index(data[i:], piClose); end >= 0 {
				i += end + len(piClose)
				continue
			}
			return nil, errUnparseableXML
		case c == '/':
			// The parse never checks end names: every end tag pops one
			// scope, whatever it claims to close.
			if end := bytes.IndexByte(data[i:], '>'); end >= 0 {
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
				i += end + 1
				continue
			}
			return nil, errUnparseableXML
		default:
			elem, decls, empty, next, err := scanStartTag(data, i, stack)
			if err != nil {
				return nil, err
			}
			elems = append(elems, elem)
			if !empty {
				stack = append(stack, decls)
			}
			i = next
		}
	}
	return elems, nil
}

// nsScope is the set of namespace declarations one element contributes.
type nsScope struct {
	prefixes   map[string]string
	defaultNS  string
	hasDefault bool
}

// rawAttr is an attribute as it appears in the tag, before namespace
// resolution.
type rawAttr struct {
	name  []byte
	value []byte
}

// scanStartTag parses the start (or empty) tag whose '<' is at data[i],
// against the enclosing namespace scopes. It returns the element, the
// declarations the tag contributes (pushed unless it is empty), whether
// the tag was self-closing, and the offset just past the tag.
func scanStartTag(data []byte, i int, stack []nsScope) (xmlElem, nsScope, bool, int, error) {
	var elem xmlElem
	var decls nsScope
	// Find the tag's end, respecting quoted attribute values.
	quote := byte(0)
	end := -1
	for j := i + 1; j < len(data); j++ {
		c := data[j]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			continue
		}
		if c == '>' {
			end = j
			break
		}
	}
	if end < 0 {
		return elem, decls, false, 0, errUnparseableXML
	}
	empty := end > i+1 && data[end-1] == '/'
	body := data[i+1 : end]
	if empty {
		body = body[:len(body)-1]
	}

	nameEnd := 0
	for nameEnd < len(body) && !isXMLSpace(body[nameEnd]) {
		nameEnd++
	}
	prefix, local := splitQName(body[:nameEnd])
	rest := body[nameEnd:]

	var rawAttrs []rawAttr
	p := 0
	for p < len(rest) {
		for p < len(rest) && isXMLSpace(rest[p]) {
			p++
		}
		if p >= len(rest) {
			break
		}
		q := p
		for q < len(rest) && !isXMLSpace(rest[q]) && rest[q] != '=' {
			q++
		}
		name := rest[p:q]
		p = q
		for p < len(rest) && isXMLSpace(rest[p]) {
			p++
		}
		var value []byte
		if p < len(rest) && rest[p] == '=' {
			p++
			for p < len(rest) && isXMLSpace(rest[p]) {
				p++
			}
			if p < len(rest) && (rest[p] == '"' || rest[p] == '\'') {
				qc := rest[p]
				p++
				v0 := p
				for p < len(rest) && rest[p] != qc {
					p++
				}
				value = rest[v0:p]
				if p < len(rest) {
					p++
				}
			} else {
				v0 := p
				for p < len(rest) && !isXMLSpace(rest[p]) {
					p++
				}
				value = rest[v0:p]
			}
		}
		if len(name) > 0 {
			rawAttrs = append(rawAttrs, rawAttr{name, value})
		}
	}

	// Namespace declarations are consumed here; the element carries only
	// resolved names.
	for _, a := range rawAttrs {
		switch {
		case bytes.Equal(a.name, xmlnsName):
			decls.defaultNS, decls.hasDefault = decodeAttrValue(a.value), true
		case bytes.HasPrefix(a.name, xmlnsPrefix):
			if decls.prefixes == nil {
				decls.prefixes = make(map[string]string)
			}
			decls.prefixes[string(a.name[len(xmlnsPrefix):])] = decodeAttrValue(a.value)
		}
	}
	elem.local = local
	if prefix != "" {
		elem.ns = internNS(lookupPrefix(decls, stack, prefix))
	} else {
		elem.ns = internNS(lookupDefault(decls, stack))
	}
	for _, a := range rawAttrs {
		if bytes.Equal(a.name, xmlnsName) || bytes.HasPrefix(a.name, xmlnsPrefix) {
			continue
		}
		aprefix, alocal := splitQName(a.name)
		ans := ""
		if aprefix != "" {
			ans = internNS(lookupPrefix(decls, stack, aprefix))
		}
		elem.attrs = append(elem.attrs, xmlAttr{ns: ans, local: alocal, value: decodeAttrValue(a.value)})
	}
	return elem, decls, empty, end + 1, nil
}

// internNS resolves an element or attribute namespace, normalizing
// ISO/IEC 29500 Strict URIs onto their Transitional forms so the rest of
// the package matches a single set of constants.
func internNS(uri string) string {
	if normalized, ok := normalizeOOXMLURI(uri); ok {
		return normalized
	}
	return uri
}

// lookupPrefix resolves a namespace prefix against the element's own
// declarations, then the enclosing scopes, innermost first.
func lookupPrefix(decls nsScope, stack []nsScope, prefix string) string {
	if uri, ok := decls.prefixes[prefix]; ok {
		return uri
	}
	for i := len(stack) - 1; i >= 0; i-- {
		if uri, ok := stack[i].prefixes[prefix]; ok {
			return uri
		}
	}
	return ""
}

// lookupDefault resolves the default namespace against the element's own
// declaration, then the enclosing scopes, innermost first.
func lookupDefault(decls nsScope, stack []nsScope) string {
	if decls.hasDefault {
		return decls.defaultNS
	}
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i].hasDefault {
			return stack[i].defaultNS
		}
	}
	return ""
}

// splitQName splits a qualified name into prefix and local part at the
// first colon.
func splitQName(name []byte) (prefix, local string) {
	if c := bytes.IndexByte(name, ':'); c >= 0 {
		return string(name[:c]), string(name[c+1:])
	}
	return "", string(name)
}

// decodeAttrValue resolves entities in an attribute value — the five
// predefined entities and character references; anything else stays
// literal — and applies XML attribute-value normalization, turning tabs
// and newlines into spaces.
func decodeAttrValue(v []byte) string {
	var out []byte
	if bytes.IndexByte(v, '&') >= 0 {
		out = make([]byte, 0, len(v))
		for i := 0; i < len(v); i++ {
			if v[i] != '&' {
				out = append(out, v[i])
				continue
			}
			semi := bytes.IndexByte(v[i+1:], ';')
			if semi < 0 {
				out = append(out, '&')
				continue
			}
			name := v[i+1 : i+1+semi]
			i += semi + 1
			switch {
			case bytes.Equal(name, []byte("amp")):
				out = append(out, '&')
			case bytes.Equal(name, []byte("lt")):
				out = append(out, '<')
			case bytes.Equal(name, []byte("gt")):
				out = append(out, '>')
			case bytes.Equal(name, []byte("apos")):
				out = append(out, '\'')
			case bytes.Equal(name, []byte("quot")):
				out = append(out, '"')
			default:
				if r, ok := charRef(name); ok {
					out = utf8.AppendRune(out, r)
				} else {
					out = append(out, '&')
					out = append(out, name...)
					out = append(out, ';')
				}
			}
		}
		v = out
	}
	if bytes.IndexAny(v, "\t\r\n") < 0 {
		return string(v)
	}
	normalized := make([]byte, len(v))
	for i, c := range v {
		if c == '\t' || c == '\r' || c == '\n' {
			normalized[i] = ' '
		} else {
			normalized[i] = c
		}
	}
	return string(normalized)
}

// charRef parses a decimal or hex XML character reference.
func charRef(name []byte) (rune, bool) {
	if len(name) < 2 || name[0] != '#' {
		return 0, false
	}
	digits := name[1:]
	base := 10
	if len(digits) > 1 && (digits[0] == 'x' || digits[0] == 'X') {
		base = 16
		digits = digits[1:]
	}
	if len(digits) == 0 {
		return 0, false
	}
	v := uint64(0)
	for _, d := range digits {
		var n int
		switch {
		case '0' <= d && d <= '9':
			n = int(d - '0')
		case 'a' <= d && d <= 'f':
			n = int(d-'a') + 10
		case 'A' <= d && d <= 'F':
			n = int(d-'A') + 10
		default:
			return 0, false
		}
		if n >= base {
			return 0, false
		}
		v = v*uint64(base) + uint64(n)
		if v > utf8.MaxRune {
			return 0, false
		}
	}
	switch r := rune(v); {
	case r == 0x9 || r == 0xA || r == 0xD,
		0x20 <= r && r <= 0xD7FF,
		0xE000 <= r && r <= 0xFFFD,
		0x10000 <= r:
		return r, true
	}
	return 0, false
}

// isXMLSpace reports an XML whitespace byte: space, tab, CR, or LF.
func isXMLSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n'
}

// toUTF8 transcodes an XML part to UTF-8 based on its BOM or encoding
// declaration, so namespace resolution sees one consistent encoding. An
// encoding the decoder does not know is left as-is: the parse then fails
// and the part is skipped.
func toUTF8(data []byte) []byte {
	switch {
	case len(data) >= 2 && data[0] == 0xFF && data[1] == 0xFE:
		return utf16ToUTF8(data[2:], binary.LittleEndian)
	case len(data) >= 2 && data[0] == 0xFE && data[1] == 0xFF:
		return utf16ToUTF8(data[2:], binary.BigEndian)
	case len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF:
		return data[3:]
	}
	head := data
	if len(head) > 200 {
		head = head[:200]
	}
	label, ok := declaredEncoding(head)
	if !ok {
		return data
	}
	label = asciiLower(strings.TrimSpace(label))
	switch label {
	case "utf-8", "utf8", "unicode-1-1-utf-8":
		return data
	case "utf-16", "utf-16le", "unicode":
		return utf16ToUTF8(data, binary.LittleEndian)
	case "utf-16be", "unicodefeff":
		return utf16ToUTF8(data, binary.BigEndian)
	}
	enc, err := ianaindex.IANA.Encoding(label)
	if err != nil || enc == nil {
		return data
	}
	if name, err := ianaindex.IANA.Name(enc); err == nil && name == "UTF-8" {
		return data
	}
	if decoded, err := enc.NewDecoder().Bytes(data); err == nil {
		return decoded
	}
	return data
}

// utf16ToUTF8 decodes a UTF-16 byte stream to UTF-8, replacing unpaired
// surrogates like the reference's lossy transcode.
func utf16ToUTF8(data []byte, order binary.ByteOrder) []byte {
	units := make([]uint16, 0, len(data)/2)
	for i := 0; i+1 < len(data); i += 2 {
		units = append(units, order.Uint16(data[i:]))
	}
	return []byte(string(utf16.Decode(units)))
}

// declaredEncoding extracts the encoding label from an XML prolog the
// way the reference sniffs it: the first `encoding` marker followed by
// an `=` and a quoted value.
func declaredEncoding(head []byte) (string, bool) {
	if !utf8.Valid(head) {
		return "", false
	}
	s := string(head)
	idx := strings.Index(s, "encoding")
	if idx < 0 {
		return "", false
	}
	rest, ok := strings.CutPrefix(strings.TrimLeftFunc(s[idx+len("encoding"):], unicode.IsSpace), "=")
	if !ok {
		return "", false
	}
	rest = strings.TrimLeftFunc(rest, unicode.IsSpace)
	quote, size := utf8.DecodeRuneInString(rest)
	if quote != '"' && quote != '\'' {
		return "", false
	}
	rest = rest[size:]
	end := strings.IndexByte(rest, byte(quote))
	if end < 0 {
		return "", false
	}
	return rest[:end], true
}
