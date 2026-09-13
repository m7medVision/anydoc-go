// Port of src/shared/chain.rs: style-chain traversal. Walk a parent
// (basedOn) chain child-to-root. A cycle is a resource-safety bound and
// hard-fails — recovery never weakens resource safety.

package shared

import (
	"fmt"

	"github.com/m7medVision/anydoc-go/internal/cerr"
)

type styleNode[D any] struct {
	def       *D
	parent    string
	hasParent bool
}

// StyleChains maps style id -> (definition, parent style id).
type StyleChains[D any] struct {
	raw map[string]styleNode[D]
}

// Insert records a style definition and its optional parent id.
func (c *StyleChains[D]) Insert(id string, def *D, parent *string) {
	if c.raw == nil {
		c.raw = make(map[string]styleNode[D])
	}
	n := styleNode[D]{def: def}
	if parent != nil {
		n.parent = *parent
		n.hasParent = true
	}
	c.raw[id] = n
}

// Definition returns the recorded definition for id, or nil.
func (c *StyleChains[D]) Definition(id string) *D {
	n, ok := c.raw[id]
	if !ok {
		return nil
	}
	return n.def
}

// Walk walks a chain child-to-root, visiting each definition until visit
// returns ok. Unknown ids end the walk (a dangling reference is producer
// sloppiness, not fatal); a cycle hard-fails.
//
// Go cannot put an extra type parameter on a method, so this is a package
// function matching StyleChains.walk.
func Walk[D, T any](c *StyleChains[D], id string, visit func(*D) (T, bool)) (T, bool, error) {
	var zero T
	if c == nil || c.raw == nil {
		return zero, false, nil
	}
	if _, ok := c.raw[id]; !ok {
		return zero, false, nil
	}
	visited := make(map[string]struct{})
	cursor, has := id, true
	for has {
		if _, seen := visited[cursor]; seen {
			return zero, false, &cerr.Error{
				Kind:   cerr.KindMalformed,
				Detail: fmt.Sprintf("style inheritance cycle at %q", cursor),
			}
		}
		visited[cursor] = struct{}{}
		n := c.raw[cursor]
		if hit, ok := visit(n.def); ok {
			return hit, true, nil
		}
		if !n.hasParent {
			break
		}
		_, has = c.raw[n.parent]
		cursor = n.parent
	}
	return zero, false, nil
}
