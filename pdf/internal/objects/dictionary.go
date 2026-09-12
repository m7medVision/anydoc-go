package objects

import "sort"

// Dictionary mirrors lopdf::Dictionary (an insertion-ordered IndexMap keyed by
// raw name bytes). The zero value is an empty dictionary ready for use.
type Dictionary struct {
	keys []string
	m    map[string]*Object
}

// NewDictionary mirrors Dictionary::new.
func NewDictionary() *Dictionary { return &Dictionary{m: map[string]*Object{}} }

func (d *Dictionary) ensure() {
	if d.m == nil {
		d.m = map[string]*Object{}
	}
}

// Has mirrors Dictionary::has.
func (d *Dictionary) Has(key []byte) bool {
	if d.m == nil {
		return false
	}
	_, ok := d.m[string(key)]
	return ok
}

// Get mirrors Dictionary::get: Err(DictKey) when missing.
func (d *Dictionary) Get(key []byte) (*Object, error) {
	d.ensure()
	o, ok := d.m[string(key)]
	if !ok {
		return nil, errDictKey(key)
	}
	return o, nil
}

// GetOK is Dictionary::get(...).ok() as the idiomatic two-value form.
func (d *Dictionary) GetOK(key []byte) (*Object, bool) {
	if d.m == nil {
		return nil, false
	}
	o, ok := d.m[string(key)]
	return o, ok
}

// GetDeref mirrors Dictionary::get_deref: resolves a Reference value through doc.
func (d *Dictionary) GetDeref(key []byte, doc *Document) (*Object, error) {
	o, err := d.Get(key)
	if err != nil {
		return nil, err
	}
	_, obj, err := doc.Dereference(o)
	if err != nil {
		return nil, err
	}
	return obj, nil
}

// Set mirrors Dictionary::set (insert-or-replace, preserving first-insert
// position like IndexMap).
func (d *Dictionary) Set(key []byte, value Object) {
	d.ensure()
	k := string(key)
	if _, ok := d.m[k]; !ok {
		d.keys = append(d.keys, k)
	}
	v := value
	d.m[k] = &v
}

// Remove mirrors Dictionary::remove (swap_remove in lopdf; order of remaining
// entries is not relied upon by upstream readers). Returns the removed object.
func (d *Dictionary) Remove(key []byte) (*Object, bool) {
	if d.m == nil {
		return nil, false
	}
	k := string(key)
	o, ok := d.m[k]
	if !ok {
		return nil, false
	}
	delete(d.m, k)
	for i, ek := range d.keys {
		if ek == k {
			d.keys = append(d.keys[:i], d.keys[i+1:]...)
			break
		}
	}
	return o, true
}

// Len mirrors Dictionary::len.
func (d *Dictionary) Len() int {
	if d.m == nil {
		return 0
	}
	return len(d.m)
}

// IsEmpty mirrors Dictionary::is_empty.
func (d *Dictionary) IsEmpty() bool { return d.Len() == 0 }

// HasType mirrors Dictionary::has_type.
func (d *Dictionary) HasType(typeName []byte) bool {
	name, err := d.Get([]byte("Type"))
	if err != nil {
		return false
	}
	n, err := name.AsName()
	return err == nil && string(n) == string(typeName)
}

// GetType mirrors Dictionary::get_type (falls back to b"Linearized").
func (d *Dictionary) GetType() ([]byte, error) {
	if o, err := d.Get([]byte("Type")); err == nil {
		if n, err2 := o.AsName(); err2 == nil {
			return n, nil
		}
	}
	if _, err := d.Get([]byte("Linearized")); err == nil {
		return []byte("Linearized"), nil
	} else {
		return nil, err
	}
}

// Range iterates entries in insertion order. Return false to stop.
func (d *Dictionary) Range(f func(key []byte, value *Object) bool) {
	if d.m == nil {
		return
	}
	for _, k := range d.keys {
		if !f([]byte(k), d.m[k]) {
			return
		}
	}
}

// Keys returns the keys in insertion order.
func (d *Dictionary) Keys() [][]byte {
	out := make([][]byte, 0, len(d.keys))
	for _, k := range d.keys {
		out = append(out, []byte(k))
	}
	return out
}

// Clone deep-copies the dictionary (mirrors Dictionary's Clone derive).
func (d *Dictionary) Clone() *Dictionary {
	c := NewDictionary()
	c.keys = append(c.keys, d.keys...)
	for k, v := range d.m {
		vo := cloneObject(v)
		c.m[k] = &vo
	}
	return c
}

// cloneObject deep-copies an Object tree (Clone derive parity).
func cloneObject(o *Object) Object {
	if o == nil {
		return Null()
	}
	c := *o
	switch o.Kind {
	case KindArray:
		c.Array = make([]Object, len(o.Array))
		for i := range o.Array {
			c.Array[i] = cloneObject(&o.Array[i])
		}
	case KindDictionary:
		c.Dict = o.Dict.Clone()
	case KindStream:
		sc := *o.Stream
		sc.Dict = *o.Stream.Dict.Clone()
		sc.Content = append([]byte(nil), o.Stream.Content...)
		c.Stream = &sc
	}
	return c
}

// String renders like lopdf's Debug for Dictionary.
func (d *Dictionary) String() string {
	s := "<<"
	d.Range(func(key []byte, value *Object) bool {
		s += "/" + string(key) + " " + value.String()
		return true
	})
	return s + ">>"
}

// sortedKeysSortedNames is a helper for maps keyed by raw name bytes that
// upstream stores in BTreeMap (ascending byte order iteration).
func sortedByteKeys(keys [][]byte) [][]byte {
	out := append([][]byte(nil), keys...)
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		for k := 0; k < len(a) && k < len(b); k++ {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return len(a) < len(b)
	})
	return out
}
