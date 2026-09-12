package model

// AssetID indexes into Document.Assets.
type AssetID int

// Asset is an embedded binary asset (image, object payload). Bytes are
// always retained so the document stays self-contained; total retained
// bytes are capped by the fixed max_asset_total_bytes limit at parse time.
type Asset struct {
	// ID is this asset's own index, so a detached Asset still identifies
	// itself.
	ID AssetID
	// MediaType is the MIME type, e.g. "image/png".
	MediaType string
	// OriginPart is the package part or stream the asset came from, for
	// provenance.
	OriginPart string
	// Bytes is the payload, exactly as stored in the source.
	Bytes []byte
}
