// Table rendering over the canonical grid. Covered positions render as
// blank cells (GFM has no span syntax); the grid invariant means the
// renderer never synthesizes or duplicates columns.

package markdown

import (
	"strings"

	"github.com/m7medVision/anydoc-go/internal/model"
)

type renderedCell struct {
	text        string
	coveredSpan bool
}

func renderTable(table *model.Table, rc *ctx) (string, bool) {
	// Interior empty rows render as blank rows — they carry the source's
	// row coordinates; trailing blank rows are popped below.
	if len(table.Grid) == 0 {
		return "", false
	}
	width := 0
	for _, row := range table.Grid {
		if len(row) > width {
			width = len(row)
		}
	}
	rendered := make([][]renderedCell, 0, len(table.Grid))
	for _, row := range table.Grid {
		cells := make([]renderedCell, 0, width)
		for _, slot := range row {
			switch s := slot.(type) {
			case model.OriginCell:
				cells = append(cells, renderedCell{text: renderCell(&s.Cell, rc)})
			case model.CoveredCell:
				cells = append(cells, renderedCell{coveredSpan: true})
			}
		}
		for len(cells) < width {
			cells = append(cells, renderedCell{})
		}
		rendered = append(rendered, cells)
	}
	for len(rendered) > 1 && rowIsBlank(rendered[len(rendered)-1]) {
		rendered = rendered[:len(rendered)-1]
	}
	width = 0
	for _, row := range rendered {
		for i := len(row) - 1; i >= 0; i-- {
			if row[i].text != "" || row[i].coveredSpan {
				if i+1 > width {
					width = i + 1
				}
				break
			}
		}
	}
	if width == 0 {
		return "", false
	}
	for i := range rendered {
		rendered[i] = rendered[i][:width]
	}

	var out strings.Builder
	// GFM tables always carry a delimiter row, so a table with no header
	// row of its own renders an empty one above the data.
	var header []string
	if table.HeaderRows >= 1 && len(rendered) > 0 {
		first := rendered[0]
		rendered = rendered[1:]
		header = make([]string, len(first))
		for i, c := range first {
			header[i] = c.text
		}
	} else {
		header = make([]string, width)
	}
	out.WriteString(formatRow(header))
	out.WriteByte('\n')
	dashes := make([]string, width)
	for i := range dashes {
		dashes[i] = "---"
	}
	out.WriteString(formatRow(dashes))
	for _, row := range rendered {
		out.WriteByte('\n')
		cells := make([]string, len(row))
		for i, c := range row {
			cells[i] = c.text
		}
		out.WriteString(formatRow(cells))
	}
	return out.String(), true
}

func rowIsBlank(row []renderedCell) bool {
	for _, c := range row {
		if c.text != "" || c.coveredSpan {
			return false
		}
	}
	return true
}

func formatRow(cells []string) string {
	var s strings.Builder
	s.WriteByte('|')
	for _, cell := range cells {
		s.WriteByte(' ')
		s.WriteString(cell)
		s.WriteString(" |")
	}
	return s.String()
}

// renderCell flattens arbitrary block content into a single table-cell
// line.
func renderCell(cell *model.Cell, rc *ctx) string {
	var parts []string
	for _, block := range cell.Blocks {
		cellBlockText(block, rc, &parts)
	}
	// A cell's edge whitespace is padding the table's own padding would
	// swallow anyway, and spreadsheets carry it by the thousand.
	var kept []string
	for _, line := range rustLines(strings.Join(parts, "<br>")) {
		if strings.TrimSpace(line) != "" {
			kept = append(kept, strings.TrimSpace(line))
		}
	}
	return strings.Join(kept, "<br>")
}

func cellBlockText(block model.Block, rc *ctx, parts *[]string) {
	switch b := block.(type) {
	case model.Heading:
		t := renderInlines(b.Inlines, inlineContextTableCell, rc)
		if strings.TrimSpace(t) != "" {
			*parts = append(*parts, "**"+strings.TrimSpace(t)+"**")
		}
	case model.Paragraph:
		// Edge whitespace is preserved here and protected in renderCell;
		// sources that retain cell padding (spreadsheets, CSV) keep it in
		// the final output.
		t := renderInlines(b.Inlines, inlineContextTableCell, rc)
		if strings.TrimSpace(t) != "" {
			*parts = append(*parts, t)
		}
	case model.ListBlock:
		for i := range b.List.Items {
			item := &b.List.Items[i]
			var inner []string
			for _, blk := range item.Blocks {
				cellBlockText(blk, rc, &inner)
			}
			var marker string
			switch {
			case item.MarkerLabel != "":
				marker = escapeMarkerLabel(item.MarkerLabel, inlineContextTableCell) + " "
			case b.List.Marker == model.MarkerBullet:
				marker = "• "
			default:
				marker = b.List.Marker.Label(saturatingAdd(b.List.Start, uint64(i))) + " "
			}
			if len(inner) > 0 {
				*parts = append(*parts, marker+strings.Join(inner, " "))
			}
		}
	case model.TableBlock:
		// Keep empty cells in the join so values stay in their source
		// column positions.
		for _, row := range b.Table.Grid {
			cells := make([]string, len(row))
			any := false
			for j, slot := range row {
				if origin, ok := slot.(model.OriginCell); ok {
					cells[j] = renderCell(&origin.Cell, rc)
				} else {
					cells[j] = ""
				}
				if cells[j] != "" {
					any = true
				}
			}
			if any {
				*parts = append(*parts, strings.Join(cells, " / "))
			}
		}
	case model.Quote:
		for _, blk := range b.Blocks {
			cellBlockText(blk, rc, parts)
		}
	case model.CodeBlock:
		t := strings.TrimSpace(b.Code)
		if t != "" {
			var s strings.Builder
			pushCodeSpan(t, inlineContextTableCell, &s)
			*parts = append(*parts, s.String())
		}
	case model.MathBlock:
		if strings.TrimSpace(b.TeX) != "" {
			var s strings.Builder
			pushMathSpan(b.TeX, inlineContextTableCell, &s)
			*parts = append(*parts, s.String())
		}
	case model.Rule:
	}
}
