package math

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// LaTeX emission shared by the OMML and MathML converters: a buffer that
// keeps control words separated from following letters, and the Unicode
// to LaTeX mappings for symbols, delimiters, accents and function names.

// tex accumulates LaTeX source.
type tex struct {
	out        strings.Builder
	afterMacro bool
}

func newTex() *tex {
	return &tex{}
}

func (t *tex) isEmpty() bool {
	return t.out.Len() == 0
}

func (t *tex) contains(c rune) bool {
	return strings.ContainsRune(t.out.String(), c)
}

func (t *tex) pushStr(s string) {
	first, size := utf8.DecodeRuneInString(s)
	if size == 0 {
		return
	}
	if t.afterMacro && isASCIILetter(first) {
		t.out.WriteByte(' ')
	}
	t.out.WriteString(s)
	t.afterMacro = endsWithControlWord(s)
}

func (t *tex) pushChar(c rune) {
	if t.afterMacro && isASCIILetter(c) {
		t.out.WriteByte(' ')
	}
	t.out.WriteRune(c)
	t.afterMacro = false
}

// pushMacro appends a control word, with its backslash (`\alpha`, `\{`).
// The backslash ends any control word before it, so no separator is needed.
func (t *tex) pushMacro(name string) {
	t.out.WriteString(name)
	t.afterMacro = endsWithControlWord(name)
}

func (t *tex) pushTex(other *tex) {
	if other.isEmpty() {
		return
	}
	t.pushStr(other.out.String())
	t.afterMacro = other.afterMacro
}

// pushGroup appends `{inner}`.
func (t *tex) pushGroup(inner *tex) {
	t.pushChar('{')
	t.pushTex(inner)
	t.pushChar('}')
}

// pushBase appends a script base: `inner` as it stands when it is one atom
// (a single character or control word), `{inner}` otherwise.
func (t *tex) pushBase(inner *tex) {
	s := inner.out.String()
	atom := utf8.RuneCountInString(s) == 1
	if name, ok := strings.CutPrefix(s, `\`); ok && !atom {
		atom = name != ""
		for i := 0; i < len(name); i++ {
			if !isASCIILetterByte(name[i]) {
				atom = false
				break
			}
		}
	}
	if atom {
		t.pushTex(inner)
	} else {
		t.pushGroup(inner)
	}
}

// pushCommand appends `\name{inner}`.
func (t *tex) pushCommand(name string, inner *tex) {
	t.pushMacro(name)
	t.pushGroup(inner)
}

// pushMathText appends text in math mode: symbols become control words,
// styled mathematical alphanumerics fold back to their base letters inside
// the matching font command, and TeX specials are escaped.
func (t *tex) pushMathText(text string) {
	var pending *variantRun
	for _, c := range text {
		if base, v, ok := foldAlnum(c); ok {
			if v != variantPlain {
				if pending != nil && pending.v == v {
					pending.run = append(pending.run, base)
					continue
				}
				t.flushVariant(pending)
				pending = &variantRun{v: v, run: []rune{base}}
				continue
			}
			t.flushVariant(pending)
			pending = nil
			t.pushMathChar(base)
			continue
		}
		t.flushVariant(pending)
		pending = nil
		t.pushMathChar(c)
	}
	t.flushVariant(pending)
}

func (t *tex) flushVariant(pending *variantRun) {
	if pending == nil {
		return
	}
	inner := newTex()
	inner.pushMathText(string(pending.run))
	t.pushCommand(pending.v.command(), inner)
}

// pushMathChar appends one character in math mode.
func (t *tex) pushMathChar(c rune) {
	if mapped, ok := symbol(c); ok {
		if strings.HasPrefix(mapped, `\`) {
			t.pushMacro(mapped)
		} else {
			t.pushStr(mapped)
		}
		return
	}
	switch c {
	case '{', '}', '$', '%', '&', '#', '_':
		t.pushChar('\\')
		t.pushChar(c)
	case '\\':
		t.pushMacro(`\backslash`)
	case '^':
		t.pushStr("\\char`^")
	case '~':
		t.pushMacro(`\sim`)
	case '\u00a0':
		t.pushStr("\\ ")
	default:
		switch {
		case unicode.IsSpace(c):
			t.pushChar(' ')
		case unicode.IsControl(c):
			// Invisible; dropped.
		default:
			t.pushChar(c)
		}
	}
}

// pushTextMode appends `\text{...}`: literal text inside a formula.
func (t *tex) pushTextMode(text string) {
	inner := newTex()
	for _, c := range text {
		switch c {
		case '{', '}', '$', '%', '&', '#', '_':
			inner.pushChar('\\')
			inner.pushChar(c)
		case '\\':
			inner.pushStr(`\textbackslash{}`)
		case '^':
			inner.pushStr(`\textasciicircum{}`)
		case '~':
			inner.pushStr(`\textasciitilde{}`)
		case '\u00a0':
			inner.pushChar('~')
		default:
			switch {
			case unicode.IsSpace(c):
				inner.pushChar(' ')
			case unicode.IsControl(c):
				// Invisible; dropped.
			default:
				inner.pushChar(c)
			}
		}
	}
	t.pushCommand(`\text`, inner)
}

func (t *tex) finish() string {
	src := strings.TrimSpace(t.out.String())
	var out strings.Builder
	out.Grow(len(src))
	prevSpace := true
	for _, c := range src {
		space := c == ' '
		if !(space && prevSpace) {
			out.WriteRune(c)
		}
		prevSpace = space
	}
	return out.String()
}

// variantRun is a maximal run of characters sharing one font variant,
// accumulated by pushMathText to fold them under a single font command.
type variantRun struct {
	v   variant
	run []rune
}

// endsWithControlWord reports whether s ends in a letter-named control word
// (`\alpha`, `\left\langle`), which would swallow a letter written directly
// after it.
func endsWithControlWord(s string) bool {
	i := len(s)
	for i > 0 && isASCIILetterByte(s[i-1]) {
		i--
	}
	return i < len(s) && i > 0 && s[i-1] == '\\'
}

func isASCIILetter(c rune) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isASCIILetterByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// variant is the font variant of a mathematical alphanumeric symbol.
type variant uint8

const (
	variantPlain variant = iota
	variantBold
	variantBoldItalic
	variantScript
	variantFraktur
	variantDoubleStruck
	variantSansSerif
	variantMonospace
)

func (v variant) command() string {
	switch v {
	case variantPlain:
		return `\mathit`
	case variantBold:
		return `\mathbf`
	case variantBoldItalic:
		return `\boldsymbol`
	case variantScript:
		return `\mathcal`
	case variantFraktur:
		return `\mathfrak`
	case variantDoubleStruck:
		return `\mathbb`
	case variantSansSerif:
		return `\mathsf`
	default:
		return `\mathtt`
	}
}

// variantFromMathvariant returns the variant a MathML mathvariant names;
// ok is false for the default and for values without a LaTeX font command.
func variantFromMathvariant(value string) (variant, bool) {
	switch value {
	case "bold":
		return variantBold, true
	case "bold-italic":
		return variantBoldItalic, true
	case "script", "bold-script":
		return variantScript, true
	case "fraktur", "bold-fraktur":
		return variantFraktur, true
	case "double-struck":
		return variantDoubleStruck, true
	case "sans-serif", "bold-sans-serif", "sans-serif-italic", "sans-serif-bold-italic":
		return variantSansSerif, true
	case "monospace":
		return variantMonospace, true
	default:
		return variantPlain, false
	}
}

var (
	greekUpper = []rune("ΑΒΓΔΕΖΗΘΙΚΛΜΝΞΟΠΡϴΣΤΥΦΧΨΩ")
	greekLower = []rune("αβγδεζηθικλμνξοπρςστυφχψω")
	greekExtra = []rune("∂ϵϑϰϕϱϖ")
)

// foldAlnum folds a Mathematical Alphanumeric Symbol (or a letterlike symbol
// in the same role) to its base character and font variant. Italic is the
// default math style, so it folds to variantPlain.
func foldAlnum(c rune) (base rune, v variant, ok bool) {
	switch c {
	case 'ℎ':
		return 'h', variantPlain, true
	case 'ℬ':
		return 'B', variantScript, true
	case 'ℰ':
		return 'E', variantScript, true
	case 'ℱ':
		return 'F', variantScript, true
	case 'ℋ':
		return 'H', variantScript, true
	case 'ℐ':
		return 'I', variantScript, true
	case 'ℒ':
		return 'L', variantScript, true
	case 'ℳ':
		return 'M', variantScript, true
	case 'ℛ':
		return 'R', variantScript, true
	case 'ℯ':
		return 'e', variantScript, true
	case 'ℊ':
		return 'g', variantScript, true
	case 'ℴ':
		return 'o', variantScript, true
	case 'ℭ':
		return 'C', variantFraktur, true
	case 'ℌ':
		return 'H', variantFraktur, true
	case 'ℑ':
		return 'I', variantFraktur, true
	case 'ℜ':
		return 'R', variantFraktur, true
	case 'ℨ':
		return 'Z', variantFraktur, true
	case 'ℂ':
		return 'C', variantDoubleStruck, true
	case 'ℍ':
		return 'H', variantDoubleStruck, true
	case 'ℕ':
		return 'N', variantDoubleStruck, true
	case 'ℙ':
		return 'P', variantDoubleStruck, true
	case 'ℚ':
		return 'Q', variantDoubleStruck, true
	case 'ℝ':
		return 'R', variantDoubleStruck, true
	case 'ℤ':
		return 'Z', variantDoubleStruck, true
	}
	cp := uint32(c)
	if cp < 0x1D400 || cp >= 0x1D800 {
		return 0, variantPlain, false
	}
	offset := cp - 0x1D400
	if offset < 13*52 {
		latin := [13]variant{
			variantBold,
			variantPlain,
			variantBoldItalic,
			variantScript,
			variantScript,
			variantFraktur,
			variantDoubleStruck,
			variantFraktur,
			variantSansSerif,
			variantSansSerif,
			variantSansSerif,
			variantSansSerif,
			variantMonospace,
		}
		style, i := int(offset/52), int(offset%52)
		if i < 26 {
			return rune('A' + i), latin[style], true
		}
		return rune('a' + (i - 26)), latin[style], true
	}
	if cp < 0x1D6A8 {
		return 0, variantPlain, false
	}
	greek := cp - 0x1D6A8
	if greek < 5*58 {
		greekStyles := [5]variant{
			variantBold,
			variantPlain,
			variantBoldItalic,
			variantSansSerif,
			variantSansSerif,
		}
		style, i := int(greek/58), int(greek%58)
		var base rune
		switch {
		case i <= 24:
			base = greekUpper[i]
		case i == 25:
			base = '∇'
		case i <= 50:
			base = greekLower[i-26]
		default:
			base = greekExtra[i-51]
		}
		return base, greekStyles[style], true
	}
	if cp < 0x1D7CE {
		return 0, variantPlain, false
	}
	digit := cp - 0x1D7CE
	if digit < 5*10 {
		digitStyles := [5]variant{
			variantBold,
			variantDoubleStruck,
			variantSansSerif,
			variantSansSerif,
			variantMonospace,
		}
		return rune('0' + digit%10), digitStyles[digit/10], true
	}
	return 0, variantPlain, false
}

// symbol returns LaTeX for a Unicode math character: a control word, a
// literal replacement, or an empty string for characters with no visible
// form. ok is false when the character has no mapping.
func symbol(c rune) (string, bool) {
	switch c {
	// Greek
	case 'α':
		return `\alpha`, true
	case 'β':
		return `\beta`, true
	case 'γ':
		return `\gamma`, true
	case 'δ':
		return `\delta`, true
	case 'ε':
		return `\varepsilon`, true
	case 'ϵ':
		return `\epsilon`, true
	case 'ζ':
		return `\zeta`, true
	case 'η':
		return `\eta`, true
	case 'θ':
		return `\theta`, true
	case 'ϑ':
		return `\vartheta`, true
	case 'ι':
		return `\iota`, true
	case 'κ':
		return `\kappa`, true
	case 'ϰ':
		return `\varkappa`, true
	case 'λ':
		return `\lambda`, true
	case 'μ':
		return `\mu`, true
	case 'ν':
		return `\nu`, true
	case 'ξ':
		return `\xi`, true
	case 'ο':
		return "o", true
	case 'π':
		return `\pi`, true
	case 'ϖ':
		return `\varpi`, true
	case 'ρ':
		return `\rho`, true
	case 'ϱ':
		return `\varrho`, true
	case 'σ':
		return `\sigma`, true
	case 'ς':
		return `\varsigma`, true
	case 'τ':
		return `\tau`, true
	case 'υ':
		return `\upsilon`, true
	case 'φ':
		return `\varphi`, true
	case 'ϕ':
		return `\phi`, true
	case 'χ':
		return `\chi`, true
	case 'ψ':
		return `\psi`, true
	case 'ω':
		return `\omega`, true
	case 'Α':
		return "A", true
	case 'Β':
		return "B", true
	case 'Γ':
		return `\Gamma`, true
	case 'Δ':
		return `\Delta`, true
	case 'Ε':
		return "E", true
	case 'Ζ':
		return "Z", true
	case 'Η':
		return "H", true
	case 'Θ', 'ϴ':
		return `\Theta`, true
	case 'Ι':
		return "I", true
	case 'Κ':
		return "K", true
	case 'Λ':
		return `\Lambda`, true
	case 'Μ':
		return "M", true
	case 'Ν':
		return "N", true
	case 'Ξ':
		return `\Xi`, true
	case 'Ο':
		return "O", true
	case 'Π':
		return `\Pi`, true
	case 'Ρ':
		return "P", true
	case 'Σ':
		return `\Sigma`, true
	case 'Τ':
		return "T", true
	case 'Υ':
		return `\Upsilon`, true
	case 'Φ':
		return `\Phi`, true
	case 'Χ':
		return "X", true
	case 'Ψ':
		return `\Psi`, true
	case 'Ω':
		return `\Omega`, true
	// Big operators
	case '∑':
		return `\sum`, true
	case '∏':
		return `\prod`, true
	case '∐':
		return `\coprod`, true
	case '∫':
		return `\int`, true
	case '∬':
		return `\iint`, true
	case '∭':
		return `\iiint`, true
	case '⨌':
		return `\iiiint`, true
	case '∮':
		return `\oint`, true
	case '∯':
		return `\oiint`, true
	case '∰':
		return `\oiiint`, true
	case '⋃':
		return `\bigcup`, true
	case '⋂':
		return `\bigcap`, true
	case '⋁':
		return `\bigvee`, true
	case '⋀':
		return `\bigwedge`, true
	case '⨁':
		return `\bigoplus`, true
	case '⨂':
		return `\bigotimes`, true
	case '⨀':
		return `\bigodot`, true
	case '⨄':
		return `\biguplus`, true
	case '⨆':
		return `\bigsqcup`, true
	// Binary operators
	case '×':
		return `\times`, true
	case '÷':
		return `\div`, true
	case '±':
		return `\pm`, true
	case '∓':
		return `\mp`, true
	case '⋅', '·', '∙':
		return `\cdot`, true
	case '∗':
		return `\ast`, true
	case '∘':
		return `\circ`, true
	case '∖':
		return `\setminus`, true
	case '⊕':
		return `\oplus`, true
	case '⊖':
		return `\ominus`, true
	case '⊗':
		return `\otimes`, true
	case '⊘':
		return `\oslash`, true
	case '⊙':
		return `\odot`, true
	case '∪':
		return `\cup`, true
	case '∩':
		return `\cap`, true
	case '⊎':
		return `\uplus`, true
	case '⊓':
		return `\sqcap`, true
	case '⊔':
		return `\sqcup`, true
	case '∧':
		return `\wedge`, true
	case '∨':
		return `\vee`, true
	case '†':
		return `\dagger`, true
	case '‡':
		return `\ddagger`, true
	case '⋆':
		return `\star`, true
	// Relations
	case '≤', '⩽':
		return `\le`, true
	case '≥', '⩾':
		return `\ge`, true
	case '≠':
		return `\ne`, true
	case '≈':
		return `\approx`, true
	case '≡':
		return `\equiv`, true
	case '≢':
		return `\not\equiv`, true
	case '≅':
		return `\cong`, true
	case '≃':
		return `\simeq`, true
	case '∼':
		return `\sim`, true
	case '≁':
		return `\nsim`, true
	case '∝':
		return `\propto`, true
	case '≪':
		return `\ll`, true
	case '≫':
		return `\gg`, true
	case '≺':
		return `\prec`, true
	case '≻':
		return `\succ`, true
	case '⪯', '≼':
		return `\preceq`, true
	case '⪰', '≽':
		return `\succeq`, true
	case '⊂':
		return `\subset`, true
	case '⊃':
		return `\supset`, true
	case '⊆':
		return `\subseteq`, true
	case '⊇':
		return `\supseteq`, true
	case '⊄':
		return `\not\subset`, true
	case '⊈':
		return `\nsubseteq`, true
	case '⊊':
		return `\subsetneq`, true
	case '⊋':
		return `\supsetneq`, true
	case '⊏':
		return `\sqsubset`, true
	case '⊐':
		return `\sqsupset`, true
	case '⊑':
		return `\sqsubseteq`, true
	case '⊒':
		return `\sqsupseteq`, true
	case '∈':
		return `\in`, true
	case '∉':
		return `\notin`, true
	case '∋':
		return `\ni`, true
	case '∌':
		return `\not\ni`, true
	case '≐':
		return `\doteq`, true
	case '≜':
		return `\triangleq`, true
	case '≝':
		return `\overset{\mathrm{def}}{=}`, true
	case '≔':
		return `\coloneqq`, true
	case '⊥':
		return `\perp`, true
	case '∥':
		return `\parallel`, true
	case '∦':
		return `\nparallel`, true
	case '⊢':
		return `\vdash`, true
	case '⊣':
		return `\dashv`, true
	case '⊨':
		return `\models`, true
	case '⊤':
		return `\top`, true
	case '≍':
		return `\asymp`, true
	case '≀':
		return `\wr`, true
	case '⋈':
		return `\bowtie`, true
	case '∣':
		return `\mid`, true
	case '∤':
		return `\nmid`, true
	// Arrows
	case '→':
		return `\to`, true
	case '←':
		return `\leftarrow`, true
	case '↔':
		return `\leftrightarrow`, true
	case '⇒':
		return `\Rightarrow`, true
	case '⇐':
		return `\Leftarrow`, true
	case '⇔':
		return `\Leftrightarrow`, true
	case '↑':
		return `\uparrow`, true
	case '↓':
		return `\downarrow`, true
	case '↕':
		return `\updownarrow`, true
	case '⇑':
		return `\Uparrow`, true
	case '⇓':
		return `\Downarrow`, true
	case '⇕':
		return `\Updownarrow`, true
	case '↦':
		return `\mapsto`, true
	case '⟶':
		return `\longrightarrow`, true
	case '⟵':
		return `\longleftarrow`, true
	case '⟷':
		return `\longleftrightarrow`, true
	case '⟹':
		return `\Longrightarrow`, true
	case '⟸':
		return `\Longleftarrow`, true
	case '⟺':
		return `\Longleftrightarrow`, true
	case '⟼':
		return `\longmapsto`, true
	case '↗':
		return `\nearrow`, true
	case '↘':
		return `\searrow`, true
	case '↙':
		return `\swarrow`, true
	case '↖':
		return `\nwarrow`, true
	case '↩':
		return `\hookleftarrow`, true
	case '↪':
		return `\hookrightarrow`, true
	case '⇀':
		return `\rightharpoonup`, true
	case '↼':
		return `\leftharpoonup`, true
	case '⇌':
		return `\rightleftharpoons`, true
	case '↶':
		return `\curvearrowleft`, true
	case '↷':
		return `\curvearrowright`, true
	// Logic and sets
	case '∀':
		return `\forall`, true
	case '∃':
		return `\exists`, true
	case '∄':
		return `\nexists`, true
	case '¬':
		return `\neg`, true
	case '∅', '⌀':
		return `\emptyset`, true
	case '∞':
		return `\infty`, true
	case '∂':
		return `\partial`, true
	case '∇':
		return `\nabla`, true
	case '∴':
		return `\therefore`, true
	case '∵':
		return `\because`, true
	case 'ℵ':
		return `\aleph`, true
	case 'ℶ':
		return `\beth`, true
	case 'ℏ':
		return `\hbar`, true
	case 'ℓ':
		return `\ell`, true
	case '℘':
		return `\wp`, true
	case '℧':
		return `\mho`, true
	case '√':
		return `\surd`, true
	case '∠':
		return `\angle`, true
	case '∡':
		return `\measuredangle`, true
	case '△':
		return `\triangle`, true
	case '□', '◻':
		return `\square`, true
	case '◊':
		return `\lozenge`, true
	case '°':
		return `^{\circ}`, true
	case '′':
		return "'", true
	case '″':
		return "''", true
	case '‴':
		return "'''", true
	case '⁗':
		return "''''", true
	case '…':
		return `\ldots`, true
	case '⋯':
		return `\cdots`, true
	case '⋮':
		return `\vdots`, true
	case '⋱':
		return `\ddots`, true
	case '⋰':
		return `\iddots`, true
	// Delimiters
	case '⟨', '〈':
		return `\langle`, true
	case '⟩', '〉':
		return `\rangle`, true
	case '⌈':
		return `\lceil`, true
	case '⌉':
		return `\rceil`, true
	case '⌊':
		return `\lfloor`, true
	case '⌋':
		return `\rfloor`, true
	case '‖':
		return `\Vert`, true
	// Spacing and invisible characters
	case '−', '‐', '‑', '‒', '–':
		return "-", true
	case '\u2009', '\u200a', '\u2006':
		return `\,`, true
	case '\u2005', '\u2004':
		return `\:`, true
	case '\u2003', '\u2002':
		return `\;`, true
	case '\u2061', '\u2062', '\u2063', '\u2064', '\u200b', '\ufeff':
		return "", true
	default:
		return "", false
	}
}

// delimiter returns LaTeX for a character in a `\left` / `\right` position;
// characters that cannot stretch are written as themselves.
func delimiter(c rune) string {
	switch c {
	case '(', '⟮':
		return "("
	case ')', '⟯':
		return ")"
	case '[':
		return "["
	case ']':
		return "]"
	case '{':
		return `\{`
	case '}':
		return `\}`
	case '|', '∣':
		return "|"
	case '‖', '∥':
		return `\Vert`
	case '⟨', '〈':
		return `\langle`
	case '⟩', '〉':
		return `\rangle`
	case '⌈':
		return `\lceil`
	case '⌉':
		return `\rceil`
	case '⌊':
		return `\lfloor`
	case '⌋':
		return `\rfloor`
	case '/':
		return "/"
	case '\\':
		return `\backslash`
	case '↑':
		return `\uparrow`
	case '↓':
		return `\downarrow`
	case '↕':
		return `\updownarrow`
	case '⇑':
		return `\Uparrow`
	case '⇓':
		return `\Downarrow`
	case '⇕':
		return `\Updownarrow`
	default:
		return "."
	}
}

// accent returns the accent command for a combining mark or its spacing
// equivalent, as used by OMML acc and MathML mover/munder.
func accent(c rune) (string, bool) {
	switch c {
	case '\u0300', '`':
		return `\grave`, true
	case '\u0301', '´':
		return `\acute`, true
	case '\u0302', '^', 'ˆ':
		return `\hat`, true
	case '\u0303', '~', '˜':
		return `\tilde`, true
	case '\u0304', '¯', 'ˉ':
		return `\bar`, true
	case '\u0305', '‾', '⎴':
		return `\overline`, true
	case '\u0306', '˘':
		return `\breve`, true
	case '\u0307', '˙':
		return `\dot`, true
	case '\u0308', '¨':
		return `\ddot`, true
	case '\u030a', '˚':
		return `\mathring`, true
	case '\u030c', 'ˇ':
		return `\check`, true
	case '\u0332', '_', '⎵':
		return `\underline`, true
	case '\u20d6', '←':
		return `\overleftarrow`, true
	case '\u20d7', '→':
		return `\vec`, true
	case '\u20e1', '↔':
		return `\overleftrightarrow`, true
	case '\u20db':
		return `\dddot`, true
	case '\u20dc':
		return `\ddddot`, true
	case '\u20ee':
		return `\underleftarrow`, true
	case '\u20ef':
		return `\underrightarrow`, true
	case '⏞', '︷':
		return `\overbrace`, true
	case '⏟', '︸':
		return `\underbrace`, true
	default:
		return "", false
	}
}

// functionName returns the control word for a function name LaTeX
// predefines (`\sin`), or an `\operatorname` for any other alphabetic name.
func functionName(name string) (string, bool) {
	known := []string{
		"arccos", "arcsin", "arctan", "arg", "cos", "cosh", "cot", "coth", "csc", "deg", "det",
		"dim", "exp", "gcd", "hom", "inf", "ker", "lg", "lim", "liminf", "limsup", "ln", "log",
		"max", "min", "Pr", "sec", "sin", "sinh", "sup", "tan", "tanh",
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false
	}
	for _, c := range name {
		if !isASCIILetter(c) {
			return "", false
		}
	}
	for _, k := range known {
		if k == name {
			return `\` + name, true
		}
	}
	return `\operatorname{` + name + `}`, true
}
