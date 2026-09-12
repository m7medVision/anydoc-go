// Package structuretree implements the tagged PDF structure tree parser.
//
// Reads the /StructTreeRoot from the document catalog and builds an in-memory
// tree of StructElement nodes. Each leaf maps back to content-stream marked
// content via MCID (Marked Content ID), which lets downstream code attach
// semantic roles (heading, paragraph, table cell, list item, …) to extracted
// TextItems.
//
// Port of pdf-inspector v1.14.2 src/structure_tree.rs.
package structuretree

import (
	"bytes"
	"strings"
	"unicode/utf8"

	"github.com/m7medVision/anydoc-go/pdf/internal/objects"
	"github.com/m7medVision/anydoc-go/pdf/internal/textutils"
)

// ─── Standard structure types ────────────────────────────────────────────────

// StructRole is a standard PDF structure element type (ISO 32000-1,
// Table 333–340). Unknown custom tags keep their raw name.
//
// Upstream models this as an enum with an Other(String) variant; a string
// type gives the same equality/hash semantics with the custom name embedded.
type StructRole string

// Standard structure types.
const (
	RoleDocument   StructRole = "Document"
	RolePart       StructRole = "Part"
	RoleArt        StructRole = "Art"
	RoleSect       StructRole = "Sect"
	RoleDiv        StructRole = "Div"
	RoleBlockQuote StructRole = "BlockQuote"
	RoleCaption    StructRole = "Caption"
	RoleTOC        StructRole = "TOC"
	RoleTOCI       StructRole = "TOCI"
	RoleIndex      StructRole = "Index"
	RoleNonStruct  StructRole = "NonStruct"
	RolePrivate    StructRole = "Private"
	// Heading & paragraph
	RoleH  StructRole = "H"
	RoleH1 StructRole = "H1"
	RoleH2 StructRole = "H2"
	RoleH3 StructRole = "H3"
	RoleH4 StructRole = "H4"
	RoleH5 StructRole = "H5"
	RoleH6 StructRole = "H6"
	RoleP  StructRole = "P"
	// List
	RoleL     StructRole = "L"
	RoleLI    StructRole = "LI"
	RoleLbl   StructRole = "Lbl"
	RoleLBody StructRole = "LBody"
	// Table
	RoleTable StructRole = "Table"
	RoleTR    StructRole = "TR"
	RoleTH    StructRole = "TH"
	RoleTD    StructRole = "TD"
	RoleTHead StructRole = "THead"
	RoleTBody StructRole = "TBody"
	RoleTFoot StructRole = "TFoot"
	// Inline
	RoleSpan      StructRole = "Span"
	RoleQuote     StructRole = "Quote"
	RoleNote      StructRole = "Note"
	RoleReference StructRole = "Reference"
	RoleBibEntry  StructRole = "BibEntry"
	RoleCode      StructRole = "Code"
	RoleLink      StructRole = "Link"
	RoleAnnot     StructRole = "Annot"
	// Illustration
	RoleFigure  StructRole = "Figure"
	RoleFormula StructRole = "Formula"
	RoleForm    StructRole = "Form"
	// Ruby / Warichu (CJK)
	RoleRuby    StructRole = "Ruby"
	RoleRB      StructRole = "RB"
	RoleRT      StructRole = "RT"
	RoleRP      StructRole = "RP"
	RoleWarichu StructRole = "Warichu"
	RoleWT      StructRole = "WT"
	RoleWP      StructRole = "WP"
)

// isStandard reports whether the role is one of the standard types
// (upstream: the Other(String) variant is absent).
func (r StructRole) isStandard() bool {
	switch r {
	case RoleDocument, RolePart, RoleArt, RoleSect, RoleDiv, RoleBlockQuote,
		RoleCaption, RoleTOC, RoleTOCI, RoleIndex, RoleNonStruct, RolePrivate,
		RoleH, RoleH1, RoleH2, RoleH3, RoleH4, RoleH5, RoleH6, RoleP,
		RoleL, RoleLI, RoleLbl, RoleLBody,
		RoleTable, RoleTR, RoleTH, RoleTD, RoleTHead, RoleTBody, RoleTFoot,
		RoleSpan, RoleQuote, RoleNote, RoleReference, RoleBibEntry, RoleCode,
		RoleLink, RoleAnnot, RoleFigure, RoleFormula, RoleForm,
		RoleRuby, RoleRB, RoleRT, RoleRP, RoleWarichu, RoleWT, RoleWP:
		return true
	}
	return false
}

// IsNonHeadingContent reports whether the role belongs to content whose text
// must never be promoted to a heading by the visual heuristic. These carry an
// explicit non-heading meaning in the struct tree (lists, quotes, notes,
// references, captions, formulas, forms, ToC entries), yet their text is
// often short and visually isolated — exactly what the heuristic keys on.
// Heading roles (H, H1–H6) and generic container/flow roles (P, Div, Sect,
// Span, …) are excluded so the heuristic can still fire there.
//
// Figure is deliberately NOT in this set: cover/banner pages routinely tag
// the document title inside a Figure (alongside a seal or logo), and that
// title is a real heading. Formula and Form stay — a line explicitly tagged
// as an equation or form field is never a heading.
//
// Table roles (Table/TR/TH/TD/THead/TBody/TFoot) are included so that when
// table reconstruction falls back and cells reach the line loop as plain
// text, a short isolated cell — a TH column header especially — is not
// promoted to a heading.
func (r StructRole) IsNonHeadingContent() bool {
	switch r {
	case RoleL, RoleLI, RoleLbl, RoleLBody, RoleBlockQuote, RoleQuote,
		RoleCaption, RoleTOC, RoleTOCI, RoleIndex, RoleNote, RoleReference,
		RoleBibEntry, RoleCode, RoleFormula, RoleForm,
		RoleTable, RoleTR, RoleTH, RoleTD, RoleTHead, RoleTBody, RoleTFoot:
		return true
	}
	return false
}

// Name returns the standard structure type name for this role ("H1", "P",
// "Table", …). For custom tags the raw name is returned verbatim.
func (r StructRole) Name() string {
	return string(r)
}

// roleFromName maps a structure type name to a role; unknown names become
// custom roles carrying the name.
func roleFromName(name string) StructRole {
	return StructRole(name)
}

// roleFromNameWithRoleMap resolves a possibly-custom tag name through a role
// map, following the chain for at most 8 hops to avoid cycles.
func roleFromNameWithRoleMap(name string, roleMap map[string]string) StructRole {
	current := name
	for range 8 {
		role := roleFromName(current)
		if role.isStandard() {
			return role
		}
		if mapped, ok := roleMap[current]; ok {
			current = mapped
		} else {
			return role
		}
	}
	return StructRole(name)
}

// ─── Marked content reference ────────────────────────────────────────────────

// MarkedContentRef is a leaf reference linking a structure element to
// content-stream content.
type MarkedContentRef struct {
	// MCID is the Marked Content ID used in the content stream's BDC/BMC.
	MCID int64
	// PageID is the page ObjectId this content belongs to (from the /Pg key);
	// nil when absent.
	PageID *objects.ObjectId
}

// ─── Structure element ───────────────────────────────────────────────────────

// StructElement is a node in the PDF structure tree.
type StructElement struct {
	// Role is the semantic role (H1, P, Table, TD, …).
	Role StructRole
	// AltText is the alternative text for figures / illustrations.
	AltText string
	// ActualText is the actual text override (e.g. for ligatures).
	ActualText string
	// Lang is the language override (e.g. "en-US").
	Lang string
	// ContentRefs are the direct marked-content references (leaf content).
	ContentRefs []MarkedContentRef
	// Children are the child structure elements.
	Children []StructElement
}

// ─── Structure tree (top level) ──────────────────────────────────────────────

// StructTree is a parsed PDF structure tree.
//
// Built from /StructTreeRoot in the document catalog via FromDoc, then
// McidToRoles produces per-page MCID → role lookup tables.
type StructTree struct {
	// Children are the root's top-level structure elements.
	Children []StructElement
}

// FromDoc attempts to parse the structure tree from a PDF document.
//
// Reports false if the PDF is not tagged (no /StructTreeRoot).
func FromDoc(doc *objects.Document) (*StructTree, bool) {
	catalog, err := doc.Catalog()
	if err != nil {
		return nil, false
	}
	structRootObj, err := catalog.Get([]byte("StructTreeRoot"))
	if err != nil {
		return nil, false
	}
	structRoot, ok := resolveDict(doc, structRootObj)
	if !ok {
		return nil, false
	}

	// Parse role map: custom tag → standard tag
	roleMap := parseRoleMap(doc, structRoot)

	// Seed the cycle guard with the struct-root's own object id so a /K that
	// points back at the root is treated as a cycle, and bound total node
	// materialization with a global budget.
	walk := newStructWalk()
	if rootID, err := structRootObj.AsReference(); err == nil {
		walk.active[rootID] = struct{}{}
	}

	// Parse child elements from /K
	children := parseKids(doc, structRoot, roleMap, nil, 0, walk)

	if walk.truncated {
		// Upstream logs a one-shot warning here; truncation still yields the
		// partial tree.
		_ = walk.truncated
	}

	if len(children) == 0 {
		return nil, false
	}

	return &StructTree{Children: children}, true
}

// McidToRoles builds the per-page MCID → StructRole lookup.
//
// Returns a map: page number (1-indexed) → (MCID → StructRole). The pages
// table should come from doc.GetPages().
func (t *StructTree) McidToRoles(pages *objects.PageTable) map[uint32]map[int64]StructRole {
	// Invert: ObjectId → page number. Ascending iteration means the highest
	// page number wins for a duplicated object id, matching the upstream
	// BTreeMap → HashMap collect.
	objToPage := map[objects.ObjectId]uint32{}
	for _, num := range pages.Numbers() {
		if id, ok := pages.Get(num); ok {
			objToPage[id] = num
		}
	}

	result := map[uint32]map[int64]StructRole{}
	collectMcidRoles(t.Children, objToPage, result)
	return result
}

func collectMcidRoles(elements []StructElement, objToPage map[objects.ObjectId]uint32, result map[uint32]map[int64]StructRole) {
	for i := range elements {
		elem := &elements[i]
		for _, mcref := range elem.ContentRefs {
			if mcref.PageID != nil {
				if pageNum, ok := objToPage[*mcref.PageID]; ok {
					if result[pageNum] == nil {
						result[pageNum] = map[int64]StructRole{}
					}
					result[pageNum][mcref.MCID] = elem.Role
				}
			}
		}
		collectMcidRoles(elem.Children, objToPage, result)
	}
}

// McidCount counts the total marked-content references across the tree.
func (t *StructTree) McidCount() int {
	var count func(elements []StructElement) int
	count = func(elements []StructElement) int {
		total := 0
		for i := range elements {
			total += len(elements[i].ContentRefs) + count(elements[i].Children)
		}
		return total
	}
	return count(t.Children)
}

// Flatten builds a flat list of structure elements with their roles and
// MCIDs, preserving document order. Useful for structure-aware markdown
// generation.
func (t *StructTree) Flatten() []FlatStructElement {
	out := []FlatStructElement{}
	flattenRecursive(t.Children, &out, 0)
	return out
}

// ExtractTables extracts table structures from the tagged PDF tree.
//
// Walks the tree to find /Table elements with /TR > /TD|TH children,
// collecting MCIDs at each cell. Returns structured descriptors that can be
// matched against extracted TextItems to build tables without relying on
// geometry-based detection.
func (t *StructTree) ExtractTables(pages *objects.PageTable) []StructTable {
	objToPage := map[objects.ObjectId]uint32{}
	for _, num := range pages.Numbers() {
		if id, ok := pages.Get(num); ok {
			objToPage[id] = num
		}
	}
	tables := []StructTable{}
	collectTables(t.Children, objToPage, &tables)
	return tables
}

// ─── Tagged table structures ────────────────────────────────────────────────

// StructTableCell is a table cell extracted from the structure tree.
type StructTableCell struct {
	// IsHeader reports whether this cell is a header cell (/TH).
	IsHeader bool
	// MCIDs holds the cell's MCIDs with their resolved page numbers.
	MCIDs []CellMCID
}

// CellMCID pairs an MCID with its resolved page number.
type CellMCID struct {
	MCID int64
	Page uint32
}

// StructTableRow is a table row extracted from the structure tree.
type StructTableRow struct {
	Cells []StructTableCell
}

// StructTable is a complete table extracted from the structure tree.
type StructTable struct {
	Rows []StructTableRow
}

func collectTables(elements []StructElement, objToPage map[objects.ObjectId]uint32, tables *[]StructTable) {
	for i := range elements {
		elem := &elements[i]
		if elem.Role == RoleTable {
			rows := []StructTableRow{}
			collectRows(elem.Children, objToPage, &rows)
			anyCells := false
			for j := range rows {
				if len(rows[j].Cells) > 0 {
					anyCells = true
					break
				}
			}
			if len(rows) >= 2 && anyCells {
				*tables = append(*tables, StructTable{Rows: rows})
			}
		} else {
			collectTables(elem.Children, objToPage, tables)
		}
	}
}

// collectRows collects rows from Table children, transparently descending
// through THead/TBody/TFoot grouping elements.
func collectRows(elements []StructElement, objToPage map[objects.ObjectId]uint32, rows *[]StructTableRow) {
	for i := range elements {
		elem := &elements[i]
		switch elem.Role {
		case RoleTR:
			cells := []StructTableCell{}
			for j := range elem.Children {
				child := &elem.Children[j]
				if child.Role == RoleTD || child.Role == RoleTH {
					isHeader := child.Role == RoleTH
					mcids := []CellMCID{}
					collectMcidsRecursive(child, objToPage, &mcids)
					cells = append(cells, StructTableCell{IsHeader: isHeader, MCIDs: mcids})
				}
			}
			*rows = append(*rows, StructTableRow{Cells: cells})
		case RoleTHead, RoleTBody, RoleTFoot:
			collectRows(elem.Children, objToPage, rows)
		}
	}
}

// collectMcidsRecursive recursively collects all MCIDs from an element and
// its descendants.
func collectMcidsRecursive(elem *StructElement, objToPage map[objects.ObjectId]uint32, mcids *[]CellMCID) {
	for _, mcref := range elem.ContentRefs {
		if mcref.PageID != nil {
			if pageNum, ok := objToPage[*mcref.PageID]; ok {
				*mcids = append(*mcids, CellMCID{MCID: mcref.MCID, Page: pageNum})
			}
		}
	}
	for i := range elem.Children {
		collectMcidsRecursive(&elem.Children[i], objToPage, mcids)
	}
}

// FlatStructElement is a flattened view of a structure element for linear
// traversal.
type FlatStructElement struct {
	// Role is the semantic role.
	Role StructRole
	// Depth is the nesting depth (0 = top-level).
	Depth int
	// AltText is the alt text (figures).
	AltText string
	// ContentRefs are the direct MCIDs with page ObjectIds.
	ContentRefs []MarkedContentRef
	// ChildCount is the number of child elements (in the original tree).
	ChildCount int
}

func flattenRecursive(elements []StructElement, out *[]FlatStructElement, depth int) {
	for i := range elements {
		elem := &elements[i]
		*out = append(*out, FlatStructElement{
			Role:        elem.Role,
			Depth:       depth,
			AltText:     elem.AltText,
			ContentRefs: elem.ContentRefs,
			ChildCount:  len(elem.Children),
		})
		flattenRecursive(elem.Children, out, depth+1)
	}
}

// ─── Parsing helpers ─────────────────────────────────────────────────────────

// parseRoleMap parses the /RoleMap dictionary (custom tag → standard tag).
func parseRoleMap(doc *objects.Document, structRoot *objects.Dictionary) map[string]string {
	m := map[string]string{}
	rmObj, err := structRoot.Get([]byte("RoleMap"))
	if err != nil {
		return m
	}
	rmDict, ok := resolveDict(doc, rmObj)
	if !ok {
		return m
	}
	rmDict.Range(func(key []byte, val *objects.Object) bool {
		keyStr := lossyString(key)
		if name, err := val.AsName(); err == nil {
			m[keyStr] = lossyString(name)
		}
		return true
	})
	return m
}

// maxDepth is the max recursion depth for structure tree parsing (prevents
// stack overflow on malformed PDFs).
const maxDepth = 64

// maxStructNodes is the global cap on the number of structure-tree nodes
// materialized in a single parse. Real tagged trees are far smaller; a
// crafted PDF can alias one struct element into its own /K (e.g.
// /K [n 0 R n 0 R]) so the tree branches exponentially (2^depth) before the
// depth cap is reached, exhausting memory. This budget bounds total work and
// allocation regardless of tree shape.
const maxStructNodes = 500_000

// maxStructWork is the cap on the number of /K items *examined* during a
// single parse, regardless of whether they materialize anything. Bounds CPU
// for crafted wide /K arrays of non-materializing entries (unsupported value
// types, /OBJR dicts, cycle back-edges) that would otherwise be scanned in
// full without ever touching the node budget. Kept well above the node
// budget so it never truncates content that already fits within
// maxStructNodes.
const maxStructWork = 2_000_000

// structWalk is the traversal state shared across the recursive
// structure-tree parse.
//
// budget is a global allowance charged once per materialized item — each
// struct-element node and each marked-content reference — so total work is
// bounded even for aliased/DAG-shaped /K graphs of distinct objects or a
// single element with a very wide /K array. active holds the object IDs
// currently on the depth-first path so a struct element that references
// itself (or an ancestor) is not expanded into an unbounded/exponential
// subtree. budget bounds *materialization* (nodes + content refs). work
// separately bounds *traversal* — every /K item examined is charged against
// it, even ones that materialize nothing (unsupported values, /OBJR, cycle
// back-edges) — so a wide malformed array cannot force an unbounded scan,
// and those skipped items don't drain the materialization budget and
// truncate real content. truncated records whether any parse work was
// skipped — the budget was exhausted, a /K reference cycle was broken, or
// the depth cap was hit — so the caller can log it once rather than per
// skipped item. stalled is set when an atomic multi-unit reservation could
// not fit in the remaining budget; it makes exhausted report done so a wide
// /K array is not scanned to the end once no further leaf can be
// materialized.
type structWalk struct {
	budget    int
	work      int
	active    map[objects.ObjectId]struct{}
	truncated bool
	stalled   bool
}

func newStructWalk() *structWalk {
	return &structWalk{
		budget: maxStructNodes,
		work:   maxStructWork,
		active: map[objects.ObjectId]struct{}{},
	}
}

// spendWork charges one unit of traversal work for an examined /K item,
// whether or not it materializes anything. Returns false (flagging
// truncation) once the traversal budget is spent, so an enclosing loop stops
// instead of scanning the rest of a wide array of non-materializing entries.
func (w *structWalk) spendWork() bool {
	if w.work == 0 {
		w.truncated = true
		return false
	}
	w.work--
	return true
}

// noteSkipped records that some parse work was skipped for a non-budget
// reason (a /K reference cycle or the depth cap), so the one-shot truncation
// warning also covers malformed/over-deep trees, not just budget exhaustion.
func (w *structWalk) noteSkipped() {
	w.truncated = true
}

// charge charges one unit against the budget for a materialized item (a
// struct element node or a marked-content reference). Returns false —
// without underflowing — once the budget is exhausted, so callers skip the
// item.
func (w *structWalk) charge() bool {
	if w.budget == 0 {
		w.truncated = true
		return false
	}
	w.budget--
	return true
}

// chargeN atomically charges n units for a single item that materializes
// several budget-counted parts at once (a leaf wrapper node *plus* its
// content reference). Charges nothing when fewer than n units remain — so a
// partial reservation never wastes capacity — and marks the walk stalled so
// the enclosing loop stops instead of scanning the rest of a wide /K array
// that can no longer fit any leaf.
func (w *structWalk) chargeN(n int) bool {
	if w.budget < n {
		w.truncated = true
		w.stalled = true
		return false
	}
	w.budget -= n
	return true
}

// exhausted reports whether traversal should stop: the budget is spent, or a
// multi-unit reservation could not fit (stalled) so no further leaf will
// materialize. Use this at the guards that break/return to skip remaining
// items; it records that truncation occurred (a guard only fires while an
// item is still pending), so callers that drop work without going through
// charge still flag the truncation for logging.
func (w *structWalk) exhausted() bool {
	if w.budget == 0 || w.stalled {
		w.truncated = true
		return true
	}
	return false
}

// parseKids parses child elements from a /K entry.
func parseKids(doc *objects.Document, dict *objects.Dictionary, roleMap map[string]string, inheritedPage *objects.ObjectId, depth int, walk *structWalk) []StructElement {
	if depth >= maxDepth {
		walk.noteSkipped()
		return nil
	}
	if walk.exhausted() {
		return nil
	}

	kObj, err := dict.Get([]byte("K"))
	if err != nil {
		return nil
	}

	// /Pg on this element (inherited by children)
	pageID := getPageRoute(doc, dict)
	if pageID == nil {
		pageID = inheritedPage
	}

	children := []StructElement{}
	if kObj.Kind == objects.KindArray {
		for i := range kObj.Array {
			if walk.exhausted() || !walk.spendWork() {
				break
			}
			processKidItem(doc, &kObj.Array[i], roleMap, pageID, depth, &children, walk)
		}
	} else {
		processKidItem(doc, kObj, roleMap, pageID, depth, &children, walk)
	}
	return children
}

// processKidItem resolves one /K array item (following at most one level of
// indirection), guarding against reference cycles and the global node
// budget, then dispatches it via parseKid.
func processKidItem(doc *objects.Document, item *objects.Object, roleMap map[string]string, inheritedPage *objects.ObjectId, depth int, out *[]StructElement, walk *structWalk) {
	if walk.exhausted() {
		return
	}
	if depth >= maxDepth {
		walk.noteSkipped()
		return
	}
	// If this child is an indirect reference, track its id on the active
	// path so a self/ancestor reference is not expanded into an exponential
	// subtree.
	var refID *objects.ObjectId
	if item.Kind == objects.KindReference {
		id := item.Ref
		refID = &id
		if _, onPath := walk.active[id]; onPath {
			walk.noteSkipped()
			return // cycle: this object is already on the current path
		}
		walk.active[id] = struct{}{}
	}
	resolved := resolveObj(doc, item)
	parseKid(doc, resolved, roleMap, inheritedPage, depth, out, walk)
	if refID != nil {
		delete(walk.active, *refID)
	}
}

// parseKid parses a single child (either a struct element dict or an MCID
// integer).
func parseKid(doc *objects.Document, obj *objects.Object, roleMap map[string]string, inheritedPage *objects.ObjectId, depth int, out *[]StructElement, walk *structWalk) {
	switch obj.Kind {
	case objects.KindInteger:
		// Direct MCID integer — create a leaf wrapper.
		// A wrapper node plus its content reference — two items — reserved
		// atomically so we never consume one unit without emitting both.
		if !walk.chargeN(2) {
			return
		}
		// A bare MCID inside /K is a content ref for the parent; represent
		// it as a minimal Span wrapper.
		*out = append(*out, StructElement{
			Role: RoleSpan,
			ContentRefs: []MarkedContentRef{{
				MCID:   obj.Int,
				PageID: inheritedPage,
			}},
		})
	case objects.KindDictionary:
		parseStructElementDict(doc, obj.Dict, roleMap, inheritedPage, depth, out, walk)
	case objects.KindStream:
		// Some PDFs wrap struct elements in streams (rare)
		parseStructElementDict(doc, &obj.Stream.Dict, roleMap, inheritedPage, depth, out, walk)
	}
}

// parseStructElementDict parses a dictionary that could be either a struct
// element or a marked-content reference (MCR) dictionary.
func parseStructElementDict(doc *objects.Document, dict *objects.Dictionary, roleMap map[string]string, inheritedPage *objects.ObjectId, depth int, out *[]StructElement, walk *structWalk) {
	if depth >= maxDepth {
		walk.noteSkipped()
		return
	}

	// A marked-content reference dict materializes a wrapper node + one
	// content reference (two items). Reserve both atomically *before* the
	// node charge so we never consume a unit without emitting the reference —
	// which would also deny that unit to a later element that would have fit.
	// This matches the bare-MCID path.
	if isMcrDict(dict) {
		if mcidObj, err := dict.Get([]byte("MCID")); err == nil && mcidObj.Kind == objects.KindInteger {
			if !walk.chargeN(2) {
				return
			}
			pageID := getPageRoute(doc, dict)
			if pageID == nil {
				pageID = inheritedPage
			}
			*out = append(*out, StructElement{
				Role: RoleSpan,
				ContentRefs: []MarkedContentRef{{
					MCID:   mcidObj.Int,
					PageID: pageID,
				}},
			})
		}
		return
	}

	// Skip object-reference dicts (/Type /OBJR) — they materialize no node,
	// so recognize and return *before* charging the budget (otherwise a
	// document full of OBJRs would drain the shared budget and truncate real
	// content).
	if isObjrDict(dict) {
		return
	}

	// It's a struct element — parse its /S (structure type). A dict without
	// a valid /S also materializes nothing, so validate before charging.
	sObj, err := dict.Get([]byte("S"))
	if err != nil {
		return
	}
	resolvedS := resolveObj(doc, sObj)
	roleName, err := resolvedS.AsName()
	if err != nil {
		return
	}

	// Charge the node only now that we know it will materialize (bounds
	// aliased/DAG-shaped /K graphs the per-path cycle guard alone cannot
	// stop).
	if !walk.charge() {
		return
	}

	role := roleFromNameWithRoleMap(lossyString(roleName), roleMap)
	pageID := getPageRoute(doc, dict)
	if pageID == nil {
		pageID = inheritedPage
	}

	// Extract optional attributes
	altText, hasAlt := getTextString(dict, []byte("Alt"))
	if !hasAlt {
		altText = ""
	}
	actualText, hasActual := getTextString(dict, []byte("ActualText"))
	if !hasActual {
		actualText = ""
	}
	lang, hasLang := getTextString(dict, []byte("Lang"))
	if !hasLang {
		lang = ""
	}

	// Parse children from /K
	var contentRefs []MarkedContentRef
	var children []StructElement

	if kObj, err := dict.Get([]byte("K")); err == nil {
		kResolved := resolveObj(doc, kObj)
		switch kResolved.Kind {
		case objects.KindInteger:
			if walk.charge() {
				contentRefs = append(contentRefs, MarkedContentRef{
					MCID:   kResolved.Int,
					PageID: pageID,
				})
			}
		case objects.KindArray:
			for i := range kResolved.Array {
				if walk.exhausted() || !walk.spendWork() {
					break
				}
				// Only content-ref items (bare MCIDs / MCR dicts) are charged
				// here — those are the unbounded allocations. Structural
				// children are charged once at their own node entry in the
				// recursive call, so charging them here too would
				// double-count and drain the budget ~2× faster than the
				// per-node semantics.
				var refID *objects.ObjectId
				if kResolved.Array[i].Kind == objects.KindReference {
					id := kResolved.Array[i].Ref
					refID = &id
				}
				resolved := resolveObj(doc, &kResolved.Array[i])
				switch resolved.Kind {
				case objects.KindInteger:
					if walk.charge() {
						contentRefs = append(contentRefs, MarkedContentRef{
							MCID:   resolved.Int,
							PageID: pageID,
						})
					}
				case objects.KindDictionary:
					if isMcrDict(resolved.Dict) {
						if mcidObj, err := resolved.Dict.Get([]byte("MCID")); err == nil && mcidObj.Kind == objects.KindInteger {
							if walk.charge() {
								pg := getPageRoute(doc, resolved.Dict)
								if pg == nil {
									pg = pageID
								}
								contentRefs = append(contentRefs, MarkedContentRef{
									MCID:   mcidObj.Int,
									PageID: pg,
								})
							}
						}
					} else if isObjrDict(resolved.Dict) {
						// Skip object references
					} else {
						recurseStructChild(doc, refID, resolved.Dict, roleMap, pageID, depth, &children, walk)
					}
				case objects.KindStream:
					recurseStructChild(doc, refID, &resolved.Stream.Dict, roleMap, pageID, depth, &children, walk)
				}
			}
		case objects.KindDictionary:
			if isMcrDict(kResolved.Dict) {
				if mcidObj, err := kResolved.Dict.Get([]byte("MCID")); err == nil && mcidObj.Kind == objects.KindInteger {
					if walk.charge() {
						pg := getPageRoute(doc, kResolved.Dict)
						if pg == nil {
							pg = pageID
						}
						contentRefs = append(contentRefs, MarkedContentRef{
							MCID:   mcidObj.Int,
							PageID: pg,
						})
					}
				}
			} else {
				var refID *objects.ObjectId
				if kObj.Kind == objects.KindReference {
					id := kObj.Ref
					refID = &id
				}
				recurseStructChild(doc, refID, kResolved.Dict, roleMap, pageID, depth, &children, walk)
			}
		}
	}

	*out = append(*out, StructElement{
		Role:        role,
		AltText:     altText,
		ActualText:  actualText,
		Lang:        lang,
		ContentRefs: contentRefs,
		Children:    children,
	})
}

// recurseStructChild recurses into a child struct-element dictionary,
// guarding against reference cycles (via the active-path object-id set) and
// the global node budget.
//
// refID is the object id of the child when it was reached through an
// indirect reference (nil for an inline dictionary, which cannot alias).
func recurseStructChild(doc *objects.Document, refID *objects.ObjectId, dict *objects.Dictionary, roleMap map[string]string, inheritedPage *objects.ObjectId, depth int, out *[]StructElement, walk *structWalk) {
	if walk.exhausted() {
		return
	}
	if refID != nil {
		if _, onPath := walk.active[*refID]; onPath {
			walk.noteSkipped()
			return // cycle: this object is already on the current path
		}
		walk.active[*refID] = struct{}{}
	}
	parseStructElementDict(doc, dict, roleMap, inheritedPage, depth+1, out, walk)
	if refID != nil {
		delete(walk.active, *refID)
	}
}

// isMcrDict checks if dict has /Type /MCR.
func isMcrDict(dict *objects.Dictionary) bool {
	return typeNameIs(dict, "MCR")
}

// isObjrDict checks if dict has /Type /OBJR.
func isObjrDict(dict *objects.Dictionary) bool {
	return typeNameIs(dict, "OBJR")
}

func typeNameIs(dict *objects.Dictionary, want string) bool {
	tObj, err := dict.Get([]byte("Type"))
	if err != nil {
		return false
	}
	name, err := tObj.AsName()
	if err != nil {
		return false
	}
	return string(name) == want
}

// getPageRoute gets the /Pg page reference from a dictionary.
func getPageRoute(doc *objects.Document, dict *objects.Dictionary) *objects.ObjectId {
	pg, err := dict.Get([]byte("Pg"))
	if err != nil {
		return nil
	}
	if pg.Kind == objects.KindReference {
		id := pg.Ref
		return &id
	}
	resolved := resolveObj(doc, pg)
	if resolved.Kind == objects.KindReference {
		id := resolved.Ref
		return &id
	}
	return nil
}

// getTextString extracts a text string from a dictionary key (handles PDF
// text encoding).
func getTextString(dict *objects.Dictionary, key []byte) (string, bool) {
	obj, err := dict.Get(key)
	if err != nil || obj.Kind != objects.KindString {
		return "", false
	}
	return textutils.DecodeTextString(obj.Str), true
}

// resolveObj resolves an Object reference, returning the target object (or
// the object itself when it is not a reference or cannot be resolved).
func resolveObj(doc *objects.Document, obj *objects.Object) *objects.Object {
	if obj.Kind == objects.KindReference {
		if target, err := doc.GetObject(obj.Ref); err == nil {
			return target
		}
	}
	return obj
}

// resolveDict resolves an Object to a dictionary (handling references).
func resolveDict(doc *objects.Document, obj *objects.Object) (*objects.Dictionary, bool) {
	switch obj.Kind {
	case objects.KindDictionary:
		return obj.Dict, true
	case objects.KindReference:
		d, err := doc.GetDictionary(obj.Ref)
		if err != nil {
			return nil, false
		}
		return d, true
	}
	return nil, false
}

// lossyString mirrors String::from_utf8_lossy for PDF name/key bytes.
func lossyString(b []byte) string {
	return lossyUTF8(b)
}

// lossyUTF8 mirrors Rust's String::from_utf8_lossy: each maximal invalid
// subpart of the input becomes one U+FFFD.
func lossyUTF8(data []byte) string {
	if utf8.Valid(data) {
		return string(data)
	}
	var b strings.Builder
	b.Grow(len(data))
	i := 0
	for i < len(data) {
		r, size := utf8.DecodeRune(data[i:])
		if r != utf8.RuneError || size != 1 {
			b.WriteRune(r)
			i += size
			continue
		}
		c := data[i]
		var need int
		switch {
		case c&0xE0 == 0xC0:
			need = 2
		case c&0xF0 == 0xE0:
			need = 3
		case c&0xF8 == 0xF0:
			need = 4
		default:
			b.WriteRune(0xFFFD)
			i++
			continue
		}
		j := i + 1
		for j < len(data) && j < i+need && data[j]&0xC0 == 0x80 {
			j++
		}
		b.WriteRune(0xFFFD)
		i = j
	}
	return b.String()
}

// FixBareStructNames fixes malformed structure element /S entries in raw PDF
// bytes.
//
// Some PDF generators (notably fpdf2) write bare names like `/S Code` instead
// of the correct `/S /Code`. The object parser cannot parse dictionaries
// containing bare tokens, so the entire object is silently dropped.
//
// This scans for the pattern `/S <bare_word>` inside struct element
// dictionaries and prepends `/` to make them valid PDF names. Returns the
// input slice unchanged when no fixes were needed.
//
// Upstream: pub fn fix_bare_struct_names(buf: &[u8]) -> Cow<[u8]>.
func FixBareStructNames(buf []byte) []byte {
	if !bytes.Contains(buf, []byte("/StructTreeRoot")) {
		return buf
	}

	pattern := []byte("/S ")
	var result []byte
	pos := 0

	for pos+len(pattern) < len(buf) {
		idxRel := bytes.Index(buf[pos:], pattern)
		if idxRel < 0 {
			break
		}
		idx := idxRel + pos
		after := idx + len(pattern)
		if after < len(buf) && buf[after] == '/' {
			pos = after
			continue
		}

		matched := false
		for _, name := range knownStructNames {
			end := after + len(name)
			if end <= len(buf) && bytes.Equal(buf[after:end], name) &&
				(end >= len(buf) || buf[end] == '\n' || buf[end] == '\r' || buf[end] == ' ' || buf[end] == '/' || buf[end] == '>') {
				if result == nil {
					result = append([]byte(nil), buf[:after]...)
				}
				if len(result) < after {
					result = append(result, buf[len(result):after]...)
				}
				result = append(result, '/')
				result = append(result, name...)
				pos = end
				matched = true
				break
			}
		}
		if !matched {
			pos = after
		}
	}

	if result == nil {
		return buf
	}
	if len(result) < len(buf) {
		result = append(result, buf[len(result):]...)
	}
	return result
}
