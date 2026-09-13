#!/bin/sh
set -eu
cd "$(dirname "$0")"
export CARGO_TARGET_DIR="${CARGO_TARGET_DIR:-$(pwd)/target}"
cargo build --release
goos=${GOOS:-}
goarch=${GOARCH:-}
if [ -z "$goos" ] || [ -z "$goarch" ]; then
	if command -v go >/dev/null 2>&1; then
		goos=${goos:-$(go env GOOS)}
		goarch=${goarch:-$(go env GOARCH)}
	fi
fi
if [ -n "${goos:-}" ] && [ -n "${goarch:-}" ]; then
	dest="$(pwd)/prebuilt/${goos}_${goarch}"
	mkdir -p "$dest"
	cp "$CARGO_TARGET_DIR/release/libanydoc_ffi.a" "$dest/libanydoc_ffi.a"
fi
