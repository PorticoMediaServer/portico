package catalog

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// RemovedTitle is personal, temporary evidence of a real removal. Outages,
// missing files and retired inventory do not create tombstones.
type RemovedTitle struct {
	ID                    string `json:"id"`
	Title                 string `json:"title"`
	Kind                  string `json:"kind"`
	PosterURL             string `json:"posterUrl,omitempty"`
	BackdropURL           string `json:"backdropUrl,omitempty"`
	RemovedAt             string `json:"removedAt"`
	CanRemoveFromContinue bool   `json:"canRemoveFromContinue"`
}

func removedTitlePredicate(v Viewer) (string, []any) {
	where := `t.public_id=? AND t.expires_at>strftime('%Y-%m-%dT%H:%M:%fZ','now') AND EXISTS(SELECT 1 FROM catalog_title_tombstone_viewers pv WHERE pv.public_id=t.public_id AND pv.profile_id=?) AND (t.library_id IN(SELECT value FROM json_each(?)) OR NOT EXISTS(SELECT 1 FROM libraries WHERE id=t.library_id))`
	args := []any{v.Profile, v.librariesJSON()}
	r := v.EffectiveRestrictions()
	if r.MaximumAge != nil {
		where += ` AND NOT EXISTS(SELECT 1 FROM catalog_title_tombstone_attributes a JOIN content_rating_ages age ON age.value_key=a.value_key WHERE a.public_id=t.public_id AND a.field_id=1 AND age.minimum_age>?)`
		args = append(args, *r.MaximumAge)
	}
	if r.BlockUnrated {
		where += ` AND EXISTS(SELECT 1 FROM catalog_title_tombstone_attributes a JOIN content_rating_ages age ON age.value_key=a.value_key WHERE a.public_id=t.public_id AND a.field_id=1 AND age.minimum_age>=0)`
	}
	for _, label := range r.BlockedLabels {
		where += ` AND NOT EXISTS(SELECT 1 FROM catalog_title_tombstone_attributes a WHERE a.public_id=t.public_id AND a.field_id=2 AND a.value_key=?)`
		args = append(args, strings.ToLower(label))
	}
	for _, label := range r.MemberDeniedLabels {
		where += ` AND NOT EXISTS(SELECT 1 FROM catalog_title_tombstone_attributes a WHERE a.public_id=t.public_id AND a.field_id IN(2,3) AND lower(a.source_value)=?)`
		args = append(args, strings.ToLower(label))
	}
	if r.BlockRecordings {
		where += ` AND t.recording=0`
	}
	return where, args
}

func (s *Service) RemovedTitle(v Viewer, id string) (RemovedTitle, error) {
	out := RemovedTitle{}
	where, args := removedTitlePredicate(v)
	err := s.read().QueryRow(`SELECT t.public_id,t.title,`+compactBrowseKindSQL("t.kind")+`,t.poster_url,t.backdrop_url,t.removed_at,(SELECT pv.continuing FROM catalog_title_tombstone_viewers pv WHERE pv.public_id=t.public_id AND pv.profile_id=?) FROM catalog_title_tombstones t WHERE `+where, append([]any{v.Profile, id}, args...)...).Scan(&out.ID, &out.Title, &out.Kind, &out.PosterURL, &out.BackdropURL, &out.RemovedAt, &out.CanRemoveFromContinue)
	return out, err
}

// DismissRemovedTitle removes only this profile's reference. The title and
// other profiles' activity are unchanged; a repeated delete is harmless.
func (s *Service) DismissRemovedTitle(v Viewer, id string, authorize func(*sql.Tx) error) error {
	ctx := s.Context()
	return dbwork.WithWriteTx(ctx, s.db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		if authorize != nil {
			if err := authorize(tx); err != nil {
				return err
			}
		}
		where, args := removedTitlePredicate(v)
		var found string
		err := tx.QueryRowContext(ctx, `SELECT t.public_id FROM catalog_title_tombstones t WHERE `+where, append([]any{id}, args...)...).Scan(&found)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE catalog_title_tombstone_viewers SET continuing=0 WHERE public_id=? AND profile_id=? AND continuing<>0`, id, v.Profile)
		return err
	})
}

const expiredTombstoneHeadSQL = `SELECT public_id FROM catalog_title_tombstones INDEXED BY catalog_title_tombstones_expiry WHERE expires_at<=strftime('%Y-%m-%dT%H:%M:%fZ','now') ORDER BY expires_at LIMIT 1`

// PruneRemovedTitles drains expired references child-first. Empty upkeep is a
// read; each admitted writer changes at most eight rows including revision
// triggers (four viewers or eight attributes), or one empty header.
// Every turn rechecks expiry, so no durable cursor or snapshot spans turns.
func (s *Service) PruneRemovedTitles(ctx context.Context) error {
	ctx = dbwork.WithClass(ctx, dbwork.ClassMaintenance)
	for ctx.Err() == nil {
		var id string
		err := s.WithContext(ctx).read().QueryRowContext(ctx, expiredTombstoneHeadSQL).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if !dbwork.PaceBackground(ctx, time.Time{}) {
			return ctx.Err()
		}
		err = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
			_, err := pruneRemovedTitleTurnTx(ctx, tx)
			return err
		})
		if err != nil {
			return err
		}
	}
	return ctx.Err()
}

func pruneRemovedTitleTurnTx(ctx context.Context, tx *sql.Tx) (bool, error) {
	var id string
	err := tx.QueryRowContext(ctx, expiredTombstoneHeadSQL).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, statement := range []string{
		`DELETE FROM catalog_title_tombstone_viewers WHERE public_id=?1 AND profile_id IN(SELECT profile_id FROM catalog_title_tombstone_viewers WHERE public_id=?1 ORDER BY profile_id LIMIT 4)`,
		`DELETE FROM catalog_title_tombstone_attributes WHERE public_id=?1 AND (field_id,value_key) IN(SELECT field_id,value_key FROM catalog_title_tombstone_attributes WHERE public_id=?1 ORDER BY field_id,value_key LIMIT 8)`,
	} {
		result, e := tx.ExecContext(ctx, statement, id)
		if e != nil {
			return false, e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return false, e
		}
		if n > 0 {
			return true, nil
		}
	}
	// The two child-prefix proofs prevent the parent's CASCADE from doing work.
	_, err = tx.ExecContext(ctx, `DELETE FROM catalog_title_tombstones WHERE public_id=? AND expires_at<=strftime('%Y-%m-%dT%H:%M:%fZ','now') AND NOT EXISTS(SELECT 1 FROM catalog_title_tombstone_viewers WHERE public_id=catalog_title_tombstones.public_id LIMIT 1) AND NOT EXISTS(SELECT 1 FROM catalog_title_tombstone_attributes WHERE public_id=catalog_title_tombstones.public_id LIMIT 1)`, id)
	return true, err
}

// homeRemovedSource has exactly the same personal/current-policy fence as a
// direct tombstone read. Only unfinished progress participates in Continue.
func (s *Service) homeRemovedSource(v Viewer) (homeSource, error) {
	where, args := removedTitlePredicate(v)
	where = strings.Replace(where, "t.public_id=? AND ", "", 1)
	return homeSource{base: `SELECT t.public_id AS id,pv.activity_at AS ord FROM catalog_title_tombstones t JOIN catalog_title_tombstone_viewers pv ON pv.public_id=t.public_id WHERE pv.profile_id=? AND pv.continuing=1 AND t.kind IN(1,4) AND ` + where, args: append([]any{v.Profile}, args...), selfRestricted: true}, nil
}

func (s *Service) homeContinueEntries(v Viewer, ids []string) ([]ContentEntry, error) {
	where, args := removedTitlePredicate(v)
	where = strings.Replace(where, "t.public_id=?", "t.public_id IN(SELECT value FROM json_each(?))", 1)
	rows, err := s.read().Query(`SELECT t.public_id,t.title,`+compactBrowseKindSQL("t.kind")+`,t.poster_url,t.backdrop_url FROM catalog_title_tombstones t WHERE `+where, append([]any{idsJSON(ids)}, args...)...)
	if err != nil {
		return nil, err
	}
	removed := map[string]ContentEntry{}
	for rows.Next() {
		entry := ContentEntry{Removed: true}
		if err = rows.Scan(&entry.ID, &entry.Title, &entry.Kind, &entry.PosterURL, &entry.BackdropURL); err != nil {
			break
		}
		available := false
		entry.Available = &available
		entry.Navigation = &ContentNavigation{View: "item", EntityID: entry.ID}
		entry.HomeActions = &HomeActions{Details: true, RemoveFromContinue: true}
		removed[entry.ID] = entry
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	live := []string{}
	for _, id := range ids {
		if _, ok := removed[id]; !ok {
			live = append(live, id)
		}
	}
	entries, err := s.homeEntries(v.Profile, live)
	if err != nil {
		return nil, err
	}
	byID := removed
	for _, entry := range entries {
		byID[entry.ID] = entry
	}
	out := make([]ContentEntry, 0, len(ids))
	for _, id := range ids {
		entry, ok := byID[id]
		if !ok {
			return nil, ErrStaleContinuation
		}
		out = append(out, entry)
	}
	return out, nil
}
