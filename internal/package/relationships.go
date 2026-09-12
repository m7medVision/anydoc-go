// OPC relationship parts, typed.
//
// This file ports src/package/relationships.rs.
package pkg

import (
	"iter"
	"slices"

	"github.com/m7medVision/anydoc-go/internal/package/xml"
)

// Well-known OPC relationship types (Transitional form; Strict types are
// normalized onto these when the rels part is read).
const (
	RelTypeOfficeDocument = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument"
	RelTypeStyles         = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles"
	RelTypeNumbering      = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/numbering"
	RelTypeFootnotes      = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/footnotes"
	RelTypeEndnotes       = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/endnotes"
)

// TargetMode is whether a relationship target lives inside the package.
type TargetMode int

const (
	// TargetModeInternal targets resolve inside the package.
	TargetModeInternal TargetMode = iota
	// TargetModeExternal targets are out-of-package URIs.
	TargetModeExternal
)

// Relationship is one typed OPC relationship.
type Relationship struct {
	Target  string
	RelType string
	Mode    TargetMode
}

// Relationships is a part's relationship map, keyed by relationship id.
type Relationships struct {
	rels map[string]Relationship
}

// Get returns the relationship with the given id.
func (r *Relationships) Get(id string) (Relationship, bool) {
	rel, ok := r.rels[id]
	return rel, ok
}

// InternalTarget returns the internal-mode target for an id, the common
// case for parts.
func (r *Relationships) InternalTarget(id string) (string, bool) {
	rel, ok := r.rels[id]
	if !ok || rel.Mode != TargetModeInternal {
		return "", false
	}
	return rel.Target, true
}

// Iter yields the relationships ordered by id, so consumers that upstream
// drove through an unordered map iterator see a deterministic sequence.
func (r *Relationships) Iter() iter.Seq2[string, Relationship] {
	return func(yield func(string, Relationship) bool) {
		for _, id := range sortedKeys(r.rels) {
			if !yield(id, r.rels[id]) {
				return
			}
		}
	}
}

// FirstOfType returns the internal-mode relationship of a given type, lowest
// id first so the pick is deterministic when a producer emits duplicates.
func (r *Relationships) FirstOfType(relType string) (Relationship, bool) {
	best := ""
	var found Relationship
	have := false
	for id, rel := range r.rels {
		if rel.RelType != relType || rel.Mode != TargetModeInternal {
			continue
		}
		if have && id >= best {
			continue
		}
		best, found, have = id, rel, true
	}
	return found, have
}

// ReadRels reads a relationships part. An absent, unreadable, or corrupt
// part yields an empty map (absent is valid - many parts simply have no
// relationships; the rest degrade per the unified policy). Resource-limit
// errors always propagate.
func ReadRels(p *Package, part string) (*Relationships, error) {
	root, found, err := p.OptionalXMLPart(part)
	if err != nil || !found {
		if err != nil {
			return nil, err
		}
		return &Relationships{}, nil
	}
	rels := make(map[string]Relationship)
	for rel := range root.Descendants(xml.NsPkgRels, "Relationship") {
		id, ok := rel.AttrAny("Id")
		if !ok {
			continue
		}
		target, ok := rel.AttrAny("Target")
		if !ok {
			continue
		}
		mode := TargetModeInternal
		if m, ok := rel.AttrAny("TargetMode"); ok && asciiEqualFoldString(m, "external") {
			mode = TargetModeExternal
		}
		relType, _ := rel.AttrAny("Type")
		if normalized, ok := xml.NormalizeOOXMLURI(relType); ok {
			relType = normalized
		}
		rels[id] = Relationship{Target: target, RelType: relType, Mode: mode}
	}
	return &Relationships{rels: rels}, nil
}

// RelTargetBytes loads an internal relationship target's bytes, resolved
// against the part the relationship appears in. Unknown ids, external
// targets, and unresolvable or unreadable targets degrade (ok=false) per
// the unified policy; fatal resource-limit errors always propagate.
func RelTargetBytes(p *Package, rels *Relationships, basePart, relID string) (part string, data []byte, ok bool, err error) {
	rel, found := rels.Get(relID)
	if !found {
		return "", nil, false, nil
	}
	if rel.Mode != TargetModeInternal {
		return "", nil, false, nil
	}
	target, terr := Resolve(basePart, rel.Target)
	if terr != nil {
		return "", nil, false, nil
	}
	data, found, err = p.OptionalPart(target.Path)
	if err != nil {
		return "", nil, false, err
	}
	if !found {
		return "", nil, false, nil
	}
	return target.Path, data, true, nil
}

// RelsPartFor returns the conventional rels part name for a part
// (word/document.xml -> word/_rels/document.xml.rels).
func RelsPartFor(part string) string {
	if idx := lastIndexByte(part, '/'); idx >= 0 {
		return part[:idx] + "/_rels/" + part[idx+1:] + ".rels"
	}
	return "_rels/" + part + ".rels"
}

func asciiEqualFoldString(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		if a[i]|0x20 != b[i]|0x20 {
			return false
		}
	}
	return true
}

func lastIndexByte(s string, c byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func sortedKeys(m map[string]Relationship) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
