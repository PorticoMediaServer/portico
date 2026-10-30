package remotesources

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/naming"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

var _ storage.RemoteInventoryBlobReader = (*Service)(nil)

// ErrInventoryBlobSourceTier is a known automatic-operation ceiling, not a
// provider failure, range capability, STRM permission or generic authorization.
// It returns no bytes; unknown profile/job/target facts remain separate errors.
var ErrInventoryBlobSourceTier = errors.New("inventory metadata exceeds the saved source scan tier")

// inventoryBlobCapture binds a candidate metadata/configuration file separately
// from the actual analysis target. Ignore candidates can precede catalogue
// publication, so their evidence is the exact closed listing, never a fabricated
// normalized observation or a media object's revision.
type inventoryBlobCapture struct {
	service    *Service
	life       context.Context
	admission  storage.InventoryBlobAdmission
	request    storage.InventoryRequest
	file       string
	binding    SourceReadBinding
	scan       ScanObservation
	mediaRef   ObservationRef
	listingRaw string
}

func (s *Service) captureInventoryBlobTx(ctx context.Context, tx *sql.Tx, source string, request storage.InventoryRequest, a storage.InventoryBlobAdmission, limit int64) (*inventoryBlobCapture, error) {
	if s == nil || tx == nil || source == "" || source != a.LibrarySourceID || a.JobID == "" || a.LibraryID == "" || a.SourceGeneration < 1 || a.RootIncarnation == "" || a.RootIdentity == "" || a.PolicyRevision < 1 || !filepath.IsAbs(request.Root) || filepath.Clean(request.Root) != request.Root || request.RootIdentity != a.RootIdentity || request.RelativePath == "." || filepath.Clean(request.RelativePath) != request.RelativePath || !filepath.IsLocal(request.RelativePath) || limit < 1 {
		return nil, storage.ErrRemoteConfig
	}
	file := filepath.Join(request.Root, request.RelativePath)
	switch a.Purpose {
	case storage.InventoryBlobAnalysisSidecar:
		if a.ObjectID == "" || a.SourceRevision == "" || a.ListingID != "" || a.Member != (storage.Snapshot{}) || !inventorySidecarCandidate(a.Operation, file) || limit > 8<<20 && (a.Operation != "subtitles" || limit > 512<<20 || !strings.EqualFold(filepath.Ext(file), ".sup") && !strings.EqualFold(filepath.Ext(file), ".sub")) {
			return nil, storage.ErrRemoteConfig
		}
		switch a.Operation {
		case "probe", "local_metadata", "subtitles":
		default:
			return nil, storage.ErrRemoteConfig
		}
	case storage.InventoryBlobIgnore:
		name := filepath.Base(file)
		if a.ListingID == "" || a.Member.Path != file || a.Member.Directory || a.ObjectID != "" || a.SourceRevision != "" || a.Operation != "" || limit > naming.MaxIgnoreBytes || name != ".ignore" && name != ".plexignore" {
			return nil, storage.ErrRemoteConfig
		}
	default:
		return nil, storage.ErrRemoteConfig
	}
	b, object, ok := s.find(file)
	if !ok || b.Removed || !validObservationObject(object) {
		return nil, storage.ErrRemoteOffline
	}
	generation, err := observationGenerationTx(ctx, tx, b.ID)
	if err != nil {
		return nil, err
	}
	profile, err := SourceReadProfileTx(ctx, tx, b.ID)
	if err != nil {
		return nil, err
	}
	c := &inventoryBlobCapture{service: s, life: ctx, admission: a, request: request, file: file, binding: SourceReadBinding{Purpose: SourceInventory, Observation: ObservationRef{SourceID: b.ID, Generation: generation, Object: object}, Path: file, Root: b.Root, Profile: profile}}
	if a.Purpose == storage.InventoryBlobAnalysisSidecar {
		recorded, _, err := s.ScanSnapshotTx(ctx, tx, file)
		if err != nil {
			return nil, err
		}
		c.scan, c.binding.Observation = recorded.Observation, recorded.Ref
		var relative string
		if err = tx.QueryRowContext(ctx, `SELECT relative_path FROM inventory_objects WHERE id=? AND source_id=?`, a.ObjectID, a.LibrarySourceID).Scan(&relative); err != nil {
			return nil, err
		}
		if !filepath.IsLocal(relative) || relative == "." {
			return nil, storage.ErrRemoteConfig
		}
		media, _, err := s.ScanSnapshotTx(ctx, tx, filepath.Join(request.Root, relative))
		if err != nil {
			return nil, err
		}
		c.mediaRef = media.Ref
	} else {
		var directory string
		var ready bool
		if err = tx.QueryRowContext(ctx, `SELECT l.directory,l.ready,e.snapshot FROM remote_listing_sessions l JOIN remote_listing_entries e ON e.listing_id=l.id WHERE l.id=? AND l.source_id=? AND l.generation=? AND e.name=?`, a.ListingID, b.ID, generation, file).Scan(&directory, &ready, &c.listingRaw); err != nil {
			return nil, err
		}
		var entry serveListingEntry
		if !ready || directory != filepath.Dir(file) || len(c.listingRaw) > 64<<10 || json.Unmarshal([]byte(c.listingRaw), &entry) != nil || entry.Snapshot != a.Member || entry.Observation == nil || entry.Epoch == "" || entry.Profile != profile {
			return nil, storage.ErrRemoteChanged
		}
		var receipt serveListingReceipt
		var rawReceipt []byte
		if err = tx.QueryRowContext(ctx, `SELECT token FROM remote_listing_progress WHERE listing_id=? AND mode='serve' AND steps=1`, a.ListingID).Scan(&rawReceipt); err != nil {
			return nil, err
		}
		if len(rawReceipt) > 4096 || json.Unmarshal(rawReceipt, &receipt) != nil || receipt.Epoch != entry.Epoch || receipt.Profile != profile {
			return nil, storage.ErrRemoteChanged
		}
		c.scan = *entry.Observation
	}
	snapshot, err := observationSnapshot(b, generation, c.scan.Facts)
	if err != nil || c.scan.ScanRevision != snapshot.Revision || snapshot.Path != file || snapshot.Size < 0 || snapshot.Size > limit || a.Purpose == storage.InventoryBlobIgnore && snapshot != a.Member {
		return nil, storage.ErrRemoteChanged
	}
	c.binding.Size, c.binding.ScanRevision = snapshot.Size, c.scan.ScanRevision
	if err = c.checkTx(ctx, tx); err != nil {
		return nil, err
	}
	return c, nil
}

// These are the existing localmetadata and subtitle inventory readers' input
// formats. An analysis operation cannot use a different media file as a
// whole-file "sidecar", even when that file is smaller than the blob limit.
func inventorySidecarCandidate(operation, file string) bool {
	name := strings.ToLower(filepath.Base(file))
	ext := strings.ToLower(filepath.Ext(file))
	if operation == "subtitles" {
		switch ext {
		case ".srt", ".vtt", ".ass", ".ssa", ".sub", ".sup", ".idx", ".smi", ".sami", ".ttml", ".dfxp":
			return true
		}
		return false
	}
	if operation != "probe" && operation != "local_metadata" {
		return false
	}
	switch ext {
	case ".nfo", ".opf", ".cue", ".jpg", ".jpeg", ".png", ".webp", ".tbn":
		return true
	case ".json":
		return name == "metadata.json" || strings.HasSuffix(name, ".portico.json")
	case ".txt":
		return name == "desc.txt" || name == "reader.txt"
	}
	return false
}

// checkTx is DB/local-process-only. It never calls a storage read guard,
// provider, nested snapshot, or generic task/pool admission.
func (c *inventoryBlobCapture) checkTx(ctx context.Context, tx *sql.Tx) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.life.Err(); err != nil {
		return err
	}
	a := c.admission
	source, err := catalog.InventoryFenceTx(ctx, tx, a.JobID)
	if err != nil {
		return err
	}
	if source.Kind != "remote" || source.ID != a.LibrarySourceID || source.LibraryID != a.LibraryID || source.Generation != a.SourceGeneration || source.Incarnation != a.RootIncarnation || source.RootIdentity != a.RootIdentity || source.ResolvedPath != c.request.Root || source.FollowSymlinks != c.request.FollowSymlinks {
		return storage.ErrRemoteChanged
	}
	if err = c.service.ValidateScan(ctx, tx, a.JobID, source.ResolvedPath); err != nil {
		return err
	}
	policy, err := catalog.ScanPolicyTx(ctx, tx, source.LibraryID)
	if err != nil {
		return err
	}
	var capturedPolicy int64
	if err = tx.QueryRowContext(ctx, `SELECT policy_revision FROM inventory_runs WHERE job_id=?`, a.JobID).Scan(&capturedPolicy); err != nil {
		return err
	}
	if policy.Revision != a.PolicyRevision || capturedPolicy != a.PolicyRevision {
		return storage.ErrRemoteChanged
	}
	profile, err := SourceReadProfileTx(ctx, tx, c.binding.Observation.SourceID)
	if err != nil {
		return err
	}
	generation, err := observationGenerationTx(ctx, tx, c.binding.Observation.SourceID)
	if err != nil {
		return err
	}
	var root string
	if err = tx.QueryRowContext(ctx, `SELECT root FROM remote_sources WHERE id=? AND removed=0`, c.binding.Observation.SourceID).Scan(&root); err != nil {
		return err
	}
	if profile != c.binding.Profile || generation != c.binding.Observation.Generation || root != c.binding.Root {
		return storage.ErrRemoteChanged
	}
	if a.Purpose == storage.InventoryBlobAnalysisSidecar {
		if profile.ScanDepth == "file_list_only" {
			return ErrInventoryBlobSourceTier
		}
		if !policy.Allows(a.Operation) {
			return context.Canceled
		}
		var relative string
		if err = tx.QueryRowContext(ctx, `SELECT o.relative_path FROM inventory_objects o JOIN inventory_analysis_queue q ON q.object_id=o.id WHERE q.job_id=? AND q.object_id=? AND q.revision=? AND q.policy_revision=? AND q.state='pending' AND o.source_id=? AND o.revision=q.revision AND o.root_incarnation=? AND o.state='available' AND o.retired=0 AND o.unsupported_reason=''`, a.JobID, a.ObjectID, a.SourceRevision, a.PolicyRevision, source.ID, source.Incarnation).Scan(&relative); err != nil {
			return err
		}
		if !filepath.IsLocal(relative) || relative == "." || filepath.Join(source.ResolvedPath, relative) == c.file {
			return storage.ErrRemoteConfig // A bounded sidecar cannot read the media target wholesale.
		}
		media, snapshot, err := c.service.ScanSnapshotTx(ctx, tx, filepath.Join(source.ResolvedPath, relative))
		if err != nil {
			return err
		}
		revision, _, err := catalog.ObservationRevision(source, snapshot)
		if err != nil || revision != a.SourceRevision || media.Ref != c.mediaRef {
			return storage.ErrRemoteChanged
		}
		recorded, _, err := c.service.ScanSnapshotTx(ctx, tx, c.file)
		if err != nil {
			return err
		}
		if recorded.Ref != c.binding.Observation || recorded.Observation.ScanRevision != c.scan.ScanRevision || !recorded.Observation.Facts.Equal(c.scan.Facts) {
			return storage.ErrRemoteChanged
		}
	} else {
		var raw string
		if err = tx.QueryRowContext(ctx, `SELECT e.snapshot FROM remote_listing_sessions l JOIN remote_listing_entries e ON e.listing_id=l.id WHERE l.id=? AND l.source_id=? AND l.generation=? AND l.directory=? AND l.ready=1 AND e.name=?`, a.ListingID, c.binding.Observation.SourceID, generation, filepath.Dir(c.file), c.file).Scan(&raw); err != nil {
			return err
		}
		if raw != c.listingRaw {
			return storage.ErrRemoteChanged
		}
		var entry serveListingEntry
		if json.Unmarshal([]byte(raw), &entry) != nil {
			return storage.ErrRemoteConfig
		}
		if _, err = c.service.currentServeProcessTx(ctx, tx, c.binding.Observation.SourceID, profile, entry.Epoch); err != nil {
			return err
		}
	}
	return nil
}

func (c *inventoryBlobCapture) check(ctx context.Context) error {
	view, err := dbwork.BeginSnapshot(ctx, c.service.db)
	if err != nil {
		return err
	}
	defer view.Rollback()
	return c.checkTx(ctx, view.Tx())
}

// ReadInventoryBlob reads only a bounded sidecar/configuration file admitted by
// a real running inventory. This genuine from-start body is the file's primary
// metadata read, never a media-file fallback, prefix cache or ReaderAt adapter.
// The source-wide budget and actual profile TPS/bandwidth remain in force.
func (s *Service) ReadInventoryBlob(ctx context.Context, source string, request storage.InventoryRequest, admission storage.InventoryBlobAdmission, limit int64) ([]byte, error) {
	if s == nil || s.db == nil {
		return nil, storage.ErrRemoteConfig
	}
	view, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return nil, err
	}
	capture, err := s.captureInventoryBlobTx(ctx, view.Tx(), source, request, admission, limit)
	view.Rollback()
	if err != nil {
		return nil, err
	}
	epoch := ""
	if admission.Purpose == storage.InventoryBlobIgnore {
		var entry serveListingEntry
		if json.Unmarshal([]byte(capture.listingRaw), &entry) != nil {
			return nil, storage.ErrRemoteConfig
		}
		epoch = entry.Epoch
	}
	process, err := s.ensureSourceServe(ctx, capture.binding, epoch)
	if err != nil {
		return nil, err
	}
	check := func(ctx context.Context) error {
		if err := capture.check(ctx); err != nil {
			return err
		}
		return process.Check(ctx)
	}
	endpoint, err := process.Endpoint(ctx)
	if err != nil {
		return nil, err
	}
	observe := func(ctx context.Context) (remotemedia.FileObservation, error) {
		return observeProcessFile(ctx, process, capture.binding.Observation.Object, check)
	}
	session, err := OpenSequentialSession(ctx, endpoint, SequentialAdmission{Scan: capture.scan.Facts, Check: check, Observe: observe, OnChanged: process.Stop})
	if err != nil {
		return nil, err
	}
	defer session.Close()
	data := make([]byte, capture.binding.Size)
	for offset := 0; offset < len(data); {
		n, err := session.Read(ctx, data[offset:min(offset+(1<<20), len(data))])
		if err != nil || n == 0 {
			clear(data)
			return nil, errors.Join(err, io.ErrUnexpectedEOF, session.StopAndRecheck(ctx))
		}
		offset += n
	}
	// Empty files also need EOF and fresh metadata with the body slot released.
	if capture.binding.Size == 0 {
		if err = session.StopAndRecheck(ctx); err != nil {
			return nil, err
		}
	}
	if err = check(ctx); err != nil {
		clear(data)
		return nil, err
	}
	return data, nil
}
