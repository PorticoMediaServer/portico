package mediaanalysis

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
	"github.com/PorticoMediaServer/portico/server/internal/sonic"
)

const Algorithm = "portico-analysis-3"

var (
	ErrInput       = errors.New("Invalid media analysis request.")
	ErrBudget      = errors.New("Analysis exceeded the configured work or generated-storage budget.")
	ErrUnsupported = errors.New("This analysis is not supported by the selected media or installed decoder.")
)

type Input interface {
	Evidence() string
	Size() int64
	ReadExtent(context.Context, int64, int64) ([]byte, error)
	Validate(context.Context) error
	Close() error
}
type Decode func(context.Context, string, decoder.AnalysisSpec, func(io.Reader) error) error
type Options struct {
	Sonic             *sonic.Runtime
	OpenSonic         func() (*sonic.Runtime, error)
	DB                *sql.DB
	Directory, FFmpeg string
	Open              func(context.Context, string, string) (Input, error)
	Decoder           func(Input) Decode
	// These are explicit operator budgets, not duration-derived playback limits.
	MaxInputBytes, MaxGeneratedBytes, MaxCacheBytes int64
	MaxPreviewFrames                                int
	RetainDays                                      int
}
type Service struct {
	Admission func(context.Context, *sql.Tx, string) (bool, error)
	db        *sql.DB
	artifacts *mediaartifact.Store
	options   Options
	// Serializes closed-byte publication/reference checks, opening leases and GC.
	// Source IO and decoder execution never hold this mutex or a database writer.
	publication     sync.Mutex
	sonicProgressMu sync.Mutex
	sonicMu         sync.RWMutex
	sonicProgress   map[string][]sonicProgressSample
	slot            chan struct{}
	physical        *livechannels.PhysicalLocks
	custody         *artifactCustody
	inventory       *mediaartifact.Inventory
	// retireCursor is the last result id the retire sweep examined; Sweep holds
	// s.publication while it reads and moves it.
	retireCursor string
	// segmentOrphanCursor is the last segment row the catch-up's orphan check
	// examined; only the segment worker's catch-up reads and moves it.
	segmentOrphanCursor string
}

func New(o Options) (*Service, error) {
	if o.DB == nil || o.Open == nil || o.Decoder == nil || !filepath.IsAbs(o.Directory) {
		return nil, ErrInput
	}
	if o.MaxInputBytes == 0 {
		o.MaxInputBytes = 512 << 30
	}
	if o.MaxGeneratedBytes == 0 {
		o.MaxGeneratedBytes = 256 << 20
	}
	if o.MaxPreviewFrames == 0 {
		o.MaxPreviewFrames = 4096
	}
	if o.MaxCacheBytes == 0 {
		o.MaxCacheBytes = 32 << 30
	}
	if o.RetainDays == 0 {
		o.RetainDays = 7
	}
	if o.MaxCacheBytes < o.MaxGeneratedBytes || o.MaxCacheBytes > 16<<40 || o.MaxInputBytes < 1<<20 || o.MaxInputBytes > 16<<40 || o.MaxGeneratedBytes < 1<<20 || o.MaxGeneratedBytes > 4<<30 || o.MaxPreviewFrames < 16 || o.MaxPreviewFrames > 4096 || o.RetainDays < 1 || o.RetainDays > 365 {
		return nil, ErrInput
	}
	if p, e := exec.LookPath(o.FFmpeg); e == nil {
		o.FFmpeg, _ = filepath.Abs(p)
	}
	a, err := mediaartifact.New(o.Directory)
	if err != nil {
		return nil, err
	}
	physical, err := livechannels.NewPhysicalLocks(filepath.Join(o.Directory, "producer-locks"))
	if err != nil {
		a.Close()
		return nil, err
	}
	return &Service{custody: &artifactCustody{locks: physical}, physical: physical, db: o.DB, artifacts: a, options: o, slot: make(chan struct{}, 1)}, nil
}
func (s *Service) Close() error {
	// Caller cancels/joins the scanner before closing this service.
	s.slot <- struct{}{}
	defer func() { <-s.slot }()
	s.publication.Lock()
	defer s.publication.Unlock()
	if s.inventory != nil {
		s.inventory.Close()
		s.inventory = nil
	}
	return s.artifacts.Close()
}

type Request struct {
	JobID, ObjectID, SourceRevision string
	PolicyRevision                  int64
}
type binding struct {
	Request
	Source                                                            catalog.LibrarySource
	Consent                                                           catalog.SourceAnalysisConsent
	SourceSize, SourceModifiedNS                                      int64
	AssetID, ItemID, Container, AudioCodec, VideoCodec, BasicRevision string
	DurationUS, MarkerRevision                                        int64
	ProbeEvidence, SourceBinding, WorkAlgorithm                       string
	Trickplay                                                         catalog.TrickplaySettings
	Video                                                             assets.StreamDetail
	SonicTracks                                                       []sonicTrackBinding
	SonicBinding                                                      string
}

func (s *Service) bindingTx(ctx context.Context, tx *sql.Tx, r Request) (binding, error) {
	return s.readBindingTx(ctx, tx, r, true)
}

// readBindingTx reads the binding; markers also prepares the object's marker
// set, the one write a binding takes. Without it the read runs in a read-only
// snapshot.
func (s *Service) readBindingTx(ctx context.Context, tx *sql.Tx, r Request, markers bool) (b binding, err error) {
	b.Request = r
	b.Source, err = catalog.InventoryFenceTx(ctx, tx, r.JobID)
	if err != nil {
		return
	}
	b.Consent, err = catalog.SourceAnalysisConsentTx(ctx, tx, b.Source.ID)
	if err != nil {
		return
	}
	p, err := catalog.ScanPolicyTx(ctx, tx, b.Source.LibraryID)
	if err != nil {
		return b, err
	}
	if p.Revision != r.PolicyRevision {
		return b, context.Canceled
	}
	b.Trickplay = p.Trickplay.Normalized()
	var duration float64
	err = tx.QueryRowContext(ctx, `SELECT o.asset_id,a.container,a.audio_codec,a.video_codec,a.duration,o.analysis_revision,o.size,o.modified_ns FROM inventory_objects o JOIN catalog_assets a ON a.token=o.asset_id WHERE o.id=? AND o.source_id=? AND o.revision=? AND o.root_incarnation=? AND o.retired=0 AND o.state='available' AND a.available=1`, r.ObjectID, b.Source.ID, r.SourceRevision, b.Source.Incarnation).Scan(&b.AssetID, &b.Container, &b.AudioCodec, &b.VideoCodec, &duration, &b.BasicRevision, &b.SourceSize, &b.SourceModifiedNS)
	if err != nil {
		return
	}
	if b.VideoCodec != "" {
		var detail string
		e := tx.QueryRowContext(ctx, `SELECT st.detail_json FROM asset_streams st JOIN catalog_assets a ON a.id=st.asset_id WHERE a.token=? AND st.type='video' ORDER BY st.stream_index LIMIT 1`, b.AssetID).Scan(&detail)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return b, e
		}
		if e == nil {
			var stream assets.Stream
			stream.DecodeDetail(detail)
			b.Video = stream.Detail
		}
	}
	b.DurationUS, err = exactUS(duration)
	if err != nil && (b.AudioCodec == "mpc7" || b.AudioCodec == "mpc8") {
		b.DurationUS, err = musepackDurationUS(ctx, tx, b.AssetID, b.AudioCodec, duration)
	}
	if err != nil {
		return
	}
	err = tx.QueryRowContext(ctx, `SELECT pid(i.public_id) FROM catalog_assets a JOIN catalog_asset_links ia ON ia.asset_id=a.id JOIN catalog_entities i ON i.id=ia.entity_id JOIN catalog_libraries l ON l.id=i.library_id WHERE a.token=? AND l.library_id=? ORDER BY ia.entity_id LIMIT 1`, b.AssetID, b.Source.LibraryID).Scan(&b.ItemID)
	if err != nil {
		return
	}
	if markers {
		_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO analysis_marker_sets(object_id,source_revision) VALUES(?,?)`, r.ObjectID, r.SourceRevision)
		if err == nil {
			err = tx.QueryRowContext(ctx, `SELECT revision FROM analysis_marker_sets WHERE object_id=? AND source_revision=?`, r.ObjectID, r.SourceRevision).Scan(&b.MarkerRevision)
		}
	}
	if err == nil && b.Container == "strm" {
		var allowed bool
		err = tx.QueryRowContext(ctx, `SELECT enabled FROM source_strm_analysis WHERE library_id=?`, b.Source.LibraryID).Scan(&allowed)
		if err == nil && !allowed {
			err = context.Canceled
		}
	}
	if err == nil && b.Container == "strm" {
		e := tx.QueryRowContext(ctx, `SELECT evidence FROM analysis_probe_bindings WHERE object_id=? AND source_revision=?`, b.ObjectID, b.SourceRevision).Scan(&b.ProbeEvidence)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			err = e
		}
	}
	b.SourceBinding = token(b.Source.Incarnation, strconv.FormatInt(b.Source.Generation, 10), b.ProbeEvidence)
	if err == nil && p.Allows(catalog.SonicOperation) {
		b.SonicTracks, b.SonicBinding, err = sonicBindingsTx(ctx, tx, b)
	}
	b.WorkAlgorithm = Algorithm
	if b.Container == "strm" {
		b.WorkAlgorithm += ":" + token(b.ProbeEvidence)
	}
	return
}

// Legacy catalog seconds are converted once at the boundary. Analysis storage
// and public positions use integer microseconds; ambiguous/invalid clocks fail.
func exactUS(seconds float64) (int64, error) {
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds > float64((1<<53)-1)/1000000 {
		return 0, ErrInput
	}
	// Catalog probe values have microsecond precision; tolerate only float storage
	// representation noise, not a guessed frame rate or meaningful rounding.
	scaled := seconds * 1e6
	if math.Abs(scaled-math.Round(scaled)) > 0.001 {
		return 0, ErrInput
	}
	return int64(math.Round(scaled)), nil
}
func token(parts ...string) string {
	h := sha256.New()
	for _, v := range parts {
		h.Write([]byte(strconv.Itoa(len(v)) + ":" + v))
	}
	return hex.EncodeToString(h.Sum(nil))
}
func toolHash(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", ErrUnsupported
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(f, 4<<30+1))
	if e != nil || n > 4<<30 {
		return "", ErrUnsupported
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func jsonString(v any) string { b, _ := json.Marshal(v); return string(b) }
func nowMS() int64            { return time.Now().UnixMilli() }
