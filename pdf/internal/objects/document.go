package objects

import "sort"

// Document port of lopdf src/document.rs (load-side surface).

const derefLimit = 128

// Document mirrors lopdf::Document.
type Document struct {
	Version    string
	Trailer    *Dictionary
	xref       *Xref
	objects    map[ObjectId]*Object
	objectIDs  []ObjectId // sorted by (Num, Gen); BTreeMap parity
	maxID      uint32
	xrefStart  int
	encryption *encryptionState
}

func newDocument() *Document {
	return &Document{
		Version: "1.4",
		Trailer: NewDictionary(),
		xref:    NewXref(0, XrefTypeCrossReferenceStream),
		objects: map[ObjectId]*Object{},
	}
}

// New mirrors Document::new.
func New() *Document { return newDocument() }

// WithVersion mirrors Document::with_version.
func WithVersion(version string) *Document {
	d := newDocument()
	d.Version = version
	return d
}

// Objects returns the document object map (lopdf's public `objects` BTreeMap).
// Callers that would write `doc.objects.insert(id, obj)` use SetObject.
func (d *Document) Objects() map[ObjectId]*Object { return d.objects }

// setObject inserts or replaces an object.
func (d *Document) setObject(id ObjectId, obj Object) {
	if _, ok := d.objects[id]; !ok {
		d.objectIDs = append(d.objectIDs, id)
	}
	d.objects[id] = &obj
}

// sortedObjectIDs returns all object IDs in ascending (Num, Gen) order,
// mirroring iteration over lopdf's BTreeMap<ObjectId, Object>.
func (d *Document) sortedObjectIDs() []ObjectId {
	out := append([]ObjectId(nil), d.objectIDs...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Num != out[j].Num {
			return out[i].Num < out[j].Num
		}
		return out[i].Gen < out[j].Gen
	})
	return out
}

// HasObject mirrors Document::has_object.
func (d *Document) HasObject(id ObjectId) bool {
	_, ok := d.objects[id]
	return ok
}

// Dereference mirrors Document::dereference: follows reference chains.
func (d *Document) Dereference(object *Object) (*ObjectId, *Object, error) {
	nbDeref := 0
	var id *ObjectId
	for object != nil && object.Kind == KindReference {
		refID := object.Ref
		id = &refID
		next, ok := d.objects[refID]
		if !ok {
			return nil, nil, &Error{Kind: KindObjectNotFound, ID: refID}
		}
		object = next
		nbDeref++
		if nbDeref > derefLimit {
			return nil, nil, ErrReferenceLimit
		}
	}
	return id, object, nil
}

// GetObject mirrors Document::get_object (lookup + dereference).
func (d *Document) GetObject(id ObjectId) (*Object, error) {
	object, ok := d.objects[id]
	if !ok {
		return nil, &Error{Kind: KindObjectNotFound, ID: id}
	}
	_, obj, err := d.Dereference(object)
	return obj, err
}

// GetDictionary mirrors Document::get_dictionary.
func (d *Document) GetDictionary(id ObjectId) (*Dictionary, error) {
	obj, err := d.GetObject(id)
	if err != nil {
		return nil, err
	}
	return obj.AsDict()
}

// Catalog mirrors Document::catalog.
func (d *Document) Catalog() (*Dictionary, error) {
	rootObj, err := d.Trailer.Get([]byte("Root"))
	if err != nil {
		return nil, err
	}
	id, err := rootObj.AsReference()
	if err != nil {
		return nil, err
	}
	return d.GetDictionary(id)
}

// GetEncrypted mirrors Document::get_encrypted.
func (d *Document) GetEncrypted() (*Dictionary, error) {
	encObj, err := d.Trailer.Get([]byte("Encrypt"))
	if err != nil {
		return nil, err
	}
	id, err := encObj.AsReference()
	if err != nil {
		return nil, err
	}
	return d.GetDictionary(id)
}

// IsEncrypted mirrors Document::is_encrypted.
func (d *Document) IsEncrypted() bool {
	_, err := d.GetEncrypted()
	return err == nil
}

// WasEncrypted mirrors Document::was_encrypted.
func (d *Document) WasEncrypted() bool { return d.encryption != nil }

// AddObject mirrors Document::add_object.
func (d *Document) AddObject(object Object) ObjectId {
	id := d.NewObjectID()
	d.setObject(id, object)
	return id
}

// NewObjectID mirrors Document::new_object_id.
func (d *Document) NewObjectID() ObjectId {
	d.maxID++
	return ObjectId{Num: d.maxID}
}

// SetObject mirrors Document::set_object.
func (d *Document) SetObject(id ObjectId, object Object) {
	if id.Num > d.maxID {
		d.maxID = id.Num
	}
	d.setObject(id, object)
}

// ── Page tree ────────────────────────────────────────────────────────

const pageTreeDepthLimit = 256

// PageTable mirrors the BTreeMap<u32, ObjectId> returned by get_pages:
// entries iterate in ascending page-number order.
type PageTable struct {
	nums []uint32
	ids  []ObjectId
}

func (p *PageTable) Len() int { return len(p.nums) }

// Get looks a page up by 1-indexed page number.
func (p *PageTable) Get(n uint32) (ObjectId, bool) {
	for i, num := range p.nums {
		if num == n {
			return p.ids[i], true
		}
	}
	return ObjectId{}, false
}

// Numbers returns page numbers in ascending order.
func (p *PageTable) Numbers() []uint32 { return append([]uint32(nil), p.nums...) }

// IDs returns object IDs in page order.
func (p *PageTable) IDs() []ObjectId { return append([]ObjectId(nil), p.ids...) }

// At returns the i-th (0-indexed) entry.
func (p *PageTable) At(i int) (uint32, ObjectId) { return p.nums[i], p.ids[i] }

// GetPages mirrors Document::get_pages.
func (d *Document) GetPages() *PageTable {
	pt := &PageTable{}
	for _, id := range d.pageTreeIter() {
		pt.nums = append(pt.nums, uint32(len(pt.nums)+1))
		pt.ids = append(pt.ids, id)
	}
	return pt
}

// pageTreeIter mirrors PageTreeIter: depth-first Kids traversal with an
// iteration budget of one node per object in the document.
func (d *Document) pageTreeIter() []ObjectId {
	var out []ObjectId
	catalog, err := d.Catalog()
	if err != nil {
		return out
	}
	pagesObj, err := catalog.Get([]byte("Pages"))
	if err != nil {
		return out
	}
	pagesID, err := pagesObj.AsReference()
	if err != nil {
		return out
	}
	iterLimit := len(d.objects) + 1
	var stack [][]Object
	kids := d.pageKids(pagesID)
	for {
		for len(kids) > 0 {
			if iterLimit == 0 {
				return out
			}
			iterLimit--
			kid := kids[0]
			kids = kids[1:]
			if kid.Kind != KindReference {
				continue
			}
			kidID := kid.Ref
			dict, err := d.GetDictionary(kidID)
			if err != nil {
				continue
			}
			tn, err := dict.GetType()
			if err != nil {
				continue
			}
			switch string(tn) {
			case "Page":
				out = append(out, kidID)
			case "Pages":
				if len(stack) < pageTreeDepthLimit {
					if len(kids) > 0 {
						stack = append(stack, kids)
					}
					kids = d.pageKids(kidID)
				}
			}
		}
		if len(stack) == 0 {
			return out
		}
		kids = stack[len(stack)-1]
		stack = stack[:len(stack)-1]
	}
}

func (d *Document) pageKids(pageTreeID ObjectId) []Object {
	dict, err := d.GetDictionary(pageTreeID)
	if err != nil {
		return nil
	}
	obj, err := dict.GetDeref([]byte("Kids"), d)
	if err != nil {
		return nil
	}
	arr, err := obj.AsArray()
	if err != nil {
		return nil
	}
	return arr
}

// GetPageContents mirrors Document::get_page_contents.
func (d *Document) GetPageContents(pageID ObjectId) []ObjectId {
	var streams []ObjectId
	page, err := d.GetDictionary(pageID)
	if err != nil {
		return streams
	}
	contents, err := page.Get([]byte("Contents"))
	if err != nil {
		return streams
	}
	nbDeref := 0
	for {
		switch contents.Kind {
		case KindReference:
			id := contents.Ref
			obj, ok := d.objects[id]
			if !ok || obj != nil && obj.Kind == KindStream {
				streams = append(streams, id)
			} else {
				nbDeref++
				if nbDeref < derefLimit {
					contents = obj
					continue
				}
			}
		case KindArray:
			for i := range contents.Array {
				if id, err := contents.Array[i].AsReference(); err == nil {
					streams = append(streams, id)
				}
			}
		}
		break
	}
	return streams
}

// GetPageResources mirrors Document::get_page_resources: returns the inline
// /Resources dictionary (if direct) and the IDs of indirect resource
// dictionaries found along the /Parent chain.
func (d *Document) GetPageResources(pageID ObjectId) (*Dictionary, []ObjectId, error) {
	var resourceDict *Dictionary
	var resourceIDs []ObjectId
	page, err := d.GetDictionary(pageID)
	if err != nil {
		return nil, resourceIDs, nil
	}
	if o, err := page.Get([]byte("Resources")); err == nil {
		if dict, err2 := o.AsDict(); err2 == nil {
			resourceDict = dict
		}
	}
	seen := map[ObjectId]bool{}
	node := page
	for {
		parentObj, err := node.Get([]byte("Parent"))
		if err != nil {
			break
		}
		parentID, err := parentObj.AsReference()
		if err != nil {
			break
		}
		if seen[parentID] {
			return nil, nil, &Error{Kind: KindReferenceCycle, ID: parentID}
		}
		seen[parentID] = true
		if o, err := node.Get([]byte("Resources")); err == nil {
			if id, err2 := o.AsReference(); err2 == nil {
				resourceIDs = append(resourceIDs, id)
			}
		}
		parent, err := d.GetDictionary(parentID)
		if err != nil {
			return nil, nil, err
		}
		node = parent
	}
	if o, err := node.Get([]byte("Resources")); err == nil {
		if id, err2 := o.AsReference(); err2 == nil {
			resourceIDs = append(resourceIDs, id)
		}
	}
	return resourceDict, resourceIDs, nil
}

// NameDictMap mirrors BTreeMap<Vec<u8>, &Dictionary>: entries iterate in
// ascending key order.
type NameDictMap struct {
	keys [][]byte
	vals []*Dictionary
}

func (m *NameDictMap) Len() int { return len(m.keys) }

func (m *NameDictMap) Get(name []byte) (*Dictionary, bool) {
	for i, k := range m.keys {
		if string(k) == string(name) {
			return m.vals[i], true
		}
	}
	return nil, false
}

// Range visits entries in ascending key order; return false to stop.
func (m *NameDictMap) Range(f func(name []byte, dict *Dictionary) bool) {
	for i := range m.keys {
		if !f(m.keys[i], m.vals[i]) {
			return
		}
	}
}

// Names returns the keys in ascending order.
func (m *NameDictMap) Names() [][]byte { return m.keys }

// NewNameDictMap creates an empty map for callers building the
// BTreeMap<Vec<u8>, &Dictionary> shape themselves (e.g. Form XObject font walks).
func NewNameDictMap() *NameDictMap { return &NameDictMap{} }

// Insert adds an entry keeping ascending-key order. The first entry for a
// given name wins (mirrors the `if !fonts.contains_key(name)` guard upstream).
func (m *NameDictMap) Insert(name []byte, dict *Dictionary) {
	for i, k := range m.keys {
		if string(k) == string(name) {
			return // first entry wins (BTreeMap insert semantics differ; callers guard)
		}
		if string(k) > string(name) {
			m.keys = append(m.keys, nil)
			m.vals = append(m.vals, nil)
			copy(m.keys[i+1:], m.keys[i:])
			copy(m.vals[i+1:], m.vals[i:])
			m.keys[i] = append([]byte(nil), name...)
			m.vals[i] = dict
			return
		}
	}
	m.keys = append(m.keys, append([]byte(nil), name...))
	m.vals = append(m.vals, dict)
}

// GetPageFonts mirrors Document::get_page_fonts.
func (d *Document) GetPageFonts(pageID ObjectId) (*NameDictMap, error) {
	fonts := &NameDictMap{}
	collect := func(resources *Dictionary) {
		fontObj, err := resources.Get([]byte("Font"))
		if err != nil {
			return
		}
		var fontDict *Dictionary
		switch fontObj.Kind {
		case KindReference:
			if obj, err := d.GetObject(fontObj.Ref); err == nil {
				if dict, err2 := obj.AsDict(); err2 == nil {
					fontDict = dict
				}
			}
		case KindDictionary:
			fontDict = fontObj.Dict
		}
		if fontDict == nil {
			return
		}
		fontDict.Range(func(name []byte, value *Object) bool {
			if _, exists := fonts.Get(name); exists {
				return true
			}
			var font *Dictionary
			switch value.Kind {
			case KindReference:
				if dict, err := d.GetDictionary(value.Ref); err == nil {
					font = dict
				}
			case KindDictionary:
				font = value.Dict
			}
			if font != nil {
				fonts.Insert(name, font)
			}
			return true
		})
	}
	resourceDict, resourceIDs, err := d.GetPageResources(pageID)
	if err != nil {
		return nil, err
	}
	if resourceDict != nil {
		collect(resourceDict)
	}
	for _, id := range resourceIDs {
		if resources, err := d.GetDictionary(id); err == nil {
			collect(resources)
		}
	}
	return fonts, nil
}

// GetPageAnnotations mirrors Document::get_page_annotations.
func (d *Document) GetPageAnnotations(pageID ObjectId) ([]*Dictionary, error) {
	var annotations []*Dictionary
	page, err := d.GetDictionary(pageID)
	if err != nil {
		return annotations, nil
	}
	annots, err := page.Get([]byte("Annots"))
	if err != nil {
		return annotations, nil
	}
	var arr []Object
	switch annots.Kind {
	case KindReference:
		if obj, err := d.GetObject(annots.Ref); err == nil {
			if a, err2 := obj.AsArray(); err2 == nil {
				arr = a
			}
		}
	case KindArray:
		arr = annots.Array
	}
	for i := range arr {
		if id, err := arr[i].AsReference(); err == nil {
			if dict, err := d.GetDictionary(id); err == nil {
				annotations = append(annotations, dict)
			}
		}
	}
	return annotations, nil
}

// GetPageContent mirrors Document::get_page_content: concatenated (decoded)
// content streams, newline separated.
func (d *Document) GetPageContent(pageID ObjectId) ([]byte, error) {
	var content []byte
	for _, objectID := range d.GetPageContents(pageID) {
		obj, err := d.GetObject(objectID)
		if err != nil {
			continue
		}
		stream, err := obj.AsStream()
		if err != nil {
			continue
		}
		if data, err := stream.DecompressedContent(); err == nil {
			content = append(content, data...)
		} else {
			content = append(content, stream.Content...)
		}
		content = append(content, '\n')
	}
	return content, nil
}

// ── lopdf extract_text (parser_aux) ───────────────────────────────────

// ExtractText mirrors Document::extract_text.
func (d *Document) ExtractText(pageNumbers []uint32) (string, error) {
	chunks, err := d.ExtractTextChunks(pageNumbers)
	if err != nil {
		return "", err
	}
	var sb []byte
	for _, c := range chunks {
		sb = append(sb, c...)
	}
	return string(sb), nil
}

// ExtractTextChunks mirrors Document::extract_text_chunks. The first failing
// chunk aborts extraction (error parity with upstream's `?` in extract_text).
func (d *Document) ExtractTextChunks(pageNumbers []uint32) ([]string, error) {
	pages := d.GetPages()
	var out []string
	for _, pageNumber := range pageNumbers {
		pageID, ok := pages.Get(pageNumber)
		if !ok {
			return nil, &Error{Kind: KindPageNumberNotFound, Page: pageNumber}
		}
		chunks, err := d.extractTextChunksFromPage(pageID)
		if err != nil {
			return nil, err
		}
		for _, chunk := range chunks {
			if chunk.err != nil {
				return nil, chunk.err
			}
			out = append(out, chunk.text)
		}
	}
	return out, nil
}

type textChunk struct {
	text string
	err  error
}

func (d *Document) extractTextChunksFromPage(pageID ObjectId) ([]textChunk, error) {
	var collected []textChunk

	fonts, err := d.GetPageFonts(pageID)
	if err != nil {
		return nil, err
	}
	encodings := map[string]*FontEncoding{}
	var encOrder [][]byte
	fonts.Range(func(name []byte, font *Dictionary) bool {
		enc, err := font.GetFontEncoding(d)
		if err != nil {
			collected = append(collected, textChunk{err: err})
			return true
		}
		encodings[string(name)] = enc
		encOrder = append(encOrder, name)
		return true
	})
	_ = encOrder

	contentData, err := d.GetPageContent(pageID)
	if err != nil {
		return nil, err
	}
	content := parseContent(contentData)

	var currentEncoding *FontEncoding
	var currentText []byte
	for i := range content.Operations {
		op := &content.Operations[i]
		switch op.Operator {
		case "Tf":
			if len(op.Operands) == 0 {
				collected = append(collected, textChunk{err: &Error{Kind: KindSyntax, Detail: "missing font operand"}})
				currentEncoding = nil
			} else if name, err := op.Operands[0].AsName(); err != nil {
				collected = append(collected, textChunk{err: err})
				currentEncoding = nil
			} else {
				currentEncoding = encodings[string(name)]
			}
			if len(currentText) > 0 {
				collected = append(collected, textChunk{text: string(currentText)})
				currentText = nil
			}
		case "Tj", "TJ":
			if currentEncoding != nil {
				if err := collectText(&currentText, currentEncoding, op.Operands); err != nil {
					collected = append(collected, textChunk{err: err})
				}
			}
		case "'":
			if currentEncoding != nil {
				if len(currentText) == 0 || currentText[len(currentText)-1] != '\n' {
					currentText = append(currentText, '\n')
				}
				if err := collectText(&currentText, currentEncoding, op.Operands); err != nil {
					collected = append(collected, textChunk{err: err})
				}
			}
		case "\"":
			if currentEncoding != nil {
				if len(currentText) == 0 || currentText[len(currentText)-1] != '\n' {
					currentText = append(currentText, '\n')
				}
				if len(op.Operands) > 2 {
					operands := []Object{op.Operands[2]}
					if err := collectText(&currentText, currentEncoding, operands); err != nil {
						collected = append(collected, textChunk{err: err})
					}
				}
			}
		case "T*":
			if len(currentText) == 0 || currentText[len(currentText)-1] != '\n' {
				currentText = append(currentText, '\n')
			}
		case "ET":
			if len(currentText) == 0 || currentText[len(currentText)-1] != '\n' {
				currentText = append(currentText, '\n')
			}
		}
	}
	if len(currentText) > 0 {
		collected = append(collected, textChunk{text: string(currentText)})
	}
	return collected, nil
}

// collectText mirrors parser_aux::collect_text.
func collectText(text *[]byte, encoding *FontEncoding, operands []Object) error {
	for i := range operands {
		operand := &operands[i]
		switch operand.Kind {
		case KindString:
			if err := encoding.writeToString(operand.Str, text); err != nil {
				return err
			}
		case KindArray:
			if err := collectText(text, encoding, operand.Array); err != nil {
				return err
			}
			*text = append(*text, ' ')
		case KindInteger:
			if operand.Int < -100 {
				*text = append(*text, ' ')
			}
		}
	}
	return nil
}
