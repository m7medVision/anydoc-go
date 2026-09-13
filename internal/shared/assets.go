// Port of src/shared/assets.rs: embedded-asset accumulation with the fixed
// retained-bytes cap.

package shared

import (
	"strings"

	"github.com/m7medVision/anydoc-go/internal/cerr"
	"github.com/m7medVision/anydoc-go/internal/model"
	pkg "github.com/m7medVision/anydoc-go/internal/package"
)

// AssetSink retains embedded asset bytes under the fixed retained-bytes cap.
type AssetSink struct {
	Assets []model.Asset
	// byPart maps origin part -> retained asset, so repeated references to
	// the same part share one asset instead of duplicating its bytes (and
	// falsely accumulating against the cap).
	byPart map[string]model.AssetID
	total  int
}

// NewAssetSink returns an empty sink.
func NewAssetSink() *AssetSink {
	return &AssetSink{}
}

// Add retains an asset's bytes (copied only when the part is not already
// retained). Crossing the retained-bytes cap is a hard error.
func (s *AssetSink) Add(mediaType, originPart string, b []byte) (model.AssetID, error) {
	if s.byPart == nil {
		s.byPart = make(map[string]model.AssetID)
	}
	if id, ok := s.byPart[originPart]; ok {
		return id, nil
	}
	s.total += len(b)
	if s.total > pkg.MaxAssetTotalBytes {
		return 0, &cerr.Error{
			Kind:   cerr.KindResourceLimit,
			Limit:  "max_asset_total_bytes",
			Detail: "embedded assets exceed the retained-bytes cap",
		}
	}
	id := model.AssetID(len(s.Assets))
	s.byPart[originPart] = id
	copied := append([]byte(nil), b...)
	s.Assets = append(s.Assets, model.Asset{
		ID:         id,
		MediaType:  mediaType,
		OriginPart: originPart,
		Bytes:      copied,
	})
	return id, nil
}

// RelImageSource resolves an image relationship to its source.
// External-mode relationships carry the image's URL directly; internal-mode
// targets load from the package and are retained as assets. Failures
// degrade to ok=false per the unified policy; fatal errors propagate.
//
// Upstream takes RefCell wrappers; Go uses pointers to the same values.
func RelImageSource(p *pkg.Package, rels *pkg.Relationships, basePart string, assets *AssetSink, relID string) (model.ImageSource, bool, error) {
	rel, ok := rels.Get(relID)
	if !ok {
		return nil, false, nil
	}
	if rel.Mode == pkg.TargetModeExternal {
		if rel.Target == "" {
			return nil, false, nil
		}
		return model.ExternalImage{URL: rel.Target}, true, nil
	}
	part, data, ok, err := pkg.RelTargetBytes(p, rels, basePart, relID)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}
	id, err := assets.Add(MediaTypeFor(part), part, data)
	if err != nil {
		return nil, false, err
	}
	return model.AssetImage{Asset: id}, true, nil
}

// MediaTypeFor is the MIME type from a part path's extension.
func MediaTypeFor(part string) string {
	ext := ""
	if i := strings.LastIndexByte(part, '.'); i >= 0 {
		ext = asciiLower(part[i+1:])
	}
	switch ext {
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	case "bmp":
		return "image/bmp"
	case "tif", "tiff":
		return "image/tiff"
	case "svg":
		return "image/svg+xml"
	case "emf":
		return "image/emf"
	case "wmf":
		return "image/wmf"
	case "webp":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}
