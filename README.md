# anydoc-go

Go bindings for [anydoc](https://github.com/firecrawl/anydoc) v0.2.4. Same engine as the Python (`firecrawl-anydoc`) and Node packages: a small C ABI around the Rust crate, not a Go rewrite of the parsers. PDF conversion uses [pdf-inspector](https://github.com/firecrawl/pdf-inspector) 1.14.2 inside that crate.

```bash
# needs a Rust toolchain (1.88+) and cgo
go generate .
go test .
```

```go
import "github.com/m7medVision/anydoc-go"

markdown, err := anydoc.ToMarkdown("report.docx")

markdown, err = anydoc.ToMarkdownBytes(data, "")          // detect from bytes
markdown, err = anydoc.ToMarkdownBytes(data, anydoc.FormatCSV) // CSV has no signature

doc, err := anydoc.ToDocument(data, anydoc.FormatDocx)
```

`ToDocument` is unsupported for PDF: pdf-inspector emits Markdown directly. Use `ToMarkdownBytes`. Scanned or image-only PDF pages return a `ConvertError` whose `Code` is `"needsOcr"` and whose `Pages` are 1-indexed.

Format helpers: `FormatFromBytes`, `FormatFromExtension`, `FormatFromPath`.
