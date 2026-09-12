// Inline run normalization and rendering.

package markdown

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/m7medVision/anydoc-go/internal/model"
)

// norm is one normalized inline run: the shape inline rendering works on.
// Runs borrow from the source inlines; text is copied only where runs
// actually merge.
type norm interface{ isNorm() }

type normText struct {
	text  string
	style model.Style
}
type normLink struct {
	content []model.Inline
	target  model.LinkTarget
}
type normImage struct {
	alt    string
	source model.ImageSource
}
type normAnchor struct{ id string }
type normNoteRef struct{ id string }
type normLineBreak struct{}
type normMath struct{ tex string }
type normCheckbox struct{ checked bool }

func (normText) isNorm()      {}
func (normLink) isNorm()      {}
func (normImage) isNorm()     {}
func (normAnchor) isNorm()    {}
func (normNoteRef) isNorm()   {}
func (normLineBreak) isNorm() {}
func (normMath) isNorm()      {}
func (normCheckbox) isNorm()  {}

// normalize is a single-pass normalization: drops empty runs, strips
// styling from whitespace-only runs, merges adjacent same-style runs, and
// re-joins styled runs split only by whitespace (`**a** **b**` ->
// `**a b**`). Untargeted anchors drop out here: they render as nothing, so
// leaving them in would part runs that belong together.
func normalize(inlines []model.Inline, rc *ctx) []norm {
	out := make([]norm, 0, len(inlines))
	plain := model.Style{}
	for _, inline := range inlines {
		switch inl := inline.(type) {
		case model.Run:
			if inl.Text == "" {
				continue
			}
			style := inl.Style
			if strings.TrimSpace(inl.Text) == "" {
				style = plain
			}
			if prev, ok := lastNorm(out).(normText); ok && prev.style == style {
				out[len(out)-1] = normText{text: prev.text + inl.Text, style: style}
				continue
			}
			// Bridge: [styled S][ws plain][incoming styled S] merges into
			// one run.
			if style != plain && !style.Code && len(out) >= 2 {
				if ws, ok := out[len(out)-1].(normText); ok &&
					ws.style == plain && strings.TrimSpace(ws.text) == "" {
					if prev, ok := out[len(out)-2].(normText); ok && prev.style == style {
						out = out[:len(out)-1]
						out[len(out)-1] = normText{text: prev.text + ws.text + inl.Text, style: style}
						continue
					}
				}
			}
			out = append(out, normText{text: inl.Text, style: style})
		case model.Link:
			if inl.Target.IsEmpty() {
				// No usable destination: keep the content as plain
				// inlines.
				if !model.InlinesAreEmpty(inl.Inlines) {
					out = append(out, normalize(inl.Inlines, rc)...)
				}
				continue
			}
			out = append(out, normLink{content: inl.Inlines, target: inl.Target})
		case model.Image:
			out = append(out, normImage{alt: inl.Alt, source: inl.Source})
		case model.Anchor:
			if _, ok := rc.anchors.htmlID(inl.ID); !ok {
				continue
			}
			out = append(out, normAnchor{id: inl.ID})
		case model.NoteRef:
			out = append(out, normNoteRef{id: inl.NoteID})
		case model.LineBreak:
			out = append(out, normLineBreak{})
		case model.InlineMath:
			if strings.TrimSpace(inl.TeX) == "" {
				continue
			}
			out = append(out, normMath{tex: strings.TrimSpace(inl.TeX)})
		case model.Checkbox:
			out = append(out, normCheckbox{checked: inl.Checked})
		}
	}
	return out
}

func lastNorm(out []norm) norm {
	if len(out) == 0 {
		return nil
	}
	return out[len(out)-1]
}

func renderInlines(inlines []model.Inline, ic inlineContext, rc *ctx) string {
	return renderInlinesMode(inlines, ic, false, rc)
}

func renderInlinesMode(inlines []model.Inline, ic inlineContext, inLabel bool, rc *ctx) string {
	runs := normalize(inlines, rc)
	suffix := delimsAhead(runs, rc)
	var out strings.Builder
	for idx, run := range runs {
		switch r := run.(type) {
		case normText:
			nextActive := false
			nextNonspace := false
			if idx+1 < len(runs) {
				switch n := runs[idx+1].(type) {
				case normLink, normImage, normNoteRef, normMath:
					nextActive = true
				case normText:
					nextActive = n.style != model.Style{}
				}
				// A hard break renders as `\`, an anchor as `<a ...>`: not
				// markup, but a nonspace character a run-final delimiter
				// can be left-flanking against.
				switch runs[idx+1].(type) {
				case normAnchor, normCheckbox:
					nextNonspace = true
				case normLineBreak:
					nextNonspace = ic != inlineContextHeading
				}
			}
			opts := escapeOpts{
				trailingActive:   nextActive,
				trailingNonspace: nextNonspace,
				trailingDelims:   suffix[idx+1],
				inLabel:          inLabel,
			}
			renderTextRun(r.text, r.style, ic, opts, &out)
		case normNoteRef:
			if num, ok := rc.nums[r.id]; ok {
				out.WriteString("[^" + strconv.Itoa(num) + "]")
			}
		case normLink:
			renderLink(r.content, r.target, ic, rc, &out)
		case normImage:
			renderImage(r.alt, r.source, ic, inLabel, &out)
		case normAnchor:
			if htmlID, ok := rc.anchors.htmlID(r.id); ok {
				out.WriteString(`<a id="` + htmlID + `"></a>`)
			}
		case normLineBreak:
			switch ic {
			case inlineContextBlock:
				out.WriteString("\\\n")
			case inlineContextHeading:
				out.WriteByte(' ')
			case inlineContextTableCell:
				out.WriteByte('\n')
			}
		case normMath:
			pushMathSpan(r.tex, ic, &out)
		case normCheckbox:
			out.WriteString(model.CheckboxText(r.checked))
			// The token stands apart from a caption that follows it.
			if idx+1 < len(runs) && !startsWithSpace(runs[idx+1]) {
				out.WriteByte(' ')
			}
		}
	}
	return out.String()
}

func renderLink(content []model.Inline, target model.LinkTarget, ic inlineContext, rc *ctx, out *strings.Builder) {
	label := renderInlinesMode(content, ic, true, rc)
	var url string
	switch t := target.(type) {
	case model.ExternalLink:
		url = t.URL
	case model.RelativeLink:
		url = t.URL
	case model.AnchorLink:
		fragment, ok := rc.anchors.fragment(t.ID)
		if !ok {
			// Target exists nowhere in the document: degrade to plain
			// text (upstream logs the unresolved id at debug level).
			out.WriteString(renderInlinesMode(content, ic, false, rc))
			return
		}
		url = "#" + fragment
	}
	// Emptiness is tested on the trimmed label, but the rendered label
	// keeps its source-significant edge spaces.
	if strings.TrimSpace(label) == "" {
		if _, isAnchor := target.(model.AnchorLink); isAnchor {
			return
		}
		out.WriteString("[" + escapeURLAsText(url, ic) + "](" + formatURL(url) + ")")
	} else {
		out.WriteString("[" + label + "](" + formatURL(url) + ")")
	}
}

func renderImage(alt string, source model.ImageSource, ic inlineContext, inLabel bool, out *strings.Builder) {
	switch src := source.(type) {
	case model.ExternalImage:
		escaped := escapeText(strings.TrimSpace(alt), ic, escapeOpts{inLabel: true})
		out.WriteString("![" + escaped + "](" + formatURL(src.URL) + ")")
	// Embedded assets render as their alt text: Markdown cannot embed
	// bytes, and the bytes stay available in Document.Assets. A source-less
	// image has only its alt text to offer.
	case model.AssetImage, model.UnavailableImage:
		if strings.TrimSpace(alt) != "" {
			out.WriteString(escapeText(strings.TrimSpace(alt), ic, escapeOpts{inLabel: inLabel}))
		}
	}
}

// delimsAhead returns the pairable delimiters the remaining runs will emit
// into the current rendered line: closer-capable literals in plain text,
// plus the markup that styled runs, code spans, links and images produce.
// A delimiter in an earlier run can pair with any of them across the run
// seam, hard breaks included. The delimiters each suffix of runs emits,
// indexed by where the suffix starts, so one reverse pass answers every
// run's lookahead.
func delimsAhead(runs []norm, rc *ctx) []delims {
	suffix := make([]delims, len(runs)+1)
	for idx := len(runs) - 1; idx >= 0; idx-- {
		d := suffix[idx+1]
		d.union(delimsOf(runs[idx], rc))
		suffix[idx] = d
	}
	return suffix
}

// delimsOf returns what one run contributes to a later run's pairing
// partners.
func delimsOf(run norm, rc *ctx) delims {
	var d delims
	switch r := run.(type) {
	case normText:
		if r.style.Code {
			d.insert('`')
			return d
		}
		if r.style == (model.Style{}) {
			d.insertClosers(r.text)
			return d
		}
		// Emphasis content is escaped, which neutralizes everything but
		// backticks: code spans ignore backslash escapes, so an emitted
		// `\`` still closes a span an earlier raw backtick opens. `]` is
		// the one character escaping leaves raw.
		if r.style.Bold || r.style.Italic {
			d.insert('*')
		}
		if r.style.Strike {
			d.insert('~')
		}
		if strings.ContainsRune(r.text, '`') {
			d.insert('`')
		}
		if strings.ContainsRune(r.text, ']') {
			d.insert(']')
		}
		var masked strings.Builder
		masked.Grow(len(r.text))
		for _, c := range r.text {
			if c != '$' && !unicode.IsSpace(c) {
				c = 'x'
			}
			masked.WriteRune(c)
		}
		d.insertClosers(masked.String())
	case normLink:
		if anchor, ok := r.target.(model.AnchorLink); ok {
			if _, resolved := rc.anchors.fragment(anchor.ID); !resolved {
				// An unresolved target degrades to its rendered content
				// (see renderLink), which emits like any sibling runs.
				for _, sub := range normalize(r.content, rc) {
					d.union(delimsOf(sub, rc))
				}
				return d
			}
		}
		// Emphasis cannot cross a link boundary, but a code span can: a
		// backtick in the label or destination pairs with one outside.
		if emitsBacktick(r.content) || targetHasBacktick(r.target) {
			d.insert('`')
		}
	case normImage:
		switch r.source.(type) {
		case model.ExternalImage:
			if strings.ContainsRune(r.alt, '`') {
				d.insert('`')
			}
		// Sourceless images degrade to their alt as plain text.
		case model.AssetImage, model.UnavailableImage:
			d.insertClosers(r.alt)
		}
	}
	return d
}

func startsWithSpace(run norm) bool {
	switch r := run.(type) {
	case normText:
		if r.text == "" {
			return false
		}
		first, _ := utf8.DecodeRuneInString(r.text)
		return unicode.IsSpace(first)
	case normLineBreak:
		return true
	}
	return false
}

func targetHasBacktick(target model.LinkTarget) bool {
	switch t := target.(type) {
	case model.ExternalLink:
		return strings.ContainsRune(t.URL, '`')
	case model.RelativeLink:
		return strings.ContainsRune(t.URL, '`')
	}
	return false
}

// emitsBacktick reports whether rendering inlines inside a link label
// emits a backtick an earlier raw one can pair with: any backtick in text
// counts even where the label escapes it (code spans ignore backslash
// escapes), and a code run emits fences unless it is whitespace-only and
// loses its styling.
func emitsBacktick(inlines []model.Inline) bool {
	for _, inline := range inlines {
		switch inl := inline.(type) {
		case model.Run:
			if strings.ContainsRune(inl.Text, '`') ||
				(inl.Style.Code && strings.TrimSpace(inl.Text) != "") {
				return true
			}
		case model.Link:
			if emitsBacktick(inl.Inlines) || targetHasBacktick(inl.Target) {
				return true
			}
		case model.Image:
			if strings.ContainsRune(inl.Alt, '`') {
				return true
			}
		}
	}
	return false
}

// renderTextRun emits a styled run, moving edge whitespace outside the
// delimiters. opts carries the trailing context; atLineStart and styled are
// filled in here.
func renderTextRun(text string, style model.Style, ic inlineContext, opts escapeOpts, out *strings.Builder) {
	if style == (model.Style{}) {
		o := opts
		s := out.String()
		o.atLineStart = s == "" || s[len(s)-1] == '\n'
		out.WriteString(escapeText(text, ic, o))
		return
	}
	coreStart := len(text) - len(strings.TrimLeftFunc(text, unicode.IsSpace))
	coreEnd := len(strings.TrimRightFunc(text, unicode.IsSpace))
	lead, core, trail := text[:coreStart], text[coreStart:coreEnd], text[coreEnd:]
	if lead != "" {
		out.WriteString(lead)
	}
	if core != "" {
		if style.Code {
			pushCodeSpan(core, ic, out)
		} else {
			var open strings.Builder
			if style.Strike {
				open.WriteString("~~")
			}
			if style.Bold {
				open.WriteString("**")
			}
			if style.Italic {
				open.WriteByte('*')
			}
			openc := open.String()
			closec := reverseRunes(openc)
			out.WriteString(openc)
			out.WriteString(escapeText(core, ic, escapeOpts{styled: true, inLabel: opts.inLabel}))
			out.WriteString(closec)
		}
	}
	if trail != "" {
		out.WriteString(trail)
	}
}

// reverseRunes reverses a string character by character (Rust's
// open.chars().rev().collect()).
func reverseRunes(s string) string {
	runes := []rune(s)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}

// pushMathSpan emits GFM inline math: `$` hugging both ends of the source.
// A line break inside it would end the paragraph's math span, and a bare
// `$` (never valid inside math) would close it early.
func pushMathSpan(tex string, ic inlineContext, out *strings.Builder) {
	var source strings.Builder
	source.Grow(len(tex))
	backslashes := 0
	for _, c := range strings.TrimSpace(tex) {
		switch {
		case c == '\n':
			source.WriteByte(' ')
		case c == '$' && backslashes%2 == 0:
			source.WriteString("\\$")
		default:
			source.WriteRune(c)
		}
		if c == '\\' {
			backslashes++
		} else {
			backslashes = 0
		}
	}
	s := source.String()
	// A row is split into cells before the math span is parsed, so a bare
	// pipe is syntax here; GFM strips the escaping backslash before the
	// math is read, so an already escaped pipe stays as it is.
	if ic == inlineContextTableCell {
		var escaped strings.Builder
		escaped.Grow(len(s))
		backslashes = 0
		for _, c := range s {
			if c == '|' && backslashes%2 == 0 {
				escaped.WriteByte('\\')
			}
			escaped.WriteRune(c)
			if c == '\\' {
				backslashes++
			} else {
				backslashes = 0
			}
		}
		s = escaped.String()
	}
	out.WriteByte('$')
	out.WriteString(s)
	out.WriteByte('$')
}

// pushCodeSpan emits a code span for text.
func pushCodeSpan(text string, ic inlineContext, out *strings.Builder) {
	t := strings.ReplaceAll(text, "\n", " ")
	fence := backtickFence(t, 1)
	pad := ""
	if strings.HasPrefix(t, "`") || strings.HasSuffix(t, "`") {
		pad = " "
	}
	// A row is split into cells before any code span is parsed, so a pipe
	// is syntax here even though everything else between the fences is
	// literal.
	if ic == inlineContextTableCell {
		t = escapeCellCodeSpan(t)
	}
	out.WriteString(fence)
	out.WriteString(pad)
	out.WriteString(t)
	out.WriteString(pad)
	out.WriteString(fence)
}
