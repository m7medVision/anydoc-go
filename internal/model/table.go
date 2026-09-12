package model

import (
	"sort"

	"github.com/m7medVision/anydoc-go/internal/cerr"
)

// maxExpansion bounds the content-bearing cells span expansion may produce
// per table. Upstream this is package::limits::MAX_EXPANSION; the limits
// package is delivered by its own ticket, so the model pins the same
// documented default here.
const maxExpansion = 4_000_000

// Table is the canonical table grid. Invariant: every logical grid position
// appears exactly once - content and spans exist only on the origin slot,
// and each position covered by a span holds a CoveredCell marker pointing
// back at its origin. Frontends construct grids through one internal
// builder that enforces this, so a Table handed to a consumer always holds.
type Table struct {
	// Grid holds the rows of slots. Rows may differ in length when the
	// source is ragged.
	Grid [][]CellSlot
	// HeaderRows is the number of leading rows that are header rows
	// (0 = no header).
	HeaderRows int
	// Kind is what the source used this table for.
	Kind TableKind
}

// TableKind is what a table is for.
type TableKind int

const (
	// TableData: a real data table.
	TableData TableKind = iota
	// TableLayout: layout scaffolding (text boxes, positioning tables);
	// renderers may unwrap trivial layout tables.
	TableLayout
)

// CellSlot is one position in a Table.Grid: either a cell or the shadow of
// one. A sealed sum mirroring anydoc's CellSlot enum.
type CellSlot interface {
	isCellSlot()
}

// OriginCell is the cell itself, holding the content and the span extents.
type OriginCell struct {
	Cell Cell
}

// CoveredCell is a position swallowed by a span, pointing back at the
// origin that covers it.
type CoveredCell struct {
	// OriginRow is the row of the covering origin.
	OriginRow int
	// OriginCol is the column of the covering origin.
	OriginCol int
}

func (OriginCell) isCellSlot()  {}
func (CoveredCell) isCellSlot() {}

// Cell is a table cell and the extent it spans.
type Cell struct {
	// Blocks is the cell's content.
	Blocks []Block
	// ColSpan is the columns covered, at least 1.
	ColSpan uint32
	// RowSpan is the rows covered, at least 1.
	RowSpan uint32
}

// NewCell builds a cell spanning one position.
func NewCell(blocks []Block) Cell {
	return Cell{Blocks: blocks, ColSpan: 1, RowSpan: 1}
}

// CellFromInlines builds a one-paragraph cell spanning one position.
func CellFromInlines(inlines []Inline) Cell {
	return NewCell([]Block{Paragraph{Inlines: inlines}})
}

// SpanningCell builds a cell covering colSpan by rowSpan positions; either
// span given as 0 is raised to 1.
func SpanningCell(blocks []Block, colSpan, rowSpan uint32) Cell {
	if colSpan < 1 {
		colSpan = 1
	}
	if rowSpan < 1 {
		rowSpan = 1
	}
	return Cell{Blocks: blocks, ColSpan: colSpan, RowSpan: rowSpan}
}

// IsEmpty reports whether the cell holds nothing that would render: only
// paragraphs count toward emptiness, so a cell with a table or list in it
// is not empty even if that content is blank.
func (c Cell) IsEmpty() bool {
	for _, block := range c.Blocks {
		if paragraph, ok := block.(Paragraph); ok {
			if !InlinesAreEmpty(paragraph.Inlines) {
				return false
			}
		} else {
			return false
		}
	}
	return true
}

// TableFromRows builds a plain span-less table from rows of cells
// (spreadsheets, CSV).
func TableFromRows(rows [][]Cell, headerRows int, kind TableKind) Table {
	b := NewGridBuilder()
	for _, row := range rows {
		b.NextRow()
		for _, cell := range row {
			cell.ColSpan = 1
			cell.RowSpan = 1
			// Span-less placement never charges the expansion budget.
			if err := b.Place(cell); err != nil {
				panic("span-less placement cannot exceed the expansion budget")
			}
		}
	}
	table := b.Finish(kind)
	table.HeaderRows = headerRows
	return table
}

// IsSingleCell reports whether the table is a single origin cell (any
// covered padding aside).
func (t Table) IsSingleCell() bool {
	return len(t.Grid) == 1 && len(t.Grid[0]) == 1 && isOrigin(t.Grid[0][0])
}

func isOrigin(slot CellSlot) bool {
	_, ok := slot.(OriginCell)
	return ok
}

// GridBuilder is the sole constructor for Table grids. It enforces the
// exactly-once invariant: spans register their covered positions, later
// placements skip over them, and overlapping spans are clamped rather than
// double-counted.
type GridBuilder struct {
	grid [][]CellSlot
	// pending maps positions in future rows covered by an earlier
	// row-spanning origin, as [row, col] -> [originRow, originCol].
	pending map[[2]int][2]int
	// expansion counts covered positions claimed by span expansion,
	// charged against maxExpansion *before* any per-position work so a
	// tiny document carrying a huge span cannot force unbounded insertions.
	expansion uint64
	// keepCoveredTail: whether trailing rows holding only covered positions
	// survive Finish; see KeepCoveredTail.
	keepCoveredTail bool
}

// NewGridBuilder creates an empty builder.
func NewGridBuilder() *GridBuilder {
	return &GridBuilder{pending: make(map[[2]int][2]int)}
}

// NextRow starts a new row.
func (b *GridBuilder) NextRow() {
	b.grid = append(b.grid, nil)
}

// KeepCoveredTail treats covered positions as content when trimming
// trailing rows. A spreadsheet merge region is real extent even where
// every covered cell is empty; other sources treat such rows as filler and
// keep the default trim.
func (b *GridBuilder) KeepCoveredTail() {
	b.keepCoveredTail = true
}

func (b *GridBuilder) rowIndex() int {
	if len(b.grid) == 0 {
		b.grid = append(b.grid, nil)
	}
	return len(b.grid) - 1
}

// skipPending materializes pending covered positions at the cursor, so the
// next placement lands on the first genuinely free column.
func (b *GridBuilder) skipPending(row int) {
	for {
		pos := [2]int{row, len(b.grid[row])}
		origin, ok := b.pending[pos]
		if !ok {
			return
		}
		delete(b.pending, pos)
		b.grid[row] = append(b.grid[row], CoveredCell{OriginRow: origin[0], OriginCol: origin[1]})
	}
}

// Place places a cell at the next free position of the current row. The
// cell's spans register covered positions as *pending*: an explicitly
// written covered cell (ODF) consumes one via Covered, while producers
// that omit them (HTML, OOXML) have the positions materialized
// automatically when the next cell is placed.
//
// The complete span area is charged against the expansion budget before
// any expansion happens; ResourceLimit when the budget is exceeded.
func (b *GridBuilder) Place(cell Cell) error {
	colSpan := uint64(atLeastOne(cell.ColSpan))
	rowSpan := uint64(atLeastOne(cell.RowSpan))
	growth := colSpan*rowSpan - 1
	if growth > ^uint64(0)-b.expansion {
		b.expansion = ^uint64(0) // saturating add
	} else {
		b.expansion += growth
	}
	if b.expansion > maxExpansion {
		return &cerr.Error{
			Kind:   cerr.KindResourceLimit,
			Limit:  "max_expansion",
			Detail: "table span expansion exceeds the content budget",
		}
	}
	row := b.rowIndex()
	b.skipPending(row)
	col := len(b.grid[row])
	cs := int(colSpan)
	rs := int(rowSpan)
	// Clamp the spans to positions this origin can actually own - an
	// overlap with an earlier span would break the exactly-once invariant
	// (the stored span must match the covered markers).
	for dc := 1; dc < cs; dc++ {
		if _, covered := b.pending[[2]int{row, col + dc}]; covered {
			cs = dc
			break
		}
	}
rows:
	for dr := 1; dr < rs; dr++ {
		for dc := 0; dc < cs; dc++ {
			if _, covered := b.pending[[2]int{row + dr, col + dc}]; covered {
				rs = dr
				break rows
			}
		}
	}
	b.grid[row] = append(b.grid[row], OriginCell{Cell: Cell{
		Blocks:  cell.Blocks,
		ColSpan: uint32(cs),
		RowSpan: uint32(rs),
	}})
	for dr := 0; dr < rs; dr++ {
		for dc := 0; dc < cs; dc++ {
			if dr == 0 && dc == 0 {
				continue
			}
			b.pending[[2]int{row + dr, col + dc}] = [2]int{row, col}
		}
	}
	return nil
}

// Covered consumes one explicitly-written covered position (ODF
// covered-table-cell). It reports false when no span accounts for the
// position - the stray marker then becomes an empty cell.
func (b *GridBuilder) Covered() bool {
	row := b.rowIndex()
	pos := [2]int{row, len(b.grid[row])}
	if origin, ok := b.pending[pos]; ok {
		delete(b.pending, pos)
		b.grid[row] = append(b.grid[row], CoveredCell{OriginRow: origin[0], OriginCol: origin[1]})
		return true
	}
	b.grid[row] = append(b.grid[row], OriginCell{})
	return false
}

// Finish materializes every pending covered position in surviving rows,
// including tails behind a gap (a short row under a span at a later
// column): intervening slots fill with empty cells so each covered marker
// lands on its true column. It then drops trailing all-empty rows, clamps
// stored spans to the surviving grid so every claimed position is backed
// by a covered marker (exactly-once invariant), and returns the Table.
func (b *GridBuilder) Finish(kind TableKind) Table {
	byRow := make(map[int][]int)
	for pos := range b.pending {
		if pos[0] < len(b.grid) {
			byRow[pos[0]] = append(byRow[pos[0]], pos[1])
		}
	}
	rows := make([]int, 0, len(byRow))
	for row := range byRow {
		rows = append(rows, row)
	}
	sort.Ints(rows)
	for _, row := range rows {
		cols := byRow[row]
		sort.Ints(cols)
		for _, col := range cols {
			for len(b.grid[row]) < col {
				b.grid[row] = append(b.grid[row], OriginCell{})
			}
			pos := [2]int{row, col}
			origin := b.pending[pos]
			delete(b.pending, pos)
			// Placement always consumes pending slots at the cursor, so a
			// still-pending position can only sit at or past the row end.
			b.grid[row] = append(b.grid[row], CoveredCell{OriginRow: origin[0], OriginCol: origin[1]})
		}
	}
	// Drop trailing all-empty rows.
	for len(b.grid) > 0 && b.rowIsFiller(b.grid[len(b.grid)-1]) {
		b.grid = b.grid[:len(b.grid)-1]
	}
	// Clamp stored spans to the surviving grid.
	rowCount := len(b.grid)
	for r := 0; r < rowCount; r++ {
		width := len(b.grid[r])
		for c := 0; c < width; c++ {
			origin, ok := b.grid[r][c].(OriginCell)
			if !ok {
				continue
			}
			if uint32(rowCount-r) < origin.Cell.RowSpan {
				origin.Cell.RowSpan = uint32(rowCount - r)
			}
			if uint32(width-c) < origin.Cell.ColSpan {
				origin.Cell.ColSpan = uint32(width - c)
			}
			b.grid[r][c] = origin
		}
	}
	return Table{Grid: b.grid, HeaderRows: 0, Kind: kind}
}

// rowIsFiller reports whether a trailing row would be dropped: every slot
// is an empty origin, and covered positions count as filler unless
// KeepCoveredTail was set.
func (b *GridBuilder) rowIsFiller(row []CellSlot) bool {
	for _, slot := range row {
		switch s := slot.(type) {
		case OriginCell:
			if !s.Cell.IsEmpty() {
				return false
			}
		case CoveredCell:
			if b.keepCoveredTail {
				return false
			}
		}
	}
	return true
}

func atLeastOne(span uint32) uint32 {
	if span < 1 {
		return 1
	}
	return span
}
