package formats

import (
	"strings"
	"unicode/utf8"

	"github.com/m7medVision/anydoc-go/internal/model"
	"github.com/m7medVision/anydoc-go/internal/shared"
	"github.com/m7medVision/anydoc-go/internal/text"
)

func init() { parseFuncs[CSV] = parseCSV }

// parseCSV converts CSV and friends (semicolon, tab, pipe delimited) into a
// one-table document.
//
// Field content is preserved as written (RFC 4180: spaces are part of the
// field); only control characters are cleaned. Encoding is detected from the
// BOM (UTF-8, UTF-16LE/BE), then UTF-8, then Windows-1252. The delimiter is
// chosen by trial-parsing candidates and scoring record consistency, so
// delimiters inside quoted fields don't skew the choice. The format marks no
// header row, so the shape of the data decides whether the first record is
// one.
//
// The upstream reader never yields an error under this frontend's options
// (flexible records, an in-memory already-decoded input), so the upstream
// skip-and-warn path for unreadable records cannot fire and the port has no
// error branch.
func parseCSV(data []byte) (model.Document, error) {
	s := decodeCSV(data)
	delimiter := sniffDelimiter(s)

	r := csvReader{data: s, delimiter: delimiter, state: csvStartRecord}
	var rows [][]model.Cell
	for {
		record, ok := r.next()
		if !ok {
			break
		}
		cells := make([]model.Cell, len(record))
		for i, f := range record {
			cells[i] = model.CellFromInlines([]model.Inline{model.Plain(shared.CleanText(f))})
		}
		rows = append(rows, cells)
	}

	var doc model.Document
	table := model.TableFromRows(rows, 0, model.TableData)
	table.HeaderRows = shared.ResolveHeaderRows(&table, 0)
	if len(table.Grid) > 0 {
		doc.Blocks = append(doc.Blocks, model.TableBlock{Table: table})
	}
	return doc, nil
}

// decodeCSV decodes input bytes as upstream does: a UTF-16 BOM names its
// encoding, a UTF-8 BOM is stripped, valid UTF-8 passes through, and anything
// else falls back to Windows-1252 - every path lossy through the charset
// layer, exactly like the encoding_rs decodes it replaces.
func decodeCSV(data []byte) string {
	if len(data) >= 2 && data[0] == 0xFF && data[1] == 0xFE {
		return text.Decode(text.UTF16LE, data)
	}
	if len(data) >= 2 && data[0] == 0xFE && data[1] == 0xFF {
		return text.Decode(text.UTF16BE, data)
	}
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		data = data[3:]
	}
	if utf8.Valid(data) {
		return string(data)
	}
	return text.Decode(text.Windows1252, data)
}

// sniffDelimiter trial-parses each candidate over the leading records and
// scores field consistency; comma wins ties.
func sniffDelimiter(s string) byte {
	const candidates = ",;\t|"
	best, bestScore := byte(','), 0
	for i := 0; i < len(candidates); i++ {
		d := candidates[i]
		// Sample complete records (the reader is streaming, so this reads
		// only as much input as the records span): sampling physical lines
		// would cut a quoted multiline field in half.
		r := csvReader{data: s, delimiter: d, state: csvStartRecord}
		var counts []int
		for len(counts) < 20 {
			record, ok := r.next()
			if !ok {
				break
			}
			counts = append(counts, len(record))
		}
		if len(counts) == 0 {
			continue
		}
		// Modal field count and how dominant it is. The field counts are
		// the map keys, so the (frequency, count) maximum is unique and the
		// result never depends on Go's random map iteration order.
		tally := make(map[int]int)
		for _, c := range counts {
			tally[c]++
		}
		modal, freq := 0, 0
		for count, f := range tally {
			if f > freq || (f == freq && count > modal) {
				modal, freq = count, f
			}
		}
		if modal < 2 {
			continue // a delimiter that never splits carries no signal
		}
		// Consistency first, then wider records break the tie.
		score := freq*1000 + min(modal, 500)
		if score > bestScore {
			best, bestScore = d, score
		}
	}
	return best
}

// The reader below replaces the csv crate's Reader (csv-core's NFA and DFA)
// for the fixed configuration this frontend builds it with: quoting on,
// doubled-quote escapes, CR/LF/CRLF record terminators, no escape byte, no
// comment byte, flexible records. encoding/csv diverges from that machine
// (it rejects quotes after a closing quote, lone CR terminators, and treats
// empty lines differently), so the states and transitions are a
// byte-faithful specialization of csv-core's, not a re-derivation. Like
// csv-core, the reader "prefers *a* parse over *no* parse": it never errors,
// and empty records vanish.

type csvState uint8

// The parse states. Field-final states come last, ordered so that "at or
// past csvEndFieldDelim" is exactly "the field being read is closed" and
// "past csvEndFieldDelim" is "the record is closed too" - the two thresholds
// csv-core's DFA compares against.
const (
	csvStartRecord          csvState = iota // between records; swallows empty records
	csvStartField                           // at a field's first byte, before it
	csvInField                              // inside an unquoted field
	csvInQuotedField                        // inside a quoted field
	csvInDoubleEscapedQuote                 // right after a closing quote
	csvEndFieldTerm                         // a terminator byte ended the field
	csvInRecordTerm                         // classifying it: a CR may pair with an LF
	csvEndFieldDelim                        // a delimiter closed the field
	csvEndRecord                            // a terminator closed the record
	csvCRLF                                 // a CR closed the record; LF may follow
)

// csvAction says what routing a byte does with it: copy it into the field,
// consume it as syntax, or re-route it from the next state without consuming
// it (the NFA's epsilon transition).
type csvAction uint8

const (
	csvCopy csvAction = iota
	csvDiscard
	csvEpsilon
)

// csvReader parses delimiter-separated records out of one decoded string.
// The state carries across records: a record may legally end on a CR whose
// matching LF is the first byte of the next call.
type csvReader struct {
	data      string
	pos       int
	delimiter byte
	state     csvState
}

// next parses one record and reports whether there was one. It mirrors
// csv-core's read_record: bytes route one transition at a time, a field
// closes on every field-final state (only a delimiter keeps the record
// going), and end of input emits a pending record unless the reader sits
// between records.
func (r *csvReader) next() ([]string, bool) {
	var fields []string
	var field []byte
	for r.pos < len(r.data) {
		// The two run states bulk-copy everything up to the next byte they
		// treat specially, as csv-core's scan_and_copy does.
		switch r.state {
		case csvInField:
			if n := scanUnquotedRun(r.data[r.pos:], r.delimiter); n > 0 {
				field = append(field, r.data[r.pos:r.pos+n]...)
				r.pos += n
				continue
			}
		case csvInQuotedField:
			rest := r.data[r.pos:]
			n := strings.IndexByte(rest, '"')
			if n < 0 {
				n = len(rest)
			}
			if n > 0 {
				field = append(field, rest[:n]...)
				r.pos += n
				continue
			}
		}
		c := r.data[r.pos]
		state, action := r.transition(c)
		r.state = state
		switch action {
		case csvCopy:
			field = append(field, c)
			r.pos++
		case csvDiscard:
			r.pos++
		}
		// Epsilon leaves the byte unconsumed; the loop re-routes it from
		// the new state.
		if state >= csvEndFieldDelim {
			fields = append(fields, string(field))
			field = field[:0]
			if state != csvEndFieldDelim {
				return fields, true
			}
		}
	}
	// End of input: a record is pending unless the reader is between
	// records (csv-core's transition_final). A field still being read -
	// quoted or not, even one byte short of closing - closes as-is.
	switch r.state {
	case csvStartRecord, csvEndRecord, csvCRLF:
		return nil, false
	}
	fields = append(fields, string(field))
	return fields, true
}

// transition routes one byte from the current state. A direct port of
// csv-core's transition_nfa with the reader configuration's dead branches
// pruned.
func (r *csvReader) transition(c byte) (csvState, csvAction) {
	switch r.state {
	case csvStartRecord:
		if c == '\r' || c == '\n' {
			return csvStartRecord, csvDiscard
		}
		return csvStartField, csvEpsilon
	case csvStartField:
		switch {
		case c == '"':
			return csvInQuotedField, csvDiscard
		case c == r.delimiter:
			return csvEndFieldDelim, csvDiscard
		case c == '\r' || c == '\n':
			return csvEndFieldTerm, csvEpsilon
		default:
			return csvInField, csvCopy
		}
	case csvEndFieldDelim:
		return csvStartField, csvEpsilon
	case csvInField:
		switch {
		case c == r.delimiter:
			return csvEndFieldDelim, csvDiscard
		case c == '\r' || c == '\n':
			return csvEndFieldTerm, csvEpsilon
		default:
			return csvInField, csvCopy
		}
	case csvInQuotedField:
		if c == '"' {
			return csvInDoubleEscapedQuote, csvDiscard
		}
		// Delimiters and terminators are content inside quotes.
		return csvInQuotedField, csvCopy
	case csvInDoubleEscapedQuote:
		switch {
		case c == '"':
			// A doubled quote is one literal quote.
			return csvInQuotedField, csvCopy
		case c == r.delimiter:
			return csvEndFieldDelim, csvDiscard
		case c == '\r' || c == '\n':
			return csvEndFieldTerm, csvEpsilon
		default:
			// Content after a closing quote joins the field unquoted.
			return csvInField, csvCopy
		}
	case csvEndRecord:
		return csvStartRecord, csvEpsilon
	case csvEndFieldTerm:
		return csvInRecordTerm, csvEpsilon
	case csvInRecordTerm:
		if c == '\r' {
			return csvCRLF, csvDiscard
		}
		return csvEndRecord, csvDiscard
	case csvCRLF:
		if c == '\n' {
			return csvStartRecord, csvDiscard
		}
		// A lone CR already ended the record; this byte starts the next.
		return csvStartRecord, csvEpsilon
	}
	panic("csv reader: invalid state")
}

// scanUnquotedRun returns the length of the longest prefix of s free of the
// delimiter and record terminators: the bytes an unquoted field takes
// verbatim. A quote is not special there - csv-core treats it as content.
func scanUnquotedRun(s string, delimiter byte) int {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case delimiter, '\r', '\n':
			return i
		}
	}
	return len(s)
}
