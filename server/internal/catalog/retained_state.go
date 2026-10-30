package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/personalstate"
)

// When a title is finally removed, what each profile had done with it is kept
// under the title's identities (retained_title_state). When a title with one
// of those identities is catalogued again, in this library or another, each
// profile gets its state back and the kept copy is dropped.

// RetainedPerProfile bounds how many removed titles one profile's state is
// kept for. Past it the oldest are forgotten first.
const RetainedPerProfile = 100000

const retainedIdentityLimit = 12

// RestoreTitleStateTx restores state after a writer establishes additional
// identity, such as analyzed audio tags or an accepted recording match.
func RestoreTitleStateTx(ctx context.Context, tx *sql.Tx, entity int64) error {
	return restoreRetainedTx(ctx, tx, entity)
}

// retentionIdentitiesTx lists the identities a title is remembered under:
// provider identities first (they survive a move to another library), then
// the names it was read as inside its own library.
func retentionIdentitiesTx(ctx context.Context, tx *sql.Tx, entity int64) ([]string, compactcatalog.Kind, string, error) {
	var kind compactcatalog.Kind
	var title, root string
	if err := tx.QueryRowContext(ctx, `SELECT e.kind,e.title,l.root FROM catalog_entities e JOIN catalog_libraries l ON l.id=e.library_id WHERE e.id=?`, entity).Scan(&kind, &title, &root); err != nil {
		return nil, 0, "", err
	}
	var out []string
	add := func(rows *sql.Rows, err error, format func(provider, providerKind, id string) string) error {
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var provider, providerKind, id string
			if err = rows.Scan(&provider, &providerKind, &id); err != nil {
				return err
			}
			out = append(out, format(provider, providerKind, id))
		}
		return rows.Err()
	}
	switch kind {
	case compactcatalog.Episode:
		var show int64
		var numbering string
		var number int
		var season sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT ep.show_id,ep.numbering,ep.number,(SELECT number FROM catalog_seasons WHERE entity_id=ep.season_id) FROM catalog_episodes ep WHERE ep.entity_id=?`, entity).Scan(&show, &numbering, &number, &season); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, 0, "", err
		} else if err == nil {
			place := fmt.Sprintf("#%s/%d/%d", numbering, nullSeason(season), number)
			rows, e := tx.QueryContext(ctx, `SELECT provider,provider_kind,provider_id FROM catalog_external_ids WHERE entity_id=? ORDER BY provider,provider_kind,provider_id LIMIT 6`, show)
			if e = add(rows, e, func(p, k, id string) string { return compactcatalog.ProviderKey(p, k, id) + place }); e != nil {
				return nil, 0, "", e
			}
		}
	case compactcatalog.Season:
		var show int64
		var number int
		if err := tx.QueryRowContext(ctx, `SELECT show_id,number FROM catalog_seasons WHERE entity_id=?`, entity).Scan(&show, &number); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, 0, "", err
		} else if err == nil {
			place := fmt.Sprintf("#season/%d", number)
			rows, e := tx.QueryContext(ctx, `SELECT provider,provider_kind,provider_id FROM catalog_external_ids WHERE entity_id=? ORDER BY provider,provider_kind,provider_id LIMIT 6`, show)
			if e = add(rows, e, func(p, k, id string) string { return compactcatalog.ProviderKey(p, k, id) + place }); e != nil {
				return nil, 0, "", e
			}
		}
	case compactcatalog.Track:
		var recording string
		if err := tx.QueryRowContext(ctx, `SELECT recording_id FROM catalog_songs WHERE entity_id=?`, entity).Scan(&recording); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, 0, "", err
		}
		if recording != "" {
			out = append(out, compactcatalog.ProviderKey("musicbrainz", "recording", recording))
		}
	default:
		rows, e := tx.QueryContext(ctx, `SELECT provider,provider_kind,provider_id FROM catalog_external_ids WHERE entity_id=? ORDER BY provider,provider_kind,provider_id LIMIT 6`, entity)
		if e = add(rows, e, compactcatalog.ProviderKey); e != nil {
			return nil, 0, "", e
		}
	}
	keys, err := compactcatalog.IdentitiesTx(ctx, tx, entity)
	if err != nil {
		return nil, 0, "", err
	}
	for _, key := range keys {
		if len(out) >= retainedIdentityLimit {
			break
		}
		out = append(out, "local:"+root+"|"+key)
	}
	return out, kind, title, nil
}

func nullSeason(season sql.NullInt64) int64 {
	if season.Valid {
		return season.Int64
	}
	return -1
}

// retainStateTx keeps every profile's state for a title that is about to be
// removed. A profile with nothing worth keeping (never played, nothing saved)
// leaves no row.
func retainStateTx(ctx context.Context, tx *sql.Tx, entity int64) error {
	identities, kind, title, err := retentionIdentitiesTx(ctx, tx, entity)
	if err != nil || len(identities) == 0 {
		return err
	}
	// Everyone who touched the title, and everyone whose mark on its show,
	// season, album or book covers it.
	rows, err := tx.QueryContext(ctx, `SELECT profile_id FROM personal_items INDEXED BY personal_items_item_id_fk WHERE item_id=?1
 UNION SELECT profile_id FROM progress INDEXED BY progress_item_state WHERE item_id=?1
 UNION SELECT owner_key FROM reading_progress WHERE item_id=?1
 UNION SELECT profile_id FROM personal_watched_intents WHERE item_id=?1
 UNION SELECT profile_id FROM personal_play_counts INDEXED BY personal_play_counts_item WHERE item_id=?1
 UNION SELECT s.profile_id FROM container_personal_state s WHERE s.watched=1 AND s.container_id IN(
  SELECT show_id FROM catalog_episodes WHERE entity_id=?1 UNION SELECT season_id FROM catalog_episodes WHERE entity_id=?1 AND season_id IS NOT NULL
  UNION SELECT album_id FROM catalog_songs WHERE entity_id=?1 UNION SELECT book_id FROM catalog_book_files WHERE entity_id=?1)`, entity)
	if err != nil {
		return err
	}
	var profiles []string
	for rows.Next() {
		var profile string
		if err = rows.Scan(&profile); err != nil {
			rows.Close()
			return err
		}
		profiles = append(profiles, profile)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	for _, profile := range profiles {
		var watched, watchlisted, favorite, notInterested, completed, reading bool
		var rating sql.NullFloat64
		var lastPlayed, progressAt string
		var plays, position, unit int64
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(`+personalstate.SQL("?1", "?2")+`,0),
 COALESCE((SELECT watchlisted FROM personal_items WHERE profile_id=?1 AND item_id=?2),0),COALESCE((SELECT favorite FROM personal_items WHERE profile_id=?1 AND item_id=?2),0),
 COALESCE((SELECT not_interested FROM personal_items WHERE profile_id=?1 AND item_id=?2),0),(SELECT rating FROM personal_items WHERE profile_id=?1 AND item_id=?2),
 COALESCE((SELECT last_played_at FROM personal_items WHERE profile_id=?1 AND item_id=?2),''),COALESCE((SELECT plays FROM personal_play_counts WHERE profile_id=?1 AND item_id=?2),0),
 COALESCE((SELECT position FROM progress WHERE profile_id=?1 AND item_id=?2),0),COALESCE((SELECT unit FROM progress WHERE profile_id=?1 AND item_id=?2),0),
 COALESCE((SELECT completed FROM progress WHERE profile_id=?1 AND item_id=?2),0),COALESCE((SELECT updated_at FROM progress_activity WHERE profile_id=?1 AND item_id=?2),'')`, profile, entity).
			Scan(&watched, &watchlisted, &favorite, &notInterested, &rating, &lastPlayed, &plays, &position, &unit, &completed, &progressAt); err != nil {
			return err
		}
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM reading_progress WHERE owner_key=? AND item_id=?)`, profile, entity).Scan(&reading); err != nil {
			return err
		}
		if !watched && !watchlisted && !favorite && !notInterested && !rating.Valid && plays == 0 && position == 0 && !reading {
			continue
		}
		var id int64
		if err = tx.QueryRowContext(ctx, `INSERT INTO retained_title_state(profile_id,kind,title,watched,watchlisted,favorite,not_interested,rating,last_played_at,plays,position,unit,completed,progress_at,retained_ms)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING id`, profile, int(kind), title, watched, watchlisted, favorite, notInterested, rating, lastPlayed, plays, position, unit, completed, progressAt, now).Scan(&id); err != nil {
			return err
		}
		for _, identity := range identities {
			if err = mergeRetainedReadingTx(ctx, tx, id, identity, profile); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM retained_title_state WHERE id<>? AND id IN(SELECT state_id FROM retained_title_identities WHERE identity=? AND profile_id=?)`, id, identity, profile); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `INSERT OR REPLACE INTO retained_title_identities(identity,profile_id,state_id) VALUES(?,?,?)`, identity, profile, id); err != nil {
				return err
			}
		}
		if err = retainReadingTx(ctx, tx, entity, profile, id); err != nil {
			return err
		}
		if err = pruneRetainedProfileTx(ctx, tx, profile); err != nil {
			return err
		}
	}
	return nil
}

// restoreRetainedTx gives a title back the state kept for any of its
// identities. A profile that already has state for the title keeps it: only
// gaps are filled. A show also restores its seasons and episodes, because
// their provider identities hang off the show's.
func restoreRetainedTx(ctx context.Context, tx *sql.Tx, entity int64) error {
	var any bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM retained_title_state)`).Scan(&any); err != nil || !any {
		return err
	}
	identities, kind, _, err := retentionIdentitiesTx(ctx, tx, entity)
	if err != nil {
		return err
	}
	for _, identity := range identities {
		if err = restoreIdentityTx(ctx, tx, entity, identity); err != nil {
			return err
		}
	}
	if kind != compactcatalog.Show {
		return nil
	}
	children, err := scanInt64s(tx.QueryContext(ctx, `SELECT entity_id FROM catalog_seasons WHERE show_id=?1 UNION ALL SELECT entity_id FROM catalog_episodes INDEXED BY catalog_episodes_show WHERE show_id=?1`, entity))
	if err != nil {
		return err
	}
	for _, child := range children {
		childIdentities, _, _, e := retentionIdentitiesTx(ctx, tx, child)
		if e != nil {
			return e
		}
		for _, identity := range childIdentities {
			if e = restoreIdentityTx(ctx, tx, child, identity); e != nil {
				return e
			}
		}
	}
	return nil
}

func restoreIdentityTx(ctx context.Context, tx *sql.Tx, entity int64, identity string) error {
	type kept struct {
		id                                            int64
		profile, lastPlayed, progressAt               string
		watched, watchlisted, favorite, notInterested bool
		completed                                     bool
		rating                                        sql.NullFloat64
		plays, position, unit                         int64
	}
	rows, err := tx.QueryContext(ctx, `SELECT s.id,s.profile_id,s.watched,s.watchlisted,s.favorite,s.not_interested,s.rating,s.last_played_at,s.plays,s.position,s.unit,s.completed,s.progress_at
 FROM retained_title_identities i JOIN retained_title_state s ON s.id=i.state_id WHERE i.identity=?`, identity)
	if err != nil {
		return err
	}
	var all []kept
	for rows.Next() {
		var k kept
		if err = rows.Scan(&k.id, &k.profile, &k.watched, &k.watchlisted, &k.favorite, &k.notInterested, &k.rating, &k.lastPlayed, &k.plays, &k.position, &k.unit, &k.completed, &k.progressAt); err != nil {
			rows.Close()
			return err
		}
		all = append(all, k)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, k := range all {
		if k.watchlisted || k.favorite || k.notInterested || k.rating.Valid || k.watched || k.lastPlayed != "" {
			if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO personal_items(profile_id,item_id,watchlisted,favorite,not_interested,rating,revision,watched,last_played_at) VALUES(?,?,?,?,?,?,1,?,?)`,
				k.profile, entity, k.watchlisted, k.favorite, k.notInterested, k.rating, k.watched, k.lastPlayed); err != nil {
				return err
			}
		}
		if k.watched {
			if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO personal_watched_intents(profile_id,item_id,watched,authored_at) VALUES(?,?,1,?)`, k.profile, entity, personalstate.Stamp()); err != nil {
				return err
			}
		}
		if k.position > 0 {
			res, e := tx.ExecContext(ctx, `INSERT OR IGNORE INTO progress(profile_id,item_id,position,unit,completed,playback_id) VALUES(?,?,?,?,?,'')`, k.profile, entity, k.position, k.unit, k.completed)
			if e != nil {
				return e
			}
			if n, _ := res.RowsAffected(); n > 0 {
				state, at := "paused", k.progressAt
				if k.completed {
					state = "ended"
				}
				if at == "" {
					at = time.Now().UTC().Format(time.RFC3339Nano)
				}
				if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO progress_activity(profile_id,library_id,item_id,updated_at,state) SELECT ?,cl.library_id,e.id,?,? FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.id=?`, k.profile, at, state, entity); err != nil {
					return err
				}
			}
		}
		if k.plays > 0 {
			if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO personal_play_counts(profile_id,item_id,plays) VALUES(?,?,?)`, k.profile, entity, k.plays); err != nil {
				return err
			}
		}
		remaining, e := restoreReadingTx(ctx, tx, entity, k.profile, k.id)
		if e != nil {
			return e
		}
		if remaining {
			continue
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM retained_title_state WHERE id=?`, k.id); err != nil {
			return err
		}
	}
	return nil
}

// PruneRetainedState holds each profile's kept state to RetainedPerProfile
// rows, oldest first. It is cheap when nothing is over the bound.
func PruneRetainedStateTx(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT profile_id,title_count FROM retained_title_state_counts WHERE title_count>?`, RetainedPerProfile)
	if err != nil {
		return err
	}
	type over struct {
		profile string
		count   int64
	}
	var all []over
	for rows.Next() {
		var o over
		if err = rows.Scan(&o.profile, &o.count); err != nil {
			rows.Close()
			return err
		}
		all = append(all, o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, o := range all {
		if _, err = tx.ExecContext(ctx, `DELETE FROM retained_title_state WHERE id IN(SELECT id FROM retained_title_state INDEXED BY retained_title_state_profile WHERE profile_id=? ORDER BY retained_ms,id LIMIT ?)`, o.profile, o.count-RetainedPerProfile); err != nil {
			return err
		}
	}
	return nil
}

func pruneRetainedProfileTx(ctx context.Context, tx *sql.Tx, profile string) error {
	var count int64
	err := tx.QueryRowContext(ctx, `SELECT title_count FROM retained_title_state_counts WHERE profile_id=?`, profile).Scan(&count)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil || count <= RetainedPerProfile {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM retained_title_state WHERE id IN(SELECT id FROM retained_title_state INDEXED BY retained_title_state_profile WHERE profile_id=? ORDER BY retained_ms,id LIMIT ?)`, profile, count-RetainedPerProfile)
	return err
}
