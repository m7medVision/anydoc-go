package anydoc

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatFromBytes(t *testing.T) {
	if f, ok := FormatFromBytes([]byte("%PDF-1.7\n")); !ok || f != FormatPDF {
		t.Fatalf("pdf header: got %q %v", f, ok)
	}
	if f, ok := FormatFromBytes([]byte("{\\rtf1")); !ok || f != FormatRTF {
		t.Fatalf("rtf: got %q %v", f, ok)
	}
	if _, ok := FormatFromBytes([]byte("a,b\n1,2\n")); ok {
		t.Fatal("csv must not detect from bytes")
	}
}

func TestFormatFromExtensionAndPath(t *testing.T) {
	if f, ok := FormatFromExtension(".pptm"); !ok || f != FormatPPTX {
		t.Fatalf("pptm: got %q %v", f, ok)
	}
	if f, ok := FormatFromExtension("xls"); !ok || f != FormatXLSX {
		t.Fatalf("xls: got %q %v", f, ok)
	}
	if f, ok := FormatFromPath("report.odt"); !ok || f != FormatOdt {
		t.Fatalf("odt path: got %q %v", f, ok)
	}
	if _, ok := FormatFromPath("report.unknown"); ok {
		t.Fatal("unknown path should not match")
	}
}

func TestCSVMarkdownAndDocument(t *testing.T) {
	data := []byte("name,qty\nwidgets,3\n")
	_, err := ToMarkdownBytes(data, "")
	var ce *ConvertError
	if !errors.As(err, &ce) || ce.Code != CodeUnsupported {
		t.Fatalf("unnamed csv: got %v", err)
	}

	md, err := ToMarkdownBytes(data, FormatCSV)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md, "|") {
		t.Fatalf("expected a markdown table, got %q", md)
	}

	doc, err := ToDocument(data, FormatCSV)
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

func TestToMarkdownPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sheet.csv")
	if err := os.WriteFile(path, []byte("a,b\n1,2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	md, err := ToMarkdown(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md, "|") {
		t.Fatalf("got %q", md)
	}
}

func TestMalformedNamedFormat(t *testing.T) {
	_, err := ToMarkdownBytes([]byte("not a document"), FormatDocx)
	var ce *ConvertError
	if !errors.As(err, &ce) || ce.Code != CodeMalformed {
		t.Fatalf("got %v", err)
	}
}

func TestUnknownFormatName(t *testing.T) {
	_, err := ToMarkdownBytes([]byte("x"), Format("nope"))
	if err == nil {
		t.Fatal("expected error")
	}
	var ce *ConvertError
	if errors.As(err, &ce) {
		t.Fatalf("unknown format should not be ConvertError, got %v", ce)
	}
}

func TestMissingFile(t *testing.T) {
	_, err := ToMarkdown("/no/such/file/anydoc-go-missing.doc")
	var ce *ConvertError
	if !errors.As(err, &ce) || ce.Code != CodeIO {
		t.Fatalf("got %v", err)
	}
}
