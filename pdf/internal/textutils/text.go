package textutils

import (
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// IsExplicitPageNumberExpression reports whether text is an explicit
// page-number expression. This strict form is suitable before layout,
// where removing one numeric item from substantive text such as
// "Page 42 explains the result" would lose data.
func IsExplicitPageNumberExpression(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	isNumber := func(value string) bool {
		if value == "" {
			return false
		}
		for _, c := range value {
			if c < '0' || c > '9' {
				return false
			}
		}
		return true
	}

	if len(trimmed) <= 4 && isNumber(trimmed) {
		return true
	}

	if len(trimmed) >= 3 && strings.HasPrefix(trimmed, "-") && strings.HasSuffix(trimmed, "-") {
		inner := strings.TrimSpace(trimmed[1 : len(trimmed)-1])
		if isNumber(inner) {
			return true
		}
	}

	lowercase := strings.ToLower(trimmed)
	if rest, ok := strings.CutPrefix(lowercase, "page"); ok {
		words := strings.Fields(rest)
		if len(words) >= 3 && isNumber(words[0]) && words[1] == "of" && isNumber(words[2]) {
			return true
		}
		if len(words) >= 2 && words[0] == "of" && isNumber(words[1]) {
			return true
		}
		switch {
		case len(words) == 0:
			return true
		case len(words) == 1 && words[0] == "of":
			return true
		case len(words) == 1:
			return isNumber(words[0])
		case len(words) == 2 && words[0] == "of":
			return isNumber(words[1])
		case len(words) == 3 && words[1] == "of":
			return isNumber(words[0]) && isNumber(words[2])
		default:
			return false
		}
	}

	words := strings.Fields(lowercase)
	return len(words) == 3 && words[1] == "of" && isNumber(words[0]) && isNumber(words[2])
}

// IsPageNumberLine reports whether a completed Markdown line looks like a
// page number or a labeled running header.
func IsPageNumberLine(text string) bool {
	if IsExplicitPageNumberExpression(text) {
		return true
	}
	lowercase := strings.ToLower(strings.TrimSpace(text))
	rest, ok := strings.CutPrefix(lowercase, "page")
	if !ok {
		return false
	}
	rest = strings.TrimLeftFunc(rest, unicode.IsSpace)
	hasPageNumber := false
	i := 0
	for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
		hasPageNumber = true
		i++
	}
	if !hasPageNumber {
		return false
	}
	if i >= len(rest) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(rest[i:])
	return unicode.IsSpace(r)
}

// IsCJKChar reports whether c is CJK. CJK languages don't use spaces between
// words, so word-boundary heuristics should not apply when CJK is involved.
func IsCJKChar(c rune) bool {
	return (c >= 0x1100 && c <= 0x11FF) || // Hangul Jamo
		(c >= 0x3000 && c <= 0x303F) || // CJK Symbols and Punctuation
		(c >= 0x3040 && c <= 0x309F) || // Hiragana
		(c >= 0x30A0 && c <= 0x30FF) || // Katakana
		(c >= 0x3130 && c <= 0x318F) || // Hangul Compatibility Jamo
		(c >= 0x4E00 && c <= 0x9FFF) || // CJK Unified Ideographs
		(c >= 0xAC00 && c <= 0xD7AF) || // Hangul Syllables
		(c >= 0xF900 && c <= 0xFAFF) || // CJK Compatibility Ideographs
		(c >= 0xFF00 && c <= 0xFFEF) // Halfwidth and Fullwidth Forms
}

// IsRTLChar reports whether c is in a right-to-left script block.
func IsRTLChar(c rune) bool {
	return (c >= 0x0590 && c <= 0x05FF) || // Hebrew
		(c >= 0x0600 && c <= 0x06FF) || // Arabic
		(c >= 0x0700 && c <= 0x074F) || // Syriac
		(c >= 0x0750 && c <= 0x077F) || // Arabic Supplement
		(c >= 0x0780 && c <= 0x07BF) || // Thaana
		(c >= 0x07C0 && c <= 0x07FF) || // NKo
		(c >= 0x0800 && c <= 0x083F) || // Samaritan
		(c >= 0x0840 && c <= 0x085F) || // Mandaic
		(c >= 0x08A0 && c <= 0x08FF) || // Arabic Extended-A
		(c >= 0xFB1D && c <= 0xFB4F) || // Hebrew Presentation Forms
		(c >= 0xFB50 && c <= 0xFDFF) || // Arabic Presentation Forms-A
		(c >= 0xFE70 && c <= 0xFEFF) // Arabic Presentation Forms-B
}

func isArabicPresentationForm(c rune) bool {
	// U+FEFF is BOM/ZWNJ, not an Arabic presentation form despite falling
	// in the Presentation Forms-B codepoint range.
	return (c >= 0xFB50 && c <= 0xFDFF) || (c >= 0xFE70 && c <= 0xFEFE)
}

// IsRTLText reports whether the texts are majority RTL (and have at least
// one RTL character).
func IsRTLText(texts []string) bool {
	var rtl, ltr uint32
	for _, t := range texts {
		for _, c := range t {
			if IsRTLChar(c) {
				rtl++
			} else if unicode.IsLetter(c) && !IsCJKChar(c) {
				ltr++
			}
		}
	}
	return rtl > 0 && rtl > ltr
}

// SortLineItems sorts items left-to-right, or right-to-left when the line
// is majority RTL.
func SortLineItems(items []TextItem) {
	texts := make([]string, len(items))
	for i := range items {
		texts[i] = items[i].Text
	}
	rtl := IsRTLText(texts)
	sort.SliceStable(items, func(i, j int) bool {
		if rtl {
			return items[i].X > items[j].X
		}
		return items[i].X < items[j].X
	})
}

// IsBoldFont reports whether a font name indicates bold style.
func IsBoldFont(fontName string) bool {
	lower := strings.ToLower(fontName)
	return strings.Contains(lower, "bold") ||
		strings.Contains(lower, "-bd") ||
		strings.Contains(lower, "_bd") ||
		strings.Contains(lower, "black") ||
		strings.Contains(lower, "heavy") ||
		strings.Contains(lower, "demibold") ||
		strings.Contains(lower, "semibold") ||
		strings.Contains(lower, "demi-bold") ||
		strings.Contains(lower, "semi-bold") ||
		strings.Contains(lower, "extrabold") ||
		strings.Contains(lower, "ultrabold") ||
		(strings.Contains(lower, "medium") && !strings.Contains(lower, "mediumitalic")) ||
		(strings.Contains(lower, "-medi") && !strings.Contains(lower, "mediumital"))
}

// IsItalicFont reports whether a font name indicates italic/oblique style.
func IsItalicFont(fontName string) bool {
	lower := strings.ToLower(fontName)
	return strings.Contains(lower, "italic") ||
		strings.Contains(lower, "oblique") ||
		strings.Contains(lower, "-it") ||
		strings.Contains(lower, "_it") ||
		strings.Contains(lower, "slant") ||
		strings.Contains(lower, "inclined") ||
		strings.Contains(lower, "kursiv")
}

// ExpandLigatures expands Unicode ligatures, strips invisible controls, and
// NFKC-normalizes Arabic presentation forms (then reverses visual-order
// Arabic back to logical order).
func ExpandLigatures(text string) string {
	needsStrip := false
	for i := 0; i < len(text); i++ {
		b := text[i]
		if b < 0x20 && b != '\n' && b != '\r' && b != '\t' {
			needsStrip = true
			break
		}
	}
	if needsStrip {
		var b strings.Builder
		for _, c := range text {
			if c >= ' ' || c == '\n' || c == '\r' || c == '\t' {
				b.WriteRune(c)
			}
		}
		text = b.String()
	}

	hadPresentationForms := false
	for _, c := range text {
		if isArabicPresentationForm(c) {
			hadPresentationForms = true
			break
		}
	}
	if hadPresentationForms {
		text = nfkcArabicPresentation(text)
	}

	var result strings.Builder
	result.Grow(len(text))
	for _, ch := range text {
		switch ch {
		case 0xFB00:
			result.WriteString("ff")
		case 0xFB01:
			result.WriteString("fi")
		case 0xFB02:
			result.WriteString("fl")
		case 0xFB03:
			result.WriteString("ffi")
		case 0xFB04:
			result.WriteString("ffl")
		case 0xFB05, 0xFB06:
			result.WriteString("st")
		case 0x00AD, 0x200B, 0xFEFF, 0x200C, 0x200D, 0x2060:
			// strip
		default:
			if ch >= 0x2000 && ch <= 0x200A {
				result.WriteByte(' ')
			} else {
				result.WriteRune(ch)
			}
		}
	}

	out := result.String()
	if hadPresentationForms {
		out = reverseVisualArabic(out)
	}
	return out
}

func nfkcArabicPresentation(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if exp, ok := arabicPresentationNFKC[r]; ok {
			b.WriteString(exp)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func reverseVisualArabic(text string) string {
	hasLTR := false
	for _, c := range text {
		if (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
			hasLTR = true
			break
		}
	}
	if !hasLTR {
		runes := []rune(text)
		for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
			runes[i], runes[j] = runes[j], runes[i]
		}
		return string(runes)
	}

	chars := []rune(text)
	type run struct {
		ltr     bool
		content string
	}
	var runs []run
	i := 0
	for i < len(chars) {
		isLTR := isASCIIAlnum(chars[i]) || (isASCIIPunct(chars[i]) && isAdjacentToASCIIAlnum(chars, i))
		var b strings.Builder
		for i < len(chars) {
			c := chars[i]
			cIsLTR := isASCIIAlnum(c) || (isASCIIPunct(c) && isAdjacentToASCIIAlnum(chars, i))
			if cIsLTR != isLTR {
				break
			}
			b.WriteRune(c)
			i++
		}
		runs = append(runs, run{ltr: isLTR, content: b.String()})
	}

	for i, j := 0, len(runs)-1; i < j; i, j = i+1, j-1 {
		runs[i], runs[j] = runs[j], runs[i]
	}
	var result strings.Builder
	result.Grow(len(text))
	for _, r := range runs {
		if r.ltr {
			result.WriteString(r.content)
		} else {
			rs := []rune(r.content)
			for i, j := 0, len(rs)-1; i < j; i, j = i+1, j-1 {
				rs[i], rs[j] = rs[j], rs[i]
			}
			result.WriteString(string(rs))
		}
	}
	return result.String()
}

func isASCIIAlnum(c rune) bool {
	return (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

func isASCIIPunct(c rune) bool {
	return c < 0x80 && unicode.IsPunct(c)
}

func isAdjacentToASCIIAlnum(chars []rune, idx int) bool {
	return (idx > 0 && isASCIIAlnum(chars[idx-1])) ||
		(idx+1 < len(chars) && isASCIIAlnum(chars[idx+1]))
}

// DecodeTextString decodes a PDF text string (ActualText, etc.) that may be
// UTF-16BE (BOM \xFE\xFF) or PDFDocEncoding (Latin-1 superset).
func DecodeTextString(bytes []byte) string {
	if len(bytes) >= 2 && bytes[0] == 0xFE && bytes[1] == 0xFF {
		n := (len(bytes) - 2) / 2
		units := make([]uint16, n)
		for i := 0; i < n; i++ {
			units[i] = uint16(bytes[2+2*i])<<8 | uint16(bytes[3+2*i])
		}
		return string(utf16.Decode(units))
	}
	var b strings.Builder
	b.Grow(len(bytes))
	for _, c := range bytes {
		b.WriteRune(rune(c))
	}
	return b.String()
}

// EffectiveFontSize is the font size after applying the text matrix
// [a, b, c, d, tx, ty].
func EffectiveFontSize(baseSize float32, textMatrix [6]float32) float32 {
	scaleX := float32(math.Sqrt(float64(textMatrix[0]*textMatrix[0] + textMatrix[1]*textMatrix[1])))
	scaleY := float32(math.Sqrt(float64(textMatrix[2]*textMatrix[2] + textMatrix[3]*textMatrix[3])))
	scale := scaleX
	if scaleY > scale {
		scale = scaleY
	}
	return baseSize * scale
}

// EffectiveWidth is the item width, falling back to a character-count
// heuristic when Width is 0.
func EffectiveWidth(item *TextItem) float32 {
	if item.Width > 0 {
		return item.Width
	}
	return float32(utf16CountRunes(item.Text)) * item.FontSize * 0.5
}

func utf16CountRunes(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

// IsCIDFont reports whether a font resource name looks like a CID font
// (C2_* / C0_*).
func IsCIDFont(font string) bool {
	return strings.HasPrefix(font, "C2_") || strings.HasPrefix(font, "C0_")
}

// FixLetterspacedItems detects Canva-style letter-spacing within text items
// and removes the spurious spaces. Returns the adaptive join threshold for
// the page: 0.10 for normal pages, or a higher Otsu-derived threshold for
// Canva-style pages.
func FixLetterspacedItems(items []TextItem) float32 {
	const defaultT float32 = 0.10
	if len(items) == 0 {
		return defaultT
	}

	isLetterspaced := func(text string) bool {
		trimmed := strings.TrimSpace(text)
		chars := []rune(trimmed)
		if len(chars) < 3 {
			return false
		}
		for i, c := range chars {
			if i%2 == 0 {
				if c == ' ' {
					return false
				}
			} else if c != ' ' {
				return false
			}
		}
		return true
	}

	var letterspacedCount, totalTextItems uint32
	for i := range items {
		trimmed := strings.TrimSpace(items[i].Text)
		if trimmed == "" || len(trimmed) < 3 {
			continue
		}
		totalTextItems++
		if isLetterspaced(items[i].Text) {
			letterspacedCount++
		}
	}

	if totalTextItems < 4 || letterspacedCount*2 < totalTextItems {
		singleCharCount := 0
		for i := range items {
			if utf16CountRunes(strings.TrimSpace(items[i].Text)) == 1 {
				singleCharCount++
			}
		}
		if len(items) >= 10 && singleCharCount*2 >= len(items) {
			threshold := computeCanvaJoinThreshold(items)
			if threshold > 0.40 {
				return threshold
			}
		}
		return defaultT
	}

	threshold := computeCanvaJoinThreshold(items)
	for i := range items {
		if isLetterspaced(items[i].Text) {
			var b strings.Builder
			for _, c := range items[i].Text {
				if c != ' ' {
					b.WriteRune(c)
				}
			}
			items[i].Text = b.String()
		}
	}
	return threshold
}

func computeCanvaJoinThreshold(items []TextItem) float32 {
	const defaultT float32 = 0.10
	const minSamples = 8

	ratios := collectGapRatios(items)
	if len(ratios) < minSamples {
		return defaultT
	}
	sort.Slice(ratios, func(i, j int) bool { return ratios[i] < ratios[j] })
	if ratios[len(ratios)-1] < 0.40 || ratios[0] < 0.40 {
		return defaultT
	}
	median := ratios[len(ratios)/2]
	t := median * 1.55
	if t < 0.50 {
		t = 0.50
	}
	if t > 2.0 {
		t = 2.0
	}
	return t
}

func collectGapRatios(items []TextItem) []float32 {
	var ratios []float32
	for i := 0; i+1 < len(items); i++ {
		prev := &items[i]
		curr := &items[i+1]
		prevC, prevOK := lastNonSpace(prev.Text)
		currC, currOK := firstNonSpace(curr.Text)
		if (prevOK && IsCJKChar(prevC)) || (currOK && IsCJKChar(currC)) {
			continue
		}
		if prev.Width <= 0 || prev.FontSize <= 0 {
			continue
		}
		var gap float32
		if prev.X <= curr.X {
			gap = curr.X - (prev.X + prev.Width)
		} else {
			gap = prev.X - (curr.X + curr.Width)
		}
		ratio := gap / prev.FontSize
		if ratio >= 0 && ratio <= 3.0 {
			ratios = append(ratios, ratio)
		}
	}
	return ratios
}

func lastNonSpace(s string) (rune, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	var last rune
	for _, c := range s {
		last = c
	}
	return last, true
}

func firstNonSpace(s string) (rune, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	for _, c := range s {
		return c, true
	}
	return 0, false
}

// ShouldJoinItems reports whether two adjacent items should be joined without
// a space, based on page positions and character case.
func ShouldJoinItems(prevItem, currItem *TextItem, singleCharThreshold float32) bool {
	if strings.HasSuffix(prevItem.Text, " ") || strings.HasPrefix(currItem.Text, " ") {
		return false
	}

	prevLast, prevLastOK := lastTrimEnd(prevItem.Text)
	currFirst, currFirstOK := firstTrimStart(currItem.Text)

	if currFirstOK {
		switch currFirst {
		case '.', ',', ';', '!', '?', ')', ']', '}', '\'':
			return true
		}
	}
	if prevLastOK && currFirstOK && prevLast == ':' && isAlphanumeric(currFirst) {
		return false
	}

	if prevItem.Width > 0 {
		var gap float32
		if prevItem.X <= currItem.X {
			gap = currItem.X - (prevItem.X + prevItem.Width)
		} else {
			gap = prevItem.X - (currItem.X + currItem.Width)
		}
		fontSize := prevItem.FontSize

		if gap > fontSize*3.0 || gap < -fontSize {
			return false
		}

		prevChars := utf16CountRunes(strings.TrimSpace(prevItem.Text))
		currChars := utf16CountRunes(strings.TrimSpace(currItem.Text))
		prevLastChar, prevLastCharOK := lastNonSpace(prevItem.Text)
		currFirstChar, currFirstCharOK := firstNonSpace(currItem.Text)
		isCJK := (prevLastCharOK && IsCJKChar(prevLastChar)) || (currFirstCharOK && IsCJKChar(currFirstChar))

		if !isCJK && gap >= 0 && gap < fontSize*0.01 && IsCIDFont(prevItem.Font) {
			prevWordCount := len(strings.Fields(prevItem.Text))
			if prevWordCount >= 3 {
				return gap < fontSize*0.15
			}
			return false
		}

		if prevLastOK && currFirstOK {
			prevIsNumeric := isASCIIDigit(prevLast) || prevLast == ',' || prevLast == '.'
			currIsNumeric := isASCIIDigit(currFirst) || currFirst == '%' || currFirst == '.'
			if prevIsNumeric && currIsNumeric {
				return gap > -fontSize && gap < fontSize*0.3
			}
			if (prevLast == '+' || prevLast == '-') && isASCIIDigit(currFirst) {
				return gap > -fontSize && gap < fontSize*0.3
			}
		}

		if singleCharThreshold > 0.20 {
			if prevChars == 1 {
				return gap < prevItem.Width*1.25
			}
			if currChars == 1 {
				avg := prevItem.Width / float32(prevChars)
				return gap < avg*1.25
			}
			return gap < fontSize*singleCharThreshold
		}

		if (prevChars == 1) != (currChars == 1) {
			return gap < fontSize*0.20
		}

		if prevChars == 1 && currChars == 1 {
			if prevLastOK && currFirstOK {
				pNumeric := isASCIIDigit(prevLast) || prevLast == ',' || prevLast == '.' || prevLast == '%' || prevLast == '+' || prevLast == '-'
				cNumeric := isASCIIDigit(currFirst) || currFirst == ',' || currFirst == '.' || currFirst == '%'
				if pNumeric && cNumeric {
					return gap < fontSize*0.25
				}
			}
			return gap < fontSize*singleCharThreshold
		}

		if utf16CountRunes(strings.TrimSpace(prevItem.Text)) >= 2 && utf16CountRunes(strings.TrimSpace(currItem.Text)) >= 2 {
			prevEndsLower := false
			if c, ok := lastNonSpace(prevItem.Text); ok {
				prevEndsLower = unicode.IsLower(c)
			}
			currStartsLower := false
			if c, ok := firstNonSpace(currItem.Text); ok {
				currStartsLower = unicode.IsLower(c)
			}
			if prevEndsLower && currStartsLower {
				return gap < fontSize*0.18
			}
		}
		return gap < fontSize*0.15
	}

	charWidth := prevItem.FontSize * 0.45
	prevTextLen := float32(utf16CountRunes(prevItem.Text))
	estimatedPrevWidth := prevTextLen * charWidth
	prevEndX := prevItem.X + estimatedPrevWidth
	gap := currItem.X - prevEndX

	if gap > charWidth*6.0 {
		return false
	}

	isCJK := (prevLastOK && IsCJKChar(prevLast)) || (currFirstOK && IsCJKChar(currFirst))
	if isCJK {
		return gap < charWidth*0.8
	}

	if prevLastOK && currFirstOK && unicode.IsLetter(prevLast) && unicode.IsLetter(currFirst) {
		sameCase := (unicode.IsUpper(prevLast) && unicode.IsUpper(currFirst)) ||
			(unicode.IsLower(prevLast) && unicode.IsLower(currFirst))
		if sameCase {
			return gap < charWidth*0.8
		}
		if unicode.IsLower(prevLast) && unicode.IsUpper(currFirst) {
			return false
		}
		return gap < charWidth*0.3
	}
	return gap < charWidth*0.5
}

func lastTrimEnd(s string) (rune, bool) {
	s = strings.TrimRight(s, " \t\n\r")
	if s == "" {
		return 0, false
	}
	var last rune
	for _, c := range s {
		last = c
	}
	return last, true
}

func firstTrimStart(s string) (rune, bool) {
	s = strings.TrimLeft(s, " \t\n\r")
	if s == "" {
		return 0, false
	}
	for _, c := range s {
		return c, true
	}
	return 0, false
}

func isAlphanumeric(c rune) bool {
	return unicode.IsLetter(c) || unicode.IsDigit(c)
}

func isASCIIDigit(c rune) bool {
	return c >= '0' && c <= '9'
}
