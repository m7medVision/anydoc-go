package objects

// Xref / XrefEntry port of lopdf src/xref.rs.

type XrefType uint8

const (
	XrefTypeCrossReferenceStream XrefType = iota
	XrefTypeCrossReferenceTable
)

type XrefEntryType uint8

const (
	XrefEntryFree XrefEntryType = iota
	XrefEntryUnusableFree
	XrefEntryNormal
	XrefEntryCompressed
)

type XrefEntry struct {
	Type XrefEntryType
	// Normal
	Offset     uint32
	Generation uint16
	// Compressed
	Container uint32
	Index     uint16
}

func (e *XrefEntry) IsNormal() bool     { return e.Type == XrefEntryNormal }
func (e *XrefEntry) IsCompressed() bool { return e.Type == XrefEntryCompressed }

// Xref mirrors lopdf::Xref. Entries iterate in ascending object-number order
// (BTreeMap parity).
type Xref struct {
	CrossReferenceType XrefType
	entries            map[uint32]XrefEntry
	order              []uint32 // sorted object numbers
	Size               uint32
}

func NewXref(size uint32, t XrefType) *Xref {
	return &Xref{CrossReferenceType: t, entries: map[uint32]XrefEntry{}, Size: size}
}

func (x *Xref) Get(id uint32) (XrefEntry, bool) {
	e, ok := x.entries[id]
	return e, ok
}

func (x *Xref) Insert(id uint32, entry XrefEntry) {
	if _, ok := x.entries[id]; !ok {
		x.order = append(x.order, id)
		// keep sorted
		i := len(x.order) - 1
		for i > 0 && x.order[i-1] > id {
			x.order[i] = x.order[i-1]
			x.order[i-1] = id
			i--
		}
	}
	x.entries[id] = entry
	next := id + 1
	if next == 0 { // saturating_add parity
		next = 0xFFFFFFFF
	}
	if next > x.Size {
		x.Size = next
	}
}

// Merge combines xref entries; existing entries are never replaced.
func (x *Xref) Merge(other *Xref) {
	for _, id := range other.order {
		if _, ok := x.entries[id]; !ok {
			x.Insert(id, other.entries[id])
		}
	}
}

func (x *Xref) Len() int { return len(x.entries) }

// SortedIDs returns object numbers in ascending order.
func (x *Xref) SortedIDs() []uint32 { return x.order }

func (x *Xref) MaxID() uint32 {
	if len(x.order) == 0 {
		return 0
	}
	return x.order[len(x.order)-1]
}

func (x *Xref) Clear() {
	x.entries = map[uint32]XrefEntry{}
	x.order = nil
}
