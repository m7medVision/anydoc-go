// Package textutils is the pdf-inspector types.rs + text_utils.rs port:
// TextItem / layout types and the character-classification helpers the
// extraction and markdown pipelines share. No PDF parsing happens here.
package textutils

import "strings"

// PageExtraction is the page-level extraction result: items, rectangles,
// line segments, and whether fonts with unresolvable gid-encoded glyphs
// were encountered.
type PageExtraction struct {
	Items              []TextItem
	Rects              []PdfRect
	Lines              []PdfLine
	HasUnresolvableGID bool
}

// FontEncodingMap maps byte codes to Unicode characters.
type FontEncodingMap map[byte]rune

// PageFontEncodings holds all font encodings for a page, keyed by resource name.
type PageFontEncodings map[string]FontEncodingMap

// FontWidthInfo is width data extracted from a PDF font dictionary.
type FontWidthInfo struct {
	// Widths maps character code to width in font units.
	Widths map[uint16]uint16
	// DefaultWidth is used for glyphs not in Widths.
	DefaultWidth uint16
	// SpaceWidth is the width of the space character (code 32) if known.
	SpaceWidth uint16
	// IsCID is true for CID fonts (2-byte character codes).
	IsCID bool
	// UnitsScale converts font units to text space. Type1/TrueType: 0.001;
	// Type3: FontMatrix[0].
	UnitsScale float32
	// WMode is 0 = horizontal (default), 1 = vertical.
	WMode uint8
}

// PageFontWidths is font width info for a page, keyed by font resource name.
type PageFontWidths map[string]FontWidthInfo

// ItemKind is the discriminant of ItemType.
type ItemKind uint8

const (
	ItemKindText ItemKind = iota
	ItemKindImage
	ItemKindLink
	ItemKindFormField
)

// ItemType is the kind of extracted item. The zero value is Text.
type ItemType struct {
	Kind ItemKind
	// URL is set when Kind is ItemKindLink.
	URL string
}

// ItemText is regular text content.
func ItemText() ItemType { return ItemType{Kind: ItemKindText} }

// ItemImage is an image placeholder.
func ItemImage() ItemType { return ItemType{Kind: ItemKindImage} }

// ItemLink is a hyperlink with URL.
func ItemLink(url string) ItemType { return ItemType{Kind: ItemKindLink, URL: url} }

// ItemFormField is a form field (name: value).
func ItemFormField() ItemType { return ItemType{Kind: ItemKindFormField} }

// LayoutComplexity is the layout analysis result. Callers use it to decide
// whether extracted markdown is reliable or the PDF should go to OCR.
type LayoutComplexity struct {
	// IsComplex is true if any page has tables or multi-column text.
	IsComplex bool
	// PagesWithTables are 1-indexed pages where table borders were detected
	// (rect count > 6).
	PagesWithTables []uint32
	// PagesWithColumns are 1-indexed pages where 2+ text columns were detected.
	PagesWithColumns []uint32
}

// PdfLine is a line segment from PDF path operators (m/l/S).
type PdfLine struct {
	X1, Y1, X2, Y2 float32
	Page           uint32
}

// PdfRect is a rectangle from a PDF re operator (cell boundary, border, …).
type PdfRect struct {
	X, Y, Width, Height float32
	Page                uint32
}

// TextItem is a text (or image/link) item with page position.
type TextItem struct {
	Text        string
	X, Y        float32
	Width       float32
	Height      float32
	Font        string
	FontSize    float32
	Page        uint32 // 1-indexed
	IsBold      bool
	IsItalic    bool
	IsUnderline bool
	IsStrikeout bool
	ItemType    ItemType
	// MCID is the marked-content id from BDC/BMC, linking this item to the
	// structure tree on tagged PDFs.
	MCID *int64
}

// TextLine is a grouped line of text items.
type TextLine struct {
	Items []TextItem
	Y     float32
	Page  uint32
	// AdaptiveThreshold is the page-level letter-spacing join threshold.
	// Default 0.10 for normal PDFs; higher for Canva-style PDFs.
	AdaptiveThreshold float32
}

// Text returns the line as plain text without formatting.
func (l *TextLine) Text() string {
	return l.TextWithFormatting(false, false, false)
}

// TextWithFormatting joins items with optional bold/italic/decoration
// markdown. formatDecorations enables both geometrically detected source
// decorations: underline (`<u>`) and strikeout (`<s>`).
func (l *TextLine) TextWithFormatting(formatBold, formatItalic, formatDecorations bool) string {
	if !formatBold && !formatItalic && !formatDecorations {
		return l.textPlain()
	}

	singleCharThreshold := l.AdaptiveThreshold
	var result strings.Builder
	currentBold := false
	currentItalic := false
	currentUnderline := false
	currentStrikeout := false

	for i := range l.Items {
		item := &l.Items[i]
		text := item.Text
		textTrimmed := strings.TrimSpace(text)
		if textTrimmed == "" {
			continue
		}

		needsSpace := false
		if i != 0 && result.Len() > 0 {
			prev := &l.Items[i-1]
			needsSpace = l.needsSpaceBetween(prev, item, result.String(), singleCharThreshold)
		}

		hasLeadingSpace := strings.HasPrefix(text, " ")

		itemStrikeout := formatDecorations && item.IsStrikeout
		itemUnderline := formatDecorations && item.IsUnderline && !itemStrikeout
		itemBold := formatBold && item.IsBold && !itemUnderline && !itemStrikeout
		itemItalic := formatItalic && item.IsItalic && !itemUnderline && !itemStrikeout

		if currentItalic && !itemItalic {
			result.WriteByte('*')
			currentItalic = false
		}
		if currentBold && !itemBold {
			result.WriteString("**")
			currentBold = false
		}
		if currentUnderline && !itemUnderline {
			result.WriteString("</u>")
			currentUnderline = false
		}
		if currentStrikeout && !itemStrikeout {
			result.WriteString("</s>")
			currentStrikeout = false
		}

		s := result.String()
		if needsSpace || (hasLeadingSpace && len(s) > 0 && !strings.HasSuffix(s, " ")) {
			result.WriteByte(' ')
		}

		if itemUnderline && !currentUnderline {
			result.WriteString("<u>")
			currentUnderline = true
		}
		if itemStrikeout && !currentStrikeout {
			result.WriteString("<s>")
			currentStrikeout = true
		}
		if itemBold && !currentBold {
			result.WriteString("**")
			currentBold = true
		}
		if itemItalic && !currentItalic {
			result.WriteByte('*')
			currentItalic = true
		}

		result.WriteString(textTrimmed)
	}

	if currentItalic {
		result.WriteByte('*')
	}
	if currentBold {
		result.WriteString("**")
	}
	if currentUnderline {
		result.WriteString("</u>")
	}
	if currentStrikeout {
		result.WriteString("</s>")
	}
	return result.String()
}

func (l *TextLine) textPlain() string {
	singleCharThreshold := l.AdaptiveThreshold
	var result strings.Builder
	for i := range l.Items {
		item := &l.Items[i]
		if i == 0 {
			result.WriteString(item.Text)
			continue
		}
		prev := &l.Items[i-1]
		if l.needsSpaceBetween(prev, item, result.String(), singleCharThreshold) {
			result.WriteByte(' ')
		}
		result.WriteString(item.Text)
	}
	return result.String()
}

func (l *TextLine) needsSpaceBetween(prevItem, item *TextItem, result string, singleCharThreshold float32) bool {
	text := item.Text

	prevEndsWithHyphen := strings.HasSuffix(result, "-")
	currIsHyphen := strings.TrimSpace(text) == "-"
	currStartsWithHyphen := strings.HasPrefix(text, "-")

	fontRatio := float32(0)
	reverseFontRatio := float32(0)
	if prevItem.FontSize != 0 {
		fontRatio = item.FontSize / prevItem.FontSize
		if item.FontSize != 0 {
			reverseFontRatio = prevItem.FontSize / item.FontSize
		}
	}
	yDiff := item.Y - prevItem.Y
	if yDiff < 0 {
		yDiff = -yDiff
	}

	isSubSuper := fontRatio < 0.85 && yDiff > 1.0
	wasSubSuper := reverseFontRatio < 0.85 && yDiff > 1.0

	shouldJoin := ShouldJoinItems(prevItem, item, singleCharThreshold)

	prevEndsWithSpace := strings.HasSuffix(result, " ")
	currStartsWithSpace := strings.HasPrefix(text, " ")
	spaceAlreadyExists := prevEndsWithSpace || currStartsWithSpace

	return !(prevEndsWithHyphen || currIsHyphen || currStartsWithHyphen || isSubSuper || wasSubSuper || shouldJoin || spaceAlreadyExists)
}
