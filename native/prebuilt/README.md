# Prebuilt native libraries

`libanydoc_ffi.a` for `GOOS_GOARCH` so `go get` + `go build` can link from the read-only module cache.

Linux amd64 ships here. `go generate .` refreshes the archive for the current platform.
