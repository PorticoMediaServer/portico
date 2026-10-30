package preparedmedia

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
)

type artifactLease struct {
	file    *os.File
	readers int
}

// The inherited-lock primitive is also used as a cross-process object gate.
// Within this server concurrent readers share one gate. Another server retries
// admission rather than racing a private-store deletion or publication.
func (s *Service) objectGate(digest string) (*os.File, error) {
	return s.locks.Lock(livechannels.Allocation{ID: preparedLockID("object", digest), Generation: 1})
}
func (s *Service) readerLease(digest string) (func(), error) {
	s.mu.Lock()
	lease := s.readers[digest]
	if lease == nil {
		file, e := s.objectGate(digest)
		if e != nil {
			s.mu.Unlock()
			return nil, e
		}
		lease = &artifactLease{file: file}
		s.readers[digest] = lease
	}
	lease.readers++
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			lease.readers--
			if lease.readers == 0 {
				delete(s.readers, digest)
				lease.file.Close()
			}
		})
	}, nil
}

// Reader owns verified immutable bytes plus a physical deletion lease. Seeking
// remains against this descriptor, not an asset path or a replaceable filename.
type Reader struct {
	*io.SectionReader
	reader  *mediaartifact.Reader
	release func()
	once    sync.Once
	Version Version
}

func (r *Reader) Close() error {
	var e error
	r.once.Do(func() { e = r.reader.Close(); r.release() })
	return e
}
func (s *Service) OpenSession(ctx context.Context, session string) (*Reader, bool, error) {
	var id, digest string
	var size int64
	e := s.db.QueryRowContext(ctx, `SELECT version_id,digest,size FROM prepared_media_session_pins WHERE session_id=?`, session).Scan(&id, &digest, &size)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, false, nil
	}
	if e != nil {
		return nil, true, e
	}
	release, e := s.readerLease(digest)
	if e != nil {
		return nil, true, e
	}
	// Recheck after obtaining custody; a collector may have run after the first
	// lookup. Grant/family/library authority is additionally enforced by playback.
	v, e := readVersion(s.db.QueryRowContext(ctx, `SELECT `+versionColumns+` FROM `+versionSource+` WHERE v.id=? AND v.digest=? AND v.size=? AND v.state IN('published','deleting') AND EXISTS(SELECT 1 FROM prepared_media_session_pins pin JOIN playback_sessions ps ON ps.id=pin.session_id WHERE pin.session_id=? AND pin.version_id=v.id AND ps.state NOT IN('stopped','ended','failed') AND ps.expires_at>?)`, id, digest, size, session, time.Now().UTC().Format(time.RFC3339)))
	if e != nil {
		release()
		return nil, true, e
	}
	reader, e := s.artifacts.Open(ctx, mediaartifact.Object{Digest: digest, Size: size})
	if e != nil {
		release()
		return nil, true, e
	}
	return &Reader{SectionReader: io.NewSectionReader(reader, 0, size), reader: reader, release: release, Version: v}, true, nil
}
func PinSessionTx(ctx context.Context, tx *sql.Tx, session string, v Version) error {
	current, e := SelectTx(ctx, tx, v.ItemID, v.ID)
	if e != nil {
		return e
	}
	if current.Digest != v.Digest || current.Size != v.Size {
		return ErrConflict
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO prepared_media_session_pins(session_id,version_id,digest,size) VALUES(?,?,?,?)`, session, v.ID, v.Digest, v.Size)
	return e
}
func HasSessionTx(ctx context.Context, tx *sql.Tx, session string) (bool, error) {
	var yes bool
	e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM prepared_media_session_pins WHERE session_id=?)`, session).Scan(&yes)
	return yes, e
}

func (s *Service) collect(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	// A removed catalog association has no new viewer authority. Journal its own
	// derivative for deletion; never remove the original or a preview artifact.
	_, e := dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `UPDATE prepared_media_versions SET state='deleting',revision=revision+1 WHERE id IN(SELECT v.id FROM prepared_media_versions v WHERE v.state='published' AND NOT EXISTS(SELECT 1 FROM catalog_asset_links link JOIN catalog_assets a ON a.id=link.asset_id WHERE link.entity_id=v.item_id AND a.token=v.asset_id) ORDER BY v.id LIMIT 64)`)
	if e != nil {
		return e
	}
	rows, e := s.db.QueryContext(ctx, `SELECT `+versionColumns+` FROM `+versionSource+` WHERE v.state='deleting' AND v.id>? ORDER BY v.id LIMIT 128`, s.deletingCursor)
	if e != nil {
		return e
	}
	vs := []Version{}
	for rows.Next() {
		v, err := readVersion(rows)
		if err != nil {
			rows.Close()
			return err
		}
		vs = append(vs, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	// Advance past pinned readers so a bounded page cannot starve later copies.
	if len(vs) < 128 {
		s.deletingCursor = ""
	} else {
		s.deletingCursor = vs[len(vs)-1].ID
	}

	for _, v := range vs {
		if e = s.prune(ctx, v); e != nil && !errors.Is(e, livechannels.ErrPhysicalBusy) && !errors.Is(e, mediaartifact.ErrLeased) {
			return e
		}
	}
	// Completed/cancelled attempts are independently recoverable; an old process
	// must release the constant per-job custody lock before staging is removed.
	rows, e = s.db.QueryContext(ctx, `SELECT id,generation FROM prepared_media_jobs WHERE state IN('succeeded','cancelled','failed') AND generation>cleaned_generation ORDER BY updated_ms,id LIMIT 64`)
	if e != nil {
		return e
	}
	type pending struct {
		id  string
		gen int64
	}
	pendingJobs := []pending{}
	for rows.Next() {
		var p pending
		if e = rows.Scan(&p.id, &p.gen); e != nil {
			rows.Close()
			return e
		}
		pendingJobs = append(pendingJobs, p)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, p := range pendingJobs {
		lock, err := s.acquireCustody(p.id)
		if err != nil {
			continue
		}
		err = s.removeAttempt(p.id, p.gen)
		if err == nil {
			_, err = dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `UPDATE prepared_media_jobs SET cleaned_generation=? WHERE id=? AND generation=? AND state IN('succeeded','cancelled','failed')`, p.gen, p.id, p.gen)
		}
		lock.Close()
		if err != nil {
			return err
		}
	}
	// The same object gate serializes an unreferenced-object sweep with the short
	// seal/publication gap. A crash after seal therefore leaves reclaimable bytes,
	// not a published playable version or a permanently leaked file.
	if s.inventory == nil {
		s.inventory, e = s.artifacts.Inventory()
		if e != nil {
			return e
		}
	}
	objects, nextErr := s.inventory.Next(ctx, 64)
	for _, v := range objects {
		gate, err := s.objectGate(v.Object.Digest)
		if err != nil {
			continue
		}
		var refs int
		err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM prepared_media_versions WHERE digest=? AND state!='deleted'`, v.Object.Digest).Scan(&refs)
		if err == nil && refs == 0 {
			err = s.artifacts.Remove(v.Object)
		}
		gate.Close()
		if err != nil && !errors.Is(err, mediaartifact.ErrLeased) {
			return err
		}
	}
	if errors.Is(nextErr, io.EOF) {
		s.inventory.Close()
		s.inventory = nil
		return nil
	}
	return nextErr
}
func (s *Service) prune(ctx context.Context, v Version) error {
	gate, e := s.objectGate(v.Digest)
	if e != nil {
		return e
	}
	defer gate.Close()
	var state string
	e = s.db.QueryRowContext(ctx, `SELECT state FROM prepared_media_versions WHERE id=?`, v.ID).Scan(&state)
	if e != nil {
		return e
	}
	if state != "deleting" {
		return nil
	}
	var active int
	e = s.db.QueryRowContext(ctx, `SELECT count(*) FROM prepared_media_session_pins pin JOIN playback_sessions ps ON ps.id=pin.session_id WHERE pin.version_id=? AND ps.state NOT IN('stopped','ended','failed') AND ps.expires_at>?`, v.ID, time.Now().UTC().Format(time.RFC3339)).Scan(&active)
	if e != nil || active > 0 {
		return e
	}
	var other int
	e = s.db.QueryRowContext(ctx, `SELECT count(*) FROM prepared_media_versions WHERE digest=? AND state!='deleted' AND id!=?`, v.Digest, v.ID).Scan(&other)
	if e != nil {
		return e
	}
	if other == 0 {
		e = s.artifacts.Remove(mediaartifact.Object{Digest: v.Digest, Size: v.Size})
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
	}
	_, e = dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `UPDATE prepared_media_versions SET state='deleted',revision=revision+1 WHERE id=? AND state='deleting'`, v.ID)
	return e
}
