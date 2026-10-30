package backup

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"strings"
)

const StreamVersion = 3
const manifestLineBytes = 64 << 10

// ManifestDamage identifies a damaged region in the owner's selected manifest.
// Offsets are zero-based bytes, EndByte exclusive; lines are one-based.
type ManifestDamage = persistence.ManifestDamage

type streamRecord struct {
	File     *FileEntry `json:"file,omitempty"`
	Previous string     `json:"previous,omitempty"`
	SHA256   string     `json:"sha256,omitempty"`
	End      *streamEnd `json:"end,omitempty"`
}
type streamEnd struct {
	Entries int64  `json:"entries"`
	Bytes   int64  `json:"bytes"`
	SHA256  string `json:"sha256"`
}
type streamPosition struct{ Line, Start, End int64 }

func validManifestEntry(e FileEntry) bool {
	sum, err := hex.DecodeString(e.SHA256)
	return safeRelativeFile(e.Name) && restorableFile(e.Name) && e.Bytes >= 0 && err == nil && len(sum) == sha256.Size
}
func entryDigest(previous string, e FileEntry) string {
	raw, _ := json.Marshal(e)
	h := sha256.New()
	h.Write([]byte(previous))
	h.Write([]byte{'\n'})
	h.Write(raw)
	return hex.EncodeToString(h.Sum(nil))
}

// streamManifestWriter writes one manifest; neither hashes nor filenames grow
// with the library. Each record is bounded, with a running hash and end marker.
type streamManifestWriter struct {
	file           *os.File
	buffered       *bufio.Writer
	whole          hash.Hash
	previous       string
	entries, total int64
}

func newStreamManifestWriter(path string, m Manifest) (*streamManifestWriter, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	w := &streamManifestWriter{file: f, buffered: bufio.NewWriterSize(f, manifestLineBytes), whole: sha256.New()}
	m.Version = StreamVersion
	m.Files = nil
	m.streamPath = ""
	m.streamHash = ""
	raw, err := json.Marshal(m)
	if err == nil {
		err = w.line(raw)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	w.previous = hex.EncodeToString(w.whole.Sum(nil))
	return w, nil
}
func (w *streamManifestWriter) line(raw []byte) error {
	if len(raw)+1 > manifestLineBytes {
		return fmt.Errorf("backup manifest record exceeds %d bytes", manifestLineBytes)
	}
	raw = append(raw, '\n')
	if _, err := w.buffered.Write(raw); err != nil {
		return err
	}
	w.whole.Write(raw)
	return nil
}
func (w *streamManifestWriter) add(e FileEntry) error {
	if !validManifestEntry(e) {
		return ErrSourceInvalid
	}
	if w.entries == math.MaxInt64 || e.Bytes > math.MaxInt64-w.total {
		return errors.New("backup manifest size exceeds integer range")
	}
	digest := entryDigest(w.previous, e)
	raw, err := json.Marshal(streamRecord{File: &e, Previous: w.previous, SHA256: digest})
	if err != nil {
		return err
	}
	if err = w.line(raw); err != nil {
		return err
	}
	w.previous = digest
	w.entries++
	w.total += e.Bytes
	return nil
}
func (w *streamManifestWriter) finish() error {
	raw, err := json.Marshal(streamRecord{End: &streamEnd{Entries: w.entries, Bytes: w.total, SHA256: hex.EncodeToString(w.whole.Sum(nil))}})
	if err == nil {
		err = w.line(raw)
	}
	if err == nil {
		err = w.buffered.Flush()
	}
	if err == nil {
		err = w.file.Sync()
	}
	return errors.Join(err, w.file.Close())
}

// nextManifestLine discards an oversized line in bounded pieces, so recovery
// can resume at the next newline instead of losing every later readable file.
func nextManifestLine(r *bufio.Reader) (raw []byte, size int64, oversized bool, err error) {
	for {
		part, e := r.ReadSlice('\n')
		size += int64(len(part))
		if !oversized && len(raw)+len(part) <= manifestLineBytes {
			raw = append(raw, part...)
		} else {
			oversized = true
			raw = nil
		}
		if e == bufio.ErrBufferFull {
			continue
		}
		return raw, size, oversized, e
	}
}

// scanStream verifies each readable entry and its chain, reporting exact bad
// regions. A damaged record does not prevent the next record from being tried.
// The captured whole-file digest still detects substitution after selection.
func scanStream(ctx context.Context, m Manifest, visit func(FileEntry, streamPosition) error, damage func(ManifestDamage), finished ...func(streamPosition)) error {
	if err := regularManifestFile(filepath.Dir(m.streamPath), filepath.Base(m.streamPath)); err != nil {
		return err
	}
	f, err := os.Open(m.streamPath)
	if err != nil {
		return err
	}
	defer f.Close()
	whole := sha256.New()
	r := bufio.NewReaderSize(io.TeeReader(&scrubReader{ctx: ctx, r: f}, whole), manifestLineBytes)
	var line, offset, entries, total int64
	previous := ""
	ended := false
	report := func(pos streamPosition, reason string) {
		if damage != nil {
			damage(ManifestDamage{FirstLine: pos.Line, LastLine: pos.Line, StartByte: pos.Start, EndByte: pos.End, Reason: reason})
		}
	}
	// TeeReader may prefetch later bytes, so end-record validation hashes lines
	// separately rather than consulting the reader's prefetch hash.
	chain := sha256.New()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		raw, n, oversized, readErr := nextManifestLine(r)
		if n == 0 && errors.Is(readErr, io.EOF) {
			break
		}
		line++
		pos := streamPosition{line, offset, offset + n}
		offset += n
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return readErr
		}
		if line == 1 {
			var header Manifest
			if oversized || json.Unmarshal(raw, &header) != nil || header.Format != Format || header.Version != StreamVersion {
				report(pos, "unreadable_record")
			}
			chain.Write(raw)
			previous = hex.EncodeToString(chain.Sum(nil))
			continue
		}
		if oversized {
			report(pos, "record_too_large")
			continue
		}
		var rec streamRecord
		if json.Unmarshal(raw, &rec) != nil {
			report(pos, "unreadable_record")
			chain.Write(raw)
			continue
		}
		if rec.End != nil {
			if ended || rec.File != nil || rec.End.Entries != entries || rec.End.Bytes != total || rec.End.SHA256 != hex.EncodeToString(chain.Sum(nil)) {
				report(pos, "manifest_end_mismatch")
			}
			ended = true
			chain.Write(raw)
			continue
		}
		if ended {
			report(pos, "record_after_end")
			continue
		}
		chain.Write(raw)
		if rec.File == nil || !validManifestEntry(*rec.File) {
			report(pos, "invalid_file_entry")
			continue
		}
		e := *rec.File
		computed := entryDigest(rec.Previous, e)
		if rec.SHA256 != computed {
			// A damaged chain checksum does not erase a readable file record.
			// Recovery still has to verify its payload against e.SHA256; strict
			// validation reports the damage and refuses ordinary publication.
			report(pos, "entry_hash_mismatch")
		}
		if rec.Previous != previous {
			report(pos, "entry_chain_gap")
		}
		previous = computed
		if entries == math.MaxInt64 || e.Bytes > math.MaxInt64-total {
			return ErrSourceInvalid
		}
		entries++
		total += e.Bytes
		if err := visit(e, pos); err != nil {
			return err
		}
	}
	for _, finish := range finished {
		finish(streamPosition{line + 1, offset, offset})
	}
	if !ended {
		report(streamPosition{line + 1, offset, offset}, "missing_end_record")
	}
	if m.streamHash != "" && hex.EncodeToString(whole.Sum(nil)) != m.streamHash {
		return ErrIntegrity
	}
	return nil
}

func visitManifest(ctx context.Context, folder string, m Manifest, visit func(FileEntry) error) error {
	if m.Version == StreamVersion && m.streamPath != "" {
		var damaged bool
		err := scanStream(ctx, m, func(e FileEntry, _ streamPosition) error { return visit(e) }, func(ManifestDamage) { damaged = true })
		if err != nil {
			return err
		}
		if damaged {
			return ErrIntegrity
		}
		return nil
	}
	for _, e := range m.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(e); err != nil {
			return err
		}
	}
	return nil
}

// walkStateTree does not load a flat million-blob directory into memory.
func walkStateTree(ctx context.Context, root, relative string, visit func(string, fs.DirEntry) error) error {
	var walk func(string, int) error
	walk = func(rel string, depth int) error {
		if depth > 128 {
			return ErrSourceInvalid
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		p := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Lstat(p)
		if os.IsNotExist(err) && depth == 0 {
			return nil
		}
		if err != nil {
			return err
		}
		entry := fs.FileInfoToDirEntry(info)
		if rel == "Keys/managed-storage" && !entry.IsDir() {
			return ErrSourceInvalid
		}
		if strings.HasPrefix(rel, "Keys/managed-storage/") && (entry.IsDir() || !strings.HasSuffix(rel, ".conf")) {
			return nil
		}
		if err = visit(rel, entry); err != nil {
			if err == fs.SkipDir && entry.IsDir() {
				return nil
			}
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		for {
			batch, err := f.ReadDir(128)
			for _, e := range batch {
				name := e.Name()
				if rel != "" {
					name = rel + "/" + name
				}
				if err := walk(name, depth+1); err != nil {
					return err
				}
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				return err
			}
		}
	}
	return walk(relative, 0)
}

func writeStreamTree(ctx context.Context, folder string, m Manifest) (Manifest, error) {
	next := filepath.Join(folder, ManifestName+".next")
	if err := os.Remove(next); err != nil && !os.IsNotExist(err) {
		return m, err
	}
	defer os.Remove(next)
	writer, err := newStreamManifestWriter(next, m)
	if err != nil {
		return m, err
	}
	for _, name := range append([]string{DatabaseName, DatabaseName + "-wal"}, stateFiles()...) {
		err = walkStateTree(ctx, folder, name, func(name string, e fs.DirEntry) error {
			if e.IsDir() {
				return nil
			}
			if !e.Type().IsRegular() {
				return ErrSourceInvalid
			}
			if name == DatabaseName+"-wal" {
				info, err := e.Info()
				if err != nil {
					return err
				}
				if info.Size() == 0 {
					return nil
				}
			}
			entry, err := hashFile(ctx, folder, name)
			if err != nil {
				return err
			}
			return writer.add(entry)
		})
		if err != nil {
			writer.file.Close()
			return m, err
		}
	}
	if err = writer.finish(); err != nil {
		return m, err
	}
	if err = os.Rename(next, filepath.Join(folder, ManifestName)); err != nil {
		return m, err
	}
	if err = syncDirectory(folder); err != nil {
		return m, err
	}
	return readManifest(folder)
}

// readStreamHeader captures the complete manifest digest without retaining its
// entries. A tiny prefix view remains available to legacy small unit fixtures.
func readStreamHeader(dir string) (Manifest, error) {
	var m Manifest
	f, err := os.Open(filepath.Join(dir, ManifestName))
	if err != nil {
		return m, err
	}
	r := bufio.NewReaderSize(f, manifestLineBytes)
	raw, _, oversized, err := nextManifestLine(r)
	f.Close()
	if err != nil && !errors.Is(err, io.EOF) {
		return m, err
	}
	if oversized || json.Unmarshal(raw, &m) != nil || m.Format != Format || m.Version != StreamVersion {
		m = Manifest{Format: Format, Version: StreamVersion}
	}
	m.streamPath = filepath.Join(dir, ManifestName)
	e, err := hashFile(context.Background(), dir, ManifestName)
	if err != nil {
		return m, err
	}
	m.streamHash = e.SHA256
	// Do not retain a full file registry. The bounded convenience view does not
	// participate in production validation or restore.
	small := make([]FileEntry, 0, 128)
	large := false
	err = scanStream(context.Background(), m, func(e FileEntry, _ streamPosition) error {
		if len(small) < 128 && !large {
			small = append(small, e)
		} else {
			large = true
			small = nil
		}
		return nil
	}, func(ManifestDamage) {})
	if err != nil {
		return m, err
	}
	if !large {
		m.Files = small
	}
	return m, nil
}

func isStreamManifest(dir string) bool {
	f, err := os.Open(filepath.Join(dir, ManifestName))
	if err != nil {
		return false
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, manifestLineBytes)
	raw, _, large, _ := nextManifestLine(r)
	var m Manifest
	if !large && json.Unmarshal(raw, &m) == nil {
		if m.Format == Format && m.Version == StreamVersion {
			return true
		}
		if m.Version == LegacyVersion || m.Version == HashedVersion {
			return false
		}
	}
	// A damaged header and first entry do not discard later readable records.
	for {
		raw, size, large, readErr := nextManifestLine(r)
		var rec streamRecord
		if !large && json.Unmarshal(raw, &rec) == nil && (rec.File != nil || rec.End != nil) {
			return true
		}
		if size == 0 || errors.Is(readErr, io.EOF) || readErr != nil {
			return false
		}
	}
}

func streamHeader(m Manifest) Manifest { m.Files = nil; m.streamPath = ""; m.streamHash = ""; return m }

func syncManifestDirectories(ctx context.Context, folder string, m Manifest) error {
	return visitManifest(ctx, folder, m, func(e FileEntry) error {
		for dir := filepath.Dir(filepath.FromSlash(e.Name)); ; dir = filepath.Dir(dir) {
			if err := syncDirectory(filepath.Join(folder, dir)); err != nil {
				return err
			}
			if dir == "." {
				break
			}
		}
		return nil
	})
}

func visitBackupNames(ctx context.Context, folder string, m Manifest, visit func(string) error) error {
	if err := visit(ManifestName); err != nil {
		return err
	}
	return visitManifest(ctx, folder, m, func(e FileEntry) error { return visit(e.Name) })
}
