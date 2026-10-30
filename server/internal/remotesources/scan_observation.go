package remotesources

import (
	"context"
	"database/sql"
	"errors"
	"path"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// ObservationRef is a CAS fence for persisted scan facts, not byte-version or
// operation/cost authority. Its revision never resets when facts change A→B→A.
type ObservationRef struct {
	SourceID, Generation, Object string
	Revision                     int64
}

type ScanObservation struct {
	Facts        remotemedia.FileObservation
	ScanRevision string
}

type RecordedScanObservation struct {
	Ref         ObservationRef
	Observation ScanObservation
	LastScanJob string
}

func validObservationObject(object string) bool {
	return object != "" && object != "." && object != ".." && utf8.ValidString(object) && len(object) <= 8192 && !strings.HasPrefix(object, "/") && !strings.HasPrefix(object, "../") && path.Clean(object) == object && !strings.ContainsAny(object, "\\%:") && strings.IndexFunc(object, unicode.IsControl) < 0
}

func observationGenerationTx(ctx context.Context, tx *sql.Tx, source string) (string, error) {
	var generation string
	var removed bool
	err := tx.QueryRowContext(ctx, `SELECT CASE WHEN r.kind IN('rclone','sftp','smb') THEN CAST(m.generation AS TEXT) ELSE CAST(r.generation AS TEXT) END,r.removed FROM remote_sources r LEFT JOIN mount_backend_configs m ON m.mount_id=r.mount_id WHERE r.id=?`, source).Scan(&generation, &removed)
	if err != nil || removed || generation == "" {
		return "", storage.ErrRemoteOffline
	}
	return generation, nil
}

func readScanObservationTx(ctx context.Context, tx *sql.Tx, source, object string) (RecordedScanObservation, error) {
	var out RecordedScanObservation
	facts := &out.Observation.Facts
	out.Ref.SourceID, out.Ref.Object = source, object
	facts.SourceID, facts.Object = source, object
	var modified sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT source_generation,revision,object_id,size,modified_ns,provider_etag,served_etag,scan_revision,last_scan_job FROM remote_scan_observations WHERE source_id=? AND relative_path=?`, source, object).Scan(&out.Ref.Generation, &out.Ref.Revision, &facts.ObjectID, &facts.Size, &modified, &facts.ProviderETag, &facts.ServeETag, &out.Observation.ScanRevision, &out.LastScanJob)
	if err != nil {
		return RecordedScanObservation{}, err
	}
	facts.Incarnation = out.Ref.Generation
	facts.ModifiedKnown, facts.ModifiedNS = modified.Valid, modified.Int64
	rows, err := tx.QueryContext(ctx, `SELECT kind,value FROM remote_scan_observation_hashes WHERE source_id=? AND relative_path=? ORDER BY kind LIMIT 17`, source, object)
	if err != nil {
		return RecordedScanObservation{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var hash remotemedia.ProviderHash
		if err = rows.Scan(&hash.Kind, &hash.Value); err != nil {
			return RecordedScanObservation{}, err
		}
		facts.ProviderHashes = append(facts.ProviderHashes, hash)
	}
	if err = rows.Err(); err != nil {
		return RecordedScanObservation{}, err
	}
	if facts.Validate() != nil || out.Ref.Revision < 1 {
		return RecordedScanObservation{}, storage.ErrRemoteConfig
	}
	return out, nil
}

// RecordScanObservationTx is the only scan-fact writer. The caller already owns
// the publication transaction and must roll it back on error. A live checked
// inventory job and its exact library/remote source fences are mandatory; there
// is no generic authorization callback or permission inferred from a picker.
// Provider fields must originate from the trusted metadata producer, not be
// copied from the served ETag. This method does not acquire or decode media.
func (s *Service) RecordScanObservationTx(ctx context.Context, tx *sql.Tx, job, file string, observation ScanObservation, expected int64) (ObservationRef, error) {
	if tx == nil || expected < 0 || job == "" || !filepath.IsAbs(file) || filepath.Clean(file) != file || observation.ScanRevision == "" || len(observation.ScanRevision) > 4096 || strings.ContainsAny(observation.ScanRevision, "\x00\r\n") {
		return ObservationRef{}, storage.ErrRemoteConfig
	}
	facts, err := observation.Facts.Canonical()
	if err != nil || facts.Root != "" {
		return ObservationRef{}, storage.ErrRemoteConfig
	}
	source, err := catalog.InventoryFenceTx(ctx, tx, job)
	if err != nil {
		return ObservationRef{}, err
	}
	if source.Kind != "remote" || !filepath.IsAbs(source.ResolvedPath) || source.RootIdentity == "" || source.Incarnation == "" {
		return ObservationRef{}, storage.ErrRemoteChanged
	}
	relative, err := filepath.Rel(source.ResolvedPath, file)
	if err != nil || relative == "." || !filepath.IsLocal(relative) {
		return ObservationRef{}, storage.ErrRemoteConfig
	}
	b, object, ok := s.find(file)
	if !ok || b.Removed || !validObservationObject(object) || b.ID != facts.SourceID || object != facts.Object {
		return ObservationRef{}, storage.ErrRemoteChanged
	}
	if err = s.ValidateScan(ctx, tx, job, source.ResolvedPath); err != nil {
		return ObservationRef{}, err
	}
	generation, err := observationGenerationTx(ctx, tx, b.ID)
	if err != nil {
		return ObservationRef{}, err
	}
	if generation != facts.Incarnation {
		return ObservationRef{}, storage.ErrRemoteChanged
	}
	transport, err := observationSnapshot(b, generation, facts)
	if err != nil {
		return ObservationRef{}, err
	}
	if observation.ScanRevision != transport.Revision {
		return ObservationRef{}, storage.ErrRemoteChanged
	}
	current, err := readScanObservationTx(ctx, tx, b.ID, object)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ObservationRef{}, err
	}
	if !exists && expected != 0 || exists && current.Ref.Revision != expected {
		return ObservationRef{}, storage.ErrRemoteChanged
	}
	ref := ObservationRef{SourceID: b.ID, Generation: generation, Object: object, Revision: 1}
	if exists && current.Observation.ScanRevision == observation.ScanRevision && current.Observation.Facts.Equal(facts) {
		_, err = tx.ExecContext(ctx, `UPDATE remote_scan_observations SET last_scan_job=? WHERE source_id=? AND relative_path=? AND revision=?`, job, b.ID, object, expected)
		return current.Ref, err
	}
	if exists {
		if expected == 1<<63-1 {
			return ObservationRef{}, storage.ErrRemoteLimit
		}
		ref.Revision = expected + 1
	}
	var modified any
	if facts.ModifiedKnown {
		modified = facts.ModifiedNS
	}
	if exists {
		var result sql.Result
		result, err = tx.ExecContext(ctx, `UPDATE remote_scan_observations SET source_generation=?,revision=?,object_id=?,size=?,modified_ns=?,provider_etag=?,served_etag=?,scan_revision=?,last_scan_job=? WHERE source_id=? AND relative_path=? AND revision=?`, generation, ref.Revision, facts.ObjectID, facts.Size, modified, facts.ProviderETag, facts.ServeETag, observation.ScanRevision, job, b.ID, object, expected)
		if err == nil {
			var count int64
			count, err = result.RowsAffected()
			if err == nil && count != 1 {
				err = storage.ErrRemoteChanged
			}
		}
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO remote_scan_observations(source_id,relative_path,source_generation,revision,object_id,size,modified_ns,provider_etag,served_etag,scan_revision,last_scan_job) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, b.ID, object, generation, ref.Revision, facts.ObjectID, facts.Size, modified, facts.ProviderETag, facts.ServeETag, observation.ScanRevision, job)
	}
	if err != nil {
		return ObservationRef{}, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM remote_scan_observation_hashes WHERE source_id=? AND relative_path=?`, b.ID, object); err != nil {
		return ObservationRef{}, err
	}
	for _, hash := range facts.ProviderHashes {
		if _, err = tx.ExecContext(ctx, `INSERT INTO remote_scan_observation_hashes(source_id,relative_path,kind,value) VALUES(?,?,?,?)`, b.ID, object, hash.Kind, hash.Value); err != nil {
			return ObservationRef{}, err
		}
	}
	return ref, nil
}

// ScanObservation reads one consistent set of normalized scan facts. It is
// private source evidence, not permission to decode or proof of origin freshness.
func (s *Service) ScanObservation(ctx context.Context, file string) (RecordedScanObservation, error) {
	recorded, _, err := s.ScanSnapshot(ctx, file)
	return recorded, err
}

// ScanSnapshot reads the recorded facts and their exact transport namespace in
// one DB snapshot. It performs no provider I/O and grants no content access.
func (s *Service) ScanSnapshot(ctx context.Context, file string) (RecordedScanObservation, storage.Snapshot, error) {
	if s == nil || s.db == nil {
		return RecordedScanObservation{}, storage.Snapshot{}, storage.ErrRemoteConfig
	}
	view, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return RecordedScanObservation{}, storage.Snapshot{}, err
	}
	defer view.Rollback()
	return s.ScanSnapshotTx(ctx, view.Tx(), file)
}

// ScanSnapshotTx is the caller-transaction variant for real actor/target
// resolvers. The snapshot's ChangeToken is Binding.ScanRevision; catalogue
// callers must separately use catalog.ObservationRevision with their actual
// library source. Neither revision is an immutable provider byte version.
func (s *Service) ScanSnapshotTx(ctx context.Context, tx *sql.Tx, file string) (RecordedScanObservation, storage.Snapshot, error) {
	empty := func(err error) (RecordedScanObservation, storage.Snapshot, error) {
		return RecordedScanObservation{}, storage.Snapshot{}, err
	}
	if s == nil || tx == nil || !filepath.IsAbs(file) || filepath.Clean(file) != file {
		return empty(storage.ErrRemoteConfig)
	}
	if err := ctx.Err(); err != nil {
		return empty(err)
	}
	b, object, ok := s.find(file)
	if !ok || b.Removed || !validObservationObject(object) {
		return empty(storage.ErrRemoteOffline)
	}
	var root, kind string
	if err := tx.QueryRowContext(ctx, `SELECT root,kind FROM remote_sources WHERE id=? AND removed=0`, b.ID).Scan(&root, &kind); err != nil {
		return empty(err)
	}
	if root != b.Root || kind != b.Kind {
		return empty(storage.ErrRemoteChanged)
	}
	generation, err := observationGenerationTx(ctx, tx, b.ID)
	if err != nil {
		return empty(err)
	}
	out, err := readScanObservationTx(ctx, tx, b.ID, object)
	if errors.Is(err, sql.ErrNoRows) {
		return empty(storage.ErrRemoteChanged)
	}
	if err != nil {
		return empty(err)
	}
	if out.Ref.Generation != generation {
		return empty(storage.ErrRemoteChanged)
	}
	snapshot, err := observationSnapshot(b, generation, out.Observation.Facts)
	if err != nil {
		return empty(err)
	}
	if snapshot.ChangeToken != out.Observation.ScanRevision {
		return empty(storage.ErrRemoteChanged)
	}
	return out, snapshot, nil
}

// CheckScanObservation rejects a retired scan observation even if the provider
// later returns identical facts. Fresh metadata and operation authority must be
// checked separately before/after source I/O and before publishing output.
func (s *Service) CheckScanObservation(ctx context.Context, ref ObservationRef) error {
	if ref.SourceID == "" || ref.Generation == "" || ref.Revision < 1 || !validObservationObject(ref.Object) {
		return storage.ErrRemoteConfig
	}
	gated, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	generation, err := observationGenerationTx(ctx, tx, ref.SourceID)
	if err != nil {
		return err
	}
	if generation != ref.Generation {
		return storage.ErrRemoteChanged
	}
	var current int64
	var captured string
	err = tx.QueryRowContext(ctx, `SELECT source_generation,revision FROM remote_scan_observations WHERE source_id=? AND relative_path=?`, ref.SourceID, ref.Object).Scan(&captured, &current)
	if errors.Is(err, sql.ErrNoRows) {
		return storage.ErrRemoteChanged
	}
	if err != nil {
		return err
	}
	if captured != ref.Generation || current != ref.Revision {
		return storage.ErrRemoteChanged
	}
	return nil
}
