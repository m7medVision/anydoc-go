//! Document model as JSON matching the Python/Node binding shape.

use base64::engine::general_purpose::STANDARD;
use base64::Engine;
use serde::Serialize;

#[derive(Serialize)]
pub struct DocumentOut {
    blocks: Vec<BlockOut>,
    notes: Vec<NoteOut>,
    assets: Vec<AssetOut>,
}

#[derive(Serialize)]
struct BlockOut {
    kind: &'static str,
    #[serde(skip_serializing_if = "Option::is_none")]
    level: Option<u8>,
    #[serde(skip_serializing_if = "Option::is_none")]
    anchor: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    content: Option<Vec<InlineOut>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    list: Option<ListOut>,
    #[serde(skip_serializing_if = "Option::is_none")]
    table: Option<TableOut>,
    #[serde(skip_serializing_if = "Option::is_none")]
    blocks: Option<Vec<BlockOut>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    lang: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    text: Option<String>,
}

#[derive(Serialize)]
struct InlineOut {
    kind: &'static str,
    #[serde(skip_serializing_if = "Option::is_none")]
    text: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    style: Option<StyleOut>,
    #[serde(skip_serializing_if = "Option::is_none")]
    content: Option<Vec<InlineOut>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    target: Option<LinkTargetOut>,
    #[serde(skip_serializing_if = "Option::is_none")]
    alt: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    source: Option<ImageSourceOut>,
    #[serde(skip_serializing_if = "Option::is_none")]
    anchor: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    note_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    checked: Option<bool>,
}

#[derive(Serialize)]
struct StyleOut {
    bold: bool,
    italic: bool,
    strike: bool,
    code: bool,
}

#[derive(Serialize)]
struct LinkTargetOut {
    kind: &'static str,
    value: String,
}

#[derive(Serialize)]
struct ImageSourceOut {
    kind: &'static str,
    #[serde(skip_serializing_if = "Option::is_none")]
    url: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    asset_id: Option<usize>,
}

#[derive(Serialize)]
struct ListOut {
    marker: &'static str,
    start: u64,
    items: Vec<ListItemOut>,
}

#[derive(Serialize)]
struct ListItemOut {
    blocks: Vec<BlockOut>,
    #[serde(skip_serializing_if = "Option::is_none")]
    marker_label: Option<String>,
}

#[derive(Serialize)]
struct TableOut {
    grid: Vec<Vec<CellSlotOut>>,
    header_rows: usize,
    kind: &'static str,
}

#[derive(Serialize)]
struct CellSlotOut {
    kind: &'static str,
    #[serde(skip_serializing_if = "Option::is_none")]
    cell: Option<CellOut>,
    #[serde(skip_serializing_if = "Option::is_none")]
    origin_row: Option<usize>,
    #[serde(skip_serializing_if = "Option::is_none")]
    origin_col: Option<usize>,
}

#[derive(Serialize)]
struct CellOut {
    blocks: Vec<BlockOut>,
    col_span: u32,
    row_span: u32,
}

#[derive(Serialize)]
struct NoteOut {
    id: String,
    kind: &'static str,
    blocks: Vec<BlockOut>,
}

#[derive(Serialize)]
struct AssetOut {
    id: usize,
    media_type: String,
    origin_part: String,
    data: String,
}

impl From<anydoc::model::Document> for DocumentOut {
    fn from(document: anydoc::model::Document) -> Self {
        DocumentOut {
            blocks: document.blocks.into_iter().map(block_out).collect(),
            notes: document.notes.into_iter().map(note_out).collect(),
            assets: document.assets.into_iter().map(asset_out).collect(),
        }
    }
}

fn block_out(block: anydoc::model::Block) -> BlockOut {
    match block {
        anydoc::model::Block::Heading { level, anchor, content } => BlockOut {
            kind: "heading",
            level: Some(level),
            anchor,
            content: Some(inlines_out(content)),
            list: None,
            table: None,
            blocks: None,
            lang: None,
            text: None,
        },
        anydoc::model::Block::Paragraph(content) => BlockOut {
            kind: "paragraph",
            level: None,
            anchor: None,
            content: Some(inlines_out(content)),
            list: None,
            table: None,
            blocks: None,
            lang: None,
            text: None,
        },
        anydoc::model::Block::List(inner) => BlockOut {
            kind: "list",
            level: None,
            anchor: None,
            content: None,
            list: Some(list_out(inner)),
            table: None,
            blocks: None,
            lang: None,
            text: None,
        },
        anydoc::model::Block::Table(inner) => BlockOut {
            kind: "table",
            level: None,
            anchor: None,
            content: None,
            list: None,
            table: Some(table_out(inner)),
            blocks: None,
            lang: None,
            text: None,
        },
        anydoc::model::Block::BlockQuote(inner) => BlockOut {
            kind: "block_quote",
            level: None,
            anchor: None,
            content: None,
            list: None,
            table: None,
            blocks: Some(inner.into_iter().map(block_out).collect()),
            lang: None,
            text: None,
        },
        anydoc::model::Block::CodeBlock { lang, text } => BlockOut {
            kind: "code_block",
            level: None,
            anchor: None,
            content: None,
            list: None,
            table: None,
            blocks: None,
            lang,
            text: Some(text),
        },
        anydoc::model::Block::Rule => BlockOut {
            kind: "rule",
            level: None,
            anchor: None,
            content: None,
            list: None,
            table: None,
            blocks: None,
            lang: None,
            text: None,
        },
        anydoc::model::Block::Math(tex) => BlockOut {
            kind: "math",
            level: None,
            anchor: None,
            content: None,
            list: None,
            table: None,
            blocks: None,
            lang: None,
            text: Some(tex),
        },
    }
}

fn inlines_out(items: Vec<anydoc::model::Inline>) -> Vec<InlineOut> {
    items.into_iter().map(inline_out).collect()
}

fn inline_out(inline: anydoc::model::Inline) -> InlineOut {
    match inline {
        anydoc::model::Inline::Text { text, style } => InlineOut {
            kind: "text",
            text: Some(text),
            style: Some(StyleOut {
                bold: style.bold,
                italic: style.italic,
                strike: style.strike,
                code: style.code,
            }),
            content: None,
            target: None,
            alt: None,
            source: None,
            anchor: None,
            note_id: None,
            checked: None,
        },
        anydoc::model::Inline::Link { content, target } => InlineOut {
            kind: "link",
            text: None,
            style: None,
            content: Some(inlines_out(content)),
            target: Some(link_target_out(target)),
            alt: None,
            source: None,
            anchor: None,
            note_id: None,
            checked: None,
        },
        anydoc::model::Inline::Image { alt, source } => InlineOut {
            kind: "image",
            text: None,
            style: None,
            content: None,
            target: None,
            alt: Some(alt),
            source: Some(image_source_out(source)),
            anchor: None,
            note_id: None,
            checked: None,
        },
        anydoc::model::Inline::Anchor(id) => InlineOut {
            kind: "anchor",
            text: None,
            style: None,
            content: None,
            target: None,
            alt: None,
            source: None,
            anchor: Some(id),
            note_id: None,
            checked: None,
        },
        anydoc::model::Inline::NoteRef(id) => InlineOut {
            kind: "note_ref",
            text: None,
            style: None,
            content: None,
            target: None,
            alt: None,
            source: None,
            anchor: None,
            note_id: Some(id),
            checked: None,
        },
        anydoc::model::Inline::LineBreak => InlineOut {
            kind: "line_break",
            text: None,
            style: None,
            content: None,
            target: None,
            alt: None,
            source: None,
            anchor: None,
            note_id: None,
            checked: None,
        },
        anydoc::model::Inline::Math(tex) => InlineOut {
            kind: "math",
            text: Some(tex),
            style: None,
            content: None,
            target: None,
            alt: None,
            source: None,
            anchor: None,
            note_id: None,
            checked: None,
        },
        anydoc::model::Inline::Checkbox(checked) => InlineOut {
            kind: "checkbox",
            text: None,
            style: None,
            content: None,
            target: None,
            alt: None,
            source: None,
            anchor: None,
            note_id: None,
            checked: Some(checked),
        },
    }
}

fn link_target_out(target: anydoc::model::LinkTarget) -> LinkTargetOut {
    match target {
        anydoc::model::LinkTarget::External(value) => LinkTargetOut { kind: "external", value },
        anydoc::model::LinkTarget::Relative(value) => LinkTargetOut { kind: "relative", value },
        anydoc::model::LinkTarget::Anchor(value) => LinkTargetOut { kind: "anchor", value },
    }
}

fn image_source_out(source: anydoc::model::ImageSource) -> ImageSourceOut {
    match source {
        anydoc::model::ImageSource::External(url) => {
            ImageSourceOut { kind: "external", url: Some(url), asset_id: None }
        }
        anydoc::model::ImageSource::Asset(id) => {
            ImageSourceOut { kind: "asset", url: None, asset_id: Some(id.0) }
        }
        anydoc::model::ImageSource::Unavailable => {
            ImageSourceOut { kind: "unavailable", url: None, asset_id: None }
        }
    }
}

fn list_out(list: anydoc::model::List) -> ListOut {
    ListOut {
        marker: match list.marker {
            anydoc::model::MarkerKind::Bullet => "bullet",
            anydoc::model::MarkerKind::Decimal => "decimal",
            anydoc::model::MarkerKind::LowerAlpha => "lower_alpha",
            anydoc::model::MarkerKind::UpperAlpha => "upper_alpha",
            anydoc::model::MarkerKind::LowerRoman => "lower_roman",
            anydoc::model::MarkerKind::UpperRoman => "upper_roman",
        },
        start: list.start,
        items: list
            .items
            .into_iter()
            .map(|item| ListItemOut {
                blocks: item.blocks.into_iter().map(block_out).collect(),
                marker_label: item.marker_label,
            })
            .collect(),
    }
}

fn table_out(table: anydoc::model::Table) -> TableOut {
    TableOut {
        grid: table
            .grid
            .into_iter()
            .map(|row| row.into_iter().map(cell_slot_out).collect())
            .collect(),
        header_rows: table.header_rows,
        kind: match table.kind {
            anydoc::model::TableKind::Data => "data",
            anydoc::model::TableKind::Layout => "layout",
        },
    }
}

fn cell_slot_out(slot: anydoc::model::CellSlot) -> CellSlotOut {
    match slot {
        anydoc::model::CellSlot::Origin(cell) => CellSlotOut {
            kind: "origin",
            cell: Some(CellOut {
                blocks: cell.blocks.into_iter().map(block_out).collect(),
                col_span: cell.col_span,
                row_span: cell.row_span,
            }),
            origin_row: None,
            origin_col: None,
        },
        anydoc::model::CellSlot::Covered { origin_row, origin_col } => CellSlotOut {
            kind: "covered",
            cell: None,
            origin_row: Some(origin_row),
            origin_col: Some(origin_col),
        },
    }
}

fn note_out(note: anydoc::model::Note) -> NoteOut {
    NoteOut {
        id: note.id,
        kind: match note.kind {
            anydoc::model::NoteKind::Footnote => "footnote",
            anydoc::model::NoteKind::Endnote => "endnote",
        },
        blocks: note.blocks.into_iter().map(block_out).collect(),
    }
}

fn asset_out(asset: anydoc::model::Asset) -> AssetOut {
    AssetOut {
        id: asset.id.0,
        media_type: asset.media_type,
        origin_part: asset.origin_part,
        data: STANDARD.encode(&asset.bytes),
    }
}
