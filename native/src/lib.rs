//! C ABI wrapping anydoc 0.2.4 for the Go bindings.

use std::ffi::{c_char, CStr, CString};
use std::path::Path;
use std::ptr;
use std::slice;

mod document;

const FORMATS: &[(&str, anydoc::Format)] = &[
    ("doc", anydoc::Format::Doc),
    ("docx", anydoc::Format::Docx),
    ("odt", anydoc::Format::Odt),
    ("pdf", anydoc::Format::Pdf),
    ("ppt", anydoc::Format::Ppt),
    ("pptx", anydoc::Format::Pptx),
    ("rtf", anydoc::Format::Rtf),
    ("epub", anydoc::Format::Epub),
    ("xlsx", anydoc::Format::Excel),
    ("ods", anydoc::Format::Ods),
    ("odp", anydoc::Format::Odp),
    ("csv", anydoc::Format::Csv),
];

fn format_name(format: anydoc::Format) -> &'static str {
    FORMATS
        .iter()
        .find(|(_, f)| *f == format)
        .map(|(name, _)| *name)
        .expect("every format is named")
}

fn parse_format(name: &str) -> Result<anydoc::Format, String> {
    FORMATS
        .iter()
        .find(|(n, _)| *n == name)
        .map(|(_, format)| *format)
        .ok_or_else(|| {
            let names: Vec<&str> = FORMATS.iter().map(|(n, _)| *n).collect();
            format!("unknown format {name:?}; expected one of {}", names.join(", "))
        })
}

fn cstring(s: impl AsRef<str>) -> *mut c_char {
    let cleaned = s.as_ref().replace('\0', "");
    CString::new(cleaned).unwrap_or_else(|_| CString::new("").unwrap()).into_raw()
}

fn cstr_opt<'a>(p: *const c_char) -> Option<&'a str> {
    if p.is_null() {
        return None;
    }
    unsafe { CStr::from_ptr(p) }.to_str().ok()
}

fn bytes<'a>(data: *const u8, len: usize) -> &'a [u8] {
    if data.is_null() || len == 0 {
        return &[];
    }
    unsafe { slice::from_raw_parts(data, len) }
}

fn write_format(out: *mut *mut c_char, format: Option<anydoc::Format>) {
    if out.is_null() {
        return;
    }
    unsafe {
        *out = match format {
            Some(f) => cstring(format_name(f)),
            None => ptr::null_mut(),
        };
    }
}

fn fill_error(err: *mut AnydocError, error: anydoc::ConvertError) {
    if err.is_null() {
        return;
    }
    let dst = unsafe { &mut *err };
    dst.code = cstring(error.code());
    dst.message = cstring(error.to_string());
    match error {
        anydoc::ConvertError::NeedsOcr { pages, page_count } => {
            dst.page_count = page_count;
            let mut pages = pages;
            pages.shrink_to_fit();
            dst.pages_len = pages.len();
            dst.pages = pages.as_mut_ptr();
            std::mem::forget(pages);
        }
        anydoc::ConvertError::Malformed { part: Some(part), .. } => {
            dst.part = cstring(part);
        }
        anydoc::ConvertError::ResourceLimit { limit, .. } => {
            dst.limit = cstring(limit);
        }
        anydoc::ConvertError::MissingPart { part } => {
            dst.part = cstring(part);
        }
        _ => {}
    }
}

fn fill_message(err: *mut AnydocError, code: &str, message: impl AsRef<str>) {
    if err.is_null() {
        return;
    }
    let dst = unsafe { &mut *err };
    dst.code = cstring(code);
    dst.message = cstring(message);
}

fn named_format(format: *const c_char) -> Result<Option<anydoc::Format>, String> {
    match cstr_opt(format) {
        None | Some("") => Ok(None),
        Some(name) => parse_format(name).map(Some),
    }
}

#[repr(C)]
pub struct AnydocError {
    code: *mut c_char,
    message: *mut c_char,
    pages: *mut u32,
    pages_len: usize,
    page_count: u32,
    part: *mut c_char,
    limit: *mut c_char,
}

#[no_mangle]
pub unsafe extern "C" fn anydoc_string_free(s: *mut c_char) {
    if !s.is_null() {
        drop(CString::from_raw(s));
    }
}

#[no_mangle]
pub unsafe extern "C" fn anydoc_error_free(err: *mut AnydocError) {
    if err.is_null() {
        return;
    }
    let e = &mut *err;
    anydoc_string_free(e.code);
    anydoc_string_free(e.message);
    anydoc_string_free(e.part);
    anydoc_string_free(e.limit);
    if !e.pages.is_null() {
        drop(Vec::from_raw_parts(e.pages, e.pages_len, e.pages_len));
    }
    e.code = ptr::null_mut();
    e.message = ptr::null_mut();
    e.pages = ptr::null_mut();
    e.pages_len = 0;
    e.page_count = 0;
    e.part = ptr::null_mut();
    e.limit = ptr::null_mut();
}

#[no_mangle]
pub unsafe extern "C" fn anydoc_format_from_bytes(
    data: *const u8,
    len: usize,
    out_format: *mut *mut c_char,
) -> i32 {
    let result = std::panic::catch_unwind(|| anydoc::Format::from_bytes(bytes(data, len)));
    match result {
        Ok(format) => {
            write_format(out_format, format);
            1
        }
        Err(_) => 0,
    }
}

#[no_mangle]
pub unsafe extern "C" fn anydoc_format_from_extension(
    extension: *const c_char,
    out_format: *mut *mut c_char,
) -> i32 {
    let Some(ext) = cstr_opt(extension) else {
        write_format(out_format, None);
        return 1;
    };
    let trimmed = ext.trim_start_matches('.');
    write_format(out_format, anydoc::Format::from_extension(trimmed));
    1
}

#[no_mangle]
pub unsafe extern "C" fn anydoc_format_from_path(
    path: *const c_char,
    out_format: *mut *mut c_char,
) -> i32 {
    let Some(path) = cstr_opt(path) else {
        write_format(out_format, None);
        return 1;
    };
    write_format(out_format, anydoc::Format::from_path(Path::new(path)));
    1
}

#[no_mangle]
pub unsafe extern "C" fn anydoc_to_markdown(
    path: *const c_char,
    out_md: *mut *mut c_char,
    err: *mut AnydocError,
) -> i32 {
    let Some(path) = cstr_opt(path) else {
        fill_message(err, "io", "path is required");
        return 0;
    };
    match std::panic::catch_unwind(|| anydoc::to_markdown(path)) {
        Ok(Ok(md)) => {
            if !out_md.is_null() {
                *out_md = cstring(md);
            }
            1
        }
        Ok(Err(e)) => {
            fill_error(err, e);
            0
        }
        Err(_) => {
            fill_message(err, "malformed", "internal panic during conversion");
            0
        }
    }
}

#[no_mangle]
pub unsafe extern "C" fn anydoc_to_markdown_bytes(
    data: *const u8,
    len: usize,
    format: *const c_char,
    out_md: *mut *mut c_char,
    err: *mut AnydocError,
) -> i32 {
    let format = match named_format(format) {
        Ok(f) => f,
        Err(msg) => {
            fill_message(err, "unsupported", msg);
            return 0;
        }
    };
    let input = bytes(data, len).to_vec();
    match std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
        anydoc::to_markdown_bytes(&input, format)
    })) {
        Ok(Ok(md)) => {
            if !out_md.is_null() {
                *out_md = cstring(md);
            }
            1
        }
        Ok(Err(e)) => {
            fill_error(err, e);
            0
        }
        Err(_) => {
            fill_message(err, "malformed", "internal panic during conversion");
            0
        }
    }
}

#[no_mangle]
pub unsafe extern "C" fn anydoc_to_document_json(
    data: *const u8,
    len: usize,
    format: *const c_char,
    out_json: *mut *mut c_char,
    err: *mut AnydocError,
) -> i32 {
    let format = match named_format(format) {
        Ok(f) => f,
        Err(msg) => {
            fill_message(err, "unsupported", msg);
            return 0;
        }
    };
    let input = bytes(data, len).to_vec();
    match std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
        anydoc::to_document(&input, format).map(|doc| {
            serde_json::to_string(&document::DocumentOut::from(doc))
                .expect("document json is utf-8")
        })
    })) {
        Ok(Ok(json)) => {
            if !out_json.is_null() {
                *out_json = cstring(json);
            }
            1
        }
        Ok(Err(e)) => {
            fill_error(err, e);
            0
        }
        Err(_) => {
            fill_message(err, "malformed", "internal panic during conversion");
            0
        }
    }
}
