package backup

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"

	"golang.org/x/crypto/argon2"
)

// The existing Portico v2 backup envelope: bounded, authenticated records and a
// mandatory final record protect against damaged, reordered or truncated files.
var ErrPassphrase = errors.New("the backup needs its passphrase, or is damaged")

const chunkBytes = 64 << 10

var envelopeMagic = []byte("PORTICO-BACKUP-ENC\x00\x02")

const kdfSaltBytes = 16
const archiveSaltBytes = 16

// Only derived material is retained for unattended backups. The passphrase
// remains a write-only input. Fixed v2 KDF parameters bound untrusted headers.
type archiveKeyMaterial struct {
	salt [kdfSaltBytes]byte
	key  []byte
}

func (m *archiveKeyMaterial) clear() {
	if m != nil {
		clear(m.key)
	}
}
func deriveMaster(password string, salt []byte) []byte {
	input := []byte(password)
	defer clear(input)
	return argon2.IDKey(input, salt, 3, 64*1024, 4, 32)
}
func newArchiveKeyMaterial(password string) (*archiveKeyMaterial, error) {
	m := &archiveKeyMaterial{}
	if _, err := rand.Read(m.salt[:]); err != nil {
		return nil, err
	}
	m.key = deriveMaster(password, m.salt[:])
	return m, nil
}

// A fresh archive salt derives an independent AES key, so sequence zero can
// safely start every archive even while the saved master remains unchanged.
func archiveAEAD(master, salt []byte) (cipher.AEAD, error) {
	if len(master) != 32 || len(salt) != archiveSaltBytes {
		return nil, ErrPassphrase
	}
	key, err := hkdf.Key(sha256.New, master, salt, "portico backup envelope v2", 32)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

type encryptedWriter struct {
	out      io.Writer
	aead     cipher.AEAD
	header   []byte
	sequence uint64
}

func newEncryptedWriter(out io.Writer, material *archiveKeyMaterial) (*encryptedWriter, error) {
	if material == nil || len(material.key) != 32 {
		return nil, ErrPassphrase
	}
	salt := make([]byte, archiveSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	header := append(append(append([]byte(nil), envelopeMagic...), material.salt[:]...), salt...)
	aead, err := archiveAEAD(material.key, salt)
	if err != nil {
		return nil, err
	}
	if err = writeAll(out, header); err != nil {
		return nil, err
	}
	return &encryptedWriter{out: out, aead: aead, header: header}, nil
}
func writeAll(w io.Writer, b []byte) error {
	n, e := w.Write(b)
	if e == nil && n != len(b) {
		e = io.ErrShortWrite
	}
	return e
}
func (w *encryptedWriter) record(b []byte) error {
	if w.sequence >= 1<<32 {
		return ErrPassphrase
	}
	nonce := make([]byte, w.aead.NonceSize())
	binary.BigEndian.PutUint64(nonce[4:], w.sequence)
	size := make([]byte, 4)
	binary.BigEndian.PutUint32(size, uint32(len(b)))
	aad := append(append([]byte(nil), w.header...), size...)
	sealed := w.aead.Seal(nil, nonce, b, aad)
	if e := writeAll(w.out, size); e != nil {
		return e
	}
	if e := writeAll(w.out, sealed); e != nil {
		return e
	}
	w.sequence++
	return nil
}
func (w *encryptedWriter) Write(b []byte) (int, error) {
	n := 0
	for len(b) > 0 {
		c := min(len(b), chunkBytes)
		if e := w.record(b[:c]); e != nil {
			return n, e
		}
		n += c
		b = b[c:]
	}
	return n, nil
}
func (w *encryptedWriter) Close() error { return w.record(nil) }

type encryptedReader struct {
	in       io.Reader
	aead     cipher.AEAD
	header   []byte
	sequence uint64
	pending  []byte
	done     bool
}

func newEncryptedReader(in io.Reader, password string) (*encryptedReader, error) {
	header := make([]byte, len(envelopeMagic)+kdfSaltBytes+archiveSaltBytes)
	if _, e := io.ReadFull(in, header); e != nil {
		return nil, ErrPassphrase
	}
	if !bytes.Equal(header[:len(envelopeMagic)], envelopeMagic) {
		return nil, ErrPassphrase
	}
	if password == "" || len(password) > 1024 {
		return nil, ErrPassphrase
	}
	master := deriveMaster(password, header[len(envelopeMagic):len(envelopeMagic)+kdfSaltBytes])
	defer clear(master)
	aead, e := archiveAEAD(master, header[len(envelopeMagic)+kdfSaltBytes:])
	if e != nil {
		return nil, e
	}
	return &encryptedReader{in: in, aead: aead, header: header}, nil
}
func (r *encryptedReader) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	if len(r.pending) > 0 {
		n := copy(b, r.pending)
		r.pending = r.pending[n:]
		return n, nil
	}
	if r.done {
		return 0, io.EOF
	}
	if r.sequence >= 1<<32 {
		return 0, ErrPassphrase
	}
	var size [4]byte
	if _, e := io.ReadFull(r.in, size[:]); e != nil {
		return 0, ErrPassphrase
	}
	n := binary.BigEndian.Uint32(size[:])
	if n > chunkBytes {
		return 0, ErrPassphrase
	}
	sealed := make([]byte, int(n)+r.aead.Overhead())
	if _, e := io.ReadFull(r.in, sealed); e != nil {
		return 0, ErrPassphrase
	}
	nonce := make([]byte, r.aead.NonceSize())
	binary.BigEndian.PutUint64(nonce[4:], r.sequence)
	aad := append(append([]byte(nil), r.header...), size[:]...)
	plain, e := r.aead.Open(nil, nonce, sealed, aad)
	if e != nil {
		return 0, ErrPassphrase
	}
	r.sequence++
	if n == 0 {
		var extra [1]byte
		count, e := r.in.Read(extra[:])
		if count != 0 || !errors.Is(e, io.EOF) {
			return 0, ErrPassphrase
		}
		r.done = true
		return 0, io.EOF
	}
	r.pending = plain
	return r.Read(b)
}
