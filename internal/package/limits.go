// Package pkg is anydoc's shared package layer for container-backed formats
// (DOCX, PPTX, XLSX, ODF, EPUB over ZIP; DOC, PPT, XLS over OLE compound
// files): limited archive access behind one Archive interface, namespace-aware
// XML in the xml subpackage, typed relationships, and OPC/EPUB target
// resolution.
//
// This file ports src/package/limits.rs.
package pkg

// Fixed safety limits, applied identically to every conversion.
//
// These are hard caps against attack/abuse input shapes (decompression
// bombs, pathological nesting, runaway expansion) - crossing one returns a
// cerr.Error of kind cerr.KindResourceLimit, always. They are deliberately
// not configurable: real-world documents sit orders of magnitude below every
// value here.
//
// The ResourceLimit limit names (shown in parentheses) are stable contract
// strings surfaced in errors.
const (
	// MaxEntryBytes is the maximum decompressed size of a single archive
	// entry: 128 MiB (max_entry_bytes).
	MaxEntryBytes uint64 = 128 * 1024 * 1024
	// MaxTotalBytes is the maximum total decompressed bytes read from one
	// archive: 512 MiB (max_total_bytes).
	MaxTotalBytes uint64 = 512 * 1024 * 1024
	// MaxEntryCount is the maximum number of entries in one archive
	// (max_entry_count).
	MaxEntryCount int = 100_000
	// MaxXMLDepth is the maximum XML element nesting depth (max_xml_depth).
	MaxXMLDepth int = 256
	// MaxXMLNodes is the maximum number of XML nodes (elements + text runs)
	// in one part (max_xml_nodes). Sized from the measured worst-case DOM
	// cost (~400 bytes/node, see the node-cap memory test) so a saturating
	// part stays around the archive budget.
	MaxXMLNodes int = 2_000_000
	// MaxGridSlots is the maximum grid positions one spreadsheet table may
	// materialize: 4 million (max_grid_slots).
	MaxGridSlots uint64 = 4_000_000
	// MaxExpansion is the maximum content-bearing cells a repeat expansion
	// may produce per table (max_expansion).
	MaxExpansion uint64 = 4_000_000
	// MaxExpansionTextBytes is the maximum total text bytes *duplicated* by
	// repeat expansion per document: 64 MiB
	// (max_expansion_text_bytes). The slot budget above bounds positions;
	// this bounds the memory a small document can amplify by repeating
	// content-bearing cells.
	MaxExpansionTextBytes uint64 = 64 * 1024 * 1024
	// MaxAssetTotalBytes is the maximum total bytes of embedded assets
	// retained in a model Document: 128 MiB (max_asset_total_bytes).
	MaxAssetTotalBytes int = 128 * 1024 * 1024
	// MaxRecordDepth is the maximum nesting depth of binary record
	// containers (legacy PPT stream) (max_record_depth).
	MaxRecordDepth int = 64
	// MaxRecords is the maximum total binary records visited in one legacy
	// record stream (max_records).
	MaxRecords uint64 = 16_000_000
)
