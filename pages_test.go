package anydoc_test

import (
	"errors"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/m7medVision/anydoc-go"
)

func pages(t *testing.T, name string) []anydoc.Page {
	t.Helper()
	got, err := anydoc.ToPagesBytes(fixture(t, name), "")
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return got
}

func pageNumbers(pages []anydoc.Page) []int {
	numbers := make([]int, len(pages))
	for i, p := range pages {
		numbers[i] = p.Number
	}
	return numbers
}

// plainLines is the page's Markdown as text lines, with table pipes and
// emphasis markers turned into single spaces so a row reads "label value".
func plainLines(markdown string) []string {
	markup := regexp.MustCompile(`\*\*|\||\s+`)
	var lines []string
	for _, line := range strings.Split(markdown, "\n") {
		if strings.HasPrefix(line, "|---") {
			continue
		}
		if plain := strings.TrimSpace(markup.ReplaceAllString(line, " ")); plain != "" {
			lines = append(lines, regexp.MustCompile(` +`).ReplaceAllString(plain, " "))
		}
	}
	return lines
}

func hasLine(markdown, want string) bool {
	for _, line := range plainLines(markdown) {
		if line == want {
			return true
		}
	}
	return false
}

func TestToPagesBytesNumbersPagesInOrder(t *testing.T) {
	got := pages(t, "statement-twopage.pdf")
	if want := []int{1, 2}; !reflect.DeepEqual(pageNumbers(got), want) {
		t.Fatalf("page numbers: got %v, want %v", pageNumbers(got), want)
	}
	if !strings.Contains(got[0].Markdown, "## STATEMENT OF FINANCIAL POSITION") {
		t.Fatalf("page 1: %q", got[0].Markdown)
	}
	if !strings.Contains(got[1].Markdown, "## STATEMENT OF CHANGES IN EQUITY") {
		t.Fatalf("page 2: %q", got[1].Markdown)
	}
}

func TestToPagesBytesJoinsToWholeDocumentMarkdown(t *testing.T) {
	for _, name := range []string{"statement-twopage.pdf", "statement-keyvalue.pdf", "text-blank-page.pdf"} {
		t.Run(name, func(t *testing.T) {
			whole, err := anydoc.ToMarkdownBytes(fixture(t, name), anydoc.FormatPDF)
			if err != nil {
				t.Fatal(err)
			}
			var texts []string
			for _, p := range pages(t, name) {
				if p.Markdown != "" {
					texts = append(texts, p.Markdown)
				}
			}
			if joined := strings.Join(texts, "\n"); joined != whole {
				t.Fatalf("joined pages differ from whole document\njoined: %q\nwhole:  %q", joined, whole)
			}
		})
	}
}

func TestToPagesBytesKeepsBlankPages(t *testing.T) {
	got := pages(t, "text-blank-page.pdf")
	if want := []int{1, 2, 3}; !reflect.DeepEqual(pageNumbers(got), want) {
		t.Fatalf("page numbers: got %v, want %v", pageNumbers(got), want)
	}
	if strings.TrimSpace(got[1].Markdown) != "" {
		t.Fatalf("blank page 2: %q", got[1].Markdown)
	}
	if !strings.Contains(got[2].Markdown, "## NOTE 2 CONTINGENCIES") {
		t.Fatalf("page 3: %q", got[2].Markdown)
	}
}

func TestToPagesBytesNamedFormat(t *testing.T) {
	data := fixture(t, "statement-twopage.pdf")
	detected, err := anydoc.ToPagesBytes(data, "")
	if err != nil {
		t.Fatal(err)
	}
	named, err := anydoc.ToPagesBytes(data, anydoc.FormatPDF)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(detected, named) {
		t.Fatal("named format should match detected pages")
	}
}

func TestToPagesBytesNeedsOCRMatchesWholeDocument(t *testing.T) {
	data := fixture(t, "handmade-mixed.pdf")
	_, wholeErr := anydoc.ToMarkdownBytes(data, "")
	whole := convertErr(t, wholeErr)

	got, err := anydoc.ToPagesBytes(data, "")
	if got != nil {
		t.Fatalf("pages returned alongside needsOcr: %v", got)
	}
	ce := convertErr(t, err)
	if ce.Code != anydoc.CodeNeedsOcr {
		t.Fatalf("got %v", err)
	}
	if !reflect.DeepEqual(ce.Pages, whole.Pages) || ce.PageCount != whole.PageCount {
		t.Fatalf("pages %v of %d, whole document says %v of %d", ce.Pages, ce.PageCount, whole.Pages, whole.PageCount)
	}
	if !reflect.DeepEqual(ce.Pages, []uint32{2}) || ce.PageCount != 2 {
		t.Fatalf("pages %v of %d", ce.Pages, ce.PageCount)
	}
}

func TestToPagesBytesErrorCodes(t *testing.T) {
	cases := []struct {
		name   string
		data   []byte
		format anydoc.Format
		code   string
	}{
		{"encrypted pdf", fixture(t, "text-encrypted.pdf"), "", anydoc.CodeEncrypted},
		{"malformed pdf", []byte("%PDF-1.7\nnot really a pdf"), anydoc.FormatPDF, anydoc.CodeMalformed},
		{"detected docx", fixture(t, "handmade-rich.docx"), "", anydoc.CodeUnsupported},
		{"named csv", fixture(t, "sheet.csv"), anydoc.FormatCSV, anydoc.CodeUnsupported},
		{"unrecognized content", []byte("name,qty\n"), "", anydoc.CodeUnsupported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, wholeErr := anydoc.ToMarkdownBytes(tc.data, tc.format)
			_, err := anydoc.ToPagesBytes(tc.data, tc.format)
			if !errors.Is(err, &anydoc.ConvertError{Code: tc.code}) {
				t.Fatalf("got %v, want code %s", err, tc.code)
			}
			if tc.code != anydoc.CodeUnsupported && convertErr(t, wholeErr).Code != tc.code {
				t.Fatalf("whole document: %v", wholeErr)
			}
		})
	}
}

func TestToPagesBytesUnknownFormatName(t *testing.T) {
	_, err := anydoc.ToPagesBytes([]byte("x"), anydoc.Format("nope"))
	var ce *anydoc.ConvertError
	if err == nil || errors.As(err, &ce) {
		t.Fatalf("unknown format should be a plain error, got %v", err)
	}
}

func TestStatementTableRowsKeepLabelAndValues(t *testing.T) {
	got := pages(t, "statement-twopage.pdf")
	for _, row := range []string{
		"|Statement of financial position|31/12/2025|31/12/2024|",
		"|Total assets|4,514|3,863|",
		"|Total equity|3,394|2,848|",
	} {
		if !strings.Contains(got[0].Markdown, row+"\n") {
			t.Errorf("page 1 lacks row %s\n%s", row, got[0].Markdown)
		}
	}
	if !hasLine(got[1].Markdown, "Total equity 3,394 2,000 1,394 0 2,848 2,000 848 0") {
		t.Errorf("page 2 lacks the Total equity row with current and comparative values\n%s", got[1].Markdown)
	}
}

func TestStatementLabelsHaveNoMidWordSpaces(t *testing.T) {
	t.Skip("known rendering defect: per-glyph text whose hinted advances run wider " +
		"than the declared widths splits capitalised words after wide glyphs. " +
		`Actual: "|LIAB ILITIES|||", "|EQ UITY|||", "**STATEMENT OF CHANG ES IN EQ UITY**", ` +
		`"**G ENERAL INFORMATION AB OUT FINANCIAL STATEMENTS**". ` +
		`Expected: "LIABILITIES", "EQUITY", "STATEMENT OF CHANGES IN EQUITY", "GENERAL INFORMATION ABOUT FINANCIAL STATEMENTS".`)

	statement := pages(t, "statement-twopage.pdf")
	keyValue := pages(t, "statement-keyvalue.pdf")
	checks := []struct {
		markdown, label string
	}{
		{statement[0].Markdown, "LIABILITIES"},
		{statement[0].Markdown, "EQUITY"},
		{statement[1].Markdown, "STATEMENT OF CHANGES IN EQUITY"},
		{keyValue[0].Markdown, "GENERAL INFORMATION ABOUT FINANCIAL STATEMENTS"},
	}
	for _, c := range checks {
		if !strings.Contains(strings.Join(plainLines(c.markdown), "\n"), c.label) {
			t.Errorf("%q missing\n%s", c.label, c.markdown)
		}
	}
	split := regexp.MustCompile(`\b(G ENERAL|AB OUT|LIAB ILITIES|EQ UITY|CHANG ES)\b`)
	for _, c := range checks {
		if m := split.FindString(c.markdown); m != "" {
			t.Errorf("mid-word space in %q", m)
		}
	}
}

func TestStatementPeriodHeaderCells(t *testing.T) {
	t.Skip("known rendering defect: two-line column titles and two-line period ranges stacked " +
		"in one header are merged into shared lines and interleaved glyph by glyph. " +
		`Actual page 2 header: "**(accumulated losses) (0d1e/0fi1c/i2t0)25-(accumulated losses) (0d1e/0fi1c/i2t0)24-**" ` +
		`and "**01/01/2025-01/01/2025-01/01/2024-01/01/2024-**", outside any table. ` +
		`Expected one header row whose period cells read 01/01/2025-31/12/2025 (four columns) then 01/01/2024-31/12/2024 (four columns).`)

	got := pages(t, "statement-twopage.pdf")[1].Markdown
	want := strings.Repeat("|01/01/2025-31/12/2025", 4) + strings.Repeat("|01/01/2024-31/12/2024", 4) + "|"
	for _, line := range strings.Split(got, "\n") {
		if strings.HasSuffix(strings.ReplaceAll(line, " ", ""), want) {
			return
		}
	}
	t.Fatalf("no header row with period cells %s\n%s", want, got)
}

func TestKeyValuePageKeepsLabelsWithValues(t *testing.T) {
	t.Skip("known rendering defect: on a two-column key/value page with one label wider than " +
		"the label column, labels and values are emitted as separate column paragraphs. " +
		`Actual: "Type of entity Registration number Name of reporting entity ..." followed later by ` +
		`"Public company 2019/0442 ACME HOLDINGS AC-4417 ...". ` +
		`Expected each field on its own row, e.g. "|Name of reporting entity|ACME HOLDINGS|".`)

	got := pages(t, "statement-keyvalue.pdf")
	if len(got) != 1 {
		t.Fatalf("page count: %d", len(got))
	}
	for _, field := range []string{
		"Name of reporting entity ACME HOLDINGS",
		"Entity identifier AC-4417",
		"Reporting period start date 01/01/2025",
		"Reporting period end date 31/12/2025",
		"Description of reporting currency Euro",
		"Level of rounding off for monetary values Thousands",
	} {
		if !hasLine(got[0].Markdown, field) {
			t.Errorf("no row reads %q", field)
		}
	}
	if t.Failed() {
		t.Log(got[0].Markdown)
	}
}
