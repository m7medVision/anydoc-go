// Port of src/shared/header.rs: header-row detection for grids whose
// format designates no header row. CSV and spreadsheets have no notion of
// one; Word, RTF, and DrawingML carry only pagination and table-style
// flags, none of which means "this row labels the columns". The first row
// is a header when the columns below it are consistently typed and the row
// itself is not: a label sits above a column of numbers, dates or booleans
// without being one of them.

package shared

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/m7medVision/anydoc-go/internal/model"
)

// Body rows the column types are read from. A header shows in the first
// screenful of data, so a full pass over a million-row export buys nothing.
const sampleRows = 50

// Share of a column's non-empty body cells that must agree on one type
// before the column votes, as a numerator over dominanceDen. Stray N/A and
// footnote markers are common enough that exact agreement would silence
// otherwise obvious columns.
const (
	dominanceNum = 9
	dominanceDen = 10
)

// Longest a label can be when nothing else distinguishes the first row.
// Beyond this the text reads as prose, which is data.
const maxLabel = 64

// headerKind is what a cell value looks like, at the granularity the vote
// needs.
type headerKind int

const (
	kindNumber headerKind = iota
	kindBool
	kindDate
	kindText
)

// ResolveHeaderRows is a grid's Table.HeaderRows: what the format
// declared, or — when it declared none — 1 if the first row labels the
// columns and 0 otherwise. Only the first row is ever considered, since
// that is where labels go.
func ResolveHeaderRows(table *model.Table, declared int) int {
	if declared > 0 {
		return min(declared, len(table.Grid))
	}
	grid := table.Grid
	if len(grid) < 2 {
		return 0
	}
	// A span in the first row means grouped columns or a merged title, not
	// one label per column.
	for _, s := range grid[0] {
		o, ok := s.(model.OriginCell)
		if !ok || o.Cell.ColSpan != 1 || o.Cell.RowSpan != 1 {
			return 0
		}
	}
	end := min(len(grid), sampleRows+1)
	sample := grid[1:end]
	// A first row that does not have the body's field count is a title line
	// or a stray fragment, not a header.
	if len(grid[0]) != modalWidth(sample) {
		return 0
	}

	head := make([]string, len(grid[0]))
	for i, s := range grid[0] {
		head[i] = slotText(s)
	}
	body := make([][]string, len(sample))
	for i, row := range sample {
		body[i] = make([]string, len(row))
		for j, s := range row {
			body[i][j] = slotText(s)
		}
	}
	width := contentWidth(head)
	for _, row := range body {
		if w := contentWidth(row); w > width {
			width = w
		}
	}
	// Content past the last label is an unlabelled column, same as a blank
	// label between two filled ones.
	if width == 0 || width > len(head) {
		return 0
	}

	seen := make(map[string]struct{})
	for c, value := range head[:width] {
		// Every column carries a label, with one exception: the unnamed index
		// column that pandas and R write as a leading empty field.
		if strings.TrimSpace(value) == "" {
			if c == 0 {
				continue
			}
			return 0
		}
		// A label is one line; a cell holding several is data.
		if strings.ContainsRune(value, '\n') {
			return 0
		}
		// Repeated values are what a data row looks like, not a header.
		f := fold(value)
		if _, dup := seen[f]; dup {
			return 0
		}
		seen[f] = struct{}{}
	}

	headerVotes, dataVotes := 0, 0
	for c, label := range head[:width] {
		var values []string
		for _, row := range body {
			if c < len(row) {
				v := strings.TrimSpace(row[c])
				if v != "" {
					values = append(values, v)
				}
			}
		}
		if len(values) == 0 {
			continue
		}
		label = strings.TrimSpace(label)
		if kind, ok := dominantKind(values); ok && kind != kindText {
			// A column of numbers, dates or booleans under a label that is
			// none of those: the label does not belong to its own column.
			if lk, lok := classify(label); lok && lk == kindText {
				headerVotes++
			} else {
				dataVotes++
			}
		} else {
			// Text columns carry no type signal, but a first-row value that
			// recurs below is plainly a value of the column, not its label.
			fl := fold(label)
			for _, v := range values {
				if fold(v) == fl {
					dataVotes++
					break
				}
			}
		}
	}

	if headerVotes == 0 && dataVotes == 0 {
		// Text above text, with no value repeated: only the shape of the row
		// is left to go on. Short single-line entries read as labels, and the
		// alternative is the empty header row every such table renders today.
		for _, v := range head[:width] {
			if utf8.RuneCountInString(v) > maxLabel {
				return 0
			}
		}
		return 1
	}
	if headerVotes > dataVotes {
		return 1
	}
	return 0
}

func contentWidth(row []string) int {
	for i := len(row) - 1; i >= 0; i-- {
		if strings.TrimSpace(row[i]) != "" {
			return i + 1
		}
	}
	return 0
}

// The field count most sampled body rows agree on. Frequency ties break
// toward the wider shape so the result never depends on map iteration
// order.
func modalWidth(rows [][]model.CellSlot) int {
	tally := make(map[int]int)
	for _, row := range rows {
		tally[len(row)]++
	}
	width, n := 0, 0
	for w, f := range tally {
		if f > n || (f == n && w > width) {
			width, n = w, f
		}
	}
	return width
}

// A slot's text. Covered positions and cells holding anything but
// paragraphs yield nothing: neither is a plain value, and both then read
// as empty.
func slotText(slot model.CellSlot) string {
	o, ok := slot.(model.OriginCell)
	if !ok {
		return ""
	}
	var out strings.Builder
	for _, block := range o.Cell.Blocks {
		p, ok := block.(model.Paragraph)
		if !ok {
			return ""
		}
		if out.Len() > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(model.InlinesToPlainText(p.Inlines))
	}
	return out.String()
}

// Case- and padding-insensitive form used to compare a label against the
// values below it.
func fold(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// The kind at least dominanceNum/dominanceDen of the values share, if any.
// Values are non-empty, so they all classify.
func dominantKind(values []string) (headerKind, bool) {
	for _, kind := range []headerKind{kindNumber, kindBool, kindDate, kindText} {
		n := 0
		for _, v := range values {
			if k, ok := classify(v); ok && k == kind {
				n++
			}
		}
		if n*dominanceDen >= len(values)*dominanceNum {
			return kind, true
		}
	}
	return 0, false
}

// Classify one value; ok is false when it is blank and carries no type at
// all.
func classify(value string) (headerKind, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if isNumber(value) {
		return kindNumber, true
	}
	switch fold(value) {
	case "true", "false", "yes", "no":
		return kindBool, true
	}
	if isTemporal(value) {
		return kindDate, true
	}
	return kindText, true
}

// A numeric literal as data files write them: sign, digit grouping, either
// decimal convention, exponent, and a percent suffix. Which convention a
// value uses does not matter here, only that it is a number.
func isNumber(value string) bool {
	value = strings.TrimSuffix(value, "%")
	var b strings.Builder
	for _, c := range value {
		if c != ',' && c != ' ' && c != '_' && c != '\u00a0' {
			b.WriteRune(c)
		}
	}
	cleaned := b.String()
	hasDigit := false
	for i := 0; i < len(cleaned); i++ {
		if cleaned[i] >= '0' && cleaned[i] <= '9' {
			hasDigit = true
			break
		}
	}
	// strconv.ParseFloat accepts Go hex-floats (`0x1.2p3`) and inf/NaN;
	// Rust f64::parse does not treat those as numbers here. Digits-only
	// plus no 'x'/'X' keeps the two parsers aligned.
	if !hasDigit || strings.ContainsAny(cleaned, "xX") {
		return false
	}
	_, err := strconv.ParseFloat(cleaned, 64)
	return err == nil
}

// A numeric date triple in any field order, optionally followed by a time,
// or a bare clock time. Month names stay Text: a column of them is a
// column of labels, and it would cast the same vote either way.
func isTemporal(value string) bool {
	date, time := value, ""
	if i := strings.IndexAny(value, "T "); i >= 0 {
		date, time = value[:i], value[i+1:]
	}
	time = strings.TrimRight(time, "Z")
	if i := strings.IndexByte(time, '.'); i >= 0 {
		time = time[:i]
	}
	isClock := func(t string) bool {
		n := digitGroups(t, ":")
		return n == 2 || n == 3
	}
	if digitGroups(date, "-/.") == 3 {
		return time == "" || isClock(time)
	}
	return time == "" && isClock(date)
}

// How many components value has when split on the first of seps it
// contains, or 0 unless every component is a short digit run.
func digitGroups(value, seps string) int {
	sep := byte(0)
	found := false
	for i := 0; i < len(seps); i++ {
		if strings.IndexByte(value, seps[i]) >= 0 {
			sep = seps[i]
			found = true
			break
		}
	}
	if !found {
		return 0
	}
	parts := strings.Split(value, string(sep))
	for _, p := range parts {
		if len(p) < 1 || len(p) > 4 {
			return 0
		}
		for i := 0; i < len(p); i++ {
			if p[i] < '0' || p[i] > '9' {
				return 0
			}
		}
	}
	return len(parts)
}
