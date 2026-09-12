// Context-sensitive minimal escaping: document text is escaped only where a
// character could actually parse as Markdown syntax in its context.

package markdown

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// inlineContext is where an inline run is being rendered; it controls which
// characters can be syntax there.
type inlineContext int

const (
	inlineContextBlock inlineContext = iota
	inlineContextHeading
	inlineContextTableCell
)

// escapeOpts is fine-grained escaping context beyond inlineContext: where
// the run sits relative to its surroundings.
type escapeOpts struct {
	// atLineStart: the run begins at the start of an output line, where
	// block syntax (headings, list markers, setext underlines) could form.
	atLineStart bool
	// styled: the run is wrapped in emphasis delimiters, so delimiter
	// characters inside it always need escaping.
	styled bool
	// trailingActive: the character following the run is unknown or active
	// markup; pairable delimiters must assume the worst.
	trailingActive bool
	// trailingNonspace: the rest of the line starts with a non-whitespace
	// character that is not active markup (a hard break's backslash, an
	// anchor tag), so a run-final delimiter can still be left-flanking.
	trailingNonspace bool
	// trailingDelims: delimiters that later runs on the same rendered line
	// will emit; a delimiter in this run can pair with one of them across
	// the run seam.
	trailingDelims delims
	// inLabel: inside a link label / image alt, where an unmatched `]` (or
	// `[`) would terminate the label early.
	inLabel bool
}

// delims is the set of pairable delimiter characters (`*` `_` `~` “ ` “
// `]` `$`).
type delims [6]bool

// Delimiter slots: 0 `*`, 1 `_`, 2 `~`, 3 “ ` “, 4 `]`, 5 `$`.
const (
	slotStar = iota
	slotUnderscore
	slotTilde
	slotBacktick
	slotBracket
	slotDollar
)

func delimSlot(c rune) (int, bool) {
	switch c {
	case '*':
		return slotStar, true
	case '_':
		return slotUnderscore, true
	case '~':
		return slotTilde, true
	case '`':
		return slotBacktick, true
	case ']':
		return slotBracket, true
	case '$':
		return slotDollar, true
	}
	return 0, false
}

func (d *delims) insert(c rune) {
	if slot, ok := delimSlot(c); ok {
		d[slot] = true
	}
}

// insertClosers records the partners text contributes when emitted as
// document text: every backtick and `]`, plus the emphasis delimiters that
// can close.
func (d *delims) insertClosers(text string) {
	chars := []rune(text)
	j := 0
	for j < len(chars) {
		end := runEnd(chars, j)
		if slot, ok := partnerSlot(chars, j, end); ok {
			d[slot] = true
		}
		j = end
	}
}

func (d *delims) union(o delims) {
	for slot := range d {
		d[slot] = d[slot] || o[slot]
	}
}

func (d delims) contains(slot int) bool { return d[slot] }

// runEnd returns the end of the run of identical characters starting at j.
func runEnd(chars []rune, j int) int {
	end := j + 1
	for end < len(chars) && chars[end] == chars[j] {
		end++
	}
	return end
}

// partnerSlot returns the slot for the delimiter run j..end when it can act
// as a pairing partner. Backticks and `]` always can: code spans pair by
// backtick-string length (even a backslash-escaped backtick still closes
// one) and brackets pair as link structure. `*`, `_` and `~` pair by
// flanking, and `$` closes math only after a non-space and before a
// non-digit, so those count only where they can close.
func partnerSlot(chars []rune, j, end int) (int, bool) {
	slot, ok := delimSlot(chars[j])
	if !ok {
		return 0, false
	}
	var closes bool
	switch chars[j] {
	case '`', ']':
		closes = true
	case '$':
		closes = canCloseMath(chars, j, end)
	default:
		closes = canClose(chars, j, end)
	}
	return slot, closes
}

// canCloseMath reports whether the `$` run j..end could close a math span:
// not preceded by whitespace, not followed by a digit. Unknown neighbours
// at the edges assume the worst.
func canCloseMath(chars []rune, j, end int) bool {
	hasPrev := j > 0
	prev := rune(0)
	if hasPrev {
		prev = chars[j-1]
	}
	hasNext := end < len(chars)
	next := rune(0)
	if hasNext {
		next = chars[end]
	}
	return !(hasPrev && unicode.IsSpace(prev)) && !(hasNext && isASCIIDigit(next))
}

// canClose reports whether the emphasis or strikethrough run j..end could
// close a pair: approximate right-flanking (not preceded by whitespace, nor
// preceded by punctuation with a word character after), plus the intraword
// exclusion for `_`. Unknown neighbours at the edges assume the worst; the
// punctuation test stays ASCII so an unclassified character never
// suppresses a genuine closer.
func canClose(chars []rune, j, end int) bool {
	hasPrev := j > 0
	prev := rune(0)
	if hasPrev {
		prev = chars[j-1]
	}
	hasNext := end < len(chars)
	next := rune(0)
	if hasNext {
		next = chars[end]
	}
	if hasPrev && unicode.IsSpace(prev) {
		return false
	}
	if hasPrev && isASCIIPunct(prev) && hasNext && isAlphanumeric(next) {
		return false
	}
	return chars[j] != '_' || !(hasPrev && isAlphanumeric(prev) && hasNext && isAlphanumeric(next))
}

// escapeText escapes Markdown syntax in document text.
func escapeText(text string, ic inlineContext, opts escapeOpts) string {
	chars := []rune(text)
	// Last position of each delimiter that can pair; one with no later
	// partner is inert (-1 = none).
	var last [6]int
	for slot := range last {
		last[slot] = -1
	}
	j := 0
	for j < len(chars) {
		end := runEnd(chars, j)
		if slot, ok := partnerSlot(chars, j, end); ok {
			last[slot] = end - 1
		}
		j = end
	}
	var out strings.Builder
	out.Grow(len(text) + 8)
	lineHasContent := !(opts.atLineStart && ic == inlineContextBlock)
	paired := func(slot, i int) bool {
		return opts.trailingActive || opts.trailingDelims.contains(slot) || last[slot] > i
	}
	i := 0
	for i < len(chars) {
		c := chars[i]
		if c == '\n' {
			out.WriteByte('\n')
			if ic == inlineContextBlock {
				lineHasContent = false
			}
			i++
			continue
		}
		startOfLine := !lineHasContent
		if !unicode.IsSpace(c) {
			lineHasContent = true
		}
		hasNext := i+1 < len(chars)
		next := rune(0)
		if hasNext {
			next = chars[i+1]
		}
		// At the run's end the next character is unknown; trailingActive
		// assumes the worst.
		nextNonspace := opts.trailingActive || opts.trailingNonspace
		if hasNext {
			nextNonspace = !unicode.IsSpace(next)
		}
		escape := false
		switch {
		case c == '\\':
			escape = true
		case c == '$':
			// Opens a math span only directly before non-space, and only
			// when a `$` that can close follows.
			escape = nextNonspace && paired(slotDollar, i)
		case c == ']' && opts.inLabel:
			escape = true
		case c == '`':
			escape = opts.styled || paired(slotBacktick, i)
		case c == '*':
			escape = opts.styled || startOfLine || (nextNonspace && paired(slotStar, i))
		case c == '_':
			prevAlnum := i > 0 && isAlphanumeric(chars[i-1])
			nextAlnum := hasNext && isAlphanumeric(next)
			escape = opts.styled || (nextNonspace && !(prevAlnum && nextAlnum) && paired(slotUnderscore, i))
		case c == '~':
			escape = opts.styled || (nextNonspace && paired(slotTilde, i))
		case c == '[':
			escape = opts.inLabel || paired(slotBracket, i)
		case c == '<':
			escape = hasNext && (isASCIILetter(next) || next == '/' || next == '!' || next == '?')
		case c == '!':
			escape = !hasNext && opts.trailingActive
		case c == '|' && ic == inlineContextTableCell:
			escape = true
		case c == '&' && entityAhead(chars[i:]):
			out.WriteString("&amp;")
			i++
			continue
		case c == '#' && startOfLine:
			k := i
			for k < len(chars) && chars[k] == '#' {
				k++
			}
			escape = k >= len(chars) || unicode.IsSpace(chars[k])
		case c == '-' && startOfLine:
			escape = !nextNonspace || lineIsOnly(chars[i:], '-')
		case c == '+' && startOfLine:
			escape = !nextNonspace
		case c == '>' && startOfLine:
			escape = true
		case c == '=' && startOfLine:
			escape = lineIsOnly(chars[i:], '=')
		case isASCIIDigit(c) && startOfLine:
			k := i
			for k < len(chars) && isASCIIDigit(chars[k]) {
				k++
			}
			if k < len(chars) && (chars[k] == '.' || chars[k] == ')') &&
				(k+1 >= len(chars) || unicode.IsSpace(chars[k+1])) {
				for _, d := range chars[i:k] {
					out.WriteRune(d)
				}
				out.WriteByte('\\')
				out.WriteRune(chars[k])
				i = k + 1
				continue
			}
		}
		if escape {
			out.WriteByte('\\')
		}
		out.WriteRune(c)
		i++
	}
	return out.String()
}

// lineIsOnly reports whether the rest of the current line is just c, spaces,
// and tabs (a setext underline or thematic break).
func lineIsOnly(chars []rune, c rune) bool {
	for _, ch := range chars {
		if ch == '\n' {
			break
		}
		if ch != c && ch != ' ' && ch != '\t' {
			return false
		}
	}
	return true
}

// entityAhead reports whether the '&' at chars[0] is followed by what looks
// like a character-entity reference.
func entityAhead(chars []rune) bool {
	i := 1
	if i < len(chars) && chars[i] == '#' {
		return true
	}
	seen := 0
	for i < len(chars) && isASCIIAlphanumeric(chars[i]) {
		i++
		seen++
	}
	return seen > 0 && i < len(chars) && chars[i] == ';'
}

// formatURL formats a link destination, angle-bracketing when needed.
func formatURL(url string) string {
	const hexDigits = "0123456789ABCDEF"
	var escaped strings.Builder
	escaped.Grow(len(url))
	for _, c := range url {
		switch {
		case c == '<':
			escaped.WriteString("%3C")
		case c == '>':
			escaped.WriteString("%3E")
		// Raw pipes split GFM table cells.
		case c == '|':
			escaped.WriteString("%7C")
		// Encode controls so they cannot split the Markdown output.
		case unicode.IsControl(c):
			var buf [4]byte
			n := utf8.EncodeRune(buf[:], c)
			for _, b := range buf[:n] {
				escaped.WriteByte('%')
				escaped.WriteByte(hexDigits[b>>4])
				escaped.WriteByte(hexDigits[b&0x0F])
			}
		default:
			escaped.WriteRune(c)
		}
	}
	e := escaped.String()
	for _, c := range e {
		if unicode.IsSpace(c) || c == '(' || c == ')' {
			return "<" + e + ">"
		}
	}
	return e
}

// escapeURLAsText renders a bare destination as document text.
func escapeURLAsText(url string, ic inlineContext) string {
	var cleaned strings.Builder
	cleaned.Grow(len(url))
	for _, c := range url {
		if unicode.IsControl(c) {
			cleaned.WriteByte(' ')
		} else {
			cleaned.WriteRune(c)
		}
	}
	return escapeText(cleaned.String(), ic, escapeOpts{trailingActive: true, inLabel: true})
}

// escapeCellCodeSpan prepares a code span's text for a table cell, where a
// pipe is the only character between the fences that is still syntax.
//
// A backslash run already sitting in front of a pipe would pair off with the
// escape and leave the pipe bare, so it is doubled to keep the escape
// intact. That doubling survives into the rendered code span: GFM has no
// encoding for a code span that contains a backslash immediately before a
// pipe, and an intact row is worth more than the exact backslash count.
func escapeCellCodeSpan(text string) string {
	var out strings.Builder
	out.Grow(len(text))
	backslashes := 0
	for _, c := range text {
		switch c {
		case '|':
			for range backslashes + 1 {
				out.WriteByte('\\')
			}
			backslashes = 0
		case '\\':
			backslashes++
		default:
			backslashes = 0
		}
		out.WriteRune(c)
	}
	return out.String()
}

// backtickFence returns the shortest backtick fence longer than any
// backtick run in text.
func backtickFence(text string, min int) string {
	longest, run := 0, 0
	for i := 0; i < len(text); i++ {
		if text[i] == '`' {
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	n := longest + 1
	if n < min {
		n = min
	}
	return strings.Repeat("`", n)
}

// isAlphanumeric reports Rust's char::is_alphanumeric: the Unicode
// Alphabetic derived property or a numeric category (Nd, Nl, No).
func isAlphanumeric(r rune) bool {
	return isAlphabetic(r) || unicode.IsNumber(r)
}

func isASCIILetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isASCIIDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

func isASCIIAlphanumeric(r rune) bool {
	return isASCIILetter(r) || isASCIIDigit(r)
}

// isASCIIPunct reports the ASCII punctuation set Rust's
// char::is_ascii_punctuation tests.
func isASCIIPunct(r rune) bool {
	return r > ' ' && r <= '~' && !isASCIIAlphanumeric(r)
}
