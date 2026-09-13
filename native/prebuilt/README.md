# Prebuilt native libraries

This directory holds `<GOOS>_<GOARCH>/libanydoc_ffi.a`, the static library the cgo directives in `native.go` link. It is here so `go get` + `go build` can link from the read-only module cache without a Rust toolchain. Each archive is stripped of debug sections and is roughly 16 MB.

Supported directories: `linux_amd64`, `linux_arm64`, `darwin_amd64`, `darwin_arm64`.

The archives are produced by the `release` GitHub Actions workflow, once per tag, on a runner matching each platform. Do not hand-edit them and do not commit local builds.

`go generate .` from the repo root refreshes only the archive for the host platform. That is for local development against a changed Rust shim. Do not commit the result unless you are cutting a release.
