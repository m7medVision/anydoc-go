// Package markdown is the GitHub-Flavored Markdown serializer for the
// document model (src/render/markdown).
package markdown

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/m7medVision/anydoc-go/internal/model"
)

// noteNumbers maps a footnote id to its rendered number, shared by all
// render functions.
type noteNumbers map[string]int

// ctx is the immutable render context threaded through every render
// function.
type ctx struct {
	nums    noteNumbers
	anchors anchorMap
}

// Document renders a parsed document to GitHub-Flavored Markdown.
func Document(doc model.Document) string {
	rc := ctx{nums: numberNotes(&doc), anchors: resolveAnchors(&doc)}
	var parts []string
	for _, b := range doc.Blocks {
		if s, ok := renderBlock(b, &rc); ok {
			parts = append(parts, s)
		}
	}
	renderedDefs := make(map[int]bool)
	type noteNum struct {
		note *model.Note
		num  int
	}
	var ordered []noteNum
	for i := range doc.Notes {
		if num, ok := rc.nums[doc.Notes[i].ID]; ok {
			ordered = append(ordered, noteNum{&doc.Notes[i], num})
		}
	}
	sort.SliceStable(ordered, func(a, b int) bool { return ordered[a].num < ordered[b].num })
	for _, nn := range ordered {
		body := renderBlocks(nn.note.Blocks, &rc)
		if body == "" {
			continue
		}
		// The first non-empty definition for a duplicate id wins; later
		// ones are dropped (upstream logs the duplicate at debug level).
		if renderedDefs[nn.num] {
			continue
		}
		renderedDefs[nn.num] = true
		first, rest := firstAndRest(rustLines(body))
		s := "[^" + strconv.Itoa(nn.num) + "]: " + first
		for _, line := range rest {
			s += "\n"
			if line != "" {
				s += "    " + line
			}
		}
		parts = append(parts, s)
	}
	out := strings.Join(parts, "\n\n")
	if out != "" {
		out += "\n"
	}
	return out
}

// escapeMarkerLabel escapes a source-derived composite marker label for
// literal use: control characters collapse to spaces and Markdown syntax is
// neutralized so a crafted label cannot alter document structure.
func escapeMarkerLabel(label string, ic inlineContext) string {
	var cleaned strings.Builder
	cleaned.Grow(len(label))
	for _, c := range label {
		if unicode.IsControl(c) {
			cleaned.WriteByte(' ')
		} else {
			cleaned.WriteRune(c)
		}
	}
	opts := escapeOpts{
		// List-item content re-opens block syntax after the `- ` marker.
		atLineStart:    ic == inlineContextBlock,
		trailingActive: true,
	}
	return escapeText(cleaned.String(), ic, opts)
}

// numberNotes numbers notes in first-reference order; unreferenced notes
// follow at the end. The first note wins a duplicated id.
func numberNotes(doc *model.Document) noteNumbers {
	valid := make(map[string]*model.Note)
	for i := range doc.Notes {
		note := &doc.Notes[i]
		blank := true
		for _, b := range note.Blocks {
			if !blockIsBlank(b) {
				blank = false
				break
			}
		}
		if !blank {
			if _, taken := valid[note.ID]; !taken {
				valid[note.ID] = note
			}
		}
	}
	var order []string
	seen := make(map[string]bool)
	collectNoteRefs(doc.Blocks, valid, &order, seen)
	for i := range doc.Notes {
		id := doc.Notes[i].ID
		if _, isValid := valid[id]; isValid && !seen[id] {
			seen[id] = true
			order = append(order, id)
		}
	}
	nums := make(noteNumbers, len(order))
	for i, id := range order {
		nums[id] = i + 1
	}
	return nums
}

func blockIsBlank(block model.Block) bool {
	if p, ok := block.(model.Paragraph); ok {
		return model.InlinesAreEmpty(p.Inlines)
	}
	return false
}

func collectNoteRefs(blocks []model.Block, valid map[string]*model.Note, order *[]string, seen map[string]bool) {
	var walkInlines func(inlines []model.Inline)
	walkInlines = func(inlines []model.Inline) {
		for _, inline := range inlines {
			switch inl := inline.(type) {
			case model.NoteRef:
				if _, isValid := valid[inl.NoteID]; isValid && !seen[inl.NoteID] {
					seen[inl.NoteID] = true
					*order = append(*order, inl.NoteID)
					collectNoteRefs(valid[inl.NoteID].Blocks, valid, order, seen)
				}
			case model.Link:
				walkInlines(inl.Inlines)
			}
		}
	}
	for _, block := range blocks {
		switch b := block.(type) {
		case model.Paragraph:
			walkInlines(b.Inlines)
		case model.Heading:
			walkInlines(b.Inlines)
		case model.ListBlock:
			for _, item := range b.List.Items {
				collectNoteRefs(item.Blocks, valid, order, seen)
			}
		case model.TableBlock:
			for _, row := range b.Table.Grid {
				for _, slot := range row {
					if origin, ok := slot.(model.OriginCell); ok {
						collectNoteRefs(origin.Cell.Blocks, valid, order, seen)
					}
				}
			}
		case model.Quote:
			collectNoteRefs(b.Blocks, valid, order, seen)
		}
	}
}

func renderBlocks(blocks []model.Block, rc *ctx) string {
	var parts []string
	for _, b := range blocks {
		if s, ok := renderBlock(b, rc); ok {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n\n")
}

func renderBlock(block model.Block, rc *ctx) (string, bool) {
	switch b := block.(type) {
	case model.Heading:
		text := renderInlines(b.Inlines, inlineContextHeading, rc)
		text = strings.TrimSpace(text)
		if text == "" {
			return "", false
		}
		level := b.Level
		if level < 1 {
			level = 1
		}
		if level > 6 {
			level = 6
		}
		return strings.Repeat("#", int(level)) + " " + text, true
	case model.Paragraph:
		text := renderInlines(b.Inlines, inlineContextBlock, rc)
		trimmed := trimParagraph(text)
		if trimmed == "" {
			return "", false
		}
		return trimmed, true
	case model.ListBlock:
		return renderList(&b.List, rc)
	case model.TableBlock:
		// Trivial layout tables are scaffolding; render their content
		// directly.
		if b.Table.Kind == model.TableLayout && b.Table.IsSingleCell() {
			cell := b.Table.Grid[0][0].(model.OriginCell).Cell
			inner := renderBlocks(cell.Blocks, rc)
			if inner == "" {
				return "", false
			}
			return inner, true
		}
		return renderTable(&b.Table, rc)
	case model.Quote:
		inner := renderBlocks(b.Blocks, rc)
		if inner == "" {
			return "", false
		}
		var quoted []string
		for _, l := range rustLines(inner) {
			if l == "" {
				quoted = append(quoted, ">")
			} else {
				quoted = append(quoted, "> "+l)
			}
		}
		return strings.Join(quoted, "\n"), true
	case model.CodeBlock:
		fence := backtickFence(b.Code, 3)
		body := strings.TrimRight(b.Code, "\n")
		return fence + b.Language + "\n" + body + "\n" + fence, true
	case model.Rule:
		return "---", true
	case model.MathBlock:
		tex := strings.TrimSpace(b.TeX)
		if tex == "" {
			return "", false
		}
		// A bare `$` is never valid inside math; escaped, it cannot close
		// the block early.
		var source strings.Builder
		source.Grow(len(tex))
		backslashes := 0
		for _, c := range tex {
			if c == '$' && backslashes%2 == 0 {
				source.WriteByte('\\')
			}
			source.WriteRune(c)
			if c == '\\' {
				backslashes++
			} else {
				backslashes = 0
			}
		}
		return "$$\n" + source.String() + "\n$$", true
	default:
		return "", false
	}
}

func renderList(list *model.List, rc *ctx) (string, bool) {
	if len(list.Items) == 0 {
		return "", false
	}
	var renderedItems []string
	loose := false
	for i := range list.Items {
		item := &list.Items[i]
		// GFM has decimal ordered lists only, so Roman/alphabetic levels
		// render as bullets carrying the source marker as literal text
		// (`- iv. …`) — the source marker semantics stay visible. Items
		// with an explicit label (composite number text) render it the
		// same way.
		var marker string
		switch {
		case item.MarkerLabel != "":
			marker = "- " + escapeMarkerLabel(item.MarkerLabel, inlineContextBlock) + " "
		case list.Marker == model.MarkerBullet:
			marker = "- "
		case list.Marker == model.MarkerDecimal:
			marker = strconv.FormatUint(saturatingAdd(list.Start, uint64(i)), 10) + ". "
		default:
			marker = "- " + list.Marker.Label(saturatingAdd(list.Start, uint64(i))) + " "
		}
		body := renderBlocks(item.Blocks, rc)
		if len(item.Blocks) > 1 {
			loose = true
		}
		indent := strings.Repeat(" ", utf8.RuneCountInString(marker))
		first, rest := firstAndRest(rustLines(body))
		s := marker + first
		for _, line := range rest {
			s += "\n"
			if line == "" {
				loose = true
			} else {
				s += indent + line
			}
		}
		renderedItems = append(renderedItems, s)
	}
	sep := "\n"
	if loose {
		sep = "\n\n"
	}
	return strings.Join(renderedItems, sep), true
}

// trimParagraph trims paragraph lines, keeping hard-break backslashes
// intact.
func trimParagraph(text string) string {
	rawLines := rustLines(text)
	lines := make([]string, len(rawLines))
	for i, l := range rawLines {
		t := strings.TrimLeftFunc(l, unicode.IsSpace)
		if !endsWithHardBreak(t) {
			t = strings.TrimRightFunc(t, unicode.IsSpace)
		}
		if strings.TrimSpace(strings.TrimRight(t, "\\")) == "" {
			t = ""
		}
		lines[i] = t
	}
	start, end := -1, -1
	for i, l := range lines {
		if l != "" {
			if start == -1 {
				start = i
			}
			end = i
		}
	}
	if start == -1 {
		return ""
	}
	out := strings.Join(lines[start:end+1], "\n")
	if endsWithHardBreak(out) {
		runes := []rune(out)
		out = string(runes[:len(runes)-1])
		out = out[:len(strings.TrimRightFunc(out, unicode.IsSpace))]
	}
	return out
}

func endsWithHardBreak(line string) bool {
	runes := []rune(line)
	n := 0
	for i := len(runes) - 1; i >= 0 && runes[i] == '\\'; i-- {
		n++
	}
	return n%2 == 1
}

// firstAndRest splits lines the way Rust's iterator next() plus the
// remainder does: an empty slice yields ("", nil) rather than panicking
// on [1:].
func firstAndRest(lines []string) (string, []string) {
	if len(lines) == 0 {
		return "", nil
	}
	return lines[0], lines[1:]
}

// rustLines splits s the way Rust's str::lines does: on '\n', with a
// trailing '\r' dropped from each line and no line yielded for a trailing
// newline.
func rustLines(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	for i, p := range parts {
		parts[i] = strings.TrimSuffix(p, "\r")
	}
	return parts
}

// saturatingAdd adds with the saturation of Rust's u64::saturating_add.
func saturatingAdd(a, b uint64) uint64 {
	sum := a + b
	if sum < a {
		return math.MaxUint64
	}
	return sum
}
