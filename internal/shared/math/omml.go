package math

import (
	"strings"
	"unicode/utf8"

	"github.com/m7medVision/anydoc-go/internal/package/xml"
)

// Office Math (OMML) to LaTeX. The same element tree serves docx and pptx
// directly, and rtf through its math destinations, which mirror OMML
// element for element: there, an attribute arrives as the element's text
// (`{\mtype lin}`) and a run's text sits directly in the run.

// OMathToTeX returns one m:oMath as LaTeX source without delimiters.
func OMathToTeX(omath *xml.Element) string {
	tex := newTex()
	ommlWalkChildren(omath, tex, modeMath)
	return tex.finish()
}

// OMathParaToTeX returns the equations of an m:oMathPara, one per m:oMath
// line.
func OMathParaToTeX(para *xml.Element) []string {
	var lines []string
	for omath := range para.FindAll(xml.NsM, "oMath") {
		if tex := OMathToTeX(omath); tex != "" {
			lines = append(lines, tex)
		}
	}
	if len(lines) == 0 {
		whole := OMathToTeX(para)
		if whole == "" {
			return nil
		}
		return []string{whole}
	}
	return lines
}

// mode selects how the OMML walker treats runs.
type mode uint8

const (
	modeMath mode = iota
	// modeFuncName: inside m:fName, run text names a function (`\sin`,
	// `\lim`).
	modeFuncName
	// modeArray: inside an equation-array row, `&` marks an alignment
	// point.
	modeArray
)

func ommlWalkChildren(elem *xml.Element, tex *tex, m mode) {
	for child := range elem.ChildElems() {
		ommlWalkElem(child, tex, m)
	}
}

func ommlWalkElem(e *xml.Element, tex *tex, m mode) {
	if e.NS != xml.NsM {
		// Revision and bookmark markup from the host document.
		switch e.Local {
		case "del", "moveFrom", "rPr", "pPr":
			// Dropped.
		default:
			ommlWalkChildren(e, tex, m)
		}
		return
	}
	switch e.Local {
	case "r":
		run(e, tex, m)
	case "t":
		tex.pushMathText(e.Text())
	case "f":
		fraction(e, tex)
	case "sSup":
		tex.pushBase(arg(e, "e", modeMath))
		tex.pushChar('^')
		tex.pushGroup(arg(e, "sup", modeMath))
	case "sSub":
		tex.pushBase(arg(e, "e", modeMath))
		tex.pushChar('_')
		tex.pushGroup(arg(e, "sub", modeMath))
	case "sSubSup":
		tex.pushBase(arg(e, "e", modeMath))
		tex.pushChar('_')
		tex.pushGroup(arg(e, "sub", modeMath))
		tex.pushChar('^')
		tex.pushGroup(arg(e, "sup", modeMath))
	case "sPre":
		tex.pushStr("{}_")
		tex.pushGroup(arg(e, "sub", modeMath))
		tex.pushChar('^')
		tex.pushGroup(arg(e, "sup", modeMath))
		tex.pushGroup(arg(e, "e", modeMath))
	case "rad":
		deg := arg(e, "deg", modeMath)
		tex.pushMacro(`\sqrt`)
		degHide, hasDegHide := flag(e, "radPr", "degHide")
		if !(hasDegHide && degHide) && !deg.isEmpty() {
			tex.pushChar('[')
			tex.pushTex(deg)
			tex.pushChar(']')
		}
		tex.pushGroup(arg(e, "e", modeMath))
	case "d":
		delimited(e, tex)
	case "nary":
		nary(e, tex)
	case "func":
		if name := e.Find(xml.NsM, "fName"); name != nil {
			ommlWalkChildren(name, tex, modeFuncName)
		}
		tex.pushGroup(arg(e, "e", modeMath))
	case "acc":
		chr := '\u0302'
		if v, ok := prop(e, "accPr", "chr"); ok {
			chr = firstRuneOr(v, '\u0302')
		}
		cmd, ok := accent(chr)
		if !ok {
			cmd = `\hat`
		}
		tex.pushCommand(cmd, arg(e, "e", modeMath))
	case "bar":
		pos, _ := prop(e, "barPr", "pos")
		cmd := `\underline`
		if pos == "top" {
			cmd = `\overline`
		}
		tex.pushCommand(cmd, arg(e, "e", modeMath))
	case "borderBox":
		tex.pushCommand(`\boxed`, arg(e, "e", modeMath))
	case "phant":
		if show, hasShow := flag(e, "phantPr", "show"); hasShow && !show {
			tex.pushCommand(`\phantom`, arg(e, "e", modeMath))
			return
		}
		tex.pushGroup(arg(e, "e", m))
	case "box":
		tex.pushGroup(arg(e, "e", m))
	case "groupChr":
		groupChar(e, tex)
	case "limLow", "limUpp":
		limit(e, tex, m)
	case "m":
		matrix(e, tex)
	case "eqArr":
		equationArray(e, tex)
	default:
		if strings.HasSuffix(e.Local, "Pr") {
			// A property holder.
			return
		}
		ommlWalkChildren(e, tex, m)
	}
}

// arg converts child argument name of parent on its own.
func arg(parent *xml.Element, name string, m mode) *tex {
	tex := newTex()
	if child := parent.Find(xml.NsM, name); child != nil {
		ommlWalkChildren(child, tex, m)
	}
	return tex
}

// prop returns a property value: `parent/pr/name/@m:val`, or the element's
// own text where the tree came from rtf.
func prop(parent *xml.Element, pr, name string) (string, bool) {
	prElem := parent.Find(xml.NsM, pr)
	if prElem == nil {
		return "", false
	}
	elem := prElem.Find(xml.NsM, name)
	if elem == nil {
		return "", false
	}
	if v, ok := elem.Attr(xml.NsM, "val"); ok {
		return v, true
	}
	return strings.TrimSpace(directText(elem)), true
}

// flag returns an on/off property and whether it was present; present with
// no value is on.
func flag(parent *xml.Element, pr, name string) (on, present bool) {
	value, ok := prop(parent, pr, name)
	if !ok {
		return false, false
	}
	switch value {
	case "0", "off", "false":
		return false, true
	}
	return true, true
}

// directText returns the text directly inside elem, excluding text of
// child elements.
func directText(elem *xml.Element) string {
	var out strings.Builder
	for _, node := range elem.Children {
		if t, ok := node.(xml.Text); ok {
			out.WriteString(string(t))
		}
	}
	return out.String()
}

// runText returns a run's text: its m:t children, or its own text where
// the tree came from rtf.
func runText(r *xml.Element) string {
	var fromT strings.Builder
	hasT := false
	for t := range r.FindAll(xml.NsM, "t") {
		hasT = true
		fromT.WriteString(t.Text())
	}
	if hasT {
		return fromT.String()
	}
	direct := directText(r)
	if strings.TrimSpace(direct) == "" {
		return ""
	}
	return direct
}

func run(r *xml.Element, tex *tex, m mode) {
	text := runText(r)
	if text == "" {
		return
	}
	// Run properties sit under m:rPr; rtf writes them on the run itself,
	// with sty and scr as numbers in their enumeration order.
	value := func(name string) (string, bool) {
		elem := r.Find(xml.NsM, "rPr")
		if elem != nil {
			elem = elem.Find(xml.NsM, name)
		}
		if elem == nil {
			elem = r.Find(xml.NsM, name)
		}
		if elem == nil {
			return "", false
		}
		if v, ok := elem.Attr(xml.NsM, "val"); ok {
			return v, true
		}
		return strings.TrimSpace(directText(elem)), true
	}
	if nor, ok := value("nor"); ok && !(nor == "0" || nor == "off" || nor == "false") {
		tex.pushTextMode(text)
		return
	}
	if m == modeFuncName {
		if name, ok := functionName(text); ok {
			tex.pushMacro(name)
			return
		}
	}
	var font string
	hasFont := false
	scr, hasScr := value("scr")
	switch {
	case hasScr && (scr == "script" || scr == "1"):
		font, hasFont = `\mathcal`, true
	case hasScr && (scr == "fraktur" || scr == "2"):
		font, hasFont = `\mathfrak`, true
	case hasScr && (scr == "double-struck" || scr == "3"):
		font, hasFont = `\mathbb`, true
	case hasScr && (scr == "sans-serif" || scr == "4"):
		font, hasFont = `\mathsf`, true
	case hasScr && (scr == "monospace" || scr == "5"):
		font, hasFont = `\mathtt`, true
	default:
		sty, hasSty := value("sty")
		switch {
		case hasSty && (sty == "p" || sty == "0"):
			font, hasFont = `\mathrm`, true
		case hasSty && (sty == "b" || sty == "1"):
			font, hasFont = `\mathbf`, true
		case hasSty && (sty == "bi" || sty == "3"):
			font, hasFont = `\boldsymbol`, true
		}
	}
	inner := newTex()
	if m == modeArray {
		i := 0
		for _, piece := range strings.Split(text, "&") {
			if i > 0 {
				inner.pushStr(" & ")
			}
			inner.pushMathText(piece)
			i++
		}
	} else {
		inner.pushMathText(text)
	}
	if hasFont {
		tex.pushCommand(font, inner)
	} else {
		tex.pushTex(inner)
	}
}

func fraction(e *xml.Element, tex *tex) {
	num := arg(e, "num", modeMath)
	den := arg(e, "den", modeMath)
	ftype, _ := prop(e, "fPr", "type")
	switch ftype {
	case "lin":
		tex.pushGroup(num)
		tex.pushChar('/')
		tex.pushGroup(den)
	case "skw":
		tex.pushStr("{}^")
		tex.pushGroup(num)
		tex.pushStr("/_")
		tex.pushGroup(den)
	case "noBar":
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
}

func delimited(e *xml.Element, tex *tex) {
	// An explicitly empty character means no delimiter on that side.
	chr := func(name string, def rune) *rune {
		v, ok := prop(e, "dPr", name)
		if !ok {
			return &def
		}
		if c, size := utf8.DecodeRuneInString(v); size > 0 {
			return &c
		}
		return nil
	}
	beg := chr("begChr", '(')
	end := chr("endChr", ')')
	sep := chr("sepChr", '|')
	tex.pushMacro(`\left`)
	if beg != nil {
		tex.pushStr(delimiter(*beg))
	} else {
		tex.pushStr(delimiter('.'))
	}
	i := 0
	for part := range e.FindAll(xml.NsM, "e") {
		if i > 0 {
			if sep != nil {
				tex.pushMacro(`\middle`)
				tex.pushStr(delimiter(*sep))
			} else {
				tex.pushChar(',')
			}
		}
		inner := newTex()
		ommlWalkChildren(part, inner, modeMath)
		tex.pushTex(inner)
		i++
	}
	tex.pushMacro(`\right`)
	if end != nil {
		tex.pushStr(delimiter(*end))
	} else {
		tex.pushStr(delimiter('.'))
	}
}

func nary(e *xml.Element, tex *tex) {
	chr := '∫'
	if v, ok := prop(e, "naryPr", "chr"); ok {
		chr = firstRuneOr(v, '∫')
	}
	if op, ok := symbol(chr); ok && strings.HasPrefix(op, `\`) {
		tex.pushMacro(op)
	} else {
		tex.pushMacro(`\operatorname*`)
		inner := newTex()
		inner.pushMathChar(chr)
		tex.pushGroup(inner)
	}
	integral := chr == '∫' || chr == '∬' || chr == '∭' || chr == '⨌' ||
		chr == '∮' || chr == '∯' || chr == '∰'
	limLoc, _ := prop(e, "naryPr", "limLoc")
	switch {
	case limLoc == "undOvr" && integral:
		tex.pushMacro(`\limits`)
	case limLoc == "subSup" && !integral:
		tex.pushMacro(`\nolimits`)
	}
	if subHide, hasSubHide := flag(e, "naryPr", "subHide"); !(hasSubHide && subHide) {
		sub := arg(e, "sub", modeMath)
		if !sub.isEmpty() {
			tex.pushChar('_')
			tex.pushGroup(sub)
		}
	}
	if supHide, hasSupHide := flag(e, "naryPr", "supHide"); !(hasSupHide && supHide) {
		sup := arg(e, "sup", modeMath)
		if !sup.isEmpty() {
			tex.pushChar('^')
			tex.pushGroup(sup)
		}
	}
	tex.pushGroup(arg(e, "e", modeMath))
}

func groupChar(e *xml.Element, tex *tex) {
	chr := '⏟'
	if v, ok := prop(e, "groupChrPr", "chr"); ok {
		chr = firstRuneOr(v, '⏟')
	}
	pos, _ := prop(e, "groupChrPr", "pos")
	top := pos == "top"
	body := arg(e, "e", modeMath)
	if cmd, ok := accent(chr); ok {
		tex.pushCommand(cmd, body)
		return
	}
	mark := newTex()
	mark.pushMathChar(chr)
	if top {
		tex.pushMacro(`\overset`)
	} else {
		tex.pushMacro(`\underset`)
	}
	tex.pushGroup(mark)
	tex.pushGroup(body)
}

func limit(e *xml.Element, tex *tex, m mode) {
	upper := e.Local == "limUpp"
	base := arg(e, "e", m)
	lim := arg(e, "lim", modeMath)
	if m == modeFuncName {
		// `\lim_{x \to 0}`: the operator takes its own limits.
		tex.pushTex(base)
		if upper {
			tex.pushChar('^')
		} else {
			tex.pushChar('_')
		}
		tex.pushGroup(lim)
		return
	}
	if upper {
		tex.pushMacro(`\overset`)
	} else {
		tex.pushMacro(`\underset`)
	}
	tex.pushGroup(lim)
	tex.pushGroup(base)
}

func matrix(e *xml.Element, tex *tex) {
	tex.pushStr(`\begin{matrix}`)
	i := 0
	for row := range e.FindAll(xml.NsM, "mr") {
		if i > 0 {
			tex.pushStr(` \\`)
		}
		j := 0
		for cell := range row.FindAll(xml.NsM, "e") {
			if j > 0 {
				tex.pushStr(" & ")
			} else {
				tex.pushStr(" ")
			}
			inner := newTex()
			ommlWalkChildren(cell, inner, modeMath)
			tex.pushTex(inner)
			j++
		}
		i++
	}
	tex.pushStr(` \end{matrix}`)
}

func equationArray(e *xml.Element, t *tex) {
	var rows []*tex
	for row := range e.FindAll(xml.NsM, "e") {
		inner := newTex()
		ommlWalkChildren(row, inner, modeArray)
		rows = append(rows, inner)
	}
	env := "gathered"
	for _, row := range rows {
		if row.contains('&') {
			env = "aligned"
			break
		}
	}
	t.pushStr(`\begin{` + env + `}`)
	for i, row := range rows {
		if i > 0 {
			t.pushStr(` \\`)
		}
		t.pushChar(' ')
		t.pushTex(row)
	}
	t.pushStr(` \end{` + env + `}`)
}
