// Port of src/shared/html.rs: (X)HTML element tree -> model blocks. Used by
// the EPUB frontend.
//
// Applies a deliberately small CSS subset — the semantic properties only:
// font-weight, font-style, text-decoration: line-through, and display: none
// — from inline style attributes and element/class rules. Tables build the
// canonical grid (rowspan/colspan); ordered lists honor start, reversed,
// type, and per-item value.

package shared

import (
	"cmp"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/m7medVision/anydoc-go/internal/cerr"
	"github.com/m7medVision/anydoc-go/internal/model"
	"github.com/m7medVision/anydoc-go/internal/package/xml"
	sharedmath "github.com/m7medVision/anydoc-go/internal/shared/math"
)

// HTMLCtx is the frontend hooks: how hrefs, image sources, and anchor ids
// resolve in the containing document (EPUB scopes them per chapter).
type HTMLCtx interface {
	LinkTarget(href string) (model.LinkTarget, bool)
	// ImageSource: a failed load degrades to ok=false; resource-limit
	// errors propagate.
	ImageSource(src string) (model.ImageSource, bool, error)
	AnchorID(raw string) model.AnchorID
}

// ToBlocks converts an HTML body element tree to model blocks.
func ToBlocks(body *xml.Element, css *Stylesheet, ctx HTMLCtx) ([]model.Block, error) {
	b := htmlBuilder{css: css, ctx: ctx, startBoundary: true}
	if err := b.walkChildren(body, StyleDelta{}); err != nil {
		return nil, err
	}
	return b.finish(), nil
}

// ---------------------------------------------------------------------------
// Minimal CSS

// StyleProps is the semantic subset of CSS applied to an element.
type StyleProps struct {
	Delta StyleDelta
	// Hidden is display visibility as a tri-state: a higher-priority
	// display:block (or any non-none value) can restore content a
	// lower-priority display:none hid.
	Hidden *bool
}

func (p StyleProps) merge(over StyleProps) StyleProps {
	return StyleProps{Delta: p.Delta.Merge(over.Delta), Hidden: orBool(over.Hidden, p.Hidden)}
}

func (p StyleProps) isDefault() bool {
	return p.Delta == (StyleDelta{}) && p.Hidden == nil
}

// Cascade priority tiers: rules < inline style < !important rules <
// !important inline style, with selector specificity ordering within a
// tier and source order breaking ties.
const (
	inlinePriority    uint32 = 100_000
	importantPriority uint32 = 1_000_000
)

type cssRule struct {
	tag, class string
	hasTag     bool
	hasClass   bool
	priority   uint32
	props      StyleProps
}

// Stylesheet is the collected CSS subset.
type Stylesheet struct {
	rules []cssRule
}

// Add rules from one stylesheet's text. Only simple tag, .class, and
// tag.class selectors participate; !important declarations enter the
// higher cascade tier.
func (s *Stylesheet) Add(css string) {
	css = stripCSSComments(css)
	for _, chunk := range strings.Split(css, "}") {
		sel, body, ok := strings.Cut(chunk, "{")
		if !ok {
			continue
		}
		decls := parseDeclarations(body)
		if decls.normal.isDefault() && decls.important.isDefault() {
			continue
		}
		for _, selector := range strings.Split(sel, ",") {
			raw := strings.TrimSpace(selector)
			if raw == "" || strings.ContainsAny(raw, " :[") {
				continue // combinators/pseudo/attribute selectors: out of subset
			}
			var tag, class string
			hasTag, hasClass := false, false
			if t, c, ok := strings.Cut(raw, "."); ok {
				if t != "" {
					tag = asciiLower(t)
					hasTag = true
				}
				class = c
				hasClass = true
			} else {
				tag = asciiLower(raw)
				hasTag = true
			}
			specificity := uint32(0)
			if hasClass {
				specificity += 10
			}
			if hasTag {
				specificity++
			}
			for _, pair := range []struct {
				props StyleProps
				base  uint32
			}{{decls.normal, 0}, {decls.important, importantPriority}} {
				if !pair.props.isDefault() {
					s.rules = append(s.rules, cssRule{
						tag:      tag,
						class:    class,
						hasTag:   hasTag,
						hasClass: hasClass,
						priority: pair.base + specificity,
						props:    pair.props,
					})
				}
			}
		}
	}
}

func (s *Stylesheet) matchingRules(tag string, classes []string) []ruleHit {
	var out []ruleHit
	for _, rule := range s.rules {
		if rule.hasTag && rule.tag != tag {
			continue
		}
		if rule.hasClass && !slices.Contains(classes, rule.class) {
			continue
		}
		out = append(out, ruleHit{rule.priority, rule.props})
	}
	return out
}

type ruleHit struct {
	prio  uint32
	props StyleProps
}

func stripCSSComments(css string) string {
	var out strings.Builder
	out.Grow(len(css))
	rest := css
	for {
		start := strings.Index(rest, "/*")
		if start < 0 {
			out.WriteString(rest)
			return out.String()
		}
		out.WriteString(rest[:start])
		end := strings.Index(rest[start:], "*/")
		if end < 0 {
			return out.String()
		}
		rest = rest[start+end+2:]
	}
}

type declProps struct {
	normal, important StyleProps
}

func parseDeclarations(body string) declProps {
	var out declProps
	for _, decl := range strings.Split(body, ";") {
		name, value, ok := strings.Cut(decl, ":")
		if !ok {
			continue
		}
		name = asciiLower(strings.TrimSpace(name))
		value = asciiLower(strings.TrimSpace(value))
		important := false
		if pos := strings.IndexByte(value, '!'); pos >= 0 {
			if strings.TrimSpace(value[pos+1:]) == "important" {
				important = true
			}
			value = strings.TrimRightFunc(value[:pos], unicode.IsSpace)
		}
		props := &out.normal
		if important {
			props = &out.important
		}
		switch name {
		case "font-weight":
			bold := value == "bold" || value == "bolder"
			if !bold {
				if n, err := strconv.ParseUint(value, 10, 32); err == nil && n >= 600 {
					bold = true
				}
			}
			props.Delta.Bold = boolPtr(bold)
		case "font-style":
			props.Delta.Italic = boolPtr(value == "italic" || value == "oblique")
		case "text-decoration", "text-decoration-line":
			if strings.Contains(value, "line-through") {
				props.Delta.Strike = boolPtr(true)
			} else if value == "none" {
				props.Delta.Strike = boolPtr(false)
			}
		case "display":
			props.Hidden = boolPtr(value == "none")
		}
	}
	return out
}

func boolPtr(v bool) *bool { b := v; return &b }

// ---------------------------------------------------------------------------
// Walking

type htmlBuilder struct {
	blocks  []model.Block
	inlines []model.Inline
	css     *Stylesheet
	ctx     HTMLCtx
	// Whether text appended while inlines is empty sits at a whitespace
	// boundary (block start: leading whitespace collapses away; inline
	// sub-builders inherit the surrounding run's state instead).
	startBoundary bool
}

func keepsParagraph(inlines []model.Inline) bool {
	if !model.InlinesAreEmpty(inlines) {
		return true
	}
	for _, i := range inlines {
		if _, ok := i.(model.Anchor); ok {
			return true
		}
	}
	return false
}

func atSpaceBoundary(inlines []model.Inline, start bool) bool {
	for i := len(inlines) - 1; i >= 0; i-- {
		switch v := inlines[i].(type) {
		case model.Anchor:
			continue
		case model.Run:
			if v.Text == "" {
				continue
			}
			return lastRuneSpace(v.Text)
		case model.LineBreak:
			return true
		case model.Link:
			if model.InlinesAreEmpty(v.Inlines) {
				continue
			}
			return atSpaceBoundary(v.Inlines, false)
		case model.Image, model.NoteRef, model.InlineMath, model.Checkbox:
			return false
		}
	}
	return start
}

func lastRuneSpace(s string) bool {
	var last rune
	for _, r := range s {
		last = r
	}
	return unicode.IsSpace(last)
}

func (b *htmlBuilder) flushParagraph() {
	if len(b.inlines) > 0 {
		inlines := b.inlines
		b.inlines = nil
		if keepsParagraph(inlines) {
			b.blocks = append(b.blocks, model.Paragraph{Inlines: inlines})
		}
	}
	b.startBoundary = true
}

func (b *htmlBuilder) finish() []model.Block {
	b.flushParagraph()
	return b.blocks
}

func (b *htmlBuilder) subBlocks(elem *xml.Element, delta StyleDelta) ([]model.Block, error) {
	return b.subBlocksAt(elem, delta, true)
}

func (b *htmlBuilder) subBlocksAt(elem *xml.Element, delta StyleDelta, startBoundary bool) ([]model.Block, error) {
	sub := htmlBuilder{css: b.css, ctx: b.ctx, startBoundary: startBoundary}
	if err := sub.walkChildren(elem, delta); err != nil {
		return nil, err
	}
	return sub.finish(), nil
}

func (b *htmlBuilder) elementProps(elem *xml.Element) StyleProps {
	var classes []string
	if c, ok := elem.AttrAny("class"); ok {
		classes = strings.Fields(c)
	}
	entries := b.css.matchingRules(elem.Local, classes)
	if style, ok := elem.AttrAny("style"); ok {
		decls := parseDeclarations(style)
		entries = append(entries, ruleHit{inlinePriority, decls.normal})
		entries = append(entries, ruleHit{importantPriority + inlinePriority, decls.important})
	}
	slices.SortStableFunc(entries, func(a, c ruleHit) int { return cmp.Compare(a.prio, c.prio) })
	var props StyleProps
	for _, e := range entries {
		props = props.merge(e.props)
	}
	return props
}

func (b *htmlBuilder) pushAnchor(elem *xml.Element) {
	if id, ok := elem.AttrAny("id"); ok && id != "" {
		b.inlines = append(b.inlines, model.Anchor{ID: b.ctx.AnchorID(id)})
	}
	if elem.Local == "a" {
		if name, ok := elem.AttrAny("name"); ok && name != "" {
			b.inlines = append(b.inlines, model.Anchor{ID: b.ctx.AnchorID(name)})
		}
	}
}

func (b *htmlBuilder) walkChildren(elem *xml.Element, delta StyleDelta) error {
	for _, node := range elem.Children {
		switch n := node.(type) {
		case xml.Text:
			b.pushText(string(n), delta)
		case *xml.Element:
			if err := b.walkElem(n, delta); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *htmlBuilder) pushText(text string, delta StyleDelta) {
	collapsed := CollapseWS(CleanText(text))
	if collapsed == "" {
		return
	}
	if atSpaceBoundary(b.inlines, b.startBoundary) {
		collapsed = strings.TrimPrefix(collapsed, " ")
	}
	if collapsed == "" {
		return
	}
	b.inlines = append(b.inlines, model.Run{Text: collapsed, Style: delta.Resolve()})
}

func (b *htmlBuilder) walkElem(elem *xml.Element, delta StyleDelta) error {
	props := b.elementProps(elem)
	if props.Hidden != nil && *props.Hidden {
		return nil
	}
	// Cascade order: inherited delta, then the tag's presentational
	// default (`<b>`, `<i>`, …), then CSS — so `font-weight: normal`
	// can undo a `<b>`.
	delta = mergeInlineTag(elem, delta).Merge(props.Delta)
	switch elem.Local {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		b.flushParagraph()
		level := uint8(1)
		if n, err := strconv.ParseUint(elem.Local[1:], 10, 8); err == nil {
			level = uint8(n)
		}
		content, err := b.inlineChildren(elem, delta)
		if err != nil {
			return err
		}
		RebaseEmphasis(content, delta.Resolve())
		var anchor model.AnchorID
		hasAnchor := false
		if id, ok := elem.AttrAny("id"); ok {
			hasAnchor = true
			anchor = b.ctx.AnchorID(id)
		}
		if !model.InlinesAreEmpty(content) {
			b.blocks = append(b.blocks, model.Heading{Level: level, Anchor: anchor, Inlines: content})
		} else {
			var kept []model.Inline
			if hasAnchor {
				kept = append(kept, model.Anchor{ID: anchor})
			}
			for _, i := range content {
				if _, ok := i.(model.Anchor); ok {
					kept = append(kept, i)
				}
			}
			if len(kept) > 0 {
				b.blocks = append(b.blocks, model.Paragraph{Inlines: kept})
			}
		}
	case "p":
		b.flushParagraph()
		b.pushAnchor(elem)
		content := b.inlines
		b.inlines = nil
		kids, err := b.inlineChildren(elem, delta)
		if err != nil {
			return err
		}
		content = append(content, kids...)
		if keepsParagraph(content) {
			b.blocks = append(b.blocks, model.Paragraph{Inlines: content})
		}
	case "ul", "ol":
		b.flushParagraph()
		lists, err := b.parseList(elem, delta)
		if err != nil {
			return err
		}
		b.blocks = append(b.blocks, lists...)
	case "table":
		b.flushParagraph()
		for cap := range elem.ChildElems() {
			if cap.Local == "caption" {
				content, err := b.inlineChildren(cap, delta)
				if err != nil {
					return err
				}
				if keepsParagraph(content) {
					b.blocks = append(b.blocks, model.Paragraph{Inlines: content})
				}
				break
			}
		}
		if t, err := b.parseTable(elem, delta); err != nil {
			return err
		} else if t != nil {
			b.blocks = append(b.blocks, t)
		}
	case "blockquote":
		b.flushParagraph()
		inner, err := b.subBlocks(elem, delta)
		if err != nil {
			return err
		}
		if len(inner) > 0 {
			b.blocks = append(b.blocks, model.Quote{Blocks: inner})
		}
	case "pre":
		b.flushParagraph()
		text := elem.Text()
		if strings.TrimSpace(text) != "" {
			b.blocks = append(b.blocks, model.CodeBlock{Code: text})
		}
	case "hr":
		b.flushParagraph()
		b.blocks = append(b.blocks, model.Rule{})
	case "math":
		tex := sharedmath.MathMLToTeX(elem)
		if tex == "" {
			// empty
		} else if sharedmath.MathMLIsDisplay(elem) {
			b.flushParagraph()
			b.blocks = append(b.blocks, model.MathBlock{TeX: tex})
		} else {
			b.inlines = append(b.inlines, model.InlineMath{TeX: tex})
		}
	default:
		if isContainerTag(elem.Local) {
			b.pushAnchor(elem)
			if hasBlockChildren(elem) {
				b.flushParagraph()
				if err := b.walkChildren(elem, delta); err != nil {
					return err
				}
				b.flushParagraph()
			} else if err := b.walkChildren(elem, delta); err != nil {
				return err
			}
		} else if elem.Local == "script" || elem.Local == "style" || elem.Local == "head" ||
			elem.Local == "template" || elem.Local == "noscript" {
			// skip
		} else if err := b.walkInline(elem, delta); err != nil {
			return err
		}
	}
	return nil
}

func (b *htmlBuilder) walkInline(elem *xml.Element, delta StyleDelta) error {
	b.pushAnchor(elem)
	switch elem.Local {
	case "br":
		b.inlines = append(b.inlines, model.LineBreak{})
	case "img", "image":
		alt := ""
		if a, ok := elem.AttrAny("alt"); ok {
			alt = CleanText(a)
		} else {
			alt = CleanText("")
		}
		src := ""
		if s, ok := elem.AttrAny("src"); ok {
			src = s
		} else if s, ok := elem.AttrAny("href"); ok {
			src = s
		}
		source, ok, err := b.ctx.ImageSource(src)
		if err != nil {
			return err
		}
		if ok || strings.TrimSpace(alt) != "" {
			if !ok {
				source = model.UnavailableImage{}
			}
			b.inlines = append(b.inlines, model.Image{Alt: alt, Source: source})
		}
	case "a":
		var target model.LinkTarget
		haveTarget := false
		if href, ok := elem.AttrAny("href"); ok {
			target, haveTarget = b.ctx.LinkTarget(href)
		}
		content, err := b.inlineChildrenAt(elem, delta, atSpaceBoundary(b.inlines, b.startBoundary))
		if err != nil {
			return err
		}
		if haveTarget {
			b.inlines = append(b.inlines, model.Link{Inlines: content, Target: target})
		} else {
			b.inlines = append(b.inlines, content...)
		}
	default:
		return b.walkChildren(elem, delta)
	}
	return nil
}

func (b *htmlBuilder) inlineChildren(elem *xml.Element, delta StyleDelta) ([]model.Inline, error) {
	return b.inlineChildrenAt(elem, delta, true)
}

func (b *htmlBuilder) inlineChildrenAt(elem *xml.Element, delta StyleDelta, startBoundary bool) ([]model.Inline, error) {
	blocks, err := b.subBlocksAt(elem, delta, startBoundary)
	if err != nil {
		return nil, err
	}
	if len(blocks) == 1 {
		if p, ok := blocks[0].(model.Paragraph); ok {
			return p.Inlines, nil
		}
	}
	var out []model.Inline
	for i, block := range blocks {
		if i > 0 {
			out = append(out, model.LineBreak{})
		}
		switch v := block.(type) {
		case model.Paragraph:
			out = append(out, v.Inlines...)
		case model.Heading:
			out = append(out, v.Inlines...)
		default:
			out = append(out, model.Plain(CollapseWS(blockText(v))))
		}
	}
	return out, nil
}

func (b *htmlBuilder) parseList(elem *xml.Element, delta StyleDelta) ([]model.Block, error) {
	ordered := elem.Local == "ol"
	var items []*xml.Element
	for e := range elem.ChildElems() {
		if e.Local == "li" {
			items = append(items, e)
		}
	}
	if len(items) == 0 {
		return nil, nil
	}
	if !ordered {
		listItems := make([]model.ListItem, 0, len(items))
		for _, li := range items {
			blocks, err := b.subBlocks(li, delta)
			if err != nil {
				return nil, err
			}
			listItems = append(listItems, model.ListItem{Blocks: blocks})
		}
		return []model.Block{model.ListBlock{List: model.List{
			Marker: model.MarkerBullet, Start: 1, Items: listItems,
		}}}, nil
	}
	marker := model.MarkerDecimal
	if t, ok := elem.AttrAny("type"); ok {
		switch t {
		case "a":
			marker = model.MarkerLowerAlpha
		case "A":
			marker = model.MarkerUpperAlpha
		case "i":
			marker = model.MarkerLowerRoman
		case "I":
			marker = model.MarkerUpperRoman
		}
	}
	_, reversed := elem.AttrAny("reversed")
	start := int64(1)
	if reversed {
		start = int64(len(items))
	}
	if v, ok := elem.AttrAny("start"); ok {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			start = n
		}
	}
	numbers := make([]int64, 0, len(items))
	next := start
	for _, li := range items {
		if v, ok := li.AttrAny("value"); ok {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				next = n
			}
		}
		numbers = append(numbers, next)
		if reversed {
			next = satSub1(next)
		} else {
			next = satAdd1(next)
		}
	}
	if hasNonPositive(numbers) {
		listItems := make([]model.ListItem, 0, len(items))
		for i, li := range items {
			blocks, err := b.subBlocks(li, delta)
			if err != nil {
				return nil, err
			}
			listItems = append(listItems, model.ListItem{
				Blocks:      blocks,
				MarkerLabel: strconv.FormatInt(numbers[i], 10) + ".",
			})
		}
		return []model.Block{model.ListBlock{List: model.List{
			Marker: marker, Start: 1, Items: listItems,
		}}}, nil
	}
	var out []model.Block
	var current *model.List
	lastNumber := int64(0)
	for i, li := range items {
		number := numbers[i]
		blocks, err := b.subBlocks(li, delta)
		if err != nil {
			return nil, err
		}
		item := model.ListItem{Blocks: blocks}
		contiguous := current != nil && checkedAdd1(lastNumber) == number
		if !contiguous {
			if current != nil {
				out = append(out, model.ListBlock{List: *current})
			}
			current = &model.List{Marker: marker, Start: uint64(number)}
		}
		current.Items = append(current.Items, item)
		lastNumber = number
	}
	if current != nil {
		out = append(out, model.ListBlock{List: *current})
	}
	return out, nil
}

func (b *htmlBuilder) parseTable(elem *xml.Element, delta StyleDelta) (model.Block, error) {
	type rowRef struct {
		tr     *xml.Element
		inHead bool
		grp    int
	}
	var rowElems []rowRef
	group := 0
	inImplicit := false
	for child := range elem.ChildElems() {
		switch child.Local {
		case "thead", "tbody", "tfoot":
			if inImplicit {
				inImplicit = false
				group++
			}
			inHead := child.Local == "thead"
			for tr := range child.ChildElems() {
				if tr.Local == "tr" {
					rowElems = append(rowElems, rowRef{tr, inHead, group})
				}
			}
			group++
		case "tr":
			inImplicit = true
			rowElems = append(rowElems, rowRef{child, false, group})
		}
	}
	if len(rowElems) == 0 {
		return nil, nil
	}
	groupEnd := make(map[int]int)
	for i, r := range rowElems {
		groupEnd[r.grp] = i
	}
	builder := model.NewGridBuilder()
	headerRows := 0
	for i, r := range rowElems {
		builder.NextRow()
		allTH := true
		anyCell := false
		for cell := range r.tr.ChildElems() {
			if cell.Local != "td" && cell.Local != "th" {
				continue
			}
			anyCell = true
			if cell.Local != "th" {
				allTH = false
			}
			colSpan := uint32(1)
			if v, ok := cell.AttrAny("colspan"); ok {
				if n, err := strconv.ParseUint(v, 10, 32); err == nil {
					colSpan = uint32(n)
				}
			}
			if colSpan < 1 {
				colSpan = 1
			}
			if colSpan > 1000 {
				colSpan = 1000
			}
			rowSpan := uint32(1)
			if v, ok := cell.AttrAny("rowspan"); ok {
				if n, err := strconv.ParseUint(v, 10, 32); err == nil {
					if n == 0 {
						rowSpan = uint32(groupEnd[r.grp] - i + 1)
					} else {
						rowSpan = uint32(n)
						if rowSpan < 1 {
							rowSpan = 1
						}
						if rowSpan > 65534 {
							rowSpan = 65534
						}
					}
				}
			}
			blocks, err := b.subBlocks(cell, delta)
			if err != nil {
				return nil, err
			}
			if err := builder.Place(model.SpanningCell(blocks, colSpan, rowSpan)); err != nil {
				return nil, err
			}
		}
		if i == headerRows && (r.inHead || (allTH && anyCell)) {
			headerRows++
		}
	}
	table := builder.Finish(model.TableData)
	if len(table.Grid) == 0 {
		return nil, nil
	}
	table.HeaderRows = ResolveHeaderRows(&table, headerRows)
	return model.TableBlock{Table: table}, nil
}

func mergeInlineTag(elem *xml.Element, delta StyleDelta) StyleDelta {
	switch elem.Local {
	case "b", "strong":
		delta.Bold = boolPtr(true)
	case "i", "em", "cite", "dfn", "var":
		delta.Italic = boolPtr(true)
	case "s", "del", "strike":
		delta.Strike = boolPtr(true)
	case "code", "kbd", "samp", "tt":
		delta.Code = boolPtr(true)
	}
	return delta
}

func blockText(block model.Block) string {
	switch v := block.(type) {
	case model.Paragraph:
		return model.InlinesToPlainText(v.Inlines)
	case model.Heading:
		return model.InlinesToPlainText(v.Inlines)
	case model.ListBlock:
		parts := make([]string, 0, len(v.List.Items))
		for _, it := range v.List.Items {
			for _, b := range it.Blocks {
				parts = append(parts, blockText(b))
			}
		}
		return strings.Join(parts, " ")
	case model.Quote:
		parts := make([]string, len(v.Blocks))
		for i, b := range v.Blocks {
			parts[i] = blockText(b)
		}
		return strings.Join(parts, " ")
	case model.CodeBlock:
		return v.Code
	case model.MathBlock:
		return v.TeX
	case model.TableBlock:
		var parts []string
		for _, row := range v.Table.Grid {
			for _, slot := range row {
				o, ok := slot.(model.OriginCell)
				if !ok {
					continue
				}
				inner := make([]string, 0, len(o.Cell.Blocks))
				for _, b := range o.Cell.Blocks {
					inner = append(inner, blockText(b))
				}
				parts = append(parts, strings.Join(inner, " "))
			}
		}
		return strings.Join(parts, " ")
	case model.Rule:
		return ""
	default:
		return ""
	}
}

func isContainerTag(name string) bool {
	switch name {
	case "div", "section", "article", "aside", "main", "nav", "header", "footer",
		"figure", "figcaption", "center", "details", "summary", "li", "dl", "dt", "dd", "body":
		return true
	}
	return false
}

func isBlockTag(name string) bool {
	if isContainerTag(name) {
		return true
	}
	switch name {
	case "p", "ul", "ol", "table", "blockquote", "pre", "hr",
		"h1", "h2", "h3", "h4", "h5", "h6":
		return true
	}
	return false
}

func hasBlockChildren(elem *xml.Element) bool {
	for e := range elem.ChildElems() {
		if isBlockTag(e.Local) {
			return true
		}
	}
	return false
}

func satAdd1(n int64) int64 {
	if n == math.MaxInt64 {
		return n
	}
	return n + 1
}

func satSub1(n int64) int64 {
	if n == math.MinInt64 {
		return n
	}
	return n - 1
}

func checkedAdd1(n int64) int64 {
	if n == math.MaxInt64 {
		return n // sentinel that will not equal number after overflow
	}
	return n + 1
}

func hasNonPositive(ns []int64) bool {
	for _, n := range ns {
		if n < 1 {
			return true
		}
	}
	return false
}

// Silence an unused import if ToBlocks' error path is the only cerr use —
// image_source can return *cerr.Error; the interface already uses error.
var _ = cerr.KindResourceLimit
