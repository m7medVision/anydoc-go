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

## Install

A consumer build needs cgo and a C compiler (gcc or clang; on macOS the Xcode command line tools). Setting `CGO_ENABLED=0` disables cgo and this module will not build; leave it at the default. No Rust toolchain is needed.

Prebuilt native archives ship in the module for linux/amd64, linux/arm64, darwin/amd64 and darwin/arm64 (glibc on Linux). On those platforms `go get` then `go build` is the whole install. The arm64 and darwin archives are added by the release workflow; until the first tagged release only linux/amd64 is present.

The module wraps anydoc 0.2.4 and pdf-inspector 1.14.2. Releases of this module use their own semver, independent of anydoc's version.

musl/Alpine and Windows are not v1 targets. On an unsupported platform the link step fails naming the missing archive for your GOOS/GOARCH. To use the library there anyway:

1. Clone this repo.
2. Install Rust. The pinned version is in `native/rust-toolchain.toml`; 1.88 is the floor.
3. Run `go generate .` in the checkout. This builds the archive for the host platform into `native/prebuilt/`.
4. In your app, run `go mod edit -replace github.com/m7medVision/anydoc-go=/path/to/checkout`.

Conversion stays on-box: this library does not call a network OCR service.

`ToDocument` is unsupported for PDF: pdf-inspector emits Markdown directly. Use `ToMarkdown` or `ToMarkdownBytes`.

Format helpers: `FormatFromBytes`, `FormatFromExtension`, `FormatFromPath`.

## Contributing

```bash
go generate .
go test .
```

`go generate .` rebuilds the Rust shim for the host platform (Rust required, see above). `go test .` runs the suite against that archive. PR CI (the `ci` workflow) does the same on Linux.

Archives for all four platforms are produced by the `release` GitHub Actions workflow. It is run by hand (`workflow_dispatch`) with a version, builds and tests each archive on a runner of its own OS and architecture, commits the archives, tags, and publishes a GitHub Release with the archives and a `SHA256SUMS` file. Do not hand-commit archives, including the one `go generate .` leaves in `native/prebuilt/`.
