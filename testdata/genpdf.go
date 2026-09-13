//go:build ignore

// genpdf writes the synthetic statement-style PDFs in this directory:
//
//	go run testdata/genpdf.go
//
// The layout imitates HTML-to-PDF report generators: every glyph is its own
// Tj at an integer Td offset in a flipped, 0.75-scaled coordinate space, the
// fonts are Identity-H CID fonts with a ToUnicode map, and the declared /W
// widths are the unhinted metrics while the positions use hinted whole-pixel
// advances. That mismatch is what makes extractors see gaps inside words.
package main

import (
	"bytes"
	"crypto/md5"
	"crypto/rc4"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Standard Times regular and bold AFM advance widths for the glyphs used below.
var metrics = map[bool]map[rune]int{
	false: widths(250,
		"! 333 ' 180 ( 333 ) 333 , 250 - 333 . 250 / 278 : 278 "+
			"0 500 1 500 2 500 3 500 4 500 5 500 6 500 7 500 8 500 9 500 "+
			"A 722 B 667 C 667 D 722 E 611 F 556 G 722 H 722 I 333 J 389 K 722 L 611 M 889 "+
			"N 722 O 722 P 556 Q 722 R 667 S 556 T 611 U 722 V 722 W 944 X 722 Y 722 Z 611 "+
			"a 444 b 500 c 444 d 500 e 444 f 333 g 500 h 500 i 278 j 278 k 500 l 278 m 778 "+
			"n 500 o 500 p 500 q 500 r 333 s 389 t 278 u 500 v 500 w 722 x 500 y 500 z 444"),
	true: widths(250,
		"! 333 ' 278 ( 333 ) 333 , 250 - 333 . 250 / 278 : 333 "+
			"0 500 1 500 2 500 3 500 4 500 5 500 6 500 7 500 8 500 9 500 "+
			"A 722 B 667 C 722 D 722 E 667 F 611 G 778 H 778 I 389 J 500 K 778 L 667 M 944 "+
			"N 722 O 778 P 611 Q 778 R 722 S 556 T 667 U 722 V 722 W 1000 X 722 Y 722 Z 667 "+
			"a 500 b 556 c 444 d 556 e 444 f 333 g 500 h 556 i 278 j 333 k 556 l 278 m 833 "+
			"n 556 o 500 p 556 q 556 r 444 s 389 t 333 u 556 v 500 w 722 x 500 y 500 z 444"),
}

func widths(space int, table string) map[rune]int {
	m := map[rune]int{' ': space}
	f := strings.Fields(table)
	for i := 0; i+1 < len(f); i += 2 {
		var w int
		fmt.Sscan(f[i+1], &w)
		m[[]rune(f[i])[0]] = w
	}
	return m
}

// run is one string drawn glyph by glyph from baseline (x, y), in CSS
// pixels from the top-left corner of the page.
type run struct {
	x, y float64
	bold bool
	size float64
	text string
}

type font struct {
	bold  bool
	gids  map[rune]int
	runes []rune
}

func newFont(bold bool) *font { return &font{bold: bold, gids: map[rune]int{}} }

func (f *font) gid(r rune) int {
	if g, ok := f.gids[r]; ok {
		return g
	}
	if _, ok := metrics[f.bold][r]; !ok {
		panic(fmt.Sprintf("no metric for %q", r))
	}
	f.runes = append(f.runes, r)
	f.gids[r] = len(f.runes)
	return len(f.runes)
}

func content(runs []run, regular, bold *font, pageHeight float64) string {
	var b strings.Builder
	fmt.Fprintf(&b, "1 0 0 -1 0 %.0f cm\nq\n0.75 0 0 0.75 0 0 cm\n0 0 0 rg\n", pageHeight)
	for _, r := range runs {
		f, name := regular, "F1"
		if r.bold {
			f, name = bold, "F2"
		}
		fmt.Fprintf(&b, "BT\n/%s %g Tf 1 0 0 -1 0 0 Tm\n", name, r.size)
		prev := 0
		for i, ch := range r.text {
			if i == 0 {
				fmt.Fprintf(&b, "%g %g Td <%04x> Tj\n", r.x, -r.y, f.gid(ch))
			} else {
				fmt.Fprintf(&b, "%d 0 Td <%04x> Tj\n", prev, f.gid(ch))
			}
			prev = advance(r.bold, ch, r.size)
		}
		b.WriteString("ET\n")
	}
	b.WriteString("Q\n")
	return b.String()
}

// advances8 are the whole-pixel advances a hinting rasterizer lays glyphs
// out with at an 8px font. They differ from the declared widths by up to a
// pixel and a half (bold Q is 6.22 declared and 8 laid out).
var advances8 = map[bool]map[rune]int{
	false: widths(2,
		"! 3 ' 2 ( 3 ) 3 , 2 - 3 . 2 / 2 : 2 "+
			"0 4 1 4 2 4 3 4 4 4 5 4 6 4 7 4 8 4 9 4 "+
			"A 6 B 5 C 5 D 6 E 5 F 4 G 6 H 6 I 3 J 3 K 6 L 5 M 7 "+
			"N 6 O 6 P 5 Q 6 R 5 S 4 T 5 U 6 V 6 W 8 X 6 Y 6 Z 5 "+
			"a 4 b 4 c 4 d 4 e 4 f 3 g 4 h 4 i 2 j 2 k 3 l 2 m 7 "+
			"n 4 o 4 p 4 q 4 r 3 s 3 t 2 u 4 v 4 w 6 x 4 y 5 z 4"),
	true: widths(2,
		"! 3 ' 2 ( 3 ) 3 , 2 - 3 . 2 / 2 : 3 "+
			"0 4 1 4 2 4 3 4 4 4 5 4 6 4 7 4 8 4 9 4 "+
			"A 6 B 6 C 6 D 6 E 5 F 5 G 7 H 6 I 3 J 4 K 6 L 5 M 8 "+
			"N 6 O 6 P 5 Q 8 R 6 S 4 T 5 U 6 V 6 W 8 X 6 Y 6 Z 5 "+
			"a 4 b 4 c 4 d 4 e 4 f 3 g 4 h 4 i 2 j 3 k 3 l 2 m 6 "+
			"n 4 o 4 p 4 q 4 r 4 s 3 t 3 u 4 v 4 w 6 x 4 y 4 z 4"),
}

func advance(bold bool, r rune, size float64) int {
	if size == 8 {
		return advances8[bold][r]
	}
	return int(math.Round(float64(metrics[bold][r]) * size / 1000))
}

func fontObjects(f *font, base string, first int) []string {
	var tu strings.Builder
	tu.WriteString("/CIDInit /ProcSet findresource begin\n12 dict begin\nbegincmap\n" +
		"/CIDSystemInfo << /Registry (Adobe) /Ordering (UCS) /Supplement 0 >> def\n" +
		"/CMapName /Adobe-Identity-UCS def\n/CMapType 2 def\n1 begincodespacerange\n<0000> <FFFF>\nendcodespacerange\n")
	fmt.Fprintf(&tu, "%d beginbfchar\n", len(f.runes))
	var w strings.Builder
	for i, r := range f.runes {
		fmt.Fprintf(&tu, "<%04x> <%04x>\n", i+1, r)
		fmt.Fprintf(&w, "%d ", metrics[f.bold][r])
	}
	tu.WriteString("endbfchar\nendcmap\nCMapName currentdict /CMap defineresource pop\nend\nend\n")
	flags := 34
	weight := 400
	if f.bold {
		flags = 262178
		weight = 700
	}
	cmap := tu.String()
	return []string{
		fmt.Sprintf("<< /Type /Font /Subtype /Type0 /BaseFont /%s /Encoding /Identity-H /DescendantFonts [%d 0 R] /ToUnicode %d 0 R >>", base, first+1, first+2),
		fmt.Sprintf("<< /Type /Font /Subtype /CIDFontType2 /BaseFont /%s /CIDSystemInfo << /Registry (Adobe) /Ordering (Identity) /Supplement 0 >> /CIDToGIDMap /Identity /DW 1000 /W [1 [%s]] /FontDescriptor %d 0 R >>", base, strings.TrimSpace(w.String()), first+3),
		fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(cmap), cmap),
		fmt.Sprintf("<< /Type /FontDescriptor /FontName /%s /Flags %d /FontBBox [-168 -218 1000 935] /ItalicAngle 0 /Ascent 891 /Descent -216 /CapHeight 662 /StemV 80 /FontWeight %d >>", base, flags, weight),
	}
}

func build(pages [][]run, width, height float64) []byte {
	return buildEncrypted(pages, width, height, "")
}

// buildEncrypted protects the file with a user password (Standard security
// handler, revision 2, RC4-40) when password is not empty.
func buildEncrypted(pages [][]run, width, height float64, password string) []byte {
	regular, bold := newFont(false), newFont(true)
	streams := make([]string, len(pages))
	for i, p := range pages {
		streams[i] = content(p, regular, bold, height)
	}
	const fontsAt = 3
	objs := []string{"<< /Type /Catalog /Pages 2 0 R >>", ""}
	objs = append(objs, fontObjects(regular, "GenericSerif", fontsAt)...)
	objs = append(objs, fontObjects(bold, "GenericSerif-Bold", fontsAt+4)...)
	var kids []string
	for _, s := range streams {
		page := len(objs) + 1
		kids = append(kids, fmt.Sprintf("%d 0 R", page))
		objs = append(objs,
			fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %g %g] /Resources << /Font << /F1 %d 0 R /F2 %d 0 R >> >> /Contents %d 0 R >>", width, height, fontsAt, fontsAt+4, page+1),
			fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(s), s))
	}
	objs[1] = fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(kids))

	trailer := fmt.Sprintf("/Size %d /Root 1 0 R", len(objs)+1)
	if password != "" {
		id := md5.Sum([]byte("acme-holdings-fixture"))
		o, key := standardSecurity(password, id[:])
		for i, obj := range objs {
			objs[i] = encryptStream(obj, key, i+1)
		}
		objs = append(objs, fmt.Sprintf("<< /Filter /Standard /V 1 /R 2 /Length 40 /P -44 /O <%x> /U <%x> >>", o, rc4Bytes(key, padding)))
		trailer = fmt.Sprintf("/Size %d /Root 1 0 R /Encrypt %d 0 R /ID [<%x> <%x>]", len(objs)+1, len(objs), id, id)
	}

	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, o := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<< %s >>\nstartxref\n%d\n%%%%EOF\n", trailer, xref)
	return b.Bytes()
}

var padding = []byte("\x28\xbf\x4e\x5e\x4e\x75\x8a\x41\x64\x00\x4e\x56\xff\xfa\x01\x08\x2e\x2e\x00\xb6\xd0\x68\x3e\x80\x2f\x0c\xa9\xfe\x64\x53\x69\x7a")

func rc4Bytes(key, data []byte) []byte {
	c, err := rc4.NewCipher(key)
	if err != nil {
		panic(err)
	}
	out := make([]byte, len(data))
	c.XORKeyStream(out, data)
	return out
}

func padded(password string) []byte {
	return append([]byte(password), padding...)[:32]
}

// standardSecurity returns the /O entry and the file key for a revision 2
// handler whose owner and user passwords are both password.
func standardSecurity(password string, id []byte) (o, key []byte) {
	ownerKey := md5.Sum(padded(password))
	o = rc4Bytes(ownerKey[:5], padded(password))
	h := md5.New()
	h.Write(padded(password))
	h.Write(o)
	h.Write([]byte{0xd4, 0xff, 0xff, 0xff}) // /P -44, little-endian
	h.Write(id)
	return o, h.Sum(nil)[:5]
}

func encryptStream(obj string, key []byte, num int) string {
	start := strings.Index(obj, "stream\n")
	end := strings.LastIndex(obj, "endstream")
	if start < 0 || end < 0 {
		return obj
	}
	start += len("stream\n")
	objKey := md5.Sum(append(append([]byte{}, key...), byte(num), byte(num>>8), byte(num>>16), 0, 0))
	return obj[:start] + string(rc4Bytes(objKey[:10], []byte(obj[start:end]))) + obj[end:]
}

func header(title string) []run {
	return []run{
		{x: 16, y: 14, bold: true, size: 16, text: "ACME HOLDINGS"},
		{x: 16, y: 28, bold: true, size: 12, text: title},
	}
}

// keyValue is a one-page report metadata sheet: labels in a left column,
// values in a right column on the same baseline.
func keyValue() []byte {
	rows := [][2]string{
		{"Type of entity", "Public company"},
		{"Registration number", "2019/0442"},
		{"Name of reporting entity", "ACME HOLDINGS"},
		{"Entity identifier", "AC-4417"},
		{"Listing status", "Listed"},
		{"Sector", "Industrials"},
		{"Sub-sector", "Machinery"},
		{"Reporting period frequency", "Annual"},
		{"Whether the reporting entity is preparing financial statements for its first financial period since it was established", "No"},
		{"Reporting period start date", "01/01/2025"},
		{"Reporting period end date", "31/12/2025"},
		{"Description of reporting currency", "Euro"},
		{"Level of rounding off for monetary values", "Thousands"},
		{"Description of nature of financial statements", "Audited"},
		{"Preparation format", "Consolidated"},
	}
	p := header("REPORT INFORMATION")
	p = append(p,
		run{x: 33, y: 40, bold: true, size: 8, text: "Report information"},
		run{x: 432, y: 40, bold: true, size: 8, text: "01/01/2025-31/12/2025"},
		run{x: 35, y: 51, bold: true, size: 8, text: "GENERAL INFORMATION ABOUT FINANCIAL STATEMENTS"})
	y := 61.0
	for _, r := range rows {
		p = append(p,
			run{x: 45, y: y, size: 8, text: r[0]},
			run{x: 432, y: y, size: 8, text: r[1]})
		y += 10
	}
	return build([][]run{p}, 595, 842)
}

// statement is two pages: a statement of financial position with one date
// per column, and a statement of changes in equity whose column headers wrap
// onto two lines above wrapped period ranges.
func statement() []byte {
	position := header("STATEMENT OF FINANCIAL POSITION")
	cols := []float64{293, 345, 397}
	addRow := func(p []run, y float64, bold bool, indent float64, label string, cells ...string) []run {
		p = append(p, run{x: 35 + indent, y: y, bold: bold, size: 8, text: label})
		for i, c := range cells {
			p = append(p, run{x: cols[i] + 40 - float64(4*len(c)), y: y, bold: bold, size: 8, text: c})
		}
		return p
	}
	position = append(position,
		run{x: 33, y: 40, bold: true, size: 8, text: "Statement of financial position"},
		run{x: 293, y: 40, bold: true, size: 8, text: "31/12/2025"},
		run{x: 345, y: 40, bold: true, size: 8, text: "31/12/2024"})
	position = addRow(position, 51, true, 0, "ASSETS")
	position = addRow(position, 62, false, 10, "Cash and cash equivalents", "1,204", "988")
	position = addRow(position, 73, false, 10, "Trade receivables", "3,310", "2,875")
	position = addRow(position, 84, true, 10, "Total assets", "4,514", "3,863")
	position = addRow(position, 95, true, 0, "LIABILITIES")
	position = addRow(position, 106, false, 10, "Trade payables", "1,120", "1,015")
	position = addRow(position, 117, true, 10, "Total liabilities", "1,120", "1,015")
	position = addRow(position, 128, true, 0, "EQUITY")
	position = addRow(position, 139, false, 10, "Share capital", "2,000", "2,000")
	position = addRow(position, 150, false, 10, "Retained earnings", "1,394", "848")
	position = addRow(position, 161, true, 10, "Total equity", "3,394", "2,848")

	changes := header("STATEMENT OF CHANGES IN EQUITY")
	type col struct {
		x              float64
		title          []string
		period         []string
		value1, value2 string
	}
	ecols := []col{
		{273, []string{"Total equity"}, []string{"01/01/2025-", "31/12/2025"}, "2,848", "3,394"},
		{359, []string{"Share capital"}, []string{"01/01/2025-", "31/12/2025"}, "2,000", "2,000"},
		{445, []string{"Retained earnings", "(accumulated losses)"}, []string{"01/01/2025-31/12/2025"}, "848", "1,394"},
		{590, []string{"Revaluation surplus", "(deficit)"}, []string{"01/01/2025-", "31/12/2025"}, "0", "0"},
		{676, []string{"Total equity"}, []string{"01/01/2024-", "31/12/2024"}, "2,406", "2,848"},
		{762, []string{"Share capital"}, []string{"01/01/2024-", "31/12/2024"}, "2,000", "2,000"},
		{848, []string{"Retained earnings", "(accumulated losses)"}, []string{"01/01/2024-31/12/2024"}, "406", "848"},
		{994, []string{"Revaluation surplus", "(deficit)"}, []string{"01/01/2024-", "31/12/2024"}, "0", "0"},
	}
	changes = append(changes, run{x: 33, y: 56, bold: true, size: 8, text: "Statement of changes in equity"})
	for _, c := range ecols {
		for i, t := range c.title {
			changes = append(changes, run{x: c.x, y: 45 - 5.5*float64(len(c.title)-1) + 11*float64(i), bold: true, size: 8, text: t})
		}
		for i, t := range c.period {
			changes = append(changes, run{x: c.x, y: 67 - 5.5*float64(len(c.period)-1) + 11*float64(i), bold: true, size: 8, text: t})
		}
	}
	changes = append(changes, run{x: 30, y: 84, bold: true, size: 8, text: "STATEMENT OF CHANGES IN EQUITY"})
	for i, label := range []string{"Balance at the start of the period", "Total equity"} {
		y := 95 + 11*float64(i)
		changes = append(changes, run{x: 45, y: y, bold: i == 1, size: 8, text: label})
		for _, c := range ecols {
			v := c.value1
			if i == 1 {
				v = c.value2
			}
			changes = append(changes, run{x: c.x + 40 - float64(4*len(v)), y: y, bold: i == 1, size: 8, text: v})
		}
	}
	return build([][]run{position, changes}, 842, 595)
}

func note(title string) []run {
	p := header(title)
	return append(p,
		run{x: 33, y: 60, size: 12, text: "Figures in this note are in thousands of euro unless stated otherwise."},
		run{x: 33, y: 80, size: 12, text: "The group has no contingent liabilities at the reporting date."})
}

// blankPage is three pages whose middle page has no content at all.
func blankPage() []byte {
	return build([][]run{note("NOTE 1 BASIS OF PREPARATION"), nil, note("NOTE 2 CONTINGENCIES")}, 595, 842)
}

// encrypted needs the user password "acme" to open.
func encrypted() []byte {
	return buildEncrypted([][]run{note("NOTE 1 BASIS OF PREPARATION")}, 595, 842, "acme")
}

func main() {
	dir := filepath.Dir(os.Args[0])
	if _, err := os.Stat("testdata"); err == nil {
		dir = "testdata"
	}
	files := map[string][]byte{
		"statement-keyvalue.pdf": keyValue(),
		"statement-twopage.pdf":  statement(),
		"text-blank-page.pdf":    blankPage(),
		"text-encrypted.pdf":     encrypted(),
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), files[n], 0o644); err != nil {
			panic(err)
		}
		fmt.Println(filepath.Join(dir, n))
	}
}
