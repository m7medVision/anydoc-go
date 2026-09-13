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

dest="$(pwd)/prebuilt/$platform"
archive="$dest/libanydoc_ffi.a"
mkdir -p "$dest"
cp "$built" "$archive"

# Drop debug sections but keep every symbol so the cgo link still resolves.
# Apple's strip is not GNU and has no --strip-debug; -S is its equivalent
# (strip debugging symbols only). Using the system tool avoids depending on
# xcrun/llvm-strip being on PATH.
case "$(uname -s)" in
Darwin) strip -S "$archive" ;;
*) strip --strip-debug "$archive" ;;
esac

# Verify the exported C ABI survived the strip. On Linux, --defined-only keeps
# the listing to symbols defined in the archive; Apple nm has no such flag but
# still prints defined text symbols as ' T '.
case "$(uname -s)" in
Darwin) symbols=$(nm "$archive" 2>/dev/null | grep ' T anydoc_' || true) ;;
*) symbols=$(nm --defined-only "$archive" 2>/dev/null | grep ' T anydoc_' || true) ;;
esac
missing=0
for sym in anydoc_to_markdown anydoc_to_markdown_bytes anydoc_to_document_json \
	anydoc_format_from_bytes anydoc_string_free anydoc_error_free; do
	if printf '%s\n' "$symbols" | grep -q " T ${sym}\$"; then
		printf '%s\n' "$symbols" | grep " T ${sym}\$" | head -n 1
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
