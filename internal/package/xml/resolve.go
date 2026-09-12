// Namespace resolution: quick-xml 0.41's NamespaceResolver semantics —
// per-element scopes, reserved xml/xmlns prefixes, undeclaration by empty
// binding, and the 256-declaration-per-element cap.
package xml

// maxDeclarationsPerElement is quick-xml's DEFAULT_MAX_DECLARATIONS_PER_ELEMENT.
const maxDeclarationsPerElement = 256

const (
	xmlPrefixURI   = "http://www.w3.org/XML/1998/namespace"
	xmlnsPrefixURI = "http://www.w3.org/2000/xmlns/"
)

// nsBinding is one prefix-to-URI binding at a nesting level.
type nsBinding struct {
	prefix string
	value  string
	level  int
}

// nsResolver tracks namespace bindings across the open-element stack. The
// two reserved prefixes are pre-bound at level 0 and can only be re-declared
// to their exact reserved URIs (xml) or not at all (xmlns).
type nsResolver struct {
	bindings []nsBinding
	level    int
}

// newNSResolver returns a resolver with the two reserved prefixes pre-bound
// at level 0 (NamespaceResolver::default).
func newNSResolver() *nsResolver {
	return &nsResolver{bindings: []nsBinding{
		{prefix: "xml", value: xmlPrefixURI, level: 0},
		{prefix: "xmlns", value: xmlnsPrefixURI, level: 0},
	}}
}

// push opens a scope for one start (or empty) tag and adds every xmlns
// declaration it carries, in document order (later same-prefix declarations
// win because resolution searches backward).
func (r *nsResolver) push(tagContent []byte) error {
	r.level++
	count := 0
	it := newAttrIter(tagContent, false)
	for {
		key, value, aerr, ok := it.next()
		if !ok {
			break
		}
		if aerr != 0 {
			break // binding collection stops at the first malformed attribute
		}
		if !isNamespaceBindingKey(key) {
			continue
		}
		if count >= maxDeclarationsPerElement {
			return errTooManyDeclarations(maxDeclarationsPerElement)
		}
		count++
		prefix := nsBindingPrefix(key)
		switch {
		case prefix == nil: // xmlns="..." — the default namespace
			r.bindings = append(r.bindings, nsBinding{prefix: "", value: string(value), level: r.level})
		case string(prefix) == "xml":
			if string(value) != xmlPrefixURI {
				return errInvalidXmlPrefixBind(value)
			}
			// Re-declaring xml to its reserved URI adds nothing.
		case string(prefix) == "xmlns":
			return errInvalidXmlnsPrefixBind(value)
		case string(value) == xmlPrefixURI:
			return errInvalidPrefixForXml(prefix)
		case string(value) == xmlnsPrefixURI:
			return errInvalidPrefixForXmlns(prefix)
		default:
			r.bindings = append(r.bindings, nsBinding{prefix: string(prefix), value: string(value), level: r.level})
		}
	}
	return nil
}

// pop closes the innermost scope.
func (r *nsResolver) pop() {
	if r.level == 0 {
		return
	}
	r.level--
	for len(r.bindings) > 0 && r.bindings[len(r.bindings)-1].level > r.level {
		r.bindings = r.bindings[:len(r.bindings)-1]
	}
}

// resolvePrefix resolves a name prefix (nil = no prefix) against the current
// scopes. useDefault applies the default namespace to unprefixed names
// (elements yes, attributes never). hasNS distinguishes a resolved empty
// namespace (xmlns="") from no namespace at all; callers that only feed
// namespace URIs into lookups treat both as absent, which the DOM reflects.
func (r *nsResolver) resolvePrefix(p []byte, useDefault bool) (ns string, hasNS bool) {
	if p == nil {
		if !useDefault {
			return "", false
		}
		for i := len(r.bindings) - 1; i >= 0; i-- {
			if r.bindings[i].prefix == "" {
				return r.bindings[i].value, true
			}
		}
		return "", false
	}
	for i := len(r.bindings) - 1; i >= 0; i-- {
		if r.bindings[i].prefix == string(p) {
			if r.bindings[i].value == "" {
				return "", false // undeclared (xmlns:p="")
			}
			return r.bindings[i].value, true
		}
	}
	return "", false
}

// resolveElementName resolves an element's qualified name in the scope that
// includes its own declarations.
func (r *nsResolver) resolveElementName(name []byte) (ns string, hasNS bool, local []byte) {
	ns, hasNS = r.resolvePrefix(namePrefix(name), true)
	return ns, hasNS, localName(name)
}

// resolveAttributeName resolves an attribute's qualified name; unprefixed
// attributes are never in a namespace.
func (r *nsResolver) resolveAttributeName(name []byte) (ns string, hasNS bool, local []byte) {
	ns, hasNS = r.resolvePrefix(namePrefix(name), false)
	return ns, hasNS, localName(name)
}
