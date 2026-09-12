// Package xml is anydoc's namespace-aware DOM over the hand-rolled
// tokenizer: it parses from bytes so BOMs and encoding declarations are
// honored, carries resolved namespace URIs on element and attribute names,
// enforces depth and node caps during parsing, and repairs recoverable
// malformations (unclosed or mismatched tags) deliberately.
//
// This file ports src/package/xml.rs.
package xml

import (
	"iter"
	"strconv"
	"strings"
	"unicode"

	"github.com/m7medVision/anydoc-go/internal/cerr"
	"github.com/m7medVision/anydoc-go/internal/text"
)

// The per-part XML caps this package enforces at parse time. They mirror
// limits.rs (and pkg.MaxXMLDepth / pkg.MaxXMLNodes, which re-export nothing
// here only to keep the import graph acyclic: pkg imports this package for
// its DOM types). Values are frozen by upstream.
const (
	maxXMLDepth = 256
	maxXMLNodes = 2_000_000
)

// Node is one child of an element: either *Element or Text. The empty
// interface is sealed by the unexported method.
type Node interface{ isXMLNode() }

// Text is one text run.
type Text string

func (Text) isXMLNode() {}

// Element is one element with its resolved name. NS is the namespace URI
// resolved at parse time ("" when the name is unqualified); Strict-OOXML
// URIs are normalized to their Transitional form when stored.
type Element struct {
	NS       string
	Local    string
	Attrs    []Attr
	Children []Node
}

func (*Element) isXMLNode() {}

// Attr is one attribute with its resolved name. Unqualified attributes have
// NS == ""; the value is normalized (XML 1.0 attribute-value normalization)
// and entity-decoded.
type Attr struct {
	NS    string
	Local string
	Value string
}

// Is reports whether the element has the given namespace and local name.
func (e *Element) Is(ns, local string) bool {
	return e.Local == local && e.NS == ns
}

// Attr is the same-vocabulary attribute lookup: the qualified attribute
// wins, and an explicitly unqualified one with the same local name is
// accepted as a deliberate leniency - schemas often leave their own
// attributes unqualified (DrawingML gridSpan) and producers vary. Lookups
// in a *different* vocabulary than the element's (r:id, xml:id) must use
// AttrQualified, where that fallback would misattribute.
func (e *Element) Attr(ns, local string) (string, bool) {
	if v, ok := e.AttrQualified(ns, local); ok {
		return v, true
	}
	return e.AttrUnqualified(local)
}

// AttrQualified is the strictly namespace-qualified attribute lookup, no
// fallback.
func (e *Element) AttrQualified(ns, local string) (string, bool) {
	for i := range e.Attrs {
		if a := &e.Attrs[i]; a.Local == local && a.NS == ns {
			return a.Value, true
		}
	}
	return "", false
}

// AttrUnqualified is the explicitly unqualified attribute lookup (no
// namespace), no fallback.
func (e *Element) AttrUnqualified(local string) (string, bool) {
	for i := range e.Attrs {
		if a := &e.Attrs[i]; a.Local == local && a.NS == "" {
			return a.Value, true
		}
	}
	return "", false
}

// AttrAny looks an attribute up by local name regardless of namespace, for
// elements whose vocabulary is unambiguous in context (relationships,
// container files).
func (e *Element) AttrAny(local string) (string, bool) {
	for i := range e.Attrs {
		if a := &e.Attrs[i]; a.Local == local {
			return a.Value, true
		}
	}
	return "", false
}

// ChildElems iterates the element children in document order.
func (e *Element) ChildElems() iter.Seq[*Element] {
	return func(yield func(*Element) bool) {
		for _, n := range e.Children {
			if el, ok := n.(*Element); ok && !yield(el) {
				return
			}
		}
	}
}

// Find returns the first child with the given name, or nil.
func (e *Element) Find(ns, local string) *Element {
	for el := range e.ChildElems() {
		if el.Is(ns, local) {
			return el
		}
	}
	return nil
}

// FindAll iterates the children with the given name in document order.
func (e *Element) FindAll(ns, local string) iter.Seq[*Element] {
	return func(yield func(*Element) bool) {
		for el := range e.ChildElems() {
			if el.Is(ns, local) && !yield(el) {
				return
			}
		}
	}
}

// DescendantNodes iterates all descendant nodes (elements and text),
// depth-first in document order, iteratively (deep DOMs must not overflow
// the call stack).
func (e *Element) DescendantNodes() iter.Seq[Node] {
	return func(yield func(Node) bool) {
		stack := make([]Node, len(e.Children))
		copy(stack, e.Children)
		for len(stack) > 0 {
			node := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if el, ok := node.(*Element); ok {
				stack = append(stack, el.Children...)
			}
			if !yield(node) {
				return
			}
		}
	}
}

// DescendantElems iterates all descendant elements, depth-first in document
// order.
func (e *Element) DescendantElems() iter.Seq[*Element] {
	return func(yield func(*Element) bool) {
		for node := range e.DescendantNodes() {
			if el, ok := node.(*Element); ok && !yield(el) {
				return
			}
		}
	}
}

// FirstDescendant returns the first descendant with the given name,
// depth-first, or nil.
func (e *Element) FirstDescendant(ns, local string) *Element {
	for el := range e.DescendantElems() {
		if el.Is(ns, local) {
			return el
		}
	}
	return nil
}

// Descendants iterates all descendants with the given name in document
// order.
func (e *Element) Descendants(ns, local string) iter.Seq[*Element] {
	return func(yield func(*Element) bool) {
		for el := range e.DescendantElems() {
			if el.Is(ns, local) && !yield(el) {
				return
			}
		}
	}
}

// DescendantsAny iterates all descendants matching a local name regardless
// of namespace, in document order, for vocabularies where the namespace
// varies across producers (EPUB container/OPF metadata).
func (e *Element) DescendantsAny(local string) iter.Seq[*Element] {
	return func(yield func(*Element) bool) {
		for el := range e.DescendantElems() {
			if el.Local == local && !yield(el) {
				return
			}
		}
	}
}

// Text concatenates all descendant text nodes.
func (e *Element) Text() string {
	var out strings.Builder
	for node := range e.DescendantNodes() {
		if t, ok := node.(Text); ok {
			out.WriteString(string(t))
		}
	}
	return out.String()
}

// NormalizeOOXMLURI maps ISO/IEC 29500 *Strict* namespace and
// relationship-type URIs onto their Transitional forms: the Strict family is
// the Transitional URI re-rooted under http://purl.oclc.org/ooxml/ with the
// 2006 version segment dropped. Normalizing at parse time lets the rest of
// the converter match a single set of constants. ok is false for URIs
// outside the Strict family.
func NormalizeOOXMLURI(uri string) (string, bool) {
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

// internNS stores a resolved namespace URI the way upstream's interner does:
// Strict-OOXML URIs normalize to Transitional, everything else stays as-is.
// An absent namespace is "".
func internNS(ns string, hasNS bool) string {
	if !hasNS {
		return ""
	}
	if n, ok := NormalizeOOXMLURI(ns); ok {
		return n
	}
	return ns
}

// ParseXML parses an XML part into a synthetic root element containing the
// top-level nodes. Encoding comes from the BOM or XML declaration; the part
// is transcoded to UTF-8 before parsing so namespace resolution sees one
// consistent encoding.
func ParseXML(data []byte) (*Element, error) {
	t := &tokenizer{buf: toUTF8(data)}
	root := &Element{}
	var stack []*Element
	res := newNSResolver()
	nodes := 0

	for {
		tok, err := t.next()
		if err != nil {
			return nil, malformedXML(err)
		}
		switch tok.kind {
		case tokEOF:
			// Unclosed elements are repaired: each open element attaches to
			// its parent as the stack unwinds.
			for len(stack) > 0 {
				elem := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				attach(stack, root, elem)
			}
			return root, nil
		case tokStart:
			// The namespace scope opens when the event is read, so binding
			// errors surface before any depth accounting.
			if err := res.push(tok.data); err != nil {
				return nil, malformedXML(err)
			}
			if len(stack) >= maxXMLDepth {
				return nil, &cerr.Error{
					Kind:   cerr.KindResourceLimit,
					Limit:  "max_xml_depth",
					Detail: "element nesting exceeds " + strconv.Itoa(maxXMLDepth),
				}
			}
			if err := bumpNodes(&nodes); err != nil {
				return nil, err
			}
			stack = append(stack, startToElement(tok.data, res))
		case tokEmpty:
			if err := res.push(tok.data); err != nil {
				return nil, malformedXML(err)
			}
			if err := bumpNodes(&nodes); err != nil {
				return nil, err
			}
			elem := startToElement(tok.data, res)
			res.pop()
			attach(stack, root, elem)
		case tokEnd:
			// Mismatched names would only set a recovered flag that feeds a
			// log line; the repair (pop-and-attach) is identical either way.
			if len(stack) > 0 {
				elem := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				attach(stack, root, elem)
			}
			res.pop()
		case tokText:
			if s := text.DecodeUTF8(tok.data); s != "" {
				if err := bumpNodes(&nodes); err != nil {
					return nil, err
				}
				pushText(stack, root, Text(s))
			}
		case tokRef:
			name := text.DecodeUTF8(tok.data)
			resolved, ok := resolveEntity(name)
			if !ok {
				resolved = "&" + name + ";"
			}
			if err := bumpNodes(&nodes); err != nil {
				return nil, err
			}
			pushText(stack, root, Text(resolved))
		case tokCData:
			if err := bumpNodes(&nodes); err != nil {
				return nil, err
			}
			pushText(stack, root, Text(text.DecodeUTF8(tok.data)))
		}
	}
}

// malformedXML wraps a tokenizer or resolver error the way anydoc wraps
// quick-xml's: as a Malformed error carrying the crate's Display text.
func malformedXML(err error) *cerr.Error {
	return &cerr.Error{Kind: cerr.KindMalformed, Detail: "unparseable xml: " + err.Error()}
}

// bumpNodes counts parsed nodes against the per-part cap.
func bumpNodes(nodes *int) error {
	*nodes++
	if *nodes > maxXMLNodes {
		return &cerr.Error{
			Kind:   cerr.KindResourceLimit,
			Limit:  "max_xml_nodes",
			Detail: "part exceeds " + strconv.Itoa(maxXMLNodes) + " xml nodes",
		}
	}
	return nil
}

// attach makes node a child of the innermost open element, or of the root
// when the stack is empty.
func attach(stack []*Element, root *Element, node *Element) {
	if len(stack) > 0 {
		parent := stack[len(stack)-1]
		parent.Children = append(parent.Children, node)
		return
	}
	root.Children = append(root.Children, node)
}

// pushText appends a text node, merging into the previous node when it is
// text too.
func pushText(stack []*Element, root *Element, t Text) {
	var target *[]Node
	if len(stack) > 0 {
		target = &stack[len(stack)-1].Children
	} else {
		target = &root.Children
	}
	if n := len(*target); n > 0 {
		if prev, ok := (*target)[n-1].(Text); ok {
			(*target)[n-1] = prev + t
			return
		}
	}
	*target = append(*target, t)
}

// startToElement builds one element from Start/Empty tag content: the
// resolved name, non-declaration attributes with normalized values, and the
// mc:Choice/@Requires prefix-to-URI rewrite.
func startToElement(content []byte, res *nsResolver) *Element {
	name := content[:nameLen(content)]
	ns, hasNS, localBytes := res.resolveElementName(name)
	elem := &Element{NS: internNS(ns, hasNS), Local: text.DecodeUTF8(localBytes)}

	it := newAttrIter(content, true)
	for {
		key, value, aerr, ok := it.next()
		if !ok {
			break
		}
		if aerr != 0 {
			// Malformed attributes degrade to absent, but visibly upstream.
			continue
		}
		// Namespace declarations are consumed by the resolver; the DOM
		// stores only resolved names.
		if isNamespaceBindingKey(key) {
			continue
		}
		aNS, aHasNS, aLocal := res.resolveAttributeName(key)
		valueStr, ok := normalizeAttrValue(value)
		if !ok {
			valueStr = text.DecodeUTF8(value)
		}
		elem.Attrs = append(elem.Attrs, Attr{
			NS:    internNS(aNS, aHasNS),
			Local: text.DecodeUTF8(aLocal),
			Value: valueStr,
		})
	}

	// mc:Choice/@Requires holds namespace *prefixes* evaluated in the
	// element's lexical scope (prefixes may be rebound locally). Resolve
	// them to URIs here, where the scope is known, so consumers never need
	// a document-wide prefix map. Unresolvable prefixes stay literal and
	// match no requirement.
	if elem.Local == "Choice" && elem.NS == NsMC {
		for i := range elem.Attrs {
			if elem.Attrs[i].Local == "Requires" {
				elem.Attrs[i].Value = resolveRequires(elem.Attrs[i].Value, res)
				break
			}
		}
	}
	return elem
}

// resolveRequires rewrites each whitespace-separated prefix of an
// mc:Choice/@Requires value to the URI the prefix is bound to (normalized
// like every other namespace URI); unbound prefixes stay literal.
func resolveRequires(value string, res *nsResolver) string {
	fields := strings.Fields(value)
	out := make([]string, len(fields))
	for i, prefix := range fields {
		probe := []byte(prefix + ":x")
		ns, hasNS, _ := res.resolveElementName(probe)
		if !hasNS {
			out[i] = prefix
			continue
		}
		if n, ok := NormalizeOOXMLURI(ns); ok {
			out[i] = n
		} else {
			out[i] = ns
		}
	}
	return strings.Join(out, " ")
}

// toUTF8 transcodes an XML part to UTF-8 based on its BOM or encoding
// declaration (encoding_rs decode semantics via internal/text).
func toUTF8(b []byte) []byte {
	switch {
	case len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE:
		return []byte(text.Decode(text.UTF16LE, b))
	case len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF:
		return []byte(text.Decode(text.UTF16BE, b))
	case len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF:
		return b[3:]
	default:
		// Sniff a non-UTF-8 encoding declaration in the XML prolog. The
		// 200-byte head must itself be valid UTF-8, mirroring the
		// str::from_utf8(head).ok() gate.
		head := b
		if len(head) > 200 {
			head = head[:200]
		}
		if label, ok := declaredEncoding(string(head)); ok {
			if enc, ok := text.ForLabel(label); ok && enc != text.UTF8 {
				return []byte(text.Decode(enc, b))
			}
		}
		return b
	}
}

// declaredEncoding extracts the encoding label from an XML prolog head:
// `encoding` followed by `=` and a quoted value.
func declaredEncoding(head string) (string, bool) {
	idx := strings.Index(head, "encoding")
	if idx < 0 {
		return "", false
	}
	rest := strings.TrimLeftFunc(head[idx+len("encoding"):], unicode.IsSpace)
	rest, ok := strings.CutPrefix(rest, "=")
	if !ok {
		return "", false
	}
	rest = strings.TrimLeftFunc(rest, unicode.IsSpace)
	if rest == "" {
		return "", false
	}
	quote := rest[0]
	if quote != '"' && quote != '\'' {
		return "", false
	}
	rest = rest[1:]
	end := strings.IndexByte(rest, quote)
	if end < 0 {
		return "", false
	}
	return rest[:end], true
}

// resolveEntity resolves entity references the tokenizer surfaces as
// GeneralRef events: character references and anydoc's named-entity table.
// ok is false for unknown names and unparseable references.
func resolveEntity(name string) (string, bool) {
	if num, ok := strings.CutPrefix(name, "#"); ok {
		base := 10
		if hex, isHex := strings.CutPrefix(num, "x"); isHex {
			base, num = 16, hex
		} else if hex, isHex := strings.CutPrefix(num, "X"); isHex {
			base, num = 16, hex
		}
		// Rust's from_str_radix accepts one leading '+' (never '-').
		num, _ = strings.CutPrefix(num, "+")
		code, ok := parseUint(num, base)
		if !ok {
			return "", false
		}
		if code > 0x10FFFF || (code >= 0xD800 && code <= 0xDFFF) {
			return "", false
		}
		return string(rune(code)), true
	}
	if ch, ok := namedEntities[name]; ok {
		return ch, true
	}
	return "", false
}

func parseUint(s string, base int) (uint32, bool) {
	if s == "" {
		return 0, false
	}
	var v uint64
	for i := 0; i < len(s); i++ {
		c := s[i]
		var d uint64
		switch {
		case c >= '0' && c <= '9':
			d = uint64(c - '0')
		case base == 16 && c >= 'a' && c <= 'f':
			d = uint64(c-'a') + 10
		case base == 16 && c >= 'A' && c <= 'F':
			d = uint64(c-'A') + 10
		default:
			return 0, false
		}
		v = v*uint64(base) + d
		if v > 0xFFFFFFFF {
			return 0, false
		}
	}
	return uint32(v), true
}

// namedEntities is anydoc's entity table (resolve_entity).
var namedEntities = map[string]string{
	"amp":    "&",
	"lt":     "<",
	"gt":     ">",
	"apos":   "'",
	"quot":   "\"",
	"nbsp":   " ",
	"shy":    "­",
	"mdash":  "—",
	"ndash":  "–",
	"lsquo":  "‘",
	"rsquo":  "’",
	"ldquo":  "“",
	"rdquo":  "”",
	"hellip": "…",
	"copy":   "©",
	"reg":    "®",
	"trade":  "™",
	"deg":    "°",
	"middot": "·",
	"bull":   "•",
	"sect":   "§",
	"para":   "¶",
	"laquo":  "«",
	"raquo":  "»",
	"times":  "×",
	"divide": "÷",
	"plusmn": "±",
	"frac12": "½",
	"frac14": "¼",
	"eacute": "é",
	"egrave": "è",
	"agrave": "à",
	"ccedil": "ç",
	"uuml":   "ü",
	"ouml":   "ö",
	"auml":   "ä",
	"szlig":  "ß",
	"aring":  "å",
	"oslash": "ø",
	"aelig":  "æ",
	"euro":   "€",
	"pound":  "£",
	"yen":    "¥",
	"cent":   "¢",
}
