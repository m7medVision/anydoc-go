// Anchor resolution: maps every internal anchor id to the fragment it will
// have in the rendered Markdown. Heading-coincident anchors reuse the
// heading's GFM auto-generated slug; an anchor a link targets gets a
// sanitized, stable HTML id rendered as `<a id="..."></a>` at its position.
//
// Anchors nothing links to render nothing: producers mark up far more
// positions than they reference (a bookmark per paragraph, an id per EPUB
// element), and an unreachable target is only noise in the output.

package markdown

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/m7medVision/anydoc-go/internal/model"
)

type anchorMap struct {
	resolved map[string]resolvedAnchor
}

type resolvedAnchor struct {
	fragment string
	// emitHTML is true when the anchor needs an explicit `<a id>` emitted
	// at its position (false for heading-coincident anchors - the heading's
	// own slug carries them).
	emitHTML bool
}

// fragment returns the `#fragment` a link to id should use, if the target
// exists.
func (m anchorMap) fragment(id string) (string, bool) {
	r, ok := m.resolved[id]
	return r.fragment, ok
}

// htmlID returns the HTML id to emit for a model.Anchor node, when one is
// needed.
func (m anchorMap) htmlID(id string) (string, bool) {
	r, ok := m.resolved[id]
	if !ok || !r.emitHTML {
		return "", false
	}
	return r.fragment, true
}

func resolveAnchors(doc *model.Document) anchorMap {
	ids := newUniqueIDs()
	resolved := make(map[string]resolvedAnchor)

	// Anchor ids some link in the document targets, notes included.
	linked := make(map[string]bool)
	collectLinks := func(blocks []model.Block) {
		walkBlocks(blocks, func(block model.Block) {
			collectLinkTargets(block, linked)
		})
	}
	collectLinks(doc.Blocks)
	for i := range doc.Notes {
		collectLinks(doc.Notes[i].Blocks)
	}

	// Pass 1: headings claim their GFM slugs in render order, binding any
	// heading-coincident anchor ids (the Anchor field and anchor nodes
	// inside the heading content) to those slugs.
	walkBlocks(doc.Blocks, func(block model.Block) {
		heading, ok := block.(model.Heading)
		if !ok {
			return
		}
		slug, ok := ids.claim(gfmSlug(model.InlinesToPlainText(heading.Inlines)))
		if !ok {
			return
		}
		bind := func(id string) {
			if _, exists := resolved[id]; !exists {
				resolved[id] = resolvedAnchor{fragment: slug, emitHTML: false}
			}
		}
		if heading.Anchor != "" {
			bind(heading.Anchor)
		}
		forEachAnchor(heading.Inlines, bind)
	})

	// Pass 2: every remaining anchor a link targets gets a sanitized HTML
	// id.
	assign := func(id string) {
		if !linked[id] {
			return
		}
		if _, done := resolved[id]; done {
			return
		}
		if html, ok := ids.claim(sanitizeID(id)); ok {
			resolved[id] = resolvedAnchor{fragment: html, emitHTML: true}
		}
	}
	bindBlockAnchors := func(blocks []model.Block) {
		walkBlocks(blocks, func(block model.Block) {
			bindBlockAnchorsIn(block, assign)
		})
	}
	bindBlockAnchors(doc.Blocks)
	for i := range doc.Notes {
		bindBlockAnchors(doc.Notes[i].Blocks)
	}

	return anchorMap{resolved: resolved}
}

func collectLinkTargets(block model.Block, out map[string]bool) {
	switch b := block.(type) {
	case model.Heading:
		forEachLinkTarget(b.Inlines, out)
	case model.Paragraph:
		forEachLinkTarget(b.Inlines, out)
	}
}

func forEachLinkTarget(inlines []model.Inline, out map[string]bool) {
	for _, inline := range inlines {
		if link, ok := inline.(model.Link); ok {
			if anchor, isAnchor := link.Target.(model.AnchorLink); isAnchor {
				out[anchor.ID] = true
			}
			forEachLinkTarget(link.Inlines, out)
		}
	}
}

func bindBlockAnchorsIn(block model.Block, assign func(string)) {
	switch b := block.(type) {
	case model.Heading:
		forEachAnchor(b.Inlines, assign)
	case model.Paragraph:
		forEachAnchor(b.Inlines, assign)
	}
}

// walkBlocks is a depth-first walk over all blocks, using an explicit stack.
func walkBlocks(blocks []model.Block, f func(model.Block)) {
	stack := make([]model.Block, len(blocks))
	for i := range blocks {
		stack[len(blocks)-1-i] = blocks[i]
	}
	for len(stack) > 0 {
		block := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		f(block)
		switch b := block.(type) {
		case model.ListBlock:
			for i := len(b.List.Items) - 1; i >= 0; i-- {
				itemBlocks := b.List.Items[i].Blocks
				for j := len(itemBlocks) - 1; j >= 0; j-- {
					stack = append(stack, itemBlocks[j])
				}
			}
		case model.TableBlock:
			for i := len(b.Table.Grid) - 1; i >= 0; i-- {
				row := b.Table.Grid[i]
				for j := len(row) - 1; j >= 0; j-- {
					if origin, ok := row[j].(model.OriginCell); ok {
						cellBlocks := origin.Cell.Blocks
						for k := len(cellBlocks) - 1; k >= 0; k-- {
							stack = append(stack, cellBlocks[k])
						}
					}
				}
			}
		case model.Quote:
			for i := len(b.Blocks) - 1; i >= 0; i-- {
				stack = append(stack, b.Blocks[i])
			}
		}
	}
}

func forEachAnchor(inlines []model.Inline, f func(string)) {
	for _, inline := range inlines {
		switch inl := inline.(type) {
		case model.Anchor:
			f(inl.ID)
		case model.Link:
			forEachAnchor(inl.Inlines, f)
		}
	}
}

// uniqueIDs allocates ids without repeatedly probing used numeric suffixes.
type uniqueIDs struct {
	used       map[string]bool
	nextSuffix map[string]int
}

func newUniqueIDs() *uniqueIDs {
	return &uniqueIDs{used: make(map[string]bool), nextSuffix: make(map[string]int)}
}

func (u *uniqueIDs) claim(base string) (string, bool) {
	if !u.used[base] {
		u.used[base] = true
		if _, ok := u.nextSuffix[base]; !ok {
			u.nextSuffix[base] = 1
		}
		return base, true
	}
	n := u.nextSuffix[base]
	if n == 0 {
		n = 1
	}
	for {
		candidate := base + "-" + strconv.Itoa(n)
		n++
		if n < 0 {
			// Suffix counter overflow (unreachable in practice; upstream
			// bails out through checked_add).
			return "", false
		}
		if !u.used[candidate] {
			u.used[candidate] = true
			u.nextSuffix[base] = n
			if _, ok := u.nextSuffix[candidate]; !ok {
				u.nextSuffix[candidate] = 1
			}
			return candidate, true
		}
	}
}

// gfmSlug makes GFM-style heading slugs: full-Unicode lowercase, spaces
// become hyphens, and everything except word-forming characters (letters,
// numbers, marks, connector punctuation) and hyphens drops. An empty result
// becomes "section" so the anchor stays linkable.
func gfmSlug(text string) string {
	var slug strings.Builder
	for _, c := range strings.TrimSpace(text) {
		for _, lc := range toLowercase(c) {
			switch {
			case lc == ' ':
				slug.WriteByte('-')
			case lc == '-':
				slug.WriteRune(lc)
			case isAlphanumeric(lc) || isCombiningMark(lc) || isConnectorPunctuation(lc):
				slug.WriteRune(lc)
			}
		}
	}
	if slug.Len() == 0 {
		return "section"
	}
	return slug.String()
}

// toLowercase yields a character's full lowercase mapping, the way Rust's
// char::to_lowercase does. Unicode's one unconditional multi-character
// lowercase mapping is İ (U+0130 -> i + combining dot above); every other
// character's full mapping equals Go's simple unicode.ToLower.
func toLowercase(c rune) []rune {
	if c == 'İ' {
		return []rune{'i', '\u0307'}
	}
	return []rune{unicode.ToLower(c)}
}

// isCombiningMark covers combining-mark blocks is_alphanumeric misses
// (marks outside Other_Alphabetic, such as viramas and cantillation/stress
// signs).
func isCombiningMark(c rune) bool {
	switch u := uint32(c); {
	case u >= 0x0300 && u <= 0x036F,
		u >= 0x0483 && u <= 0x0489,
		u >= 0x0900 && u <= 0x0903,
		u >= 0x093A && u <= 0x094F,
		u >= 0x0951 && u <= 0x0957,
		u >= 0x0962 && u <= 0x0963,
		u >= 0x1AB0 && u <= 0x1AFF,
		u >= 0x1DC0 && u <= 0x1DFF,
		u >= 0x20D0 && u <= 0x20FF,
		u >= 0xFE20 && u <= 0xFE2F:
		return true
	}
	return false
}

// isConnectorPunctuation tests category Pc: joins words, kept like `_`.
func isConnectorPunctuation(c rune) bool {
	switch u := uint32(c); {
	case u == 0x005F, u == 0x203F, u == 0x2040, u == 0x2054, u == 0xFE33, u == 0xFE34,
		u >= 0xFE4D && u <= 0xFE4F, u == 0xFF3F:
		return true
	}
	return false
}

// sanitizeID sanitizes a source anchor id into a stable [a-z0-9-_] HTML id.
func sanitizeID(id string) string {
	var out strings.Builder
	out.Grow(len(id))
	prevDash := false
	for _, c := range id {
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		mapped := c
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			mapped = '-'
		}
		if mapped == '-' && prevDash {
			continue
		}
		prevDash = mapped == '-'
		out.WriteRune(mapped)
	}
	trimmed := strings.Trim(out.String(), "-")
	if trimmed == "" {
		return "anchor"
	}
	return trimmed
}
