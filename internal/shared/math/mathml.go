package math

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/m7medVision/anydoc-go/internal/package/xml"
)

// Presentation MathML to LaTeX, for EPUB chapters and OpenDocument
// formula objects. A TeX annotation, when the producer kept one, is taken
// verbatim over a reconversion of the presentation tree.

// MathMLToTeX returns a math element as LaTeX source without delimiters.
func MathMLToTeX(math *xml.Element) string {
	tex := newTex()
	walkChildren(math, tex)
	return tex.finish()
}

// MathMLIsDisplay reports whether a math element asks for display (block)
// layout.
func MathMLIsDisplay(math *xml.Element) bool {
	display, ok := math.AttrAny("display")
	return ok && display == "block"
}

func walkChildren(elem *xml.Element, tex *tex) {
	for _, node := range elem.Children {
		if child, ok := node.(*xml.Element); ok {
			walkElem(child, tex)
		}
		// Text outside token elements is layout whitespace.
	}
}

// args returns the child elements, skipping nothing: MathML layout
// schemata count their arguments by position.
func args(elem *xml.Element) []*xml.Element {
	children := make([]*xml.Element, 0, len(elem.Children))
	for child := range elem.ChildElems() {
		children = append(children, child)
	}
	return children
}

func at(elems []*xml.Element, i int) *xml.Element {
	if i < len(elems) {
		return elems[i]
	}
	return nil
}

func sub(elem *xml.Element) *tex {
	tex := newTex()
	walkElem(elem, tex)
	return tex
}

func subChildren(elem *xml.Element) *tex {
	tex := newTex()
	walkChildren(elem, tex)
	return tex
}

func walkElem(e *xml.Element, tex *tex) {
	switch e.Local {
	case "semantics":
		if annotation, ok := texAnnotation(e); ok {
			tex.pushStr(annotation)
		} else {
			for child := range e.ChildElems() {
				if child.Local != "annotation" && child.Local != "annotation-xml" {
					walkElem(child, tex)
					break
				}
			}
		}
	case "annotation", "annotation-xml":
		// Replaced by the presentation tree in the semantics arm.
	case "mi":
		identifier(e, tex)
	case "mn":
		tex.pushMathText(e.Text())
	case "mo":
		operator(e, tex)
	case "mtext":
		text := e.Text()
		if strings.TrimSpace(text) != "" {
			tex.pushTextMode(strings.Trim(text, "\n\r"))
		}
	case "ms":
		lquote, rquote := `"`, `"`
		if v, ok := e.AttrAny("lquote"); ok {
			lquote = v
		}
		if v, ok := e.AttrAny("rquote"); ok {
			rquote = v
		}
		tex.pushTextMode(lquote + strings.Trim(e.Text(), "\n\r") + rquote)
	case "mspace":
		// Only the sign of the width survives: zero (the default) is
		// nothing, negative a thin backspace, anything else a space.
		width := "0"
		if v, ok := e.AttrAny("width"); ok {
			width = v
		}
		width = strings.TrimSpace(width)
		negative := strings.HasPrefix(width, "-") || strings.HasPrefix(width, "negative")
		numeric := strings.TrimLeft(width, "-")
		var digits strings.Builder
		for _, c := range numeric {
			if (c >= '0' && c <= '9') || c == '.' {
				digits.WriteRune(c)
			} else {
				break
			}
		}
		magnitude, err := strconv.ParseFloat(digits.String(), 64)
		if err != nil {
			magnitude = 1.0
		}
		switch {
		case magnitude == 0:
		case negative:
			tex.pushStr(`\!`)
		default:
			tex.pushStr("\\ ")
		}
	case "mfrac":
		parts := args(e)
		if len(parts) < 2 {
			walkChildren(e, tex)
			break
		}
		num, den := sub(parts[0]), sub(parts[1])
		bevelled, hasBevelled := e.AttrAny("bevelled")
		linethickness, hasLinethickness := e.AttrAny("linethickness")
		switch {
		case hasBevelled && bevelled == "true":
			tex.pushGroup(num)
			tex.pushChar('/')
			tex.pushGroup(den)
		case hasLinethickness && (strings.TrimSpace(linethickness) == "0" || strings.TrimSpace(linethickness) == "0px"):
			tex.pushChar('{')
			tex.pushTex(num)
			tex.pushMacro(`\atop`)
			tex.pushTex(den)
			tex.pushChar('}')
		default:
			tex.pushMacro(`\frac`)
			tex.pushGroup(num)
			tex.pushGroup(den)
		}
	case "msqrt":
		tex.pushCommand(`\sqrt`, subChildren(e))
	case "mroot":
		parts := args(e)
		tex.pushMacro(`\sqrt`)
		if len(parts) > 1 {
			tex.pushChar('[')
			tex.pushTex(sub(parts[1]))
			tex.pushChar(']')
		}
		if len(parts) > 0 {
			tex.pushGroup(sub(parts[0]))
		}
	case "msup", "msub", "msubsup":
		scripts(e, tex)
	case "munder", "mover", "munderover":
		underOver(e, tex)
	case "mmultiscripts":
		multiscripts(e, tex)
	case "mfenced":
		open, close := "(", ")"
		if v, ok := e.AttrAny("open"); ok {
			open = v
		}
		if v, ok := e.AttrAny("close"); ok {
			close = v
		}
		var separators []rune
		sepAttr := ","
		if v, ok := e.AttrAny("separators"); ok {
			sepAttr = v
		}
		for _, c := range sepAttr {
			if !unicode.IsSpace(c) {
				separators = append(separators, c)
			}
		}
		tex.pushMacro(`\left`)
		tex.pushStr(delimiter(firstRuneOr(open, '.')))
		i := 0
		for part := range e.ChildElems() {
			if i > 0 {
				// The last separator repeats for any further arguments.
				if i-1 < len(separators) {
					tex.pushMathChar(separators[i-1])
				} else if len(separators) > 0 {
					tex.pushMathChar(separators[len(separators)-1])
				}
			}
			tex.pushTex(sub(part))
			i++
		}
		tex.pushMacro(`\right`)
		tex.pushStr(delimiter(firstRuneOr(close, '.')))
	case "mtable":
		table(e, tex)
	case "mphantom":
		tex.pushCommand(`\phantom`, subChildren(e))
	case "menclose":
		notation := "longdiv"
		if v, ok := e.AttrAny("notation"); ok {
			notation = v
		}
		inner := subChildren(e)
		word, hasWord := firstWord(notation)
		switch {
		case hasWord && (word == "box" || word == "roundedbox"):
			tex.pushCommand(`\boxed`, inner)
		case hasWord && word == "top":
			tex.pushCommand(`\overline`, inner)
		case hasWord && (word == "bottom" || word == "underline"):
			tex.pushCommand(`\underline`, inner)
		case hasWord && (word == "horizontalstrike" || word == "updiagonalstrike" || word == "downdiagonalstrike"):
			tex.pushCommand(`\cancel`, inner)
		default:
			tex.pushGroup(inner)
		}
	case "mglyph", "maligngroup", "malignmark", "none", "mprescripts":
		// No LaTeX form.
	default:
		// mrow, mstyle, mpadded, merror, maction, math itself, and anything
		// unknown: the children carry the content.
		walkChildren(e, tex)
	}
}

// firstRuneOr returns s's first rune, or def when s is empty.
func firstRuneOr(s string, def rune) rune {
	first, size := utf8.DecodeRuneInString(s)
	if size == 0 {
		return def
	}
	return first
}

// firstWord returns the first whitespace-separated word of s.
func firstWord(s string) (string, bool) {
	for _, word := range strings.Fields(s) {
		return word, true
	}
	return "", false
}

func texAnnotation(semantics *xml.Element) (string, bool) {
	for annotation := range semantics.ChildElems() {
		if annotation.Local != "annotation" {
			continue
		}
		encoding, ok := annotation.AttrAny("encoding")
		if !ok {
			continue
		}
		switch asciiLower(strings.TrimSpace(encoding)) {
		case "application/x-tex", "tex", "latex", "application/x-latex":
			if text := strings.TrimSpace(annotation.Text()); text != "" {
				return text, true
			}
		}
	}
	return "", false
}

// asciiLower lowercases ASCII letters only.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

func identifier(e *xml.Element, tex *tex) {
	text := strings.TrimSpace(e.Text())
	if text == "" {
		return
	}
	mathvariant, hasVariant := e.AttrAny("mathvariant")
	single := utf8.RuneCountInString(text) == 1
	// A multi-letter identifier renders upright: a function name where
	// LaTeX has one, otherwise roman text.
	if !single && (!hasVariant || mathvariant == "normal") && isAlphabetic(text) {
		if name, ok := functionName(text); ok {
			tex.pushMacro(name)
		} else {
			inner := newTex()
			inner.pushMathText(text)
			tex.pushCommand(`\mathrm`, inner)
		}
		return
	}
	inner := newTex()
	inner.pushMathText(text)
	if v, ok := variantFromMathvariant(mathvariant); hasVariant && ok {
		tex.pushCommand(v.command(), inner)
	} else if hasVariant && mathvariant == "normal" && single {
		tex.pushCommand(`\mathrm`, inner)
	} else {
		tex.pushTex(inner)
	}
}

// isAlphabetic reports whether every rune carries the Unicode Alphabetic
// property (Rust's char::is_alphabetic).
func isAlphabetic(s string) bool {
	for _, c := range s {
		if !unicode.In(c, unicode.L, unicode.Nl, unicode.Other_Alphabetic) {
			return false
		}
	}
	return true
}

func operator(e *xml.Element, tex *tex) {
	text := strings.TrimSpace(e.Text())
	if utf8.RuneCountInString(text) > 1 && isASCIIAlpha(text) {
		// `<mo>lim</mo>`, `<mo>max</mo>`: a named operator.
		if name, ok := functionName(text); ok {
			tex.pushMacro(name)
			return
		}
	}
	tex.pushMathText(text)
}

func isASCIIAlpha(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isASCIILetterByte(s[i]) {
			return false
		}
	}
	return true
}

func scripts(e *xml.Element, tex *tex) {
	parts := args(e)
	base := at(parts, 0)
	if base == nil {
		return
	}
	baseTex := sub(base)
	if isBigOperator(base) {
		tex.pushTex(baseTex)
	} else {
		tex.pushBase(baseTex)
	}
	switch e.Local {
	case "msup":
		script(tex, '^', at(parts, 1))
	case "msub":
		script(tex, '_', at(parts, 1))
	default:
		script(tex, '_', at(parts, 1))
		script(tex, '^', at(parts, 2))
	}
}

func script(tex *tex, mark rune, arg *xml.Element) {
	if arg == nil {
		return
	}
	tex.pushChar(mark)
	tex.pushGroup(sub(arg))
}

func underOver(e *xml.Element, tex *tex) {
	parts := args(e)
	base := at(parts, 0)
	if base == nil {
		return
	}
	var under, over *xml.Element
	switch e.Local {
	case "munder":
		under = at(parts, 1)
	case "mover":
		over = at(parts, 1)
	default:
		under, over = at(parts, 1), at(parts, 2)
	}
	// Limits on a big operator or a named function take script form,
	// which LaTeX places under and over by itself.
	if isBigOperator(base) || isNamedFunction(base) {
		tex.pushTex(sub(base))
		script(tex, '_', under)
		script(tex, '^', over)
		return
	}
	// An explicit accent="false" / accentunder="false" asks for a plain
	// over- or under-script even on an accent character.
	plainOver := isFalseAttr(e, "accent")
	plainUnder := isFalseAttr(e, "accentunder")
	body := sub(base)
	if over != nil {
		body = decorate(over, body, true, plainOver)
	}
	if under != nil {
		body = decorate(under, body, false, plainUnder)
	}
	tex.pushTex(body)
}

func isFalseAttr(e *xml.Element, name string) bool {
	value, ok := e.AttrAny(name)
	return ok && value == "false"
}

// decorate places `mark` over or under `body`: an accent command where
// the mark is an accent character (unless `plain`), `\overset` /
// `\underset` otherwise.
func decorate(mark *xml.Element, body *tex, over, plain bool) *tex {
	out := newTex()
	text := strings.TrimSpace(mark.Text())
	if !plain && utf8.RuneCountInString(text) == 1 {
		if first, size := utf8.DecodeRuneInString(text); size > 0 {
			if cmd, ok := accent(first); ok {
				switch {
				case cmd == `\underline` && over:
					cmd = `\overline`
				case cmd == `\overline` && !over:
					cmd = `\underline`
				case cmd == `\underbrace` && over:
					cmd = `\overbrace`
				case cmd == `\overbrace` && !over:
					cmd = `\underbrace`
				}
				out.pushCommand(cmd, body)
				return out
			}
		}
	}
	if over {
		out.pushMacro(`\overset`)
	} else {
		out.pushMacro(`\underset`)
	}
	out.pushGroup(sub(mark))
	out.pushGroup(body)
	return out
}

func multiscripts(e *xml.Element, tex *tex) {
	parts := args(e)
	if len(parts) == 0 {
		return
	}
	base, rest := parts[0], parts[1:]
	split := len(rest)
	for i, part := range rest {
		if part.Local == "mprescripts" {
			split = i
			break
		}
	}
	post, pre := rest[:split], rest[split:]
	if len(pre) > 0 {
		pre = pre[1:]
	}
	for i := 0; i < len(pre); i += 2 {
		end := i + 2
		if end > len(pre) {
			end = len(pre)
		}
		tex.pushStr("{}")
		scriptPair(tex, pre[i:end])
	}
	tex.pushGroup(sub(base))
	for i := 0; i < len(post); i += 2 {
		end := i + 2
		if end > len(post) {
			end = len(post)
		}
		scriptPair(tex, post[i:end])
	}
}

func scriptPair(tex *tex, pair []*xml.Element) {
	if subElem := at(pair, 0); subElem != nil && subElem.Local != "none" {
		tex.pushChar('_')
		tex.pushGroup(sub(subElem))
	}
	if supElem := at(pair, 1); supElem != nil && supElem.Local != "none" {
		tex.pushChar('^')
		tex.pushGroup(sub(supElem))
	}
}

func table(e *xml.Element, tex *tex) {
	tex.pushStr(`\begin{matrix}`)
	i := 0
	for row := range e.ChildElems() {
		if row.Local != "mtr" && row.Local != "mlabeledtr" {
			continue
		}
		if i > 0 {
			tex.pushStr(` \\`)
		}
		j := 0
		for cell := range row.ChildElems() {
			if cell.Local != "mtd" {
				continue
			}
			if j > 0 {
				tex.pushStr(" & ")
			} else {
				tex.pushStr(" ")
			}
			tex.pushTex(subChildren(cell))
			j++
		}
		i++
	}
	tex.pushStr(` \end{matrix}`)
}

// isBigOperator reports whether e is an mo holding an n-ary operator
// (∑, ∫, ...).
func isBigOperator(e *xml.Element) bool {
	if e.Local != "mo" {
		return false
	}
	runes := []rune(strings.TrimSpace(e.Text()))
	if len(runes) != 1 {
		return false
	}
	s, ok := symbol(runes[0])
	if !ok {
		return false
	}
	switch s {
	case `\sum`, `\prod`, `\coprod`, `\int`, `\iint`, `\iiint`, `\iiiint`,
		`\oint`, `\oiint`, `\oiiint`, `\bigcup`, `\bigcap`, `\bigvee`,
		`\bigwedge`, `\bigoplus`, `\bigotimes`, `\bigodot`, `\biguplus`,
		`\bigsqcup`:
		return true
	default:
		return false
	}
}

// isNamedFunction reports whether e is an mi or mo naming a function that
// takes limits (`lim`, `max`).
func isNamedFunction(e *xml.Element) bool {
	if e.Local != "mi" && e.Local != "mo" {
		return false
	}
	text := strings.TrimSpace(e.Text())
	_, ok := functionName(text)
	return utf8.RuneCountInString(text) > 1 && ok
}
