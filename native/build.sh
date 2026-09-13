#!/bin/sh
# Build the anydoc FFI static archive and install it under prebuilt/<GOOS_GOARCH>.
#
# Usage: build.sh [rust-target-triple]
#
# Without an argument the host is built and GOOS/GOARCH come from the
# environment or `go env`. With a triple, cargo cross-builds for it and the
# archive lands in the matching GOOS_GOARCH directory.
set -eu
cd "$(dirname "$0")"
export CARGO_TARGET_DIR="${CARGO_TARGET_DIR:-$(pwd)/target}"

triple=${1:-}
if [ -n "$triple" ]; then
	case "$triple" in
	x86_64-unknown-linux-gnu) platform=linux_amd64 ;;
	aarch64-unknown-linux-gnu) platform=linux_arm64 ;;
	x86_64-apple-darwin) platform=darwin_amd64 ;;
	aarch64-apple-darwin) platform=darwin_arm64 ;;
	*)
		echo "build.sh: unknown Rust target triple '$triple'" >&2
		echo "build.sh: supported: x86_64-unknown-linux-gnu aarch64-unknown-linux-gnu x86_64-apple-darwin aarch64-apple-darwin" >&2
		exit 2
		;;
	esac
	cargo build --release --locked --target "$triple"
	built="$CARGO_TARGET_DIR/$triple/release/libanydoc_ffi.a"
else
	cargo build --release --locked
	built="$CARGO_TARGET_DIR/release/libanydoc_ffi.a"
	goos=${GOOS:-}
	goarch=${GOARCH:-}
	if [ -z "$goos" ] || [ -z "$goarch" ]; then
		if command -v go >/dev/null 2>&1; then
			goos=${goos:-$(go env GOOS)}
			goarch=${goarch:-$(go env GOARCH)}
		fi
	fi
	if [ -z "$goos" ] || [ -z "$goarch" ]; then
		echo "build.sh: cannot determine GOOS/GOARCH; set them or install go" >&2
		exit 2
	fi
	platform="${goos}_${goarch}"
fi

# The archive's object format decides how it is stripped and inspected, so
# derive the OS from the target triple when one was given and from the host
# otherwise. Only linux and darwin are supported (see the triple table above).
case "$triple" in
*-apple-darwin) os=darwin ;;
*-linux-*) os=linux ;;
*)
	case "$(uname -s)" in
	Darwin) os=darwin ;;
	*) os=linux ;;
	esac
	;;
esac
case "$os" in
darwin)
	# Apple's strip is not GNU and has no --strip-debug; -S is its equivalent
	# (strip debugging symbols only). Using the system tool avoids depending on
	# xcrun/llvm-strip being on PATH. Apple nm has no --defined-only but still
	# prints defined text symbols as ' T '. Mach-O prefixes C symbols with an
	# underscore, so anydoc_to_markdown is listed as _anydoc_to_markdown.
	strip_cmd="strip -S"
	nm_cmd="nm"
	sym_prefix="_"
	;;
*)
	strip_cmd="strip --strip-debug"
	nm_cmd="nm --defined-only"
	sym_prefix=""
	;;
esac

dest="$(pwd)/prebuilt/$platform"
archive="$dest/libanydoc_ffi.a"
mkdir -p "$dest"
cp "$built" "$archive"

# Drop debug sections but keep every symbol so the cgo link still resolves.
$strip_cmd "$archive"

# Verify the exported C ABI survived the strip.
symbols=$($nm_cmd "$archive" 2>/dev/null | grep " T ${sym_prefix}anydoc_" || true)
missing=0
for sym in anydoc_to_markdown anydoc_to_markdown_bytes anydoc_to_document_json \
	anydoc_format_from_bytes anydoc_pdf_pages_json anydoc_string_free anydoc_error_free; do
	if printf '%s\n' "$symbols" | grep -q " T ${sym_prefix}${sym}\$"; then
		printf '%s\n' "$symbols" | grep " T ${sym_prefix}${sym}\$" | head -n 1
	else
		echo "build.sh: missing exported symbol $sym" >&2
		missing=1
	fi
done
if [ "$missing" -ne 0 ]; then
	rm -f "$archive"
	exit 1
fi

echo "$archive $(wc -c <"$archive") bytes"
