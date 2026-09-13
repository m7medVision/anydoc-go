// Port of src/shared/grid.rs: edge-based table assembly shared by RTF
// (\cellx boundaries) and binary DOC (TAP rgdxaCenter boundaries): grid
// columns come from the clustered union of every row's cell edges,
// horizontal merges collapse into column spans, and vertical chains start
// at an explicit restart flag and are continued by continuation cells
// whose boundaries match the chain's.

package shared

import (
	"sort"

	"github.com/m7medVision/anydoc-go/internal/model"
)

// CellProp is the merge/boundary properties of one cell.
type CellProp struct {
	// MergeFirst is the first cell of a horizontally merged set.
	MergeFirst bool
	// MergeCont is a horizontal continuation: folds into the preceding
	// first cell.
	MergeCont bool
	// VMergeFirst is the first cell of a vertically merged chain.
	VMergeFirst bool
	// VMergeCont is a vertical continuation: covered by the chain's origin
	// above.
	VMergeCont bool
	// Right is the cell's right boundary in twips — vertical merge chains
	// match on actual boundaries, not cell ordinals.
	Right int64
}

// GridCell is one logical cell with its properties.
type GridCell struct {
	Blocks []model.Block
	Prop   CellProp
}

// GridRow is one logical row: its cells with their properties, and whether
// the row is a header row.
type GridRow struct {
	Cells  []GridCell
	Header bool
}

type edgeOrigin struct {
	blocks      []model.Block
	colL, colR  int
	rowSpan     uint32
	vmergeFirst bool
	covered     bool
}

// BuildEdgeTable assembles logical rows into the canonical grid. ok is
// false when the finished grid is empty.
func BuildEdgeTable(rows []GridRow) (model.Block, error) {
	// Producer boundary jitter under this threshold clusters into one edge.
	const edgeTolerance int64 = 10
	headerRows := 0
	for _, r := range rows {
		if !r.Header {
			break
		}
		headerRows++
	}

	// Normalize each row's right edges to be strictly increasing (cells past
	// the declared boundaries get synthetic edges), then cluster the union
	// into the global column-edge list.
	rowsEdged := make([][]GridCell, len(rows))
	for i, row := range rows {
		last := int64(-1 << 63) // i64::MIN
		out := make([]GridCell, len(row.Cells))
		for j, cell := range row.Cells {
			prop := cell.Prop
			if prop.Right <= last {
				prop.Right = last + 1
			}
			last = prop.Right
			out[j] = GridCell{Blocks: cell.Blocks, Prop: prop}
		}
		rowsEdged[i] = out
	}
	var edges []int64
	for _, row := range rowsEdged {
		for _, cell := range row {
			edges = append(edges, cell.Prop.Right)
		}
	}
	sort.Slice(edges, func(i, j int) bool { return edges[i] < edges[j] })
	var clusters []int64
	for _, e := range edges {
		if len(clusters) == 0 || e-clusters[len(clusters)-1] > edgeTolerance {
			clusters = append(clusters, e)
		}
	}
	colOf := func(x int64) int {
		// partition_point(|&c| c < x - EDGE_TOLERANCE)
		lo, hi := 0, len(clusters)
		thresh := x - edgeTolerance
		for lo < hi {
			mid := (lo + hi) / 2
			if clusters[mid] < thresh {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		return lo
	}

	built := make([][]edgeOrigin, 0, len(rowsEdged))
	for _, row := range rowsEdged {
		var out []edgeOrigin
		col := 0
		j := 0
		for j < len(row) {
			cell := row[j]
			j++
			merged := cell.Blocks
			right := cell.Prop.Right
			if cell.Prop.MergeFirst {
				for j < len(row) && row[j].Prop.MergeCont {
					merged = append(merged, row[j].Blocks...)
					right = row[j].Prop.Right
					j++
				}
			}
			colR := colOf(right) + 1
			if colR < col+1 {
				colR = col + 1
			}
			out = append(out, edgeOrigin{
				blocks:      merged,
				colL:        col,
				colR:        colR,
				rowSpan:     1,
				vmergeFirst: cell.Prop.VMergeFirst,
				covered:     cell.Prop.VMergeCont,
			})
			col = colR
		}
		built = append(built, out)
	}

	// Vertical chains keyed by the origin's exact column range.
	type pair struct{ a, b int }
	active := make(map[pair]pair)
	for r := 0; r < len(built); r++ {
		nextActive := make(map[pair]pair)
		for i := 0; i < len(built[r]); i++ {
			range_ := pair{built[r][i].colL, built[r][i].colR}
			if built[r][i].covered {
				if origin, ok := active[range_]; ok {
					built[origin.a][origin.b].rowSpan++
					nextActive[range_] = origin
					continue
				}
				// Continuation without a matching chain: keep it visible.
				built[r][i].covered = false
			}
			if built[r][i].vmergeFirst {
				nextActive[range_] = pair{r, i}
			}
		}
		active = nextActive
	}

	builder := model.NewGridBuilder()
	for _, row := range built {
		builder.NextRow()
		for _, origin := range row {
			span := uint32(origin.colR - origin.colL)
			if origin.covered {
				for n := uint32(0); n < span; n++ {
					builder.Covered()
				}
			} else {
				if err := builder.Place(model.SpanningCell(origin.blocks, span, origin.rowSpan)); err != nil {
					return nil, err
				}
			}
		}
	}
	table := builder.Finish(model.TableData)
	if len(table.Grid) == 0 {
		return nil, nil
	}
	table.HeaderRows = ResolveHeaderRows(&table, headerRows)
	return model.TableBlock{Table: table}, nil
}
