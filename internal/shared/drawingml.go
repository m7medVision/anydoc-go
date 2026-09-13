// Port of src/shared/drawingml.rs: textual extraction of DrawingML rich
// objects shared by DOCX and PPTX: charts (title, axis titles, cached
// series data) and SmartArt diagram data (text points).

package shared

import (
	"strings"

	"github.com/m7medVision/anydoc-go/internal/model"
	"github.com/m7medVision/anydoc-go/internal/package/xml"
)

// ChartBlocks returns a chart part as blocks: bold title paragraph plus a
// categories x series table built from the cached display strings.
func ChartBlocks(root *xml.Element) []model.Block {
	var blocks []model.Block
	if t := root.FirstDescendant(xml.NsChart, "title"); t != nil {
		title := CleanText(DrawingText(t))
		if strings.TrimSpace(title) != "" {
			bold := true
			blocks = append(blocks, model.Paragraph{Inlines: []model.Inline{
				model.Run{Text: title, Style: model.Style{Bold: bold}},
			}})
		}
	}
	type series struct {
		name   string
		values []string
	}
	var categories []string
	var all []series
	for ser := range root.Descendants(xml.NsChart, "ser") {
		// Cached display strings only; c:f formula references are not text.
		name := ""
		if tx := ser.Find(xml.NsChart, "tx"); tx != nil {
			if v := tx.FirstDescendant(xml.NsChart, "v"); v != nil {
				name = CleanText(v.Text())
			}
		}
		var cats []string
		if cat := ser.Find(xml.NsChart, "cat"); cat != nil {
			for v := range cat.Descendants(xml.NsChart, "v") {
				cats = append(cats, CleanText(v.Text()))
			}
		}
		if len(categories) == 0 {
			categories = cats
		}
		var values []string
		if val := ser.Find(xml.NsChart, "val"); val != nil {
			for p := range val.Descendants(xml.NsChart, "v") {
				values = append(values, CleanText(p.Text()))
			}
		}
		all = append(all, series{name: name, values: values})
	}
	if len(all) == 0 || len(categories) == 0 {
		return blocks
	}
	catTitle := ""
	if ax := root.FirstDescendant(xml.NsChart, "catAx"); ax != nil {
		if t := ax.Find(xml.NsChart, "title"); t != nil {
			catTitle = CleanText(DrawingText(t))
		}
	}
	header := make([]model.Cell, 0, 1+len(all))
	header = append(header, model.CellFromInlines([]model.Inline{model.Plain(catTitle)}))
	for _, s := range all {
		header = append(header, model.CellFromInlines([]model.Inline{model.Plain(s.name)}))
	}
	rows := [][]model.Cell{header}
	for i, cat := range categories {
		row := make([]model.Cell, 0, 1+len(all))
		row = append(row, model.CellFromInlines([]model.Inline{model.Plain(cat)}))
		for _, s := range all {
			v := ""
			if i < len(s.values) {
				v = s.values[i]
			}
			row = append(row, model.CellFromInlines([]model.Inline{model.Plain(v)}))
		}
		rows = append(rows, row)
	}
	blocks = append(blocks, model.TableBlock{Table: model.TableFromRows(rows, 1, model.TableData)})
	return blocks
}

// DiagramBlocks returns a SmartArt data part as a bullet list of its text
// points in order.
func DiagramBlocks(root *xml.Element) []model.Block {
	var items []model.ListItem
	for pt := range root.Descendants(xml.NsDgm, "pt") {
		t := pt.Find(xml.NsDgm, "t")
		if t == nil {
			continue
		}
		text := CleanText(t.Text())
		if strings.TrimSpace(text) == "" {
			continue
		}
		items = append(items, model.ListItem{
			Blocks: []model.Block{model.Paragraph{Inlines: []model.Inline{model.Plain(text)}}},
		})
	}
	if len(items) == 0 {
		return nil
	}
	return []model.Block{model.ListBlock{List: model.List{
		Marker: model.MarkerBullet,
		Start:  1,
		Items:  items,
	}}}
}

// DrawingText joins text runs inside DrawingML rich text (a:p/a:r/a:t).
func DrawingText(elem *xml.Element) string {
	var parts []string
	for p := range elem.Descendants(xml.NsA, "p") {
		text := p.Text()
		if strings.TrimSpace(text) != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return elem.Text()
	}
	return strings.Join(parts, " ")
}
