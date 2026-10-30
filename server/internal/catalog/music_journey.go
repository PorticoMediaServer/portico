package catalog

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/sonic"
)

// MusicJourney is selection evidence only. The playback queue still snapshots
// and reauthorizes its actual occurrences before granting media bytes.
type MusicJourney struct {
	From, To string
	Tracks   []string
	Model    *sonic.Identity
}

func (s *Service) MusicJourney(viewer Viewer, from, to string, length int) (MusicJourney, error) {
	out := MusicJourney{From: from, To: to, Tracks: []string{}}
	actor, ok := viewerFromPersonalKey(viewer.Profile)
	if !ok || actor.Authority == "" || actor.AccountID == "" || actor.ProfileID == "" || from == to || length < 2 || length > sonic.MaxJourneyLength {
		return out, ErrAdminQuery
	}
	if _, ok := entityid.Decode(from); !ok {
		return out, sql.ErrNoRows
	}
	if _, ok := entityid.Decode(to); !ok {
		return out, sql.ErrNoRows
	}
	if dbwork.Snapshot(s.Context()) == nil && s.readTx == nil {
		err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var err error
			out, err = s.WithContext(ctx).MusicJourney(viewer, from, to, length)
			return err
		})
		return out, err
	}
	visible, args := viewer.itemVisibilitySQL("item.id")
	resolve := func(public string) (int64, sonic.Embedding, error) {
		var id int64
		var vector sonic.Embedding
		err := s.read().QueryRow(`SELECT item.id FROM catalog_entities item JOIN catalog_libraries library ON library.id=item.library_id JOIN libraries configured ON configured.id=library.library_id AND configured.kind='music' WHERE item.public_id=pid_blob(?) AND item.kind=7 AND `+visible+` AND EXISTS(SELECT 1 FROM catalog_item_availability available WHERE available.entity_id=item.id AND available.available=1 AND available.retired=0)`, append([]any{public}, args...)...).Scan(&id)
		if err != nil {
			return 0, vector, err
		}
		found := false
		if err = s.sonicGroups(viewer, 7, id, func(group sonicGroup) { found = true; vector = group.vector }); err != nil {
			return 0, vector, err
		}
		if !found {
			return 0, vector, sonic.ErrUnavailable
		}
		return id, vector, nil
	}
	a, av, err := resolve(from)
	if err != nil {
		return out, err
	}
	b, bv, err := resolve(to)
	if err != nil {
		return out, err
	}
	planner, err := sonic.NewJourneyPlanner(a, b, av, bv, length)
	if err != nil {
		return out, err
	}
	var inputErr error
	if err = s.sonicGroups(viewer, 7, 0, func(group sonicGroup) {
		if inputErr == nil {
			inputErr = planner.Add(sonic.JourneyTrack{ID: group.id, Vector: group.vector})
		}
	}); err != nil {
		return out, err
	}
	if inputErr != nil {
		return out, inputErr
	}
	ids, err := planner.Tracks()
	if err != nil {
		return out, err
	}
	// A Journey captures exactly the requested finite length. Sparse coverage
	// is unavailable, never a shorter path or ordinary-selector fallback.
	if len(ids) != length {
		return out, sonic.ErrUnavailable
	}
	rows, err := s.read().Query(`SELECT pid(item.public_id) FROM json_each(?) wanted JOIN catalog_entities item ON item.id=wanted.value WHERE item.retired=0 ORDER BY CAST(wanted.key AS INTEGER)`, idsJSON64(ids))
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		out.Tracks = append(out.Tracks, id)
	}
	if next := rows.Err(); err == nil {
		err = next
	}
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(out.Tracks) != len(ids) || len(out.Tracks) < 2 || out.Tracks[0] != from || out.Tracks[len(out.Tracks)-1] != to {
		return out, sonic.ErrUnavailable
	}
	out.Model = &sonic.Identity{ID: sonic.ModelID, Version: sonic.ModelVersion, SHA256: sonic.ModelSHA256}
	return out, nil
}
