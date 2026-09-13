//! Per-page PDF Markdown.
//!
//! anydoc 0.2.4 only returns one joined string for a PDF, so this calls
//! pdf-inspector directly. The OCR and empty-document rules mirror anydoc's
//! `formats::pdf::to_markdown` so both calls fail on the same inputs.

use anydoc::ConvertError;
use pdf_inspector::{PdfError, PdfType};
use serde::Serialize;

#[derive(Serialize)]
pub struct PageOut {
    number: u32,
    markdown: String,
}

pub fn to_pages(
    bytes: &[u8],
    format: Option<anydoc::Format>,
) -> Result<Vec<PageOut>, ConvertError> {
    let format = format.or_else(|| anydoc::Format::from_bytes(bytes)).ok_or_else(|| {
        ConvertError::Unsupported("unrecognized file content: name the format explicitly".into())
    })?;
    if format != anydoc::Format::Pdf {
        return Err(ConvertError::Unsupported(format!(
            "per-page conversion supports PDF only, not {format:?}"
        )));
    }

    let detection = pdf_inspector::detect_pdf_mem(bytes).map_err(map_error)?;
    if !detection.pages_needing_ocr.is_empty() {
        // Same confirmation step as anydoc: detection over-reports, and only
        // flagged pages that extraction also finds empty are named.
        let flagged: Vec<u32> = detection.pages_needing_ocr.iter().map(|page| page - 1).collect();
        let pages = pdf_inspector::extract_pages_markdown_mem(bytes, Some(&flagged))
            .map_err(map_error)?
            .pages_needing_ocr;
        if !pages.is_empty() {
            return Err(ConvertError::NeedsOcr { pages, page_count: detection.page_count });
        }
    }

    let no_text = || {
        ConvertError::Unsupported(format!(
            "PDF has no extractable text ({:?}, {} pages)",
            detection.pdf_type, detection.page_count
        ))
    };
    if matches!(detection.pdf_type, PdfType::Scanned | PdfType::ImageBased) {
        return Err(no_text());
    }
    let pages = pdf_inspector::extract_pages_markdown_mem(bytes, None).map_err(map_error)?.pages;
    if pages.iter().all(|page| page.markdown.trim().is_empty()) {
        return Err(no_text());
    }
    Ok(pages
        .into_iter()
        .map(|page| PageOut { number: page.page + 1, markdown: page.markdown })
        .collect())
}

fn map_error(e: PdfError) -> ConvertError {
    match e {
        PdfError::Encrypted => ConvertError::Encrypted,
        PdfError::Io(e) => ConvertError::Io(e),
        PdfError::NotAPdf(detail) => malformed(format!("not a PDF: {detail}")),
        PdfError::InvalidStructure => malformed("invalid PDF structure".into()),
        PdfError::Parse(detail) => malformed(detail),
    }
}

fn malformed(detail: String) -> ConvertError {
    ConvertError::Malformed { part: None, detail }
}
