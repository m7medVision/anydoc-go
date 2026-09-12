package model

// AnchorID is a normalized, document-scoped anchor id. Frontends that
// concatenate multiple source parts (EPUB chapters) scope ids during
// normalization so they stay unique across the whole document.
type AnchorID = string

// LinkTarget is where a link points: a sealed sum mirroring anydoc's
// LinkTarget enum.
type LinkTarget interface {
	isLinkTarget()

	// IsEmpty reports whether the target string is empty, whichever kind it
	// is.
	IsEmpty() bool
}

// ExternalLink points at an absolute URL with a scheme.
type ExternalLink struct {
	URL string
}

// RelativeLink is a scheme-less relative reference, preserved as written.
type RelativeLink struct {
	URL string
}

// AnchorLink is an internal target: a heading anchor or an Anchor node.
type AnchorLink struct {
	ID AnchorID
}

func (ExternalLink) isLinkTarget() {}
func (RelativeLink) isLinkTarget() {}
func (AnchorLink) isLinkTarget()   {}

func (t ExternalLink) IsEmpty() bool { return t.URL == "" }
func (t RelativeLink) IsEmpty() bool { return t.URL == "" }
func (t AnchorLink) IsEmpty() bool   { return t.ID == "" }

// ImageSource is where an image's bytes live: a sealed sum mirroring
// anydoc's ImageSource enum.
type ImageSource interface {
	isImageSource()
}

// ExternalImage loads the image from an absolute URL with a scheme.
type ExternalImage struct {
	URL string
}

// AssetImage is an embedded image stored in Document.Assets.
type AssetImage struct {
	Asset AssetID
}

// UnavailableImage means there is no usable source: the image's part is
// missing or unreadable and it has no URL. Only the alt text remains.
type UnavailableImage struct{}

func (ExternalImage) isImageSource()    {}
func (AssetImage) isImageSource()       {}
func (UnavailableImage) isImageSource() {}
