package objects

import "crypto/md5"

// encryptionState mirrors lopdf::EncryptionState (decode side).
type encryptionState struct {
	version             int64
	revision            int64
	keyLength           *int
	encryptMetadata     bool
	cryptFilters        map[string]cryptFilter
	fileEncryptionKey   []byte
	streamFilter        []byte
	stringFilter        []byte
	ownerValue          []byte
	ownerEncrypted      []byte
	userValue           []byte
	userEncrypted       []byte
	permissions         Permissions
	permissionEncrypted []byte
}

func (s *encryptionState) getStreamFilter() cryptFilter {
	if f, ok := s.cryptFilters[string(s.streamFilter)]; ok {
		return f
	}
	return rc4CryptFilter{}
}

func (s *encryptionState) getStringFilter() cryptFilter {
	if f, ok := s.cryptFilters[string(s.stringFilter)]; ok {
		return f
	}
	return rc4CryptFilter{}
}

// decodeEncryptionState mirrors EncryptionState::decode.
func decodeEncryptionState(doc *Document, password []byte) (*encryptionState, error) {
	if !doc.IsEncrypted() {
		return nil, ErrNotEncrypted
	}

	encDict, err := doc.GetEncrypted()
	if err != nil {
		return nil, &Error{Kind: KindDictKey, Key: "Filter"}
	}
	filterObj, err := encDict.Get([]byte("Filter"))
	if err != nil {
		return nil, &Error{Kind: KindDictKey, Key: "Filter"}
	}
	filterName, err := filterObj.AsName()
	if err != nil {
		return nil, &Error{Kind: KindDictKey, Key: "Filter"}
	}
	if string(filterName) != "Standard" {
		return nil, &Error{Kind: KindUnsupportedSecurityHandler, FilterName: append([]byte(nil), filterName...)}
	}

	algorithm, err := passwordAlgorithmFromDoc(doc)
	if err != nil {
		return nil, err
	}
	fileEncryptionKey, err := algorithm.computeFileEncryptionKey(doc, password)
	if err != nil {
		return nil, err
	}

	cryptFilters := doc.getCryptFilters()
	if algorithm.version < 4 {
		cryptFilters = map[string]cryptFilter{}
	}

	state := &encryptionState{
		version:             algorithm.version,
		revision:            algorithm.revision,
		keyLength:           algorithm.length,
		encryptMetadata:     algorithm.encryptMetadata,
		cryptFilters:        cryptFilters,
		fileEncryptionKey:   fileEncryptionKey,
		ownerValue:          algorithm.ownerValue,
		ownerEncrypted:      algorithm.ownerEncrypted,
		userValue:           algorithm.userValue,
		userEncrypted:       algorithm.userEncrypted,
		permissions:         algorithm.permissions,
		permissionEncrypted: algorithm.permissionEncrypted,
	}

	if algorithm.version == 4 || algorithm.version == 5 {
		if o, err := encDict.Get([]byte("StmF")); err == nil {
			if n, err2 := o.AsName(); err2 == nil {
				state.streamFilter = append([]byte(nil), n...)
			}
		}
		if o, err := encDict.Get([]byte("StrF")); err == nil {
			if n, err2 := o.AsName(); err2 == nil {
				state.stringFilter = append([]byte(nil), n...)
			}
		}
	}
	return state, nil
}

// getCryptFilters mirrors Document::get_crypt_filters.
func (d *Document) getCryptFilters() map[string]cryptFilter {
	cryptFilters := map[string]cryptFilter{}
	encDict, err := d.GetEncrypted()
	if err != nil {
		return cryptFilters
	}
	filtersObj, err := encDict.Get([]byte("CF"))
	if err != nil {
		return cryptFilters
	}
	filters, err := filtersObj.AsDict()
	if err != nil {
		return cryptFilters
	}
	filters.Range(func(name []byte, value *Object) bool {
		filter, err := value.AsDict()
		if err != nil {
			return true
		}
		if _, err := filter.Get([]byte("Type")); err == nil && !filter.HasType([]byte("CryptFilter")) {
			return true
		}
		var cfm []byte
		if o, err := filter.Get([]byte("CFM")); err == nil {
			if n, err2 := o.AsName(); err2 == nil {
				cfm = n
			}
		}
		var cf cryptFilter
		switch string(cfm) {
		case "V2":
			cf = rc4CryptFilter{}
		case "AESV2":
			cf = aes128CryptFilter{}
		case "AESV3":
			cf = aes256CryptFilter{}
		case "Identity", "":
			cf = identityCryptFilter{}
		default:
			return true
		}
		cryptFilters[string(name)] = cf
		return true
	})
	return cryptFilters
}

// decryptObject mirrors encryption::decrypt_object.
func decryptObject(state *encryptionState, objID ObjectId, obj *Object) error {
	var isXrefStream bool
	if obj.Kind == KindStream {
		isXrefStream = obj.Stream.Dict.HasType([]byte("XRef"))
	}
	if isXrefStream {
		return nil
	}
	if tn, err := obj.TypeName(); err == nil && string(tn) == "Metadata" && !state.encryptMetadata {
		return nil
	}

	var overrideFilter cryptFilter
	if obj.Kind == KindStream {
		if filters, err := obj.Stream.Filters(); err == nil {
			hasCrypt := false
			for _, f := range filters {
				if string(f) == "Crypt" {
					hasCrypt = true
					break
				}
			}
			if hasCrypt {
				if o, err := obj.Stream.Dict.Get([]byte("DecodeParms")); err == nil {
					if pd, err2 := o.AsDict(); err2 == nil {
						var name []byte
						if nObj, err3 := pd.Get([]byte("Name")); err3 == nil {
							if n, err4 := nObj.AsName(); err4 == nil {
								name = n
							}
						}
						if f, ok := state.cryptFilters[string(name)]; ok {
							overrideFilter = f
						} else {
							overrideFilter = identityCryptFilter{}
						}
					}
				}
			}
		}
	}

	switch obj.Kind {
	case KindArray:
		for i := range obj.Array {
			if err := decryptObject(state, objID, &obj.Array[i]); err != nil {
				return err
			}
		}
		return nil
	case KindDictionary:
		var keys [][]byte
		obj.Dict.Range(func(k []byte, _ *Object) bool {
			keys = append(keys, k)
			return true
		})
		for _, k := range keys {
			v, _ := obj.Dict.Get(k)
			if err := decryptObject(state, objID, v); err != nil {
				return err
			}
		}
		return nil
	case KindString:
		cf := state.getStringFilter()
		if overrideFilter != nil {
			cf = overrideFilter
		}
		key, err := cf.computeKey(state.fileEncryptionKey, objID)
		if err != nil {
			return wrapDecErr(err)
		}
		plaintext, err := cf.decrypt(key, obj.Str)
		if err != nil {
			return wrapDecErr(err)
		}
		obj.Str = plaintext
		return nil
	case KindStream:
		cf := state.getStreamFilter()
		if overrideFilter != nil {
			cf = overrideFilter
		}
		key, err := cf.computeKey(state.fileEncryptionKey, objID)
		if err != nil {
			return wrapDecErr(err)
		}
		plaintext, err := cf.decrypt(key, obj.Stream.Content)
		if err != nil {
			return wrapDecErr(err)
		}
		obj.Stream.SetContent(plaintext)
		return nil
	}
	return nil
}

// authenticateUserPasswordR4 mirrors Algorithm 6.
func (a *passwordAlgorithm) authenticateUserPasswordR4(doc *Document, userPassword []byte) error {
	var hashed []byte
	var err error
	switch a.revision {
	case 2:
		hashed, err = a.computeHashedUserPasswordR2(doc, userPassword)
	case 3, 4:
		hashed, err = a.computeHashedUserPasswordR3R4(doc, userPassword)
	default:
		return decErr(DecInvalidRevision)
	}
	if err != nil {
		return err
	}
	length := len(hashed)
	if a.revision == 3 || a.revision == 4 {
		length = 16
	}
	if len(a.userValue) < length {
		return decErr(DecInvalidHashLength)
	}
	for i := 0; i < length; i++ {
		if hashed[i] != a.userValue[i] {
			return decErr(DecIncorrectPassword)
		}
	}
	return nil
}

// authenticateOwnerPasswordR4 mirrors Algorithm 7.
func (a *passwordAlgorithm) authenticateOwnerPasswordR4(doc *Document, ownerPassword []byte) error {
	length := len(ownerPassword)
	if length > 32 {
		length = 32
	}
	h := md5Sum(ownerPassword[:length], padBytes[:32-length])
	if a.revision >= 3 {
		for range 50 {
			h = md5Sum(h)
		}
	}
	var n int64
	if a.revision >= 3 {
		n = a.keyLengthOr(40) / 8
	} else {
		n = 5
	}
	if n > 16 {
		return decErr(DecInvalidKeyLength)
	}
	result := append([]byte(nil), a.ownerValue...)
	if a.revision >= 3 {
		key := make([]byte, n)
		for i := int64(1); i <= 19; i++ {
			for j := int64(0); j < n; j++ {
				key[j] = h[j] ^ byte(i)
			}
			result = rc4Apply(key, result)
		}
	}
	result = rc4Apply(h[:n], result)
	return a.authenticateUserPasswordR4(doc, result)
}

func md5Sum(parts ...[]byte) []byte {
	h := md5.New()
	for _, p := range parts {
		h.Write(p)
	}
	return h.Sum(nil)
}

// computeHashedUserPasswordR2 mirrors Algorithm 4 (first 16 bytes only used
// by the caller comparison for R2 which compares all 32).
func (a *passwordAlgorithm) computeHashedUserPasswordR2(doc *Document, userPassword []byte) ([]byte, error) {
	fileEncryptionKey, err := a.computeFileEncryptionKeyR4(doc, userPassword)
	if err != nil {
		return nil, err
	}
	return rc4Apply(fileEncryptionKey, padBytes[:]), nil
}

// computeHashedUserPasswordR3R4 mirrors Algorithm 5. The 16 random trailing
// bytes upstream fills do not affect authentication (only the first 16 bytes
// are compared).
func (a *passwordAlgorithm) computeHashedUserPasswordR3R4(doc *Document, userPassword []byte) ([]byte, error) {
	fileEncryptionKey, err := a.computeFileEncryptionKeyR4(doc, userPassword)
	if err != nil {
		return nil, err
	}
	fileID0, err := fileIDFirst(doc)
	if err != nil {
		return nil, err
	}
	hash := md5Sum(padBytes[:], fileID0)
	result := rc4Apply(fileEncryptionKey, hash)
	key := make([]byte, len(fileEncryptionKey))
	for i := 1; i <= 19; i++ {
		for j := range fileEncryptionKey {
			key[j] = fileEncryptionKey[j] ^ byte(i)
		}
		result = rc4Apply(key, result)
	}
	out := make([]byte, 32)
	copy(out, result)
	return out, nil
}

// computeFileEncryptionKeyR6 mirrors compute_file_encryption_key_r6 (2.A).
func (a *passwordAlgorithm) computeFileEncryptionKeyR6(password []byte) ([]byte, error) {
	if len(password) > 127 {
		password = password[:127]
	}

	hashedOwnerPassword := a.ownerValue[:32]
	ownerValidationSalt := a.ownerValue[32:40]
	ownerKeySalt := a.ownerValue[40:48]

	hashedUserPassword := a.userValue[:32]
	userValidationSalt := a.userValue[32:40]
	userKeySalt := a.userValue[40:48]

	ownerHash, err := a.computeHashR6(password, ownerValidationSalt, a.userValue)
	if err != nil {
		return nil, err
	}
	if equalBytes(ownerHash, hashedOwnerPassword) {
		hash, err := a.computeHashR6(password, ownerKeySalt, a.userValue)
		if err != nil {
			return nil, err
		}
		key := make([]byte, 32)
		copy(key, hash)
		iv := make([]byte, 16)
		ownerEncrypted := append([]byte(nil), a.ownerEncrypted...)
		ownerEncrypted = aesCBCDecryptNoPad(key, iv, ownerEncrypted)
		return ownerEncrypted, nil
	}

	userHash, err := a.computeHashR6(password, userValidationSalt, nil)
	if err != nil {
		return nil, err
	}
	if equalBytes(userHash, hashedUserPassword) {
		hash, err := a.computeHashR6(password, userKeySalt, nil)
		if err != nil {
			return nil, err
		}
		key := make([]byte, 32)
		copy(key, hash)
		iv := make([]byte, 16)
		userEncrypted := append([]byte(nil), a.userEncrypted...)
		userEncrypted = aesCBCDecryptNoPad(key, iv, userEncrypted)
		if err := a.validatePermissions(userEncrypted); err != nil {
			return nil, err
		}
		return userEncrypted, nil
	}

	return nil, decErr(DecIncorrectPassword)
}

// authenticateUserPasswordR6 mirrors Algorithm 11.
func (a *passwordAlgorithm) authenticateUserPasswordR6(userPassword []byte) error {
	if len(userPassword) > 127 {
		userPassword = userPassword[:127]
	}
	hashedUserPassword := a.userValue[:32]
	userValidationSalt := a.userValue[32:40]
	hash, err := a.computeHashR6(userPassword, userValidationSalt, nil)
	if err != nil {
		return err
	}
	if !equalBytes(hash, hashedUserPassword) {
		return decErr(DecIncorrectPassword)
	}
	return nil
}

// authenticateOwnerPasswordR6 mirrors Algorithm 12.
func (a *passwordAlgorithm) authenticateOwnerPasswordR6(ownerPassword []byte) error {
	if len(ownerPassword) > 127 {
		ownerPassword = ownerPassword[:127]
	}
	hashedOwnerPassword := a.ownerValue[:32]
	ownerValidationSalt := a.ownerValue[32:40]
	hash, err := a.computeHashR6(ownerPassword, ownerValidationSalt, a.userValue)
	if err != nil {
		return err
	}
	if !equalBytes(hash, hashedOwnerPassword) {
		return decErr(DecIncorrectPassword)
	}
	return nil
}

// validatePermissions mirrors Algorithm 13.
func (a *passwordAlgorithm) validatePermissions(fileEncryptionKey []byte) error {
	bytes := make([]byte, 16)
	copy(bytes, a.permissionEncrypted)
	key := make([]byte, 32)
	copy(key, fileEncryptionKey)
	decrypted := aesECBBlocks(key, bytes, false)
	if string(decrypted[9:12]) != "adb" {
		return decErr(DecIncorrectPassword)
	}
	var pLE [8]byte
	p := uint64(a.permissions)
	for i := range pLE {
		pLE[i] = byte(p >> (8 * i))
	}
	for i := 0; i < 3; i++ {
		if decrypted[i] != pLE[i] {
			return decErr(DecIncorrectPassword)
		}
	}
	want := byte('F')
	if a.encryptMetadata {
		want = byte('T')
	}
	if decrypted[8] != want {
		return decErr(DecIncorrectPassword)
	}
	return nil
}

// computeFileEncryptionKey dispatches by revision.
func (a *passwordAlgorithm) computeFileEncryptionKey(doc *Document, password []byte) ([]byte, error) {
	switch a.revision {
	case 2, 3, 4:
		return a.computeFileEncryptionKeyR4(doc, password)
	case 5, 6:
		return a.computeFileEncryptionKeyR6(password)
	}
	return nil, decErr(DecUnsupportedRevision)
}

// authenticateUserPassword dispatches by revision.
func (a *passwordAlgorithm) authenticateUserPassword(doc *Document, userPassword []byte) error {
	switch a.revision {
	case 2, 3, 4:
		return a.authenticateUserPasswordR4(doc, userPassword)
	case 5, 6:
		return a.authenticateUserPasswordR6(userPassword)
	}
	return decErr(DecUnsupportedRevision)
}

// authenticateOwnerPassword dispatches by revision.
func (a *passwordAlgorithm) authenticateOwnerPassword(doc *Document, ownerPassword []byte) error {
	switch a.revision {
	case 2, 3, 4:
		return a.authenticateOwnerPasswordR4(doc, ownerPassword)
	case 5, 6:
		return a.authenticateOwnerPasswordR6(ownerPassword)
	}
	return decErr(DecUnsupportedRevision)
}

// authenticateOwnerOrUser mirrors Document::authenticate_password
// (owner OR user).
func (a *passwordAlgorithm) authenticateOwnerOrUser(doc *Document, password []byte) error {
	if err := a.authenticateOwnerPassword(doc, password); err == nil {
		return nil
	}
	return a.authenticateUserPassword(doc, password)
}

func wrapDecErr(err error) error {
	if _, ok := err.(*Error); ok {
		return err
	}
	return &Error{Kind: KindDecryption, Inner: err}
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
