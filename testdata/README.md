# Test fixtures

Copied from [firecrawl/anydoc](https://github.com/firecrawl/anydoc) v0.2.4
(`tests/fixtures/...`), MIT license. They exercise the same engine the Python
and Node packages wrap.

| File | Upstream path |
| --- | --- |
| `handmade-outline.docx` | `tests/fixtures/docx/handmade-outline.docx` |
| `handmade-rich.docx` | `tests/fixtures/docx/handmade-rich.docx` |
| `sheet.csv` | `tests/fixtures/csv/sheet.csv` |
| `encrypted--errors.odt` | `tests/fixtures/malformed/encrypted--errors.odt` |
| `zipbomb--errors.docx` | `tests/fixtures/abuse/zipbomb--errors.docx` |
| `handmade-mixed.pdf` | `tests/fixtures/pdf/handmade-mixed.pdf` |

## Generated PDFs

`go run testdata/genpdf.go` writes these synthetic PDFs. Every name and figure in them is made up.

| File | Content |
| --- | --- |
| `statement-keyvalue.pdf` | One-page report information sheet: labels in a left column, values in a right column |
| `statement-twopage.pdf` | Statement of financial position, then a statement of changes in equity with two-line column and period headers |
| `text-blank-page.pdf` | Three pages; the second is blank |
| `text-encrypted.pdf` | One page, user password `acme` |
