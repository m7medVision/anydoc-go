# anydoc-go

Unofficial Go bindings for [anydoc](https://github.com/firecrawl/anydoc), the Rust document converter by [Firecrawl](https://github.com/firecrawl). It turns Word, PowerPoint, Excel, OpenDocument, RTF, EPUB, CSV and PDF files into Markdown. PDF conversion inside anydoc is done by [pdf-inspector](https://github.com/firecrawl/pdf-inspector), also by Firecrawl.

This module is not made or maintained by Firecrawl. It is a thin C ABI around the anydoc crate, so the Markdown you get is the same Markdown the official Python (`firecrawl-anydoc`) and Node (`@firecrawl/anydoc`) packages produce. Nothing is re-implemented in Go.

Versions inside this module:

| Crate | Version |
| --- | --- |
| anydoc | 0.2.4 |
| pdf-inspector | 1.19.0 |

anydoc 0.2.4 asks for pdf-inspector 1.14.2 or any later 1.x. This module pins 1.19.0, the newest release, for its PDF extraction fixes, so PDF Markdown can differ slightly from the official packages.

## Install

```bash
go get github.com/m7medVision/anydoc-go
```

You need a C compiler (gcc or clang; on macOS run `xcode-select --install`). You do not need Rust.

Prebuilt native archives are inside the module for:

| GOOS/GOARCH | Notes |
| --- | --- |
| linux/amd64 | glibc |
| linux/arm64 | glibc |
| darwin/amd64 | Intel Mac |
| darwin/arm64 | Apple Silicon |

Do not set `CGO_ENABLED=0`. That turns cgo off and this module cannot build without it. Leave it at the default.

Use a tagged version. The release workflow commits the archives for all four platforms when it cuts a tag, so an untagged `main` may only have some of them.

Windows and musl (Alpine) are not supported yet. See [Unsupported platforms](#unsupported-platforms) for a workaround.

## Quick start

```go
package main

import (
	"fmt"
	"log"

	"github.com/m7medVision/anydoc-go"
)

func main() {
	markdown, err := anydoc.ToMarkdown("report.docx")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(markdown)
}
```

```bash
go run .
```

## Convert a file

`ToMarkdown` reads the file, detects the format from its content, and returns Markdown. The extension is only used when the content has no signature (CSV).

```go
markdown, err := anydoc.ToMarkdown("slides.pptx")
```

## Convert bytes

`ToMarkdownBytes` works on data you already have in memory, for example an upload. Pass `""` as the format to detect it from the content.

```go
data, err := os.ReadFile("sheet.xlsx")
if err != nil {
	return err
}
markdown, err := anydoc.ToMarkdownBytes(data, "")
```

Pass the format explicitly when you know it:

```go
markdown, err := anydoc.ToMarkdownBytes(data, anydoc.FormatDocx)
```

### CSV needs a name

CSV is plain text with no signature, so detection cannot find it. Name the format:

```go
csv := []byte("name,qty\nwidgets,3\n")

_, err := anydoc.ToMarkdownBytes(csv, "")
// err is a *ConvertError with Code "unsupported"

markdown, err := anydoc.ToMarkdownBytes(csv, anydoc.FormatCSV)
// markdown is a table:
// | name | qty |
// | --- | --- |
// | widgets | 3 |
```

`ToMarkdown("sheet.csv")` works without a hint because the `.csv` extension names it.

## Convert a PDF page by page

`ToPagesBytes` returns one entry per page of a PDF, in document order, so you can tell which page text came from. `Number` is 1-indexed, the way a PDF viewer counts. A page with no text is still in the list, with empty Markdown, so numbers never shift.

```go
data, err := os.ReadFile("report.pdf")
if err != nil {
	return err
}
pages, err := anydoc.ToPagesBytes(data, "")
if err != nil {
	return err
}
for _, page := range pages {
	fmt.Printf("page %d:\n%s\n", page.Number, page.Markdown)
}
```

It fails on the same PDFs as `ToMarkdownBytes`, with the same codes. If any page needs OCR, the whole call fails with `needsOcr` and no pages. Joining the non-empty pages with `"\n"` normally reproduces the `ToMarkdownBytes` output.

Only PDF has pages. Any other format, detected or named, returns a `ConvertError` with Code `unsupported`.

## Detect a format

```go
f, ok := anydoc.FormatFromBytes(data)      // from content: "pdf", "docx", ...
f, ok = anydoc.FormatFromExtension(".pptm") // "pptx"
f, ok = anydoc.FormatFromPath("notes.odt")  // "odt"
```

`ok` is false for unknown input. `FormatFromBytes` is also false for CSV, because CSV has no signature.

Variants of a container map to the format that parses them:

| Extension | Format |
| --- | --- |
| `.docm` | `docx` |
| `.pptm`, `.ppsx`, `.ppsm` | `pptx` |
| `.xls`, `.xlsm`, `.xlsb` | `xlsx` |
| `.pps`, `.pot` | `ppt` |

## Get the document model

`ToDocument` returns the parsed structure instead of Markdown: blocks, inline runs, tables, lists, notes and embedded images. Use it when you want to walk the document yourself.

```go
doc, err := anydoc.ToDocument(data, anydoc.FormatDocx)
if err != nil {
	return err
}

for _, block := range doc.Blocks {
	switch block.Kind {
	case "heading":
		fmt.Printf("H%d: %s\n", *block.Level, plainText(block.Content))
	case "paragraph":
		fmt.Println(plainText(block.Content))
	case "table":
		fmt.Printf("table %dx%d\n", len(block.Table.Grid), len(block.Table.Grid[0]))
	}
}
```

Helper used above:

```go
func plainText(inlines []anydoc.Inline) string {
	var b strings.Builder
	for _, in := range inlines {
		if in.Text != nil {
			b.WriteString(*in.Text)
		}
		b.WriteString(plainText(in.Content))
	}
	return b.String()
}
```

Save embedded images:

```go
for _, asset := range doc.Assets {
	// asset.MediaType is "image/png" and asset.OriginPart is "word/media/dot.png", for example.
	name := fmt.Sprintf("asset-%d%s", asset.ID, filepath.Ext(asset.OriginPart))
	if err := os.WriteFile(name, asset.Data, 0o644); err != nil {
		return err
	}
}
```

Block kinds: `heading`, `paragraph`, `list`, `table`, `block_quote`, `code_block`, `rule`, `math`.
Inline kinds: `text`, `link`, `image`, `anchor`, `note_ref`, `line_break`, `math`, `checkbox`.
These are the same names the Python and Node packages use.

`ToDocument` does not work for PDF. pdf-inspector produces Markdown directly and has no document model, so `ToDocument` on a PDF returns a `ConvertError` with Code `unsupported`. Use `ToMarkdown` or `ToMarkdownBytes` for PDF.

## Handle errors

Every conversion failure is a `*anydoc.ConvertError`. Check the `Code` field.

| Code | Meaning | Extra fields |
| --- | --- | --- |
| `unsupported` | Format not recognised, CSV without a name, `ToDocument` on PDF, or `ToPagesBytes` on anything but PDF | |
| `needsOcr` | PDF has scanned or image-only pages | `Pages` (1-indexed), `PageCount` |
| `malformed` | File is structurally broken | `Part` when a package part is at fault |
| `encrypted` | File is password protected | |
| `resourceLimit` | A safety limit was hit (zip bomb, huge part) | `Limit` names the cap |
| `missingPart` | A required part of the package is missing | `Part` |
| `io` | The path could not be read | |

```go
markdown, err := anydoc.ToMarkdown("scan.pdf")

var ce *anydoc.ConvertError
if errors.As(err, &ce) {
	switch ce.Code {
	case anydoc.CodeNeedsOcr:
		// The whole document failed. Nothing was converted.
		fmt.Printf("pages %v of %d are scanned and need OCR\n", ce.Pages, ce.PageCount)
	case anydoc.CodeEncrypted:
		fmt.Println("password protected, skipping")
	case anydoc.CodeResourceLimit:
		fmt.Printf("refused: hit limit %s\n", ce.Limit)
	default:
		fmt.Printf("%s: %s\n", ce.Code, ce.Message)
	}
}
```

`errors.Is` also works with a code:

```go
if errors.Is(err, &anydoc.ConvertError{Code: anydoc.CodeEncrypted}) {
	// skip
}
```

Passing a format name that does not exist is a plain error, not a `ConvertError`:

```go
_, err := anydoc.ToMarkdownBytes(data, "word")
// err: unknown format "word"; expected one of doc, docx, odt, pdf, ppt, pptx, rtf, epub, xlsx, ods, odp, csv
```

## OCR

anydoc does not run OCR. When a PDF page is an image, conversion fails with `needsOcr` and tells you which pages. This module does not call any OCR service, local or remote. Conversion never touches the network.

## Concurrency

Calls are safe from multiple goroutines. Each call is independent.

```go
var wg sync.WaitGroup
for _, path := range paths {
	wg.Add(1)
	go func(p string) {
		defer wg.Done()
		md, err := anydoc.ToMarkdown(p)
		// ...
	}(path)
}
wg.Wait()
```

## Supported formats

| Format | Extensions | `ToMarkdown` | `ToDocument` |
| --- | --- | --- | --- |
| Word | `.doc`, `.docx`, `.docm` | yes | yes |
| PowerPoint | `.ppt`, `.pps`, `.pot`, `.pptx`, `.pptm`, `.ppsx`, `.ppsm` | yes | yes |
| Excel | `.xls`, `.xlsx`, `.xlsm`, `.xlsb` | yes | yes |
| OpenDocument | `.odt`, `.ods`, `.odp` | yes | yes |
| Rich Text | `.rtf` | yes | yes |
| EPUB | `.epub` | yes | yes |
| CSV | `.csv` | yes | yes |
| PDF | `.pdf` | yes | no |

## Unsupported platforms

On a GOOS/GOARCH without a prebuilt archive the build fails at link time with undefined `anydoc_*` references or a missing archive path. You can still build the native library yourself:

1. Install Rust. The pinned version is in `native/rust-toolchain.toml` (1.88 is the minimum).
2. Clone this repository.
3. Run `go generate .` in the clone. It builds `native/prebuilt/<GOOS>_<GOARCH>/libanydoc_ffi.a` for your machine.
4. Point your app at the clone:

```bash
go mod edit -replace github.com/m7medVision/anydoc-go=/path/to/anydoc-go
```

## Development

```bash
go generate .   # rebuild the Rust shim for this machine (needs Rust)
go test .       # run the tests against it
```

Pull request CI runs those two commands on Linux.

Releases are cut by hand from the `release` GitHub Actions workflow. It takes a version such as `v0.1.0`, builds and tests the archive on a runner for each of the four platforms, commits the archives, tags, and publishes a GitHub Release with the archives and a `SHA256SUMS` file. Release numbers are this module's own and do not follow anydoc's version.

Do not commit archives by hand, including the one `go generate .` leaves behind.

## Credits and license

- [anydoc](https://github.com/firecrawl/anydoc) by Firecrawl, MIT.
- [pdf-inspector](https://github.com/firecrawl/pdf-inspector) by Firecrawl, MIT.
- Test fixtures under `testdata/` are copied from the anydoc repository or generated by `testdata/genpdf.go`. See `testdata/README.md`.

This module is MIT licensed. See `LICENSE`.
