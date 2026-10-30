package catalog

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/sonic"
)

// MusicSmoothOrder describes positions in an already seeded window. Queue
// persistence/seed/lap authority remains with the ordinary queue writer.
type MusicSmoothOrder struct {
	Positions []int64
	BySound   bool
	Model     *sonic.Identity
}

func (s *Service) MusicSmoothOrder(viewer Viewer, ids []string, previous string) (MusicSmoothOrder, error) {
	out := MusicSmoothOrder{Positions: make([]int64, len(ids))}
	for n := range out.Positions {
		out.Positions[n] = int64(n)
	}
	actor, ok := viewerFromPersonalKey(viewer.Profile)
	if !ok || actor.Authority == "" || actor.AccountID == "" || actor.ProfileID == "" || len(ids) > sonic.SmoothWindowMax {
		return out, ErrAdminQuery
	}
	if len(ids) == 0 {
		return out, nil
	}
	lookup := append([]string(nil), ids...)
	if previous != "" {
		lookup = append(lookup, previous)
	}
	for _, id := range lookup {
		if _, ok := entityid.Decode(id); !ok {
			return out, sql.ErrNoRows
		}
	}
	if dbwork.Snapshot(s.Context()) == nil && s.readTx == nil {
		err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var err error
			out, err = s.WithContext(ctx).MusicSmoothOrder(viewer, ids, previous)
			return err
		})
		return out, err
	}
	visible, args := viewer.itemVisibilitySQL("item.id")
	encoded, _ := json.Marshal(lookup)
	bind := []any{SonicDefaultEnabled(), sonic.ModelID, sonic.ModelVersion, sonic.ModelSHA256, string(encoded)}
	bind = append(bind, args...)
	rows, err := s.read().Query(`SELECT item.id,pid(item.public_id),`+radioArtistsSQL+`,
 CASE WHEN `+currentSonicEligibleSQL+` AND COALESCE((SELECT enabled FROM library_sonic_settings settings WHERE settings.library_id=library.library_id),?)=1 THEN `+currentSonicVectorSQL+` ELSE NULL END
 FROM catalog_entities item JOIN catalog_libraries library ON library.id=item.library_id
 JOIN libraries configured ON configured.id=library.library_id AND configured.kind='music'
 JOIN catalog_songs song ON song.entity_id=item.id JOIN catalog_albums album ON album.entity_id=song.album_id
 WHERE item.public_id IN(SELECT pid_blob(value) FROM json_each(?)) AND item.kind=7 AND `+visible+`
 AND EXISTS(SELECT 1 FROM catalog_item_availability available WHERE available.entity_id=item.id AND available.available=1 AND available.retired=0)
 ORDER BY item.id`, bind...)
	if err != nil {
		return out, err
	}
	tracks := map[string]sonic.SmoothTrack{}
	valid := map[string]bool{}
	for rows.Next() {
		var track sonic.SmoothTrack
		var public, artists string
		var data []byte
		if err = rows.Scan(&track.ID, &public, &artists, &data); err != nil {
			break
		}
		if err = json.Unmarshal([]byte(artists), &track.Artists); err != nil {
			break
		}
		var vectorErr error
		track.Vector, vectorErr = sonic.FromBytes(data)
		tracks[public], valid[public] = track, vectorErr == nil
	}
	if next := rows.Err(); err == nil {
		err = next
	}
	rows.Close()
	if err != nil {
		return out, err
	}
	window := make([]sonic.SmoothTrack, len(ids))
	complete := true
	for n, public := range ids {
		track, exists := tracks[public]
		if !exists {
			return out, sql.ErrNoRows
		}
		track.Occurrence = int64(n)
		window[n] = track
		complete = complete && valid[public]
	}
	var last *sonic.SmoothTrack
	if previous != "" {
		track, exists := tracks[previous]
		complete = complete && exists && valid[previous]
		last = &track
	}
	if !complete {
		return out, nil
	}
	positions, err := sonic.SmoothWindow(window, last)
	if err != nil {
		return out, err
	}
	out.Positions, out.BySound = positions, true
	out.Model = &sonic.Identity{ID: sonic.ModelID, Version: sonic.ModelVersion, SHA256: sonic.ModelSHA256}
	return out, nil
}
