package objects

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/sha256"
	"crypto/sha512"
	"hash"
)

// Encryption port of lopdf src/encryption.rs + algorithms.rs + crypt_filters.rs.

// padBytes is the standard PDF password padding string.
var padBytes = [32]byte{
	0x28, 0xBF, 0x4E, 0x5E, 0x4E, 0x75, 0x8A, 0x41, 0x64, 0x00, 0x4E, 0x56, 0xFF, 0xFA, 0x01, 0x08,
	0x2E, 0x2E, 0x00, 0xB6, 0xD0, 0x68, 0x3E, 0x80, 0x2F, 0x0C, 0xA9, 0xFE, 0x64, 0x53, 0x69, 0x7A,
}

// DecryptionError mirrors lopdf::DecryptionError kinds.
type DecryptionError struct {
	Kind    DecErrKind
	Detail  string
	Details string // prohibited character etc.
}

type DecErrKind int

const (
	DecMissingEncryptDictionary DecErrKind = iota
	DecMissingVersion
	DecMissingRevision
	DecMissingOwnerPassword
	DecMissingUserPassword
	DecMissingPermissions
	DecMissingFileID
	DecInvalidHashLength
	DecInvalidKeyLength
	DecInvalidCipherTextLength
	DecInvalidPermissionLength
	DecInvalidVersion
	DecInvalidRevision
	DecInvalidType
	DecNotDecryptable
	DecIncorrectPassword
	DecUnsupportedEncryption
	DecUnsupportedVersion
	DecUnsupportedRevision
	DecStringPrep
	DecPadding
)

func (e *DecryptionError) Error() string {
	switch e.Kind {
	case DecMissingEncryptDictionary:
		return "the /Encrypt dictionary is missing"
	case DecMissingVersion:
		return "missing encryption version"
	case DecMissingRevision:
		return "missing encryption revision"
	case DecMissingOwnerPassword:
		return "missing the owner password (/O)"
	case DecMissingUserPassword:
		return "missing the user password (/U)"
	case DecMissingPermissions:
		return "missing the permissions field (/P)"
	case DecMissingFileID:
		return "missing the file /ID elements"
	case DecInvalidHashLength:
		return "invalid hash length"
	case DecInvalidKeyLength:
		return "invalid key length"
	case DecInvalidCipherTextLength:
		return "invalid ciphertext length"
	case DecInvalidPermissionLength:
		return "invalid permission length"
	case DecInvalidVersion:
		return "invalid version"
	case DecInvalidRevision:
		return "invalid revision"
	case DecInvalidType:
		return "unexpected type; document does not comply with the spec"
	case DecNotDecryptable:
		return "the object is not capable of being decrypted"
	case DecIncorrectPassword:
		return "the supplied password is incorrect"
	case DecUnsupportedEncryption:
		return "the document uses an encryption scheme that is not implemented in lopdf"
	case DecUnsupportedVersion:
		return "the encryption version is not implemented in lopdf"
	case DecUnsupportedRevision:
		return "the encryption revision is not implemented in lopdf"
	case DecStringPrep:
		return "stringprep error"
	case DecPadding:
		return "invalid padding encountered when decrypting, key might be incorrect"
	}
	return "decryption error"
}

func decErr(kind DecErrKind) *Error {
	return &Error{Kind: KindDecryption, Inner: &DecryptionError{Kind: kind}}
}

// Permissions mirrors the bitflags struct (u64 bits).
type Permissions uint64

const (
	PermPrintable                Permissions = 1 << 2
	PermModifiable               Permissions = 1 << 3
	PermCopyable                 Permissions = 1 << 4
	PermAnnotable                Permissions = 1 << 5
	PermFillable                 Permissions = 1 << 8
	PermCopyableForAccessibility Permissions = 1 << 9
	PermAssemblable              Permissions = 1 << 10
	PermPrintableInHighQuality   Permissions = 1 << 11
)

// correctBits mirrors Permissions::correct_bits.
func (p Permissions) correctBits() Permissions {
	bits := uint64(p)
	bits |= 0b11 << 6
	bits |= 0b1111<<12 | 0xffff<<16
	bits |= 0xffffffff << 32
	return Permissions(bits)
}

// cryptFilter mirrors the CryptFilter trait.
type cryptFilter interface {
	method() []byte
	computeKey(key []byte, objID ObjectId) ([]byte, error)
	decrypt(key, ciphertext []byte) ([]byte, error)
}

type identityCryptFilter struct{}

func (identityCryptFilter) method() []byte { return []byte("Identity") }
func (identityCryptFilter) computeKey(key []byte, _ ObjectId) ([]byte, error) {
	return append([]byte(nil), key...), nil
}
func (identityCryptFilter) decrypt(_ []byte, ct []byte) ([]byte, error) {
	return append([]byte(nil), ct...), nil
}

type rc4CryptFilter struct{}

func (rc4CryptFilter) method() []byte { return []byte("V2") }

func (rc4CryptFilter) computeKey(key []byte, objID ObjectId) ([]byte, error) {
	h := md5.New()
	h.Write(key)
	var numLe [4]byte
	numLe[0] = byte(objID.Num)
	numLe[1] = byte(objID.Num >> 8)
	numLe[2] = byte(objID.Num >> 16)
	h.Write(numLe[:3])
	var genLe [2]byte
	genLe[0] = byte(objID.Gen)
	genLe[1] = byte(objID.Gen >> 8)
	h.Write(genLe[:2])
	sum := h.Sum(nil)
	keyLen := len(key) + 5
	if keyLen > 16 {
		keyLen = 16
	}
	return sum[:keyLen], nil
}

func (rc4CryptFilter) decrypt(key, ct []byte) ([]byte, error) {
	return rc4Apply(key, ct), nil
}

type aes128CryptFilter struct{}

func (aes128CryptFilter) method() []byte { return []byte("AESV2") }

func (aes128CryptFilter) computeKey(key []byte, objID ObjectId) ([]byte, error) {
	builder := make([]byte, 0, len(key)+9)
	builder = append(builder, key...)
	var numLe [4]byte
	numLe[0] = byte(objID.Num)
	numLe[1] = byte(objID.Num >> 8)
	numLe[2] = byte(objID.Num >> 16)
	builder = append(builder, numLe[:3]...)
	var genLe [2]byte
	genLe[0] = byte(objID.Gen)
	genLe[1] = byte(objID.Gen >> 8)
	builder = append(builder, genLe[:2]...)
	builder = append(builder, "sAlT"...)
	sum := md5.Sum(builder)
	keyLen := len(key) + 5
	if keyLen > 16 {
		keyLen = 16
	}
	return sum[:keyLen], nil
}

func (aes128CryptFilter) decrypt(key, ciphertext []byte) ([]byte, error) {
	if len(key) != 16 {
		return nil, decErr(DecInvalidKeyLength)
	}
	if len(ciphertext)%16 != 0 {
		return nil, decErr(DecInvalidCipherTextLength)
	}
	if len(ciphertext) == 0 || len(ciphertext) == 16 {
		return []byte{}, nil
	}
	return aesCBCDecryptPKCS5(key, ciphertext[:16], ciphertext[16:])
}

type aes256CryptFilter struct{}

func (aes256CryptFilter) method() []byte { return []byte("AESV3") }

func (aes256CryptFilter) computeKey(key []byte, _ ObjectId) ([]byte, error) {
	return append([]byte(nil), key...), nil
}

func (aes256CryptFilter) decrypt(key, ciphertext []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, decErr(DecInvalidKeyLength)
	}
	if len(ciphertext)%16 != 0 {
		return nil, decErr(DecInvalidCipherTextLength)
	}
	if len(ciphertext) == 0 || len(ciphertext) == 16 {
		return []byte{}, nil
	}
	return aesCBCDecryptPKCS5(key, ciphertext[:16], ciphertext[16:])
}

// aesCBCDecryptPKCS5 decrypts with AES-CBC and strict PKCS#5 unpadding.
func aesCBCDecryptPKCS5(key, iv, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, decErr(DecInvalidKeyLength)
	}
	if len(data)%16 != 0 || len(data) == 0 {
		return nil, decErr(DecInvalidCipherTextLength)
	}
	mode := cipher.NewCBCDecrypter(block, iv)
	out := make([]byte, len(data))
	mode.CryptBlocks(out, data)
	// Strict PKCS#5 unpad on the final block.
	bs := 16
	n := int(out[bs-1])
	if n == 0 || n > bs {
		return nil, decErr(DecPadding)
	}
	s := bs - n
	for _, v := range out[s : bs-1] {
		if int(v) != n {
			return nil, decErr(DecPadding)
		}
	}
	return out[:s], nil
}

func aesCBCEncryptNoPadding(key, iv, data []byte) []byte {
	block, _ := aes.NewCipher(key)
	mode := cipher.NewCBCEncrypter(block, iv)
	out := make([]byte, len(data))
	mode.CryptBlocks(out, data)
	return out
}

// aesCBCDecryptNoPad decrypts whole CBC buffers with proper block chaining.
func aesCBCDecryptNoPad(key, iv, data []byte) []byte {
	block, _ := aes.NewCipher(key)
	out := make([]byte, 0, len(data))
	chainIV := append([]byte(nil), iv...)
	for i := 0; i+16 <= len(data); i += 16 {
		mode := cipher.NewCBCDecrypter(block, chainIV)
		chunk := make([]byte, 16)
		mode.CryptBlocks(chunk, data[i:i+16])
		out = append(out, chunk...)
		chainIV = append(chainIV[:0], data[i:i+16]...)
	}
	return out
}

func aesECBBlocks(key []byte, data []byte, encrypt bool) []byte {
	block, _ := aes.NewCipher(key)
	out := make([]byte, len(data))
	for i := 0; i+16 <= len(data); i += 16 {
		if encrypt {
			block.Encrypt(out[i:i+16], data[i:i+16])
		} else {
			block.Decrypt(out[i:i+16], data[i:i+16])
		}
	}
	return out
}

// rc4Apply mirrors lopdf's Rc4 (state initialized from key, XOR keystream).
func rc4Apply(key, input []byte) []byte {
	var state [256]byte
	for i := range state {
		state[i] = byte(i)
	}
	var j byte
	for i := 0; i < 256; i++ {
		j = j + state[i] + key[i%len(key)]
		state[i], state[j] = state[j], state[i]
	}
	out := make([]byte, len(input))
	var i byte
	for k, in := range input {
		i++
		j += state[i]
		state[i], state[j] = state[j], state[i]
		out[k] = in ^ state[byte(state[i]+state[j])]
	}
	return out
}

// passwordAlgorithm mirrors lopdf::PasswordAlgorithm (decode fields).
type passwordAlgorithm struct {
	encryptMetadata     bool
	length              *int
	version             int64
	revision            int64
	ownerValue          []byte
	ownerEncrypted      []byte
	userValue           []byte
	userEncrypted       []byte
	permissions         Permissions
	permissionEncrypted []byte
}

// passwordAlgorithmFromDoc mirrors TryFrom<&Document> for PasswordAlgorithm.
func passwordAlgorithmFromDoc(doc *Document) (*passwordAlgorithm, error) {
	encrypted, err := doc.GetEncrypted()
	if err != nil {
		return nil, decErr(DecMissingEncryptDictionary)
	}

	encryptMetadata := true
	if o, err := encrypted.Get([]byte("EncryptMetadata")); err == nil {
		v, err2 := o.AsBool()
		if err2 != nil {
			return nil, decErr(DecInvalidType)
		}
		encryptMetadata = v
	}

	var length *int
	if o, err := encrypted.Get([]byte("Length")); err == nil {
		v, err2 := o.AsI64()
		if err2 != nil {
			return nil, err2
		}
		if v < 0 {
			return nil, &Error{Kind: KindTryFromInt, Detail: "out of range integral type conversion attempted"}
		}
		l := int(v)
		length = &l
	}

	versionObj, err := encrypted.Get([]byte("V"))
	if err != nil {
		return nil, decErr(DecMissingVersion)
	}
	version, err := versionObj.AsI64()
	if err != nil {
		return nil, decErr(DecInvalidType)
	}
	switch version {
	case 1, 2, 4, 5:
	case 0, 3:
		return nil, decErr(DecInvalidVersion)
	default:
		return nil, decErr(DecUnsupportedVersion)
	}

	if length != nil {
		l := *length
		switch version {
		case 1:
			if l != 40 {
				return nil, decErr(DecInvalidKeyLength)
			}
		case 2, 3:
			if l%8 != 0 || l < 40 || l > 128 {
				return nil, decErr(DecInvalidKeyLength)
			}
		case 4:
			if l != 128 {
				return nil, decErr(DecInvalidKeyLength)
			}
		case 5:
			if l != 256 {
				return nil, decErr(DecInvalidKeyLength)
			}
		}
	}

	revisionObj, err := encrypted.Get([]byte("R"))
	if err != nil {
		return nil, decErr(DecMissingRevision)
	}
	revision, err := revisionObj.AsI64()
	if err != nil {
		return nil, decErr(DecInvalidType)
	}

	ownerObj, err := encrypted.Get([]byte("O"))
	if err != nil {
		return nil, decErr(DecMissingOwnerPassword)
	}
	ownerValue, err := ownerObj.AsStr()
	if err != nil {
		return nil, decErr(DecInvalidType)
	}
	ownerValue = append([]byte(nil), ownerValue...)
	if revision <= 4 && len(ownerValue) != 32 {
		return nil, decErr(DecInvalidHashLength)
	}
	if revision >= 5 {
		if len(ownerValue) < 48 {
			return nil, decErr(DecInvalidHashLength)
		}
		ownerValue = ownerValue[:48]
	}

	ownerEncrypted := []byte{}
	if o, err := encrypted.Get([]byte("OE")); err == nil {
		if s, err2 := o.AsStr(); err2 == nil {
			ownerEncrypted = append([]byte(nil), s...)
		}
	}
	if revision >= 5 && len(ownerEncrypted) != 32 {
		return nil, decErr(DecInvalidCipherTextLength)
	}

	userObj, err := encrypted.Get([]byte("U"))
	if err != nil {
		return nil, decErr(DecMissingUserPassword)
	}
	userValue, err := userObj.AsStr()
	if err != nil {
		return nil, decErr(DecInvalidType)
	}
	userValue = append([]byte(nil), userValue...)
	if revision <= 4 && len(userValue) != 32 {
		return nil, decErr(DecInvalidHashLength)
	}
	if revision >= 5 {
		if len(userValue) < 48 {
			return nil, decErr(DecInvalidHashLength)
		}
		userValue = userValue[:48]
	}

	userEncrypted := []byte{}
	if o, err := encrypted.Get([]byte("UE")); err == nil {
		if s, err2 := o.AsStr(); err2 == nil {
			userEncrypted = append([]byte(nil), s...)
		}
	}
	if revision >= 5 && len(userEncrypted) != 32 {
		return nil, decErr(DecInvalidCipherTextLength)
	}

	permObj, err := encrypted.Get([]byte("P"))
	if err != nil {
		return nil, decErr(DecMissingPermissions)
	}
	permValue, err := permObj.AsI64()
	if err != nil {
		return nil, decErr(DecInvalidType)
	}
	permissions := Permissions(uint64(permValue))

	permissionEncrypted := []byte{}
	if o, err := encrypted.Get([]byte("Perms")); err == nil {
		if s, err2 := o.AsStr(); err2 == nil {
			permissionEncrypted = append([]byte(nil), s...)
		}
	}
	if revision >= 5 && len(permissionEncrypted) != 16 {
		return nil, decErr(DecInvalidCipherTextLength)
	}

	return &passwordAlgorithm{
		encryptMetadata:     encryptMetadata,
		length:              length,
		version:             version,
		revision:            revision,
		ownerValue:          ownerValue,
		ownerEncrypted:      ownerEncrypted,
		userValue:           userValue,
		userEncrypted:       userEncrypted,
		permissions:         permissions,
		permissionEncrypted: permissionEncrypted,
	}, nil
}

func (a *passwordAlgorithm) keyLengthOr(defaultLen int64) int64 {
	if a.length != nil {
		return int64(*a.length)
	}
	return defaultLen
}

// fileIDFirst mirrors reading trailer /ID[0].
func fileIDFirst(doc *Document) ([]byte, error) {
	idObj, err := doc.Trailer.Get([]byte("ID"))
	if err != nil {
		return nil, decErr(DecMissingFileID)
	}
	arr, err := idObj.AsArray()
	if err != nil {
		return nil, decErr(DecInvalidType)
	}
	if len(arr) == 0 {
		return nil, decErr(DecInvalidType)
	}
	s, err := arr[0].AsStr()
	if err != nil {
		return nil, decErr(DecInvalidType)
	}
	return s, nil
}

// sanitizePasswordR4 mirrors sanitize_password_r4: encode via PDFDocEncoding.
func (a *passwordAlgorithm) sanitizePasswordR4(password string) ([]byte, error) {
	return stringToBytesPDFDoc(password), nil
}

// sanitizePasswordR6 mirrors sanitize_password_r6: SASLprep + UTF-8 bytes.
func (a *passwordAlgorithm) sanitizePasswordR6(password string) ([]byte, error) {
	prepped, err := saslprep(password)
	if err != nil {
		return nil, &Error{Kind: KindDecryption, Inner: err}
	}
	return []byte(prepped), nil
}

// sanitizePassword mirrors sanitize_password.
func (a *passwordAlgorithm) sanitizePassword(password string) ([]byte, error) {
	switch a.revision {
	case 2, 3, 4:
		return a.sanitizePasswordR4(password)
	case 5, 6:
		return a.sanitizePasswordR6(password)
	}
	return nil, decErr(DecUnsupportedRevision)
}

// computeFileEncryptionKeyR4 mirrors compute_file_encryption_key_r4 (Alg. 2).
func (a *passwordAlgorithm) computeFileEncryptionKeyR4(doc *Document, password []byte) ([]byte, error) {
	length := len(password)
	if length > 32 {
		length = 32
	}
	h := md5.New()
	h.Write(password[:length])
	h.Write(padBytes[:32-length])
	h.Write(a.ownerValue)
	var pLE [4]byte
	p := uint32(a.permissions)
	pLE[0] = byte(p)
	pLE[1] = byte(p >> 8)
	pLE[2] = byte(p >> 16)
	pLE[3] = byte(p >> 24)
	h.Write(pLE[:])
	fileID0, err := fileIDFirst(doc)
	if err != nil {
		return nil, err
	}
	h.Write(fileID0)
	if a.revision >= 4 && !a.encryptMetadata {
		h.Write([]byte{0xff, 0xff, 0xff, 0xff})
	}
	hash := h.Sum(nil)

	var n int64
	if a.revision >= 3 {
		n = a.keyLengthOr(40) / 8
	} else {
		n = 5
	}
	if n > 16 {
		return nil, decErr(DecInvalidKeyLength)
	}
	if a.revision >= 3 {
		for range 50 {
			sum := md5.Sum(hash[:n])
			hash = sum[:]
		}
	}
	return hash[:n], nil
}

// computeHashR6 mirrors compute_hash (Alg. 2.B).
func (a *passwordAlgorithm) computeHashR6(password, salt []byte, userKey []byte) ([]byte, error) {
	h := sha256.New()
	h.Write(password)
	h.Write(salt)
	if userKey != nil {
		h.Write(userKey)
	}
	k := h.Sum(nil)
	if a.revision == 5 {
		return k, nil
	}

	k1 := make([]byte, 0, 64*(len(password)+64+len(userKey)))
	for round := 1; ; round++ {
		k1 = k1[:0]
		for range 64 {
			k1 = append(k1, password...)
			k1 = append(k1, k...)
			if userKey != nil {
				k1 = append(k1, userKey...)
			}
		}
		e := aesCBCEncryptNoPadding(k[:16], k[16:32], k1)
		var total uint32
		for _, v := range e[:16] {
			total += uint32(v)
		}
		var hh hash.Hash
		switch total % 3 {
		case 0:
			hh = sha256.New()
		case 1:
			hh = sha512.New384()
		default:
			hh = sha512.New()
		}
		hh.Write(e)
		k = hh.Sum(nil)
		last := byte(0)
		if len(e) > 0 {
			last = e[len(e)-1]
		}
		if round >= 64 && uint32(last) <= uint32(round-32) {
			break
		}
	}
	return k[:32], nil
}
