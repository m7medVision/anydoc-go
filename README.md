# anydoc-go

Go bindings for [anydoc](https://github.com/firecrawl/anydoc) v0.2.4. Same engine as the Python (`firecrawl-anydoc`) and Node packages: a small C ABI around the Rust crate, not a Go rewrite of the parsers. PDF conversion uses [pdf-inspector](https://github.com/firecrawl/pdf-inspector) 1.14.2 inside that crate.

```bash
go get github.com/m7medVision/anydoc-go
```

```go
import (
	"errors"
	"fmt"

	"github.com/m7medVision/anydoc-go"
)

markdown, err := anydoc.ToMarkdown("report.docx")

markdown, err = anydoc.ToMarkdownBytes(data, "") // detect from bytes
markdown, err = anydoc.ToMarkdownBytes(csvBytes, anydoc.FormatCSV) // CSV has no signature

doc, err := anydoc.ToDocument(data, anydoc.FormatDocx)

markdown, err = anydoc.ToMarkdown("scan.pdf")
var ce *anydoc.ConvertError
if errors.As(err, &ce) && ce.Code == anydoc.CodeNeedsOcr {
	// Pages are 1-indexed. The whole document failed; nothing was omitted.
	fmt.Printf("pages %v of %d need OCR\n", ce.Pages, ce.PageCount)
}
```

A consumer build needs cgo and a C compiler. Linux amd64 links a prebuilt native library shipped in the module, so `go get` then `go build` works without a Rust toolchain. Other GOOS/GOARCH need `go generate .` from a writable checkout (Rust 1.88+) until those prebuilts exist. Conversion stays on-box: this library does not call a network OCR service.

`ToDocument` is unsupported for PDF: pdf-inspector emits Markdown directly. Use `ToMarkdown` or `ToMarkdownBytes`.

Format helpers: `FormatFromBytes`, `FormatFromExtension`, `FormatFromPath`.

## Contributing

```bash
go generate .
go test .
```
