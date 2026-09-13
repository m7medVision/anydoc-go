#!/bin/sh
set -eu
cd "$(dirname "$0")"
export CARGO_TARGET_DIR="${CARGO_TARGET_DIR:-$(pwd)/target}"
exec cargo build --release
