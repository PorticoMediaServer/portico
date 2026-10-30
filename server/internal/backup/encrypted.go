package backup

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/crypto/argon2"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

const encryptedPayload = "archive.pba"
const encryptedSummary = "encrypted.json"
const maxEncryptedSummaryBytes = 64 << 10
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
		return nil, ErrInvalid
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
		return nil, ErrInvalid
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
		return ErrInvalid
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
		return nil, ErrIntegrity
	}
	if !bytes.Equal(header[:len(envelopeMagic)], envelopeMagic) {
		return nil, ErrSourceInvalid
	}
	if password == "" || len(password) > 1024 {
		return nil, ErrIntegrity
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
		return 0, ErrIntegrity
	}
	var size [4]byte
	if _, e := io.ReadFull(r.in, size[:]); e != nil {
		return 0, ErrIntegrity
	}
	n := binary.BigEndian.Uint32(size[:])
	if n > chunkBytes {
		return 0, ErrIntegrity
	}
	sealed := make([]byte, int(n)+r.aead.Overhead())
	if _, e := io.ReadFull(r.in, sealed); e != nil {
		return 0, ErrIntegrity
	}
	nonce := make([]byte, r.aead.NonceSize())
	binary.BigEndian.PutUint64(nonce[4:], r.sequence)
	aad := append(append([]byte(nil), r.header...), size[:]...)
	plain, e := r.aead.Open(nil, nonce, sealed, aad)
	if e != nil {
		return 0, ErrIntegrity
	}
	r.sequence++
	if n == 0 {
		var extra [1]byte
		count, e := r.in.Read(extra[:])
		if count != 0 || !errors.Is(e, io.EOF) {
			return 0, ErrIntegrity
		}
		r.done = true
		return 0, io.EOF
	}
	r.pending = plain
	return r.Read(b)
}

func encryptFolder(ctx context.Context, folder string, destination *os.Root, material *archiveKeyMaterial, manifest Manifest) error {
	f, e := destination.OpenFile(encryptedPayload, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	enc, e := newEncryptedWriter(f, material)
	if e != nil {
		return e
	}
	archive := tar.NewWriter(enc)
	e = visitBackupNames(ctx, folder, manifest, func(name string) error {
		if e = regularManifestFile(folder, name); e != nil {
			return e
		}
		in, e := os.Open(filepath.Join(folder, filepath.FromSlash(name)))
		if e != nil {
			return e
		}
		info, e := in.Stat()
		if e == nil {
			e = archive.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: info.Size(), Typeflag: tar.TypeReg})
		}
		if e == nil {
			_, e = io.Copy(archive, &scrubReader{ctx: ctx, r: in})
		}
		closeErr := in.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
		return nil
	})
	if e != nil {
		return e
	}
	if e = archive.Close(); e != nil {
		return e
	}
	if e = enc.Close(); e != nil {
		return e
	}
	if e = finishArchiveFile(f); e != nil {
		return e
	}
	// No plaintext paths, hashes, keys or password material in the listing.
	summary := manifest
	summary.Files = nil
	raw, e := json.Marshal(summary)
	if e != nil {
		return e
	}
	return durableRootFile(destination, encryptedSummary, raw)
}

func encryptedManifest(folder string) (Manifest, error) {
	var m Manifest
	if e := regularManifestFile(folder, encryptedSummary); e != nil {
		return m, e
	}
	f, e := os.Open(filepath.Join(folder, encryptedSummary))
	if e != nil {
		return m, e
	}
	defer f.Close()
	raw, e := io.ReadAll(io.LimitReader(f, maxEncryptedSummaryBytes+1))
	if e != nil {
		return m, e
	}
	if len(raw) > maxEncryptedSummaryBytes {
		return m, ErrSourceInvalid
	}
	if e = json.Unmarshal(raw, &m); e != nil {
		return m, ErrSourceInvalid
	}
	if m.Format != Format || (m.Version != Version && m.Version != HashedVersion) || len(m.Files) != 0 {
		return m, ErrSourceInvalid
	}
	if e = regularManifestFile(folder, encryptedPayload); e != nil {
		return m, e
	}
	return m, nil
}

// Decryption writes only private data/Temp, never a caller-selected install path.
// The authenticated final record is mandatory even after tar's end marker.
func (s *Service) decryptSource(ctx context.Context, source Source, password string) (Source, func(), error) {
	folder := source.Path
	if source.BackupID != "" {
		var e error
		folder, e = s.backupPath(source.BackupID)
		if e != nil {
			return source, func() {}, ErrSourceInvalid
		}
	}
	if folder == "" {
		return source, func() {}, nil
	}
	var summary *Manifest
	payload := filepath.Join(folder, encryptedPayload)
	info, e := os.Lstat(folder)
	if e != nil {
		return source, func() {}, nil
	}
	if info.IsDir() {
		if e = s.sourceFilesSafe(filepath.Join(folder, encryptedSummary), payload); e != nil {
			return source, func() {}, e
		}
		if _, e = os.Lstat(filepath.Join(folder, encryptedSummary)); os.IsNotExist(e) {
			return source, func() {}, nil
		}
		m, e := encryptedManifest(folder)
		if e != nil {
			return source, func() {}, e
		}
		summary = &m
	} else if info.Mode().IsRegular() {
		// A portable encrypted file carries its own authenticated manifest; the
		// public listing summary is optional when selecting a file directly.
		root, e := openCanonicalRoot(filepath.Dir(folder))
		if e != nil {
			return source, func() {}, ErrSourceInvalid
		}
		prefix, e := s.inspectRootFile(ctx, root, filepath.Base(folder), 100, false)
		closeErr := root.Close()
		if e != nil {
			return source, func() {}, e
		}
		if closeErr != nil {
			return source, func() {}, ErrSourceInvalid
		}
		if !bytes.HasPrefix(prefix, envelopeMagic) {
			// A damaged/unsupported encrypted header must never enter plaintext
			// staging and replace an already validated pending restore.
			if bytes.HasPrefix(prefix, envelopeMagic[:len(envelopeMagic)-1]) {
				return source, func() {}, ErrSourceInvalid
			}
			// Preserve raw SQLite restore and the published non-Portico refusal,
			// but reject all other bare-file content before staging can mutate.
			if !inspectionSQLiteHeader(prefix) {
				return source, func() {}, persistence.ErrCandidateNotPortico
			}
			return source, func() {}, nil
		}
		payload = folder
	} else {
		return source, func() {}, ErrSourceInvalid
	}
	if e = s.sourceFilesSafe(payload); e != nil {
		return source, func() {}, e
	}
	in, e := os.Open(payload)
	if e != nil {
		return source, func() {}, ErrSourceInvalid
	}
	defer in.Close()
	enc, e := newEncryptedReader(in, password)
	if e != nil {
		return source, func() {}, e
	}
	tmp, e := s.privateScratch("backup-decrypt-")
	if e != nil {
		return source, func() {}, e
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	failed := true
	defer func() {
		if failed {
			cleanup()
		}
	}()
	tr := tar.NewReader(&scrubReader{ctx: ctx, r: enc})
	for {
		h, e := tr.Next()
		if errors.Is(e, io.EOF) {
			break
		}
		if e != nil {
			return source, func() {}, ErrIntegrity
		}
		if h.Typeflag != tar.TypeReg || h.Size < 0 || !safeRelativeFile(h.Name) || h.Name != ManifestName && !restorableFile(h.Name) {
			return source, func() {}, ErrSourceInvalid
		}
		if e = checkFreeSpace(tmp, h.Size); e != nil {
			return source, func() {}, e
		}
		target := filepath.Join(tmp, filepath.FromSlash(h.Name))
		if e = os.MkdirAll(filepath.Dir(target), 0700); e != nil {
			return source, func() {}, e
		}
		out, e := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return source, func() {}, e
		}
		n, copyErr := io.Copy(out, tr)
		syncErr := out.Sync()
		closeErr := out.Close()
		if copyErr != nil || syncErr != nil || closeErr != nil || n != h.Size {
			return source, func() {}, ErrIntegrity
		}
	}
	// tar consumes its terminator, but authentication must consume every byte.
	rest := make([]byte, 4096)
	n, e := io.ReadFull(enc, rest)
	if !errors.Is(e, io.EOF) || n != 0 {
		return source, func() {}, ErrIntegrity
	}
	manifest, e := readManifest(tmp)
	if e != nil {
		return source, func() {}, e
	}
	compare := manifest
	compare.Files = nil
	if summary != nil {
		a, _ := json.Marshal(summary)
		b, _ := json.Marshal(compare)
		if !bytes.Equal(a, b) {
			return source, func() {}, ErrIntegrity
		}
	}
	if manifest.Version != StreamVersion {
		if e = checkSourceHashes(tmp, manifest); e != nil {
			return source, func() {}, e
		}
	}
	failed = false
	return Source{Path: tmp}, cleanup, nil
}

func (s *Service) privateScratch(prefix string) (string, error) {
	root := filepath.Join(s.state, "Temp")
	if e := canonicalDirectory(root, true); e != nil {
		return "", e
	}
	return os.MkdirTemp(root, prefix)
}

func durableRootFile(root *os.Root, name string, raw []byte) error {
	f, e := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	e = writeAll(f, raw)
	if e == nil {
		e = f.Sync()
	}
	return errors.Join(e, f.Close())
}
func syncRoot(root *os.Root) error {
	f, e := root.Open(".")
	if e != nil {
		return e
	}
	defer f.Close()
	return syncDirectoryHandle(f)
}

// Root confines every write even if a selected directory is concurrently renamed.
func publishPlain(ctx context.Context, folder string, root *os.Root, manifest Manifest) error {
	return visitBackupNames(ctx, folder, manifest, func(name string) error {
		if e := regularManifestFile(folder, name); e != nil {
			return e
		}
		if e := root.MkdirAll(filepath.Dir(filepath.FromSlash(name)), 0700); e != nil {
			return e
		}
		in, e := os.Open(filepath.Join(folder, filepath.FromSlash(name)))
		if e != nil {
			return e
		}
		out, e := root.OpenFile(filepath.FromSlash(name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			in.Close()
			return e
		}
		_, e = io.Copy(out, &scrubReader{ctx: ctx, r: in})
		if e == nil {
			e = out.Sync()
		}
		e = errors.Join(e, out.Close(), in.Close())
		if e != nil {
			return e
		}
		for d := filepath.Dir(filepath.FromSlash(name)); ; d = filepath.Dir(d) {
			f, e := root.Open(d)
			if e != nil {
				return e
			}
			e = syncDirectoryHandle(f)
			f.Close()
			if e != nil {
				return e
			}
			if d == "." {
				break
			}
		}
		return nil
	})
}

// Both barriers must succeed before an encrypted copy can be published. The
// seam lets tests reproduce delayed write errors reported only by Close.
type archiveSyncCloser interface {
	Sync() error
	Close() error
}

var finishArchiveFile = func(file archiveSyncCloser) error {
	syncErr := file.Sync()
	closeErr := file.Close()
	return errors.Join(syncErr, closeErr)
}
