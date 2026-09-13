// Port of src/shared/list.rs: nested-list assembly with numbering identity.
//
// Frontends resolve each list paragraph to a ListEntry carrying its indent
// level, its list identity (ListKey), and its computed effective number.
// Assembly splits runs whenever the identity or marker kind changes at a
// level, or an ordered sequence is non-contiguous (a restart), so the
// renderer's start+index numbering reproduces the source exactly.

package shared

import "github.com/m7medVision/anydoc-go/internal/model"

// ListKey is the identity of a resolved list at one level: which list
// instance the entry belongs to and what marker its level uses.
type ListKey struct {
	// Instance is stable per-instance (DOCX numId, RTF \lsN, DOC list
	// identity lsid, a counter for HTML/ODF lists).
	Instance uint64
	Marker   model.MarkerKind
}

// ListEntry is one flat, fully resolved list paragraph (plus any blocks
// attached to the same item, like text-box content anchored in it).
type ListEntry struct {
	Level int
	Key   ListKey
	// Number is the effective item number at this entry (ignored for bullets).
	Number uint64
	// Label is literal marker text when the source number text is not
	// reproducible from the marker kind and number alone (composite number
	// text). Empty when the computed marker applies.
	Label  string
	Blocks []model.Block
}

// FlushList pops the accumulated run of list paragraphs into list blocks;
// one block per identity segment.
func FlushList(blocks *[]model.Block, run *[]ListEntry) {
	entries := *run
	*run = nil
	if len(entries) == 0 {
		return
	}
	*blocks = append(*blocks, buildLists(entries)...)
}

// Fold a flat run into nested lists, splitting at identity/marker changes
// and ordered-sequence discontinuities.
func buildLists(entries []ListEntry) []model.Block {
	if len(entries) == 0 {
		return nil
	}
	minLvl := entries[0].Level
	for _, e := range entries {
		if e.Level < minLvl {
			minLvl = e.Level
		}
	}

	type currentList struct {
		list   model.List
		key    ListKey
		last   uint64
		active bool
	}
	var cur currentList
	var out []model.Block

	flush := func() {
		if cur.active && len(cur.list.Items) > 0 {
			out = append(out, model.ListBlock{List: cur.list})
		}
		cur = currentList{}
	}

	i := 0
	for i < len(entries) {
		if entries[i].Level <= minLvl {
			entry := entries[i]
			i++
			split := true
			if cur.active {
				split = cur.key != entry.Key || orderedDiscontiguous(entry.Key.Marker, cur.last, entry.Number)
			}
			if split {
				flush()
				start := uint64(1)
				if entry.Key.Marker.Ordered() {
					start = entry.Number
				}
				cur = currentList{
					list:   model.List{Marker: entry.Key.Marker, Start: start},
					key:    entry.Key,
					last:   entry.Number,
					active: true,
				}
			}
			cur.list.Items = append(cur.list.Items, model.ListItem{Blocks: entry.Blocks, MarkerLabel: entry.Label})
			cur.last = entry.Number
			continue
		}
		subStart := i
		for i < len(entries) && entries[i].Level > minLvl {
			i++
		}
		sublists := buildLists(entries[subStart:i])
		if len(sublists) == 0 {
			continue
		}
		if !cur.active {
			// Sub-level content with no parent item yet: host it in an
			// anonymous item so nesting is preserved.
			cur = currentList{
				list:   model.List{Marker: model.MarkerBullet, Start: 1},
				key:    ListKey{Instance: ^uint64(0), Marker: model.MarkerBullet},
				last:   0,
				active: true,
			}
		}
		if len(cur.list.Items) == 0 {
			cur.list.Items = append(cur.list.Items, model.ListItem{})
		}
		last := &cur.list.Items[len(cur.list.Items)-1]
		last.Blocks = append(last.Blocks, sublists...)
	}
	flush()
	return out
}

func orderedDiscontiguous(marker model.MarkerKind, last, number uint64) bool {
	if !marker.Ordered() {
		return false
	}
	if last == ^uint64(0) {
		return true
	}
	return last+1 != number
}
