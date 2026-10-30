package reading

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

const mobiDependency = "libmobi:85dcfe803fc2a21020ddcf15c3eb66b93d388add"
const mobiOptions = "portico-epub-v1;unencrypted-only;no-replica;default-kf8"
const maxMobiInput = 64 << 20
const maxMobiOutput = 128 << 20

// MobiProvenance identifies actual original bytes and derived output separately.
// Nothing in it is a substitute for the captured Source's live authority.
type MobiProvenance struct {
	OriginalRevision string
	OriginalSourceID string
	OriginalFormat   string
	OriginalSize     int64
	OutputSize       int64
	InputSHA256      string
	Converter        string
	WorkerSHA256     string
	Options          string
	OutputSHA256     string
	Revision         string
}

type MobiBundle struct {
	// Exact Release-qualified absolute files, never PATH discovery or downloads.
	Sandbox, SandboxSHA256 string
	Worker, WorkerSHA256   string
	Scratch                string
}

type MobiConverter struct {
	bundle     MobiBundle
	supervisor *storage.Supervisor
	slot       chan struct{}
	retained   chan struct{}
}

func NewMobiConverter(bundle MobiBundle, supervisor *storage.Supervisor) (*MobiConverter, error) {
	if supervisor == nil || !filepath.IsAbs(bundle.Scratch) {
		return nil, ErrUnavailable
	}
	// The already provisioned scratch root must be private and not a symlink.
	info, err := os.Lstat(bundle.Scratch)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return nil, ErrUnavailable
	}
	for _, entry := range []struct{ path, digest string }{{bundle.Sandbox, bundle.SandboxSHA256}, {bundle.Worker, bundle.WorkerSHA256}} {
		if !filepath.IsAbs(entry.path) || len(entry.digest) != 64 {
			return nil, ErrUnavailable
		}
		digest, err := hex.DecodeString(entry.digest)
		if err != nil || len(digest) != 32 {
			return nil, ErrUnavailable
		}
	}
	if err := validateMobiBundle(bundle); err != nil {
		return nil, err
	}
	return &MobiConverter{bundle: bundle, supervisor: supervisor, slot: make(chan struct{}, 1), retained: make(chan struct{}, 2)}, nil
}

// DerivedMobi is request/session-owned, not an independently authorized file.
// Its reader keeps every parser extent behind the original physical-source fence.
type mobiCache struct {
	mu         sync.RWMutex
	raw        []byte
	provenance MobiProvenance
	closed     bool
	release    func()
}

func (cache *mobiCache) close() {
	cache.mu.Lock()
	if cache.closed {
		cache.mu.Unlock()
		return
	}
	cache.closed = true
	clear(cache.raw)
	cache.raw = nil
	release := cache.release
	cache.release = nil
	cache.mu.Unlock()
	if release != nil {
		release()
	}
}
func (cache *mobiCache) size() int {
	cache.mu.RLock()
	defer cache.mu.RUnlock()
	if cache.closed {
		return 0
	}
	return len(cache.raw)
}

type DerivedMobi struct {
	source *Source
	cache  *mobiCache
}

func (d *DerivedMobi) Provenance() MobiProvenance { return d.cache.provenance }
func (d *DerivedMobi) Close()                     { d.cache.close() }
func (d *DerivedMobi) ReadAt(p []byte, offset int64) (int, error) {
	return d.readAt(d.source.Context(), p, offset)
}

type mobiContextReader struct {
	derived *DerivedMobi
	ctx     context.Context
}

func (r mobiContextReader) ReadAt(p []byte, offset int64) (int, error) {
	return r.derived.readAt(r.ctx, p, offset)
}
func (d *DerivedMobi) readAt(ctx context.Context, p []byte, offset int64) (int, error) {
	if err := d.source.ValidateContext(ctx); err != nil {
		return 0, err
	}
	d.cache.mu.RLock()
	if d.cache.closed {
		d.cache.mu.RUnlock()
		return 0, ErrUnavailable
	}
	n, err := bytes.NewReader(d.cache.raw).ReadAt(p, offset)
	d.cache.mu.RUnlock()
	if check := d.source.ValidateContext(ctx); check != nil {
		clear(p[:n])
		return 0, check
	}
	return n, err
}
func (d *DerivedMobi) EPUB() (*EPUB, error) {
	if err := d.source.Validate(); err != nil {
		return nil, err
	}
	size := d.cache.size()
	if size == 0 {
		return nil, ErrUnavailable
	}
	publication, err := OpenEPUB(d.source.Context(), d, int64(size))
	if err != nil {
		return nil, err
	}
	if err = d.source.Validate(); err != nil {
		return nil, err
	}
	return publication, nil
}

// PublicationSelection preserves the original acquisition's physical identity;
// only the presentation format, output size and derived coordinate revision differ.
func (f *Source) PublicationSelection() Selection {
	out := f.Selection
	if f.derived != nil {
		out.Format = "epub"
		out.Revision = f.derived.cache.provenance.Revision
		out.Size = int64(f.derived.cache.size())
	}
	return out
}
func (f *Source) ConversionProvenance() *MobiProvenance {
	if f.derived == nil {
		return nil
	}
	out := f.derived.Provenance()
	return &out
}

// Convert refuses sequential-only transport rather than silently staging a
// source whose whole-file/range policy does not permit this conversion.
func (c *MobiConverter) Convert(source *Source) (*DerivedMobi, error) {
	if c == nil || source == nil || (source.Format != "mobi" && source.Format != "azw3") {
		return nil, ErrUnavailable
	}
	if source.Sequential() {
		return nil, ErrUnavailable
	}
	if source.Size <= 0 || source.Size > maxMobiInput {
		return nil, ErrLimit
	}
	select {
	case c.slot <- struct{}{}:
		defer func() { <-c.slot }()
	default:
		return nil, storage.ErrBusy
	}
	select {
	case c.retained <- struct{}{}:
	default:
		return nil, storage.ErrBusy
	}
	retained := false
	defer func() {
		if !retained {
			<-c.retained
		}
	}()
	ctx, cancel := context.WithTimeout(source.Context(), 45*time.Second)
	defer cancel()
	if err := source.ValidateContext(ctx); err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp(c.bundle.Scratch, "reading-mobi-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(directory)
	input, err := os.OpenFile(filepath.Join(directory, "input"), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	defer input.Close()
	inputHash := sha256.New()
	buffer := make([]byte, 128<<10)
	for offset := int64(0); offset < source.Size; {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		count := int(min(int64(len(buffer)), source.Size-offset))
		n, readErr := source.ReadAtContext(ctx, buffer[:count], offset)
		if n != count || (readErr != nil && readErr != io.EOF) {
			if readErr == nil {
				readErr = io.ErrUnexpectedEOF
			}
			return nil, readErr
		}
		if _, err = io.MultiWriter(input, inputHash).Write(buffer[:n]); err != nil {
			return nil, err
		}
		offset += int64(n)
	}
	if err = source.ValidateContext(ctx); err != nil {
		return nil, err
	}
	if _, err = input.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	raw, err := c.run(ctx, input)
	if err != nil {
		return nil, err
	}
	if err = source.ValidateContext(ctx); err != nil {
		return nil, err
	}
	outputHash := sha256.Sum256(raw)
	provenance := MobiProvenance{OriginalRevision: source.Revision, OriginalSourceID: source.SourceID, OriginalFormat: source.Format, OriginalSize: source.Size, OutputSize: int64(len(raw)), InputSHA256: hex.EncodeToString(inputHash.Sum(nil)), Converter: mobiDependency, WorkerSHA256: c.bundle.WorkerSHA256, Options: mobiOptions, OutputSHA256: hex.EncodeToString(outputHash[:])}
	encoded, _ := json.Marshal(provenance)
	revision := sha256.Sum256(append([]byte("portico.reading.derived-epub.v1\x00"), encoded...))
	provenance.Revision = hex.EncodeToString(revision[:])
	cache := &mobiCache{raw: raw, provenance: provenance, release: func() { <-c.retained }}
	derived := &DerivedMobi{source: source, cache: cache}
	// The genuine output must pass the ordinary bounded EPUB package parser.
	// Assets are subsequently served through EPUB.Asset's existing sanitizer.
	if _, err = OpenEPUB(ctx, mobiContextReader{derived: derived, ctx: ctx}, int64(len(raw))); err != nil {
		clear(raw)
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	retained = true
	return derived, nil
}

func (f *Source) prepareMobi() error {
	if f.Format != "mobi" && f.Format != "azw3" {
		return nil
	}
	derived, err := f.service.Mobi.Convert(f)
	if err != nil {
		return err
	}
	f.derived = derived
	return f.Validate()
}

// currentMobiProvenance is a metadata comparison, never source/read authority.
// Only an actual retained output of this exact actor/profile/session/source and
// current converter can witness current coordinates. Evicted/closed/replaced
// output stays stale; no conversion, migration or coordinate inference occurs.
func (s *Service) currentMobiProvenance(candidates []*Session, p identity.Principal, original Selection, wanted string) (MobiProvenance, bool) {
	if s.Mobi == nil {
		return MobiProvenance{}, false
	}
	for _, session := range candidates {
		session.mu.Lock()
		cache := session.mobi
		owns := !session.closed && session.life != nil && session.life.Err() == nil && session.actor == p.Hash && session.owner == identity.PersonalKey(p.Viewer) && session.selection.ItemID == original.ItemID && session.selection.SourceID == original.SourceID && session.selection.Revision == original.Revision
		if owns && cache != nil {
			actual := cache.provenance
			if actual.OriginalSourceID == original.SourceID && actual.OriginalRevision == original.Revision && actual.OriginalFormat == original.Format && actual.OriginalSize == original.Size && actual.Converter == mobiDependency && actual.Options == mobiOptions && actual.WorkerSHA256 == s.Mobi.bundle.WorkerSHA256 && actual.Revision == wanted && int64(cache.size()) == actual.OutputSize {
				session.mu.Unlock()
				return actual, true
			}
		}
		session.mu.Unlock()
	}
	return MobiProvenance{}, false
}

// Capture map membership before opening a DB snapshot; no session-map lock is
// taken under a database transaction. Later sessions can only be missed/stale.
func (s *Service) mobiSessionCandidates() []*Session {
	if s.Mobi == nil {
		return nil
	}
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	out := make([]*Session, 0, len(s.sessions))
	for _, session := range s.sessions {
		out = append(out, session)
	}
	return out
}
