package playbackv1

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

// recordRadioStartTx runs only in the accepting presentation writer. Resolving
// or refilling a radio queue is not a play. The exact admitted entry supplies
// its own immutable selector; a manually inserted song never borrows another
// segment's radio seed. Replays retain the original session/time and are no-ops.
func (s *Service) recordRadioStartTx(ctx context.Context, tx *sql.Tx, c Caller, r row) error {
	if r.queueID == "" || r.entryID == "" {
		return nil
	}
	q, err := loadQueue(ctx, tx, r.queueID)
	if err != nil || !ownsQueue(c, q) {
		return ErrNotFound
	}
	snapshot, ordinalText, ok := strings.Cut(r.entryID, "-")
	ordinal, err := strconv.ParseInt(ordinalText, 10, 64)
	if !ok || err != nil || ordinal < 0 {
		return ErrNotFound
	}
	var raw string
	if err = tx.QueryRowContext(ctx, `SELECT selector FROM queue_v1_snapshots WHERE id=? AND queue_id=?`, snapshot, q.id).Scan(&raw); err != nil {
		return ErrNotFound
	}
	var selector Selector
	if json.Unmarshal([]byte(raw), &selector) != nil {
		return ErrUnsupportedSelector
	}
	if !isRadioSource(selector) {
		return nil
	}
	var segmentID string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM queue_v1_segments INDEXED BY queue_v1_segments_entry WHERE queue_id=? AND snapshot_id=? AND first_ordinal<=? AND ?<first_ordinal+count AND NOT EXISTS(SELECT 1 FROM queue_v1_removals removed WHERE removed.snapshot_id=? AND removed.ordinal=?) ORDER BY first_ordinal DESC LIMIT 1`, q.id, snapshot, ordinal, ordinal, snapshot, ordinal).Scan(&segmentID); err != nil {
		return ErrNotFound
	}
	var chunk []byte
	if err = tx.QueryRowContext(ctx, `SELECT keys FROM queue_v1_chunks WHERE snapshot_id=? AND chunk_no=?`, snapshot, ordinal/chunkKeys).Scan(&chunk); err != nil {
		return ErrNotFound
	}
	item, ok := chunkKey(chunk, int(ordinal%chunkKeys))
	if !ok || item != r.item {
		return ErrRevision
	}
	var seed int64
	var kind int
	var library string
	err = tx.QueryRowContext(ctx, `SELECT seed.id,seed.kind,library.library_id FROM catalog_entities seed JOIN catalog_libraries library ON library.id=seed.library_id JOIN libraries configured ON configured.id=library.library_id AND configured.kind='music' WHERE seed.public_id=pid_blob(?) AND seed.retired=0 AND library.retired=0`, selector.Container.ID).Scan(&seed, &kind, &library)
	if err != nil || kind != 5 && kind != 6 && kind != 7 {
		return ErrNotFound
	}
	if s.LibraryCheck != nil {
		if err = s.LibraryCheck(ctx, tx, c.Principal, library); err != nil {
			return ErrNotFound
		}
	}
	predicate, args := "1", []any{}
	if s.Visibility != nil {
		predicate, args, err = s.Visibility(ctx, tx, c.Principal, "item.id")
		if err != nil {
			return err
		}
	}
	var allowed bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_entities item JOIN catalog_songs song ON song.entity_id=item.id JOIN catalog_albums album ON album.entity_id=song.album_id WHERE ((?=7 AND item.id=?) OR (?=6 AND song.album_id=?) OR (?=5 AND (album.artist_id=? OR EXISTS(SELECT 1 FROM catalog_song_artists credit WHERE credit.song_id=item.id AND credit.artist_id=?)))) AND `+predicate+` AND EXISTS(SELECT 1 FROM catalog_item_availability available WHERE available.entity_id=item.id AND available.available=1 AND available.retired=0))`, append([]any{kind, seed, kind, seed, kind, seed, seed}, args...)...).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrNotFound
	}
	preferences, _, _, err := operations.EffectivePreferences(tx, c.Principal.Viewer, c.DeviceID)
	if err != nil || preferences.Bool("privacy.pauseWatchHistory") {
		return err
	}
	profile := identity.PersonalKey(c.Principal.Viewer)
	if _, err = tx.ExecContext(ctx, `INSERT INTO personal_music_radios(profile_id,seed_id,last_started_ms,last_session_id) VALUES(?,?,?,?) ON CONFLICT(profile_id,seed_id) DO UPDATE SET last_started_ms=excluded.last_started_ms,last_session_id=excluded.last_session_id WHERE excluded.last_started_ms>personal_music_radios.last_started_ms OR (excluded.last_started_ms=personal_music_radios.last_started_ms AND excluded.last_session_id>personal_music_radios.last_session_id)`, profile, seed, r.created, r.id); err != nil {
		return err
	}
	// A finite recent list, not a second permanent playback log or song graph.
	_, err = tx.ExecContext(ctx, `DELETE FROM personal_music_radios WHERE profile_id=? AND seed_id IN(SELECT seed_id FROM personal_music_radios WHERE profile_id=? ORDER BY last_started_ms DESC,seed_id DESC LIMIT -1 OFFSET 256)`, profile, profile)
	return err
}
