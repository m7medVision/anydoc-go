#ifndef ANYDOC_H
#define ANYDOC_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct anydoc_error {
	char *code;
	char *message;
	uint32_t *pages;
	size_t pages_len;
	uint32_t page_count;
	char *part;
	char *limit;
} anydoc_error;

void anydoc_string_free(char *s);
void anydoc_error_free(anydoc_error *err);

/* 1 on success. *out_format is NULL when nothing matches. */
int anydoc_format_from_bytes(const uint8_t *data, size_t len, char **out_format);
int anydoc_format_from_extension(const char *extension, char **out_format);
int anydoc_format_from_path(const char *path, char **out_format);

/* 1 on success (out filled). 0 on error (err filled). format may be NULL. */
int anydoc_to_markdown(const char *path, char **out_md, anydoc_error *err);
int anydoc_to_markdown_bytes(const uint8_t *data, size_t len, const char *format, char **out_md, anydoc_error *err);
int anydoc_to_document_json(const uint8_t *data, size_t len, const char *format, char **out_json, anydoc_error *err);
/* PDF only. out_json is a JSON array of {"number", "markdown"} in page order. */
int anydoc_pdf_pages_json(const uint8_t *data, size_t len, const char *format, char **out_json, anydoc_error *err);

#ifdef __cplusplus
}
#endif

#endif
