package anydoc_test

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/m7medVision/anydoc-go"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func fixturePath(t *testing.T, name string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func convertErr(t *testing.T, err error) *anydoc.ConvertError {
	t.Helper()
	var ce *anydoc.ConvertError
	if !errors.As(err, &ce) {
		t.Fatalf("want ConvertError, got %v", err)
	}
	return ce
}

func TestFormatFromBytes(t *testing.T) {
	if f, ok := anydoc.FormatFromBytes([]byte("%PDF-1.7\n")); !ok || f != anydoc.FormatPDF {
		t.Fatalf("pdf header: got %q %v", f, ok)
	}
	if f, ok := anydoc.FormatFromBytes([]byte("{\\rtf1")); !ok || f != anydoc.FormatRTF {
		t.Fatalf("rtf: got %q %v", f, ok)
	}
	if f, ok := anydoc.FormatFromBytes(fixture(t, "handmade-rich.docx")); !ok || f != anydoc.FormatDocx {
		t.Fatalf("docx fixture: got %q %v", f, ok)
	}
	if f, ok := anydoc.FormatFromBytes(fixture(t, "handmade-mixed.pdf")); !ok || f != anydoc.FormatPDF {
		t.Fatalf("pdf fixture: got %q %v", f, ok)
	}
	if _, ok := anydoc.FormatFromBytes([]byte("a,b\n1,2\n")); ok {
		t.Fatal("csv must not detect from bytes")
	}
	if _, ok := anydoc.FormatFromBytes(fixture(t, "sheet.csv")); ok {
		t.Fatal("csv fixture must not detect from bytes")
	}
}

func TestFormatFromExtensionAndPath(t *testing.T) {
	cases := []struct {
		ext    string
		format anydoc.Format
	}{
		{".pptm", anydoc.FormatPPTX},
		{"ppsx", anydoc.FormatPPTX},
		{".docm", anydoc.FormatDocx},
		{"xls", anydoc.FormatXLSX},
		{".xlsb", anydoc.FormatXLSX},
		{"xlsm", anydoc.FormatXLSX},
	}
	for _, tc := range cases {
		f, ok := anydoc.FormatFromExtension(tc.ext)
		if !ok || f != tc.format {
			t.Fatalf("%s: got %q %v, want %q", tc.ext, f, ok, tc.format)
		}
	}
	if f, ok := anydoc.FormatFromPath("report.odt"); !ok || f != anydoc.FormatOdt {
		t.Fatalf("odt path: got %q %v", f, ok)
	}
	if _, ok := anydoc.FormatFromPath("report.unknown"); ok {
		t.Fatal("unknown path should not match")
	}
}

func TestCSVMarkdownAndDocument(t *testing.T) {
	data := []byte("name,qty\nwidgets,3\n")
	_, err := anydoc.ToMarkdownBytes(data, "")
	if convertErr(t, err).Code != anydoc.CodeUnsupported {
		t.Fatalf("unnamed csv: got %v", err)
	}

	md, err := anydoc.ToMarkdownBytes(data, anydoc.FormatCSV)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md, "|") {
		t.Fatalf("expected a markdown table, got %q", md)
	}

	doc, err := anydoc.ToDocument(data, anydoc.FormatCSV)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Blocks) != 1 || doc.Blocks[0].Kind != "table" || doc.Blocks[0].Table == nil {
		t.Fatalf("csv document: %+v", doc.Blocks)
	}
	if doc.Blocks[0].Table.HeaderRows != 1 {
		t.Fatalf("header rows: %d", doc.Blocks[0].Table.HeaderRows)
	}
}

func TestCSVFixtureRequiresNamedFormat(t *testing.T) {
	data := fixture(t, "sheet.csv")
	_, err := anydoc.ToMarkdownBytes(data, "")
	if convertErr(t, err).Code != anydoc.CodeUnsupported {
		t.Fatalf("unnamed csv fixture: got %v", err)
	}
	md, err := anydoc.ToMarkdownBytes(data, anydoc.FormatCSV)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md, "| --- |") {
		t.Fatalf("csv fixture markdown: %q", md)
	}
}

func TestToMarkdownPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sheet.csv")
	if err := os.WriteFile(path, []byte("a,b\n1,2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	md, err := anydoc.ToMarkdown(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md, "|") {
		t.Fatalf("got %q", md)
	}

	outline, err := anydoc.ToMarkdown(fixturePath(t, "handmade-outline.docx"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(outline, "# ") {
		t.Fatalf("outline markdown missing heading: %q", outline)
	}
}

func TestOfficeMarkdownAndDocument(t *testing.T) {
	rich := fixture(t, "handmade-rich.docx")
	md, err := anydoc.ToMarkdownBytes(rich, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md, "| Quarter | Widgets |") {
		t.Fatalf("rich markdown: %q", md)
	}

	named, err := anydoc.ToMarkdownBytes(rich, anydoc.FormatDocx)
	if err != nil {
		t.Fatal(err)
	}
	if named != md {
		t.Fatal("named format should match detected markdown")
	}

	outline := fixture(t, "handmade-outline.docx")
	doc, err := anydoc.ToDocument(outline, anydoc.FormatDocx)
	if err != nil {
		t.Fatal(err)
	}
	var heading *anydoc.Block
	for i := range doc.Blocks {
		if doc.Blocks[i].Kind == "heading" {
			heading = &doc.Blocks[i]
			break
		}
	}
	if heading == nil {
		t.Fatalf("no heading in %+v", doc.Blocks)
	}
	if heading.Level == nil || *heading.Level < 1 || *heading.Level > 6 {
		t.Fatalf("heading level: %v", heading.Level)
	}
	if len(heading.Content) == 0 || heading.Content[0].Kind != "text" {
		t.Fatalf("heading content: %+v", heading.Content)
	}
	if heading.Content[0].Text == nil || *heading.Content[0].Text == "" {
		t.Fatal("heading text empty")
	}
	if heading.Content[0].Style == nil {
		t.Fatal("heading text missing style")
	}

	richDoc, err := anydoc.ToDocument(rich, anydoc.FormatDocx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, asset := range richDoc.Assets {
		if asset.MediaType == "image/png" && len(asset.Data) > 0 {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected a png asset, got %+v", richDoc.Assets)
	}
}

func TestPDFToDocumentUnsupported(t *testing.T) {
	_, err := anydoc.ToDocument(fixture(t, "handmade-mixed.pdf"), anydoc.FormatPDF)
	ce := convertErr(t, err)
	if ce.Code != anydoc.CodeUnsupported {
		t.Fatalf("got %v", err)
	}
	msg := strings.ToLower(ce.Message)
	if !strings.Contains(msg, "markdown") {
		t.Fatalf("message should tell the caller to use markdown, got %q", ce.Message)
	}
}

func TestPDFNeedsOCR(t *testing.T) {
	_, err := anydoc.ToMarkdown(fixturePath(t, "handmade-mixed.pdf"))
	ce := convertErr(t, err)
	if ce.Code != anydoc.CodeNeedsOcr {
		t.Fatalf("got %v", err)
	}
	if ce.PageCount != 2 {
		t.Fatalf("page count: %d", ce.PageCount)
	}
	if len(ce.Pages) != 1 || ce.Pages[0] != 2 {
		t.Fatalf("pages: %v", ce.Pages)
	}
}

func TestEncryptedODT(t *testing.T) {
	_, err := anydoc.ToMarkdownBytes(fixture(t, "encrypted--errors.odt"), anydoc.FormatOdt)
	if convertErr(t, err).Code != anydoc.CodeEncrypted {
		t.Fatalf("got %v", err)
	}
}

func TestZipBombResourceLimit(t *testing.T) {
	_, err := anydoc.ToMarkdownBytes(fixture(t, "zipbomb--errors.docx"), anydoc.FormatDocx)
	ce := convertErr(t, err)
	if ce.Code != anydoc.CodeResourceLimit {
		t.Fatalf("got %v", err)
	}
	if ce.Limit != "max_entry_bytes" {
		t.Fatalf("limit: %q", ce.Limit)
	}
}

func TestMissingPartDocx(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("[Content_Types].xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(" ")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = anydoc.ToMarkdownBytes(buf.Bytes(), anydoc.FormatDocx)
	ce := convertErr(t, err)
	if ce.Code != anydoc.CodeMissingPart {
		t.Fatalf("got %v", err)
	}
	if ce.Part != "word/document.xml" {
		t.Fatalf("part: %q", ce.Part)
	}
}

func TestMalformedNamedFormat(t *testing.T) {
	_, err := anydoc.ToMarkdownBytes([]byte("not a document"), anydoc.FormatDocx)
	ce := convertErr(t, err)
	if ce.Code != anydoc.CodeMalformed {
		t.Fatalf("got %v", err)
	}
}

func TestUnknownFormatName(t *testing.T) {
	_, err := anydoc.ToMarkdownBytes([]byte("x"), anydoc.Format("nope"))
	if err == nil {
		t.Fatal("expected error")
	}
	var ce *anydoc.ConvertError
	if errors.As(err, &ce) {
		t.Fatalf("unknown format should not be ConvertError, got %v", ce)
	}
}

func TestMissingFile(t *testing.T) {
	_, err := anydoc.ToMarkdown("/no/such/file/anydoc-go-missing.doc")
	if convertErr(t, err).Code != anydoc.CodeIO {
		t.Fatalf("got %v", err)
	}
}

func TestConcurrentConverts(t *testing.T) {
	data := fixture(t, "handmade-outline.docx")
	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n*2)
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, err := anydoc.ToMarkdownBytes(data, "")
			errs <- err
		}()
		go func() {
			defer wg.Done()
			_, err := anydoc.ToDocument(data, anydoc.FormatDocx)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}
