package metadata

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

const (
	mbFinalOrdinaryHold = 250 * time.Millisecond
	// Approved P2-52b exception: one atomic old/new release move, each bounded
	// at 10,000 tracks. This changes timing acceptance only, never SQL authority,
	// accounting limits, write plans, or production admission/refusal behavior.
	mbFinalExtremeHold = 500 * time.Millisecond
)

type mbFinalStepTiming struct {
	kind                 string
	newTracks, oldTracks int
	started              time.Time
	duration             time.Duration
	stopped              bool
}

// Track counts are telemetry, never admission or catalog authority. Read them
// before acquiring the writer, using bounded release-key windows. A failed
// optional read records unknown (-1); it cannot refuse a production operation.
func (s *Service) mbFinalTrackCounts(ctx context.Context, j mbJob, newTracks int) (out mbFinalStepTiming) {
	out = mbFinalStepTiming{kind: j.kind, newTracks: newTracks, oldTracks: -1}
	_ = dbwork.WithReadSnapshot(dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia), s.db, func(readCtx context.Context) error {
		tx := dbwork.Snapshot(readCtx)
		old := j.base.ReleaseRevision
		if j.kind == "song" {
			var value sql.NullString
			e := tx.QueryRowContext(readCtx, `SELECT release_revision FROM mb_song_links WHERE item_id=?`, j.base.Entity).Scan(&value)
			if e != nil && e != sql.ErrNoRows {
				return e
			}
			old = value.String
			count, e := mbFinalTrackCountTx(readCtx, tx, j.base.ReleaseRevision)
			if e != nil {
				return e
			}
			out.newTracks = count
		}
		count, e := mbFinalTrackCountTx(readCtx, tx, old)
		if e != nil {
			return e
		}
		out.oldTracks = count
		return nil
	})
	return out
}
func mbFinalTrackCountTx(ctx context.Context, tx *sql.Tx, revision string) (int, error) {
	if revision == "" {
		return 0, nil
	}
	var count int
	e := tx.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT 1 FROM mb_release_tracks INDEXED BY mb_release_track_release_recording WHERE release_revision=? LIMIT ?)`, revision, mbEvidenceTracks+1).Scan(&count)
	return count, e
}

// start is called only after actual dbwork.Begin succeeds. stop is called
// immediately after Commit/Rollback returns, before any separate status write.
// report runs after deferred rollback/stop, so neither logging nor a
// test-mode assertion can retain this publication's writer gate.
func (m *mbFinalStepTiming) start() { m.started = time.Now() }
func (m *mbFinalStepTiming) stop() {
	if !m.stopped {
		m.duration = time.Since(m.started)
		m.stopped = true
	}
}
func (m *mbFinalStepTiming) report() {
	if !m.stopped {
		return
	}
	if m.duration > mbFinalOrdinaryHold {
		log.Printf("metadata: MusicBrainz final publication duration=%s kind=%s new_tracks=%d old_tracks=%d", m.duration, m.kind, m.newTracks, m.oldTracks)
	}
	limit := mbFinalOrdinaryHold
	if m.newTracks == mbEvidenceTracks || m.oldTracks == mbEvidenceTracks {
		limit = mbFinalExtremeHold
	}
	if testing.Testing() && m.duration > limit {
		panic(fmt.Sprintf("MusicBrainz final publication kind=%s duration=%s new_tracks=%d old_tracks=%d exceeded approved %s hold", m.kind, m.duration, m.newTracks, m.oldTracks, limit))
	}
}
