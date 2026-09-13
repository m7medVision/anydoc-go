// Port of src/shared/binary.rs: shared primitives for the legacy OLE2
// binary formats (DOC, PPT): checked little-endian integer readers and
// bounded compound-file stream reading.

package shared

import (
	"encoding/binary"
	"fmt"
	"io"
	"strings"

	"github.com/m7medVision/anydoc-go/internal/cerr"
	pkg "github.com/m7medVision/anydoc-go/internal/package"
	"github.com/richardlehane/mscfb"
)

// GetU16 is the little-endian u16 at off; ok is false when out of bounds.
func GetU16(b []byte, off int) (uint16, bool) {
	if off < 0 || len(b) < 2 || off > len(b)-2 {
		return 0, false
	}
	return binary.LittleEndian.Uint16(b[off:]), true
}

// GetU32 is the little-endian u32 at off; ok is false when out of bounds.
func GetU32(b []byte, off int) (uint32, bool) {
	if off < 0 || len(b) < 4 || off > len(b)-4 {
		return 0, false
	}
	return binary.LittleEndian.Uint32(b[off:]), true
}

// UTF16LEUnits returns the UTF-16LE code units of b; a trailing odd byte
// is dropped.
func UTF16LEUnits(b []byte) []uint16 {
	n := len(b) / 2
	out := make([]uint16, n)
	for i := 0; i < n; i++ {
		out[i] = binary.LittleEndian.Uint16(b[i*2:])
	}
	return out
}

// ReadOLEStream reads a named stream from an OLE2 compound file. A missing
// stream is MissingPart; the read is hard-capped at MaxEntryBytes so a
// corrupt sector chain cannot expand without bound.
//
// Upstream takes cfb::CompoundFile; the Go port uses mscfb.Reader, the
// same adapter the package layer uses for OLE containers.
func ReadOLEStream(ole *mscfb.Reader, name string) ([]byte, error) {
	var file *mscfb.File
	for i, entry := range ole.File {
		if i == 0 || entry == nil {
			continue
		}
		key := entry.Name
		if len(entry.Path) > 0 {
			key = strings.Join(entry.Path, "/") + "/" + entry.Name
		}
		if key == name {
			file = entry
			break
		}
	}
	if file == nil || file.FileInfo().IsDir() {
		return nil, &cerr.Error{Kind: cerr.KindMissingPart, Part: name}
	}
	if file.Size == 0 {
		// mscfb cannot seek within a zero-length stream.
		return []byte{}, nil
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, &cerr.Error{
			Kind:   cerr.KindMalformed,
			Part:   name,
			Detail: fmt.Sprintf("unreadable stream: %v", err),
		}
	}
	buf, err := io.ReadAll(io.LimitReader(file, int64(pkg.MaxEntryBytes)+1))
	if err != nil {
		return nil, &cerr.Error{
			Kind:   cerr.KindMalformed,
			Part:   name,
			Detail: fmt.Sprintf("unreadable stream: %v", err),
		}
	}
	if uint64(len(buf)) > pkg.MaxEntryBytes {
		return nil, &cerr.Error{
			Kind:   cerr.KindResourceLimit,
			Limit:  "max_entry_bytes",
			Detail: name + " stream exceeds the read cap",
		}
	}
	return buf, nil
}
