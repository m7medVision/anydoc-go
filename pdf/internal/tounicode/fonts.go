// FontCMaps: collection of ToUnicode CMaps indexed by object number, plus
// the subset-remap heuristics and embedded-font fallbacks.
//
// Port of pdf-inspector v1.14.2 src/tounicode.rs (FontCMaps and friends).
package tounicode

import (
	"slices"

	"github.com/m7medVision/anydoc-go/pdf/internal/korea1"
	"github.com/m7medVision/anydoc-go/pdf/internal/objects"
)

// FontCMaps is a collection of ToUnicode CMaps indexed by ToUnicode stream
// object number.
type FontCMaps struct {
	byObjNum map[uint32]*CMapEntry
}

// CMapEntry is a primary CMap plus optional alternative variants.
type CMapEntry struct {
	Primary  ToUnicodeCMap
	Remapped *ToUnicodeCMap
	Fallback *ToUnicodeCMap
}

// FromDoc builds FontCMaps from a document object model, iterating every page
// and collecting fonts (including Form XObject fonts).
func FromDoc(doc *objects.Document) *FontCMaps {
	return fromDocPagesInner(doc, nil, false)
}

// FromDocPages builds FontCMaps for specific pages only. Pass a nil filter
// for all pages.
func FromDocPages(doc *objects.Document, pageFilter map[uint32]struct{}) *FontCMaps {
	return fromDocPagesInner(doc, pageFilter, false)
}

// FromDocPagesFast builds FontCMaps in fast mode: expensive TrueType font
// fallback parsing is skipped. Fonts that can't be decoded from their
// ToUnicode CMap alone will be missing, causing text extraction to produce
// empty/garbage text which triggers the needs_ocr fallback. This is ideal
// for hybrid OCR pipelines where GPU OCR is always available as a fallback.
func FromDocPagesFast(doc *objects.Document, pageFilter map[uint32]struct{}) *FontCMaps {
	return fromDocPagesInner(doc, pageFilter, true)
}

// NewFontCMaps is the FontCMaps::default() constructor.
func NewFontCMaps() *FontCMaps {
	return &FontCMaps{byObjNum: map[uint32]*CMapEntry{}}
}

// GetByObj returns the CMap for a ToUnicode (or font file) object number.
func (fc *FontCMaps) GetByObj(objNum uint32) (*CMapEntry, bool) {
	e, ok := fc.byObjNum[objNum]
	return e, ok
}

func fromDocPagesInner(doc *objects.Document, pageFilter map[uint32]struct{}, skipTruetypeFallback bool) *FontCMaps {
	fc := NewFontCMaps()

	pages := doc.GetPages()
	for _, pageNum := range pages.Numbers() {
		if pageFilter != nil {
			if _, ok := pageFilter[pageNum]; !ok {
				continue
			}
		}
		pageID, ok := pages.Get(pageNum)
		if !ok {
			continue
		}
		// Page-level fonts (includes inherited parent resources)
		if fonts, err := doc.GetPageFonts(pageID); err == nil {
			collectCmapsFromFontsInner(fonts, doc, fc.byObjNum, skipTruetypeFallback)
		}

		if !skipTruetypeFallback {
			// Fonts inside Form XObjects referenced by this page
			collectCmapsFromXobjects(doc, pageID, fc.byObjNum)
		}
	}

	return fc
}

// collectCmapsFromFonts parses ToUnicode CMaps from a set of font
// dictionaries. Also handles Identity-H/V CID fonts without ToUnicode by
// parsing the embedded TrueType cmap from FontFile2.
func collectCmapsFromFonts(fonts *objects.NameDictMap, doc *objects.Document, byObjNum map[uint32]*CMapEntry) {
	collectCmapsFromFontsInner(fonts, doc, byObjNum, false)
}

func collectCmapsFromFontsInner(fonts *objects.NameDictMap, doc *objects.Document, byObjNum map[uint32]*CMapEntry, skipTruetypeFallback bool) {
	// First pass: collect ToUnicode CMaps
	fonts.Range(func(_ []byte, fontDict *objects.Dictionary) bool {
		tuObj, err := fontDict.Get([]byte("ToUnicode"))
		if err != nil {
			return true
		}
		objRef, err := tuObj.AsReference()
		if err != nil {
			return true
		}
		objNum := objRef.Num
		if _, ok := byObjNum[objNum]; ok {
			return true
		}
		obj, err := doc.GetObject(objRef)
		if err != nil {
			return true
		}
		stream, err := obj.AsStream()
		if err != nil {
			return true
		}
		data, err := stream.DecompressedContent()
		if err != nil {
			data = stream.Content
		}
		cmap, ok := Parse(data)
		if !ok {
			// ToUnicode present but parse failed; try fallbacks to avoid
			// empty decoding.
			var fallback (ToUnicodeCMap)
			haveFallback := false
			if skipTruetypeFallback {
				if fb, ok := buildFallbackCmapForSimple(fontDict, doc); ok {
					fallback, haveFallback = fb, true
				}
			} else if fb, ok := buildFallbackCmapForType0(fontDict, doc); ok {
				fallback, haveFallback = fb, true
			} else if fb, ok := buildFallbackCmapForSimple(fontDict, doc); ok {
				fallback, haveFallback = fb, true
			}
			if haveFallback {
				byObjNum[objNum] = &CMapEntry{Primary: fallback}
			}
			return true
		}

		primary, remapped := tryRemapSubsetCmap(cmap, fontDict, doc, objNum)

		// Only build expensive fallbacks when the primary CMap is sparse.
		// buildFallbackCmapForType0 can take seconds on large embedded
		// TrueType fonts (decompressing + parsing 100K+ byte font files).
		// Skip entirely when the primary CMap is sufficient.
		primaryEntries := len(primary.CharMap) + len(primary.Ranges)
		var fallback *ToUnicodeCMap
		switch {
		case primaryEntries < 10 && !skipTruetypeFallback:
			// Try cheap fallback first; only attempt expensive TrueType
			// parsing if cheap fallbacks don't yield results.
			cheap, okCheap := buildFallbackTounicodeFromEncoding(fontDict, doc)
			if !okCheap {
				cheap, okCheap = buildFallbackCmapForSimple(fontDict, doc)
			}
			if okCheap {
				fallback = &cheap
			} else if fb, ok := buildFallbackCmapForType0(fontDict, doc); ok {
				fallback = &fb
			}
		case primaryEntries < 10:
			// Fast mode: only try cheap fallbacks, skip TrueType parsing.
			// Regions using this font will get needs_ocr=true.
			if fb, ok := buildFallbackTounicodeFromEncoding(fontDict, doc); ok {
				fallback = &fb
			} else if fb, ok := buildFallbackCmapForSimple(fontDict, doc); ok {
				fallback = &fb
			}
		default:
			// Primary is rich enough; only try the cheap encoding fallback.
			if fb, ok := buildFallbackTounicodeFromEncoding(fontDict, doc); ok {
				fallback = &fb
			}
		}

		if primaryEntries < 10 && fallback != nil {
			remapped = &primary
			primary = *fallback
			fallback = nil
		}

		byObjNum[objNum] = &CMapEntry{Primary: primary, Remapped: remapped, Fallback: fallback}
		return true
	})

	// Second pass: Identity-H/V fonts without ToUnicode.
	// Try: (1) embedded TrueType/OpenType cmap, (2) predefined CID→Unicode
	// mapping. Skip entirely in fast mode — these fonts require expensive
	// TrueType parsing.
	if skipTruetypeFallback {
		return
	}
	fonts.Range(func(_ []byte, fontDict *objects.Dictionary) bool {
		if _, err := fontDict.Get([]byte("ToUnicode")); err == nil {
			return true
		}
		encObj, err := fontDict.Get([]byte("Encoding"))
		if err != nil {
			return true
		}
		encoding, err := encObj.AsName()
		if err != nil {
			return true
		}
		if string(encoding) != "Identity-H" && string(encoding) != "Identity-V" {
			return true
		}
		// Navigate: DescendantFonts[0]
		descFontsObj, err := fontDict.Get([]byte("DescendantFonts"))
		if err != nil {
			return true
		}
		var descFonts []objects.Object
		switch descFontsObj.Kind {
		case objects.KindArray:
			descFonts = descFontsObj.Array
		case objects.KindReference:
			obj, err := doc.GetObject(descFontsObj.Ref)
			if err != nil || obj.Kind != objects.KindArray {
				return true
			}
			descFonts = obj.Array
		default:
			return true
		}
		if len(descFonts) == 0 {
			return true
		}
		var cidFontDict *objects.Dictionary
		switch descFonts[0].Kind {
		case objects.KindReference:
			d, err := doc.GetDictionary(descFonts[0].Ref)
			if err != nil {
				return true
			}
			cidFontDict = d
		case objects.KindDictionary:
			cidFontDict = descFonts[0].Dict
		default:
			return true
		}

		// Try to build CMap from embedded font (FontFile2 or FontFile3)
		var fontDescriptor *objects.Dictionary
		if fdObj, err := fontDict2(cidFontDict, doc, []byte("FontDescriptor")); err == nil {
			fontDescriptor = fdObj
		}

		resolved := false

		// Determine the font file reference (FontFile2 or FontFile3)
		var fontFileRef *objects.ObjectId
		if fontDescriptor != nil {
			if o, err := fontDescriptor.Get([]byte("FontFile2")); err == nil {
				if r, err := o.AsReference(); err == nil {
					fontFileRef = &r
				}
			}
			if fontFileRef == nil {
				if o, err := fontDescriptor.Get([]byte("FontFile3")); err == nil {
					if r, err := o.AsReference(); err == nil {
						fontFileRef = &r
					}
				}
			}
		}

		// The lookup key must match what get_font_file2_obj_num() returns:
		// font file obj_num if present, else CIDFont dict obj_num
		lookupKey := uint32(0)
		if fontFileRef != nil {
			lookupKey = fontFileRef.Num
		} else if descFonts[0].Kind == objects.KindReference {
			lookupKey = descFonts[0].Ref.Num
		}
		if lookupKey == 0 {
			return true
		}
		if _, ok := byObjNum[lookupKey]; ok {
			return true
		}

		// Try parsing embedded TrueType/OpenType cmap
		if fontFileRef != nil {
			if obj, err := doc.GetObject(*fontFileRef); err == nil {
				if stream, err := obj.AsStream(); err == nil {
					data, err := stream.DecompressedContent()
					if err != nil {
						data = stream.Content
					}
					if cmap, ok := BuildCmapFromTrueType(data); ok {
						byObjNum[lookupKey] = &CMapEntry{Primary: cmap}
						resolved = true
					}
				}
			}
		}

		// Fallback: predefined CID→Unicode mapping from CIDSystemInfo
		if !resolved {
			if cmap, ok := buildCmapFromCidSystemInfo(cidFontDict, doc); ok {
				byObjNum[lookupKey] = &CMapEntry{Primary: cmap}
				resolved = true
			}
		}

		// Last resort: CID-as-Unicode passthrough.
		// Many PDF generators (Chromium, wkhtmltopdf) use Identity-H encoding
		// where CID values ARE Unicode codepoints, but strip the cmap table
		// and omit ToUnicode. We detect this by checking the /W (widths)
		// array: if CID values fall in typical Unicode letter/digit ranges
		// (0x41+), CIDs are likely Unicode. If CIDs are low values (< 0x41),
		// they're GIDs in a subset font.
		if !resolved {
			if CidValuesLookLikeUnicode(cidFontDict) {
				cmap := NewCMap()
				cmap.CodeByteLength = 2
				cmap.CIDPassthrough = true
				byObjNum[lookupKey] = &CMapEntry{Primary: cmap}
			}
		}
		return true
	})

	// Third pass: simple fonts without ToUnicode (use embedded font cmap as
	// fallback)
	fonts.Range(func(_ []byte, fontDict *objects.Dictionary) bool {
		if _, err := fontDict.Get([]byte("ToUnicode")); err == nil {
			return true
		}
		// Skip fonts with explicit encoding — they can be decoded by the
		// standard encoding path and don't need a fallback CMap.
		if encObj, err := fontDict.Get([]byte("Encoding")); err == nil {
			if _, err := encObj.AsName(); err == nil {
				return true
			}
			if _, err := encObj.AsDict(); err == nil {
				return true
			}
			if _, err := encObj.AsReference(); err == nil {
				return true
			}
		}
		subtypeObj, err := fontDict.Get([]byte("Subtype"))
		if err != nil {
			return true
		}
		subtype, err := subtypeObj.AsName()
		if err != nil {
			return true
		}
		if string(subtype) == "Type0" {
			return true
		}

		var fontDescriptor *objects.Dictionary
		if fdObj, err := fontDict2(fontDict, doc, []byte("FontDescriptor")); err == nil {
			fontDescriptor = fdObj
		}
		var ffRef *objects.ObjectId
		if fontDescriptor != nil {
			if o, err := fontDescriptor.Get([]byte("FontFile2")); err == nil {
				if r, err := o.AsReference(); err == nil {
					ffRef = &r
				}
			}
			if ffRef == nil {
				if o, err := fontDescriptor.Get([]byte("FontFile3")); err == nil {
					if r, err := o.AsReference(); err == nil {
						ffRef = &r
					}
				}
			}
		}
		if ffRef == nil {
			return true
		}
		lookupKey := ffRef.Num
		if _, ok := byObjNum[lookupKey]; ok {
			return true
		}
		if obj, err := doc.GetObject(*ffRef); err == nil {
			if stream, err := obj.AsStream(); err == nil {
				if data, err := stream.DecompressedContent(); err == nil {
					if cmap, ok := buildSimpleCmapFromTrueType(data); ok {
						byObjNum[lookupKey] = &CMapEntry{Primary: cmap}
					}
				}
			}
		}
		return true
	})
}

// fontDict2 resolves a dictionary-valued key that may be a direct dictionary
// or an indirect reference (mirrors the FontDescriptor lookups upstream).
func fontDict2(dict *objects.Dictionary, doc *objects.Document, key []byte) (*objects.Dictionary, error) {
	obj, err := dict.Get(key)
	if err != nil {
		return nil, err
	}
	if obj.Kind == objects.KindReference {
		return doc.GetDictionary(obj.Ref)
	}
	return obj.AsDict()
}

// collectCmapsFromXobjects walks Form XObjects in a page's resources and
// collects their font CMaps.
func collectCmapsFromXobjects(doc *objects.Document, pageID objects.ObjectId, byObjNum map[uint32]*CMapEntry) {
	resourceDict, resourceIDs, err := doc.GetPageResources(pageID)
	if err != nil {
		return
	}

	visited := map[objects.ObjectId]struct{}{}

	if resourceDict != nil {
		walkXobjectFonts(resourceDict, doc, byObjNum, visited)
	}
	for _, resourceID := range resourceIDs {
		if resources, err := doc.GetDictionary(resourceID); err == nil {
			walkXobjectFonts(resources, doc, byObjNum, visited)
		}
	}
}

// walkXobjectFonts recursively collects font CMaps from XObjects in a
// resource dictionary.
func walkXobjectFonts(resources *objects.Dictionary, doc *objects.Document, byObjNum map[uint32]*CMapEntry, visited map[objects.ObjectId]struct{}) {
	xobjObj, err := resources.Get([]byte("XObject"))
	if err != nil {
		return
	}
	var xobjectDict *objects.Dictionary
	switch xobjObj.Kind {
	case objects.KindReference:
		if obj, err := doc.GetObject(xobjObj.Ref); err == nil {
			if d, err := obj.AsDict(); err == nil {
				xobjectDict = d
			}
		}
	case objects.KindDictionary:
		xobjectDict = xobjObj.Dict
	}
	if xobjectDict == nil {
		return
	}

	xobjectDict.Range(func(_ []byte, value *objects.Object) bool {
		if value.Kind != objects.KindReference {
			return true
		}
		id := value.Ref
		if _, seen := visited[id]; seen {
			return true
		}
		visited[id] = struct{}{}
		obj, err := doc.GetObject(id)
		if err != nil {
			return true
		}
		stream, err := obj.AsStream()
		if err != nil {
			return true
		}
		subtypeObj, err := stream.Dict.Get([]byte("Subtype"))
		if err != nil {
			return true
		}
		subtype, err := subtypeObj.AsName()
		if err != nil || string(subtype) != "Form" {
			return true
		}
		// Collect fonts from this Form XObject's Resources
		resObj, err := stream.Dict.Get([]byte("Resources"))
		if err != nil {
			return true
		}
		formResources, err := resObj.AsDict()
		if err != nil {
			return true
		}
		// Extract font dict from the Form's resources
		fontObj, err := formResources.Get([]byte("Font"))
		if err == nil {
			var fontDict *objects.Dictionary
			switch fontObj.Kind {
			case objects.KindReference:
				if o, err := doc.GetObject(fontObj.Ref); err == nil {
					if d, err := o.AsDict(); err == nil {
						fontDict = d
					}
				}
			case objects.KindDictionary:
				fontDict = fontObj.Dict
			}
			if fontDict != nil {
				fonts := objects.NewNameDictMap()
				fontDict.Range(func(name []byte, v *objects.Object) bool {
					var font *objects.Dictionary
					switch v.Kind {
					case objects.KindReference:
						if d, err := doc.GetDictionary(v.Ref); err == nil {
							font = d
						}
					case objects.KindDictionary:
						font = v.Dict
					}
					if font != nil {
						fonts.Insert(name, font)
					}
					return true
				})
				collectCmapsFromFonts(fonts, doc, byObjNum)
			}
		}
		// Recurse into nested XObjects
		walkXobjectFonts(formResources, doc, byObjNum, visited)
		return true
	})
}

// BuildCmapEntryFromStream builds a CMapEntry from raw ToUnicode stream data.
// Used by the XObject walker for streams outside the page font dict.
//
// Upstream: pub(crate) fn build_cmap_entry_from_stream.
func BuildCmapEntryFromStream(data []byte, fontDict *objects.Dictionary, doc *objects.Document, objNum uint32) (*CMapEntry, bool) {
	if cmap, ok := Parse(data); ok {
		primary, remapped := tryRemapSubsetCmap(cmap, fontDict, doc, objNum)
		var fallback (ToUnicodeCMap)
		haveFallback := false
		if fb, ok := buildFallbackTounicodeFromEncoding(fontDict, doc); ok {
			fallback, haveFallback = fb, true
		} else if fb, ok := buildFallbackCmapForType0(fontDict, doc); ok {
			fallback, haveFallback = fb, true
		} else if fb, ok := buildFallbackCmapForSimple(fontDict, doc); ok {
			fallback, haveFallback = fb, true
		}

		primaryEntries := len(primary.CharMap) + len(primary.Ranges)
		var fallbackPtr *ToUnicodeCMap
		if haveFallback {
			fallbackPtr = &fallback
		}
		if primaryEntries < 10 && fallbackPtr != nil {
			remapped = &primary
			primary = *fallbackPtr
			fallbackPtr = nil
		}

		// When a sequential remap was applied and a TrueType fallback has
		// more entries than the primary ToUnicode CMap, prefer the TrueType
		// cmap. Subset fonts number GIDs by document encounter order, so the
		// sorted sequential remap scrambles characters. The TrueType cmap
		// table maps the real GID→Unicode and is authoritative.
		if remapped != nil && fallbackPtr != nil {
			fbEntries := len(fallbackPtr.CharMap) + len(fallbackPtr.Ranges)
			if fbEntries > primaryEntries {
				oldRemap := remapped
				remapped = fallbackPtr
				fallbackPtr = oldRemap
			}
		}

		return &CMapEntry{Primary: primary, Remapped: remapped, Fallback: fallbackPtr}, true
	}

	var fallback (ToUnicodeCMap)
	haveFallback := false
	if fb, ok := buildFallbackCmapForType0(fontDict, doc); ok {
		fallback, haveFallback = fb, true
	} else if fb, ok := buildFallbackCmapForSimple(fontDict, doc); ok {
		fallback, haveFallback = fb, true
	}
	if !haveFallback {
		return nil, false
	}
	return &CMapEntry{Primary: fallback}, true
}

// getDescendantCidFont navigates to the first DescendantFont dictionary of a
// Type0 font.
func getDescendantCidFont(fontDict *objects.Dictionary, doc *objects.Document) (*objects.Dictionary, bool) {
	descFontsObj, err := fontDict.Get([]byte("DescendantFonts"))
	if err != nil {
		return nil, false
	}
	var arr []objects.Object
	switch descFontsObj.Kind {
	case objects.KindArray:
		arr = descFontsObj.Array
	case objects.KindReference:
		obj, err := doc.GetObject(descFontsObj.Ref)
		if err != nil || obj.Kind != objects.KindArray {
			return nil, false
		}
		arr = obj.Array
	default:
		return nil, false
	}
	if len(arr) == 0 {
		return nil, false
	}
	switch arr[0].Kind {
	case objects.KindReference:
		d, err := doc.GetDictionary(arr[0].Ref)
		if err != nil {
			return nil, false
		}
		return d, true
	case objects.KindDictionary:
		return arr[0].Dict, true
	}
	return nil, false
}

// getWArrayStartCid gets the starting CID from a CIDFont's W (widths) array.
func getWArrayStartCid(cidFontDict *objects.Dictionary, doc *objects.Document) (uint16, bool) {
	wObj, err := cidFontDict.Get([]byte("W"))
	if err != nil {
		return 0, false
	}
	var arr []objects.Object
	switch wObj.Kind {
	case objects.KindArray:
		arr = wObj.Array
	case objects.KindReference:
		obj, err := doc.GetObject(wObj.Ref)
		if err != nil || obj.Kind != objects.KindArray {
			return 0, false
		}
		arr = obj.Array
	default:
		return 0, false
	}
	if len(arr) == 0 {
		return 0, false
	}
	resolveInt := func(o *objects.Object) (int64, bool) {
		if o.Kind == objects.KindInteger {
			return o.Int, true
		}
		if o.Kind == objects.KindReference {
			if obj, err := doc.GetObject(o.Ref); err == nil && obj.Kind == objects.KindInteger {
				return obj.Int, true
			}
		}
		return 0, false
	}
	if n, ok := resolveInt(&arr[0]); ok {
		return uint16(n), true // Rust `as u16` wraps; uint16(n) matches
	}
	return 0, false
}

// wArrayCoversCid reports whether the CIDFont's W (widths) array explicitly
// covers the given CID.
//
// The W array uses two formats (PDF 32000-1:2008, §9.7.4.3):
//
//  1. `c [w1 w2 ... wn]` — widths for CIDs c, c+1, ..., c+n-1
//  2. `c_first c_last w` — CIDs c_first..c_last all have width w
func wArrayCoversCid(cidFontDict *objects.Dictionary, doc *objects.Document, target uint16) bool {
	wObj, err := cidFontDict.Get([]byte("W"))
	if err != nil {
		return false
	}
	var arr []objects.Object
	switch wObj.Kind {
	case objects.KindArray:
		arr = wObj.Array
	case objects.KindReference:
		obj, err := doc.GetObject(wObj.Ref)
		if err != nil || obj.Kind != objects.KindArray {
			return false
		}
		arr = obj.Array
	default:
		return false
	}

	resolveInt := func(o *objects.Object) (int64, bool) {
		if o.Kind == objects.KindInteger {
			return o.Int, true
		}
		if o.Kind == objects.KindReference {
			if obj, err := doc.GetObject(o.Ref); err == nil && obj.Kind == objects.KindInteger {
				return obj.Int, true
			}
		}
		return 0, false
	}

	resolveArr := func(o *objects.Object) ([]objects.Object, bool) {
		if o.Kind == objects.KindArray {
			return o.Array, true
		}
		if o.Kind == objects.KindReference {
			if obj, err := doc.GetObject(o.Ref); err == nil && obj.Kind == objects.KindArray {
				return obj.Array, true
			}
		}
		return nil, false
	}

	t := int64(target)
	i := 0
	for i < len(arr) {
		first, ok := resolveInt(&arr[i])
		if !ok {
			break
		}
		i++
		if i >= len(arr) {
			break
		}
		// Peek at arr[i] to decide format.
		if widths, ok := resolveArr(&arr[i]); ok {
			// Format 1: c [w1 ... wn]
			last := first + int64(len(widths)) - 1
			if t >= first && t <= last {
				return true
			}
			i++
		} else if last, ok := resolveInt(&arr[i]); ok {
			// Format 2: c_first c_last w
			i++
			if i < len(arr) {
				i++ // skip the width value
			}
			if t >= first && t <= last {
				return true
			}
		} else {
			// Unknown token — abort parsing safely
			break
		}
	}
	return false
}

// getCidToGidMap extracts CIDToGIDMap as a slice of GIDs indexed by CID.
func getCidToGidMap(cidFontDict *objects.Dictionary, doc *objects.Document) ([]uint16, bool) {
	obj, err := cidFontDict.Get([]byte("CIDToGIDMap"))
	if err != nil {
		return nil, false
	}
	switch obj.Kind {
	case objects.KindName:
		if string(obj.Name) == "Identity" {
			return nil, false
		}
		return nil, false
	case objects.KindReference:
		if o, err := doc.GetObject(obj.Ref); err == nil && o.Kind == objects.KindStream {
			data, err := o.Stream.DecompressedContent()
			if err != nil {
				return nil, false
			}
			return parseCidToGidStream(data)
		}
		return nil, false
	case objects.KindStream:
		data, err := obj.Stream.DecompressedContent()
		if err != nil {
			return nil, false
		}
		return parseCidToGidStream(data)
	}
	return nil, false
}

func parseCidToGidStream(data []byte) ([]uint16, bool) {
	if len(data) < 2 {
		return nil, false
	}
	m := make([]uint16, 0, len(data)/2)
	for i := 0; i+2 <= len(data); i += 2 {
		m = append(m, uint16(data[i])<<8|uint16(data[i+1]))
	}
	return m, true
}

// buildCmapWithCidToGidMap builds a CID→Unicode CMap by applying a
// CIDToGIDMap to an existing CMap that maps GID→Unicode.
func buildCmapWithCidToGidMap(cmap ToUnicodeCMap, cidToGid []uint16) (ToUnicodeCMap, bool) {
	newCmap := NewCMap()
	for cid, gid := range cidToGid {
		if s, ok := cmap.Lookup(gid); ok {
			newCmap.CharMap[uint16(cid)] = s
		}
	}
	if len(newCmap.CharMap) == 0 {
		return ToUnicodeCMap{}, false
	}
	newCmap.CodeByteLength = 2
	return newCmap, true
}

// tryRemapSubsetCmap detects and fixes broken ToUnicode CMaps from subset
// fonts with GID mismatch.
//
// Some PDF generators subset-embed fonts by renumbering GIDs sequentially
// (1, 2, 3...) but fail to update the ToUnicode CMap, which still references
// original GID values. This detects the mismatch and remaps the CMap to
// sequential positions.
//
// Returns the (possibly replaced) primary CMap and an optional remapped
// variant.
func tryRemapSubsetCmap(cmap ToUnicodeCMap, fontDict *objects.Dictionary, doc *objects.Document, objNum uint32) (ToUnicodeCMap, *ToUnicodeCMap) {
	// Only applies to Identity-H/V CID fonts
	encObj, err := fontDict.Get([]byte("Encoding"))
	if err != nil {
		return cmap, nil
	}
	encoding, err := encObj.AsName()
	if err != nil {
		return cmap, nil
	}
	if string(encoding) != "Identity-H" && string(encoding) != "Identity-V" {
		return cmap, nil
	}

	// CMap's minimum source CID must be > 2 (indicating old, non-sequential
	// GIDs)
	minCid, ok := cmap.MinSourceCID()
	if !ok || minCid <= 2 {
		return cmap, nil
	}

	// Navigate to DescendantFonts[0]
	cidFontDict, ok := getDescendantCidFont(fontDict, doc)
	if !ok {
		return cmap, nil
	}

	// Both repair paths below assume CIDs are glyph indices that a subsetter
	// can renumber, which is only true for CIDFontType2 (TrueType). For
	// CIDFontType0 (CFF), CIDs are resolved through the CFF charset, so a
	// valid CMap stays valid after subsetting and renumbering it corrupts
	// otherwise-correct text. CIDToGIDMap is likewise CIDFontType2-only (PDF
	// 32000-1:2008, 9.7.4.2), so this also ignores a CIDToGIDMap that a
	// malformed producer attached to a CFF font. /Subtype may be an indirect
	// reference, so resolve it through the document. Only bail out when the
	// descendant is *explicitly* something other than CIDFontType2: a
	// missing or unresolvable /Subtype keeps the previous behaviour rather
	// than silently disabling the repair.
	if subtypeObj, err := cidFontDict.Get([]byte("Subtype")); err == nil {
		var subtype []byte
		switch subtypeObj.Kind {
		case objects.KindReference:
			if o, err := doc.GetObject(subtypeObj.Ref); err == nil {
				if n, err := o.AsName(); err == nil {
					subtype = n
				}
			}
		default:
			if n, err := subtypeObj.AsName(); err == nil {
				subtype = n
			}
		}
		if subtype != nil && string(subtype) != "CIDFontType2" {
			return cmap, nil
		}
	}

	// If there's an explicit CIDToGIDMap, build a repaired CMap using it.
	if cidToGid, ok := getCidToGidMap(cidFontDict, doc); ok {
		if repaired, ok := buildCmapWithCidToGidMap(cmap, cidToGid); ok {
			return cmap, &repaired
		}
		// Fall through to sequential remap if repair failed.
	}

	// W array must start at a low CID (≤ 2), indicating sequential
	// post-subset GIDs
	wStart, ok := getWArrayStartCid(cidFontDict, doc)
	if !ok || wStart > 2 {
		return cmap, nil
	}

	// If the W array actually covers the CMap's max source CID, the CMap is
	// aligned with the font — no sequential renumbering happened. A sparse W
	// array starting at CID 0 (for .notdef) with additional high-CID entries
	// matching the CMap is the normal subset layout, not a mismatch.
	if maxCid, ok := cmap.MaxSourceCID(); ok {
		if wArrayCoversCid(cidFontDict, doc, maxCid) {
			return cmap, nil
		}
	}

	remapped := cmap.RemapToSequential()
	return cmap, &remapped
}

// CidValuesLookLikeUnicode checks if a CIDFont's /W (widths) array contains
// CID values that look like Unicode codepoints rather than low-value GIDs.
//
// Returns true if the median CID is >= 0x41 (letter 'A'), indicating the PDF
// generator likely used Unicode codepoints as CIDs.
//
// Upstream: pub(crate) fn cid_values_look_like_unicode.
func CidValuesLookLikeUnicode(cidFontDict *objects.Dictionary) bool {
	wObj, err := cidFontDict.Get([]byte("W"))
	if err != nil || wObj.Kind != objects.KindArray {
		return false
	}
	wArr := wObj.Array

	// The /W array format: [cid [w1 w2 ...]] or [cid_start cid_end w].
	// Collect unique CIDs only: repeating a full-width range must not grow a
	// temporary vector (or the sort) with the range length on every copy.
	seen := map[uint16]struct{}{}
	i := 0
	for i < len(wArr) && len(seen) < maxCIDWExpansion {
		cid, err := wArr[i].AsI64()
		if err != nil {
			i++
			continue
		}
		start := uint16(cid)
		if i+1 < len(wArr) {
			if widths, err := wArr[i+1].AsArray(); err == nil {
				// [cid [w1 w2 ...]] — CIDs are cid, cid+1, ..., cid+len-1
				for j := range widths {
					if len(seen) >= maxCIDWExpansion {
						break
					}
					seen[start+uint16(j)] = struct{}{}
				}
				i += 2
			} else {
				// [cid_start cid_end w] — range of CIDs
				if i+2 < len(wArr) {
					if cidEnd, err := wArr[i+1].AsI64(); err == nil {
						recordUniqueCidRange(start, uint16(cidEnd), seen)
					}
					i += 3
				} else {
					i++
				}
			}
		} else {
			seen[start] = struct{}{}
			i++
		}
	}

	if len(seen) == 0 {
		return false
	}

	cids := make([]uint16, 0, len(seen))
	for cid := range seen {
		cids = append(cids, cid)
	}
	slices.Sort(cids)
	median := cids[len(cids)/2]
	// Unicode text CIDs are typically >= 0x20 (space) with letters at 0x41+.
	// GID-based subsets typically start at low values (0-based).
	// Use median >= 0x41 as a heuristic for Unicode CIDs.
	return median >= 0x41
}

func recordUniqueCidRange(start, end uint16, seen map[uint16]struct{}) {
	if start > end {
		return
	}
	for cid := start; ; cid = saturatingAdd1(cid) {
		if len(seen) >= maxCIDWExpansion {
			return
		}
		seen[cid] = struct{}{}
		if cid == end {
			return
		}
	}
}

// buildCmapFromCidSystemInfo builds a ToUnicode CMap from the predefined
// CID→Unicode mapping named by CIDSystemInfo.
//
// Supports Adobe-Korea1 (Korean) character collection. Can be extended for
// Adobe-Japan1, Adobe-GB1, Adobe-CNS1 in the future.
func buildCmapFromCidSystemInfo(cidFontDict *objects.Dictionary, doc *objects.Document) (ToUnicodeCMap, bool) {
	csiObj, err := cidFontDict.Get([]byte("CIDSystemInfo"))
	if err != nil {
		return ToUnicodeCMap{}, false
	}
	var csiDict *objects.Dictionary
	switch csiObj.Kind {
	case objects.KindReference:
		d, err := doc.GetDictionary(csiObj.Ref)
		if err != nil {
			return ToUnicodeCMap{}, false
		}
		csiDict = d
	case objects.KindDictionary:
		csiDict = csiObj.Dict
	default:
		return ToUnicodeCMap{}, false
	}
	orderingObj, err := csiDict.Get([]byte("Ordering"))
	if err != nil || orderingObj.Kind != objects.KindString {
		return ToUnicodeCMap{}, false
	}
	ordering := lossyUTF8(orderingObj.Str)

	switch ordering {
	case "Korea1":
		cmap := NewCMap()
		for _, entry := range korea1.AdobeKorea1CidToUnicode {
			if ch, ok := charFromU32(uint32(entry[1])); ok {
				cmap.CharMap[entry[0]] = string(ch)
			}
		}
		cmap.CodeByteLength = 2
		return cmap, true
	case "Japan1", "GB1", "CNS1":
		return buildCmapFromBuiltinCmap(ordering)
	}
	return ToUnicodeCMap{}, false
}

// buildFallbackCmapForType0 tries to build a fallback CMap from embedded
// font data or CIDSystemInfo when a ToUnicode CMap is present but incomplete.
func buildFallbackCmapForType0(fontDict *objects.Dictionary, doc *objects.Document) (ToUnicodeCMap, bool) {
	subtypeObj, err := fontDict.Get([]byte("Subtype"))
	if err != nil {
		return ToUnicodeCMap{}, false
	}
	subtype, err := subtypeObj.AsName()
	if err != nil {
		return ToUnicodeCMap{}, false
	}
	if string(subtype) != "Type0" {
		return ToUnicodeCMap{}, false
	}
	encObj, err := fontDict.Get([]byte("Encoding"))
	if err != nil {
		return ToUnicodeCMap{}, false
	}
	encoding, err := encObj.AsName()
	if err != nil {
		return ToUnicodeCMap{}, false
	}
	if string(encoding) != "Identity-H" && string(encoding) != "Identity-V" {
		return ToUnicodeCMap{}, false
	}

	cidFontDict, ok := getDescendantCidFont(fontDict, doc)
	if !ok {
		return ToUnicodeCMap{}, false
	}

	var fontDescriptor *objects.Dictionary
	if fdObj, err := fontDict2(cidFontDict, doc, []byte("FontDescriptor")); err == nil {
		fontDescriptor = fdObj
	}

	var fontFileRef *objects.ObjectId
	if fontDescriptor != nil {
		if o, err := fontDescriptor.Get([]byte("FontFile2")); err == nil {
			if r, err := o.AsReference(); err == nil {
				fontFileRef = &r
			}
		}
		if fontFileRef == nil {
			if o, err := fontDescriptor.Get([]byte("FontFile3")); err == nil {
				if r, err := o.AsReference(); err == nil {
					fontFileRef = &r
				}
			}
		}
	}

	if fontFileRef != nil {
		if obj, err := doc.GetObject(*fontFileRef); err == nil {
			if stream, err := obj.AsStream(); err == nil {
				if data, err := stream.DecompressedContent(); err == nil {
					if cmap, ok := BuildCmapFromTrueType(data); ok {
						if cidToGid, ok := getCidToGidMap(cidFontDict, doc); ok {
							if repaired, ok := buildCmapWithCidToGidMap(cmap, cidToGid); ok {
								return repaired, true
							}
						}
						return cmap, true
					}
				}
			}
		}
	}

	if cmap, ok := buildCmapFromCidSystemInfo(cidFontDict, doc); ok {
		return cmap, true
	}

	return ToUnicodeCMap{}, false
}

// buildFallbackCmapForSimple tries to build a fallback CMap for a simple font
// from its embedded font program.
func buildFallbackCmapForSimple(fontDict *objects.Dictionary, doc *objects.Document) (ToUnicodeCMap, bool) {
	subtypeObj, err := fontDict.Get([]byte("Subtype"))
	if err != nil {
		return ToUnicodeCMap{}, false
	}
	subtype, err := subtypeObj.AsName()
	if err != nil {
		return ToUnicodeCMap{}, false
	}
	if string(subtype) == "Type0" {
		return ToUnicodeCMap{}, false
	}
	fontDescriptor, err := fontDict2(fontDict, doc, []byte("FontDescriptor"))
	if err != nil {
		return ToUnicodeCMap{}, false
	}
	var ffRef *objects.ObjectId
	if o, err := fontDescriptor.Get([]byte("FontFile2")); err == nil {
		if r, err := o.AsReference(); err == nil {
			ffRef = &r
		}
	}
	if ffRef == nil {
		if o, err := fontDescriptor.Get([]byte("FontFile3")); err == nil {
			if r, err := o.AsReference(); err == nil {
				ffRef = &r
			}
		}
	}
	if ffRef == nil {
		return ToUnicodeCMap{}, false
	}
	if obj, err := doc.GetObject(*ffRef); err == nil {
		if stream, err := obj.AsStream(); err == nil {
			if data, err := stream.DecompressedContent(); err == nil {
				if cmap, ok := buildSimpleCmapFromTrueType(data); ok {
					return cmap, true
				}
			}
		}
	}
	return ToUnicodeCMap{}, false
}
