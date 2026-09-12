// Archive access with decompression limits: one Archive interface over the
// two containers the converter reads (ZIP packages and OLE compound files),
// plus Package, the policy wrapper every frontend uses.
//
// This file ports src/package/archive.rs; the CFB adapter is the Go-port
// seam replacing the cfb crate's direct use by the legacy frontends.
package pkg

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/m7medVision/anydoc-go/internal/cerr"
	"github.com/m7medVision/anydoc-go/internal/package/xml"
	"github.com/richardlehane/mscfb"
)

// Archive is the module's only container abstraction: open (the OpenZip and
// OpenCFB constructors), part list, and part read. Implementations enforce
// their own budget semantics - the ZIP adapter mirrors upstream Package
// exactly (per-entry cap, whole-archive total, decompression cache); the
// CFB adapter mirrors the legacy stream reads (per-stream cap only).
type Archive interface {
	// Len reports the number of entries in the container.
	Len() int
	// Parts lists the container's entries in container order: every file
	// entry for ZIP, the root-storage entries for OLE compound files (with
	// storage objects flagged IsStream=false).
	Parts() []Part
	// HasPart reports whether the container holds the named part, without
	// reading (or budget-charging) it. Names are container-relative;
	// leading slashes are already trimmed by Package.
	HasPart(name string) bool
	// ReadPart reads one part's bytes. found is false when the part does not
	// exist; err carries read failures. Returned bytes are shared and must
	// not be modified by callers.
	ReadPart(name string) (data []byte, found bool, err error)
}

// Part describes one container entry.
type Part struct {
	Name string
	// IsStream is true for readable streams; false for directory entries
	// (ZIP folders, OLE storages).
	IsStream bool
}

// Package is a document package over an Archive: a ZIP-based one (OOXML,
// ODF, EPUB) or an OLE compound file (legacy DOC, PPT, XLS). It owns the
// read policies frontends share: absent-optional-part tolerance, required
// parts, and XML parsing.
type Package struct {
	arch Archive
}

// OpenZip opens a ZIP-based document package.
func OpenZip(data []byte) (*Package, error) {
	arch, err := openZip(data)
	if err != nil {
		return nil, err
	}
	return &Package{arch: arch}, nil
}

// OpenCFB opens an OLE compound-file package (legacy binary formats).
func OpenCFB(data []byte) (*Package, error) {
	arch, err := openCFB(data)
	if err != nil {
		return nil, err
	}
	return &Package{arch: arch}, nil
}

// Part reads a part's bytes. found=false means the part is absent (a valid
// state for optional parts); err means it exists but cannot be read.
// Callers apply the unified policy: skip when useful output remains,
// propagate when the part is the primary content. The returned bytes are
// shared (upstream hands out Rc buffers): do not modify them.
func (p *Package) Part(name string) ([]byte, bool, error) {
	// OPC part URIs may carry a leading slash; entries never do.
	name = strings.TrimLeft(name, "/")
	return p.arch.ReadPart(name)
}

// HasPart is true when a part exists, without reading (or budget-charging)
// it.
func (p *Package) HasPart(name string) bool {
	return p.arch.HasPart(strings.TrimLeft(name, "/"))
}

// RequiredPart reads a part that must exist for any meaningful output.
func (p *Package) RequiredPart(name string) ([]byte, error) {
	data, found, err := p.Part(name)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, &cerr.Error{Kind: cerr.KindMissingPart, Part: name}
	}
	return data, nil
}

// OptionalPart reads an optional part under the unified recovery policy:
// absent is a valid state (found=false, silent); an unreadable part is
// skipped; fatal resource-limit errors always propagate.
func (p *Package) OptionalPart(name string) ([]byte, bool, error) {
	data, found, err := p.Part(name)
	if err != nil {
		if isFatal(err) {
			return nil, false, err
		}
		return nil, false, nil
	}
	return data, found, nil
}

// OptionalXMLPart reads and parses an optional XML part under the unified
// recovery policy: absent, unreadable, or corrupt parts are skipped; fatal
// resource-limit errors always propagate.
func (p *Package) OptionalXMLPart(name string) (*xml.Element, bool, error) {
	data, found, err := p.OptionalPart(name)
	if err != nil || !found {
		return nil, false, err
	}
	root, err := xml.ParseXML(data)
	if err != nil {
		if isFatal(err) {
			return nil, false, err
		}
		return nil, false, nil
	}
	return root, true, nil
}

// RequiredXMLPart reads and parses an XML part that must exist and parse
// for any meaningful output.
func (p *Package) RequiredXMLPart(name string) (*xml.Element, error) {
	data, err := p.RequiredPart(name)
	if err != nil {
		return nil, err
	}
	return xml.ParseXML(data)
}

// Len reports the number of entries in the underlying container.
func (p *Package) Len() int { return p.arch.Len() }

// Parts lists the underlying container's entries.
func (p *Package) Parts() []Part { return p.arch.Parts() }

// ProbeOLE checks bytes that failed to open as ZIP: they may be an OLE
// compound file - an encrypted package, or a legacy binary document with
// the wrong extension. It returns nil when the bytes are not OLE at all.
func ProbeOLE(data []byte) error {
	const oleMagic = "\xD0\xCF\x11\xE0\xA1\xB1\x1A\xE1"
	if !bytes.HasPrefix(data, []byte(oleMagic)) {
		return nil
	}
	if cfb, err := mscfb.New(bytes.NewReader(data)); err == nil {
		for _, entry := range cfb.File {
			if len(entry.Path) == 0 && (entry.Name == "EncryptionInfo" || entry.Name == "EncryptedPackage") {
				return &cerr.Error{Kind: cerr.KindEncrypted}
			}
		}
	}
	return &cerr.Error{
		Kind:   cerr.KindMalformed,
		Detail: "OLE compound document where an OOXML package was expected (legacy binary format?)",
	}
}

// isFatal reports whether recovery must not swallow the error.
func isFatal(err error) bool {
	if ce, ok := err.(*cerr.Error); ok {
		return ce.IsFatal()
	}
	return false
}

// zipArchive reads a ZIP package with upstream Package's exact budget
// semantics: a per-entry decompression cap, a whole-archive total, and a
// decompressed-parts cache so repeated references are neither
// re-decompressed nor re-charged (valid documents reference one part many
// times). Cache buffers are shared, bounded in aggregate by MaxTotalBytes.
type zipArchive struct {
	names     []string // entry names, central-directory order
	files     map[string]*zip.File
	totalRead uint64
	cache     map[string][]byte
}

func openZip(data []byte) (*zipArchive, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, &cerr.Error{
			Kind:   cerr.KindMalformed,
			Detail: fmt.Sprintf("not a readable zip archive: %v", err),
		}
	}
	if len(r.File) > MaxEntryCount {
		return nil, &cerr.Error{
			Kind:   cerr.KindResourceLimit,
			Limit:  "max_entry_count",
			Detail: fmt.Sprintf("archive contains %d entries", len(r.File)),
		}
	}
	files := make(map[string]*zip.File, len(r.File))
	names := make([]string, 0, len(r.File))
	for _, f := range r.File {
		if _, dup := files[f.Name]; !dup {
			files[f.Name] = f
			names = append(names, f.Name)
		}
	}
	return &zipArchive{names: names, files: files, cache: make(map[string][]byte)}, nil
}

func (a *zipArchive) Len() int { return len(a.files) }

func (a *zipArchive) Parts() []Part {
	out := make([]Part, 0, len(a.names))
	for _, name := range a.names {
		out = append(out, Part{Name: name, IsStream: !strings.HasSuffix(name, "/")})
	}
	return out
}

func (a *zipArchive) HasPart(name string) bool {
	_, ok := a.files[name]
	return ok
}

func (a *zipArchive) ReadPart(name string) ([]byte, bool, error) {
	if cached, ok := a.cache[name]; ok {
		return cached, true, nil
	}
	f, ok := a.files[name]
	if !ok {
		return nil, false, nil // ZipError::FileNotFound
	}
	if uint64(f.UncompressedSize64) > MaxEntryBytes {
		return nil, true, &cerr.Error{
			Kind:   cerr.KindResourceLimit,
			Limit:  "max_entry_bytes",
			Detail: fmt.Sprintf("%s declares %d decompressed bytes", name, f.UncompressedSize64),
		}
	}
	// The declared size can lie; read through a hard-capped reader. The cap
	// is whichever budget has less room: the per-entry limit or what remains
	// of the whole-archive total.
	remainingTotal := uint64(0)
	if a.totalRead < MaxTotalBytes {
		remainingTotal = MaxTotalBytes - a.totalRead
	}
	limit := MaxEntryBytes
	if remainingTotal < limit {
		limit = remainingTotal
	}
	rc, err := f.Open()
	if err != nil {
		return nil, true, &cerr.Error{
			Kind:   cerr.KindMalformed,
			Part:   name,
			Detail: fmt.Sprintf("unreadable archive entry: %v", err),
		}
	}
	// limit+1 bytes so an over-budget entry is detected, not truncated.
	buf, readErr := io.ReadAll(io.LimitReader(rc, int64(limit)+1))
	if closeErr := rc.Close(); readErr == nil {
		readErr = closeErr
	}
	if readErr != nil {
		return nil, true, &cerr.Error{
			Kind:   cerr.KindMalformed,
			Part:   name,
			Detail: fmt.Sprintf("corrupt archive entry: %v", readErr),
		}
	}
	if uint64(len(buf)) > limit {
		if remainingTotal < MaxEntryBytes {
			return nil, true, &cerr.Error{
				Kind:   cerr.KindResourceLimit,
				Limit:  "max_total_bytes",
				Detail: name + " exceeds the archive's remaining decompression budget",
			}
		}
		return nil, true, &cerr.Error{
			Kind:   cerr.KindResourceLimit,
			Limit:  "max_entry_bytes",
			Detail: name + " exceeds the decompression cap",
		}
	}
	a.totalRead += uint64(len(buf))
	a.cache[name] = buf
	return buf, true, nil
}

// cfbArchive reads an OLE compound file, mirroring the legacy frontends'
// stream access: per-stream reads hard-capped at MaxEntryBytes so a corrupt
// sector chain cannot expand without bound. Streams are keyed by their
// root-relative path ("WordDocument", "ObjectPool/Table"); root-storage
// children are also what Parts lists.
type cfbArchive struct {
	r     *mscfb.Reader
	byKey map[string]*mscfb.File
	root  []*mscfb.File // root-storage children, container order
}

func openCFB(data []byte) (*cfbArchive, error) {
	r, err := mscfb.New(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	a := &cfbArchive{r: r, byKey: make(map[string]*mscfb.File, len(r.File))}
	for i, entry := range r.File {
		if i == 0 {
			continue // the root storage entry itself
		}
		if len(entry.Path) == 0 {
			a.root = append(a.root, entry)
		}
		key := entry.Name
		if len(entry.Path) > 0 {
			key = strings.Join(entry.Path, "/") + "/" + entry.Name
		}
		if _, dup := a.byKey[key]; !dup {
			a.byKey[key] = entry
		}
	}
	return a, nil
}

func (a *cfbArchive) Len() int {
	if len(a.r.File) == 0 {
		return 0
	}
	return len(a.r.File) - 1
}

func (a *cfbArchive) Parts() []Part {
	out := make([]Part, 0, len(a.root))
	for _, f := range a.root {
		out = append(out, Part{Name: f.Name, IsStream: !f.FileInfo().IsDir()})
	}
	return out
}

func (a *cfbArchive) HasPart(name string) bool {
	_, ok := a.byKey[name]
	return ok
}

func (a *cfbArchive) ReadPart(name string) ([]byte, bool, error) {
	f, ok := a.byKey[name]
	if !ok {
		return nil, false, nil
	}
	if f.Size == 0 {
		// mscfb cannot seek within a zero-length stream; the empty read is
		// the answer.
		return []byte{}, true, nil
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, true, &cerr.Error{
			Kind:   cerr.KindMalformed,
			Part:   name,
			Detail: fmt.Sprintf("unreadable stream: %v", err),
		}
	}
	// MaxEntryBytes+1 so an over-cap stream is detected, not truncated.
	buf, readErr := io.ReadAll(io.LimitReader(f, int64(MaxEntryBytes)+1))
	if readErr != nil {
		return nil, true, &cerr.Error{
			Kind:   cerr.KindMalformed,
			Part:   name,
			Detail: fmt.Sprintf("unreadable stream: %v", readErr),
		}
	}
	if uint64(len(buf)) > MaxEntryBytes {
		return nil, true, &cerr.Error{
			Kind:   cerr.KindResourceLimit,
			Limit:  "max_entry_bytes",
			Detail: name + " stream exceeds the read cap",
		}
	}
	return buf, true, nil
}
