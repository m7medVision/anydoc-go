package anydoc

// Document is a parsed document: its body, notes, and embedded assets.
type Document struct {
	Blocks []Block `json:"blocks"`
	Notes  []Note  `json:"notes"`
	Assets []Asset `json:"assets"`
}

// Block is one block-level piece of a document body.
type Block struct {
	Kind    string   `json:"kind"`
	Level   *uint8   `json:"level,omitempty"`
	Anchor  *string  `json:"anchor,omitempty"`
	Content []Inline `json:"content,omitempty"`
	List    *List    `json:"list,omitempty"`
	Table   *Table   `json:"table,omitempty"`
	Blocks  []Block  `json:"blocks,omitempty"`
	Lang    *string  `json:"lang,omitempty"`
	Text    *string  `json:"text,omitempty"`
}

// Inline is one span of inline content.
type Inline struct {
	Kind    string       `json:"kind"`
	Text    *string      `json:"text,omitempty"`
	Style   *Style       `json:"style,omitempty"`
	Content []Inline     `json:"content,omitempty"`
	Target  *LinkTarget  `json:"target,omitempty"`
	Alt     *string      `json:"alt,omitempty"`
	Source  *ImageSource `json:"source,omitempty"`
	Anchor  *string      `json:"anchor,omitempty"`
	NoteID  *string      `json:"note_id,omitempty"`
	Checked *bool        `json:"checked,omitempty"`
}

// Style is fully resolved character style.
type Style struct {
	Bold   bool `json:"bold"`
	Italic bool `json:"italic"`
	Strike bool `json:"strike"`
	Code   bool `json:"code"`
}

// LinkTarget is where a link points.
type LinkTarget struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// ImageSource is where an image's bytes live.
type ImageSource struct {
	Kind    string  `json:"kind"`
	URL     *string `json:"url,omitempty"`
	AssetID *int    `json:"asset_id,omitempty"`
}

// List is a fully resolved list.
type List struct {
	Marker string     `json:"marker"`
	Start  uint64     `json:"start"`
	Items  []ListItem `json:"items"`
}

// ListItem is one item of a List.
type ListItem struct {
	Blocks      []Block `json:"blocks"`
	MarkerLabel *string `json:"marker_label,omitempty"`
}

// Table is the canonical table grid.
type Table struct {
	Grid       [][]CellSlot `json:"grid"`
	HeaderRows int          `json:"header_rows"`
	Kind       string       `json:"kind"`
}

// CellSlot is one position in a table grid.
type CellSlot struct {
	Kind      string `json:"kind"`
	Cell      *Cell  `json:"cell,omitempty"`
	OriginRow *int   `json:"origin_row,omitempty"`
	OriginCol *int   `json:"origin_col,omitempty"`
}

// Cell is a table cell and the extent it spans.
type Cell struct {
	Blocks  []Block `json:"blocks"`
	ColSpan uint32  `json:"col_span"`
	RowSpan uint32  `json:"row_span"`
}

// Note is a footnote or endnote body.
type Note struct {
	ID     string  `json:"id"`
	Kind   string  `json:"kind"`
	Blocks []Block `json:"blocks"`
}

// Asset is an embedded binary asset. Data is the payload.
type Asset struct {
	ID         int    `json:"id"`
	MediaType  string `json:"media_type"`
	OriginPart string `json:"origin_part"`
	Data       []byte `json:"data"`
}
