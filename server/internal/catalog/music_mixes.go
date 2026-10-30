package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/sonic"
)

// MusicMix is a current-profile selection, not a persisted playlist or a media
// grant. Its identity describes the actual current model and ordered members.
type MusicMix struct {
	ID      string
	Cluster MusicListeningCluster
}

// MusicMixes keeps history coverage distinct from library coverage. A caller
// must not describe the analyzed subset of recent plays as the whole library.
type MusicMixes struct {
	HistoryTracks, AnalyzedHistoryTracks int
	LibraryTracks, AnalyzedLibraryTracks int
	Model                                *sonic.Identity
	Mixes                                []MusicMix
}

func musicMixID(viewer Viewer, model sonic.Identity, tracks []string, history string) string {
	// IDs never authorize a read. Including the exact personal key prevents another profile's
	// identical listening from naming this selection. Visibility is re-read
	// for every resolution. Opaque read/queue transport fences intentionally
	// differ and are not content identity; the permitted sample is already
	// bound by its history digest. No global history or artist mean participates.
	data, _ := json.Marshal([]any{viewer.Profile, model, tracks, history})
	digest := sha256.Sum256(data)
	return "mix-" + base64.RawURLEncoding.EncodeToString(digest[:])
}

func (s *Service) MusicMixes(viewer Viewer, at time.Time) (MusicMixes, error) {
	out := MusicMixes{Mixes: []MusicMix{}}
	if dbwork.Snapshot(s.Context()) == nil && s.readTx == nil {
		err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var err error
			out, err = s.WithContext(ctx).MusicMixes(viewer, at)
			return err
		})
		return out, err
	}
	clusters, err := s.ListeningClusters(viewer, at)
	if err != nil {
		return out, err
	}
	out.HistoryTracks, out.AnalyzedHistoryTracks = clusters.EligibleTracks, clusters.AnalyzedTracks
	out.Model = clusters.Model
	visible, args := viewer.itemVisibilitySQL("item.id")
	rows, err := s.read().Query(`SELECT `+currentSonicVectorSQL+`
 FROM catalog_entities item JOIN catalog_libraries library ON library.id=item.library_id
 JOIN libraries configured ON configured.id=library.library_id AND configured.kind='music'
 WHERE item.kind=7 AND `+visible+` AND `+currentSonicEligibleSQL+`
 AND COALESCE((SELECT enabled FROM library_sonic_settings settings WHERE settings.library_id=library.library_id),?)=1
 AND EXISTS(SELECT 1 FROM catalog_item_availability available WHERE available.entity_id=item.id AND available.available=1 AND available.retired=0)
 ORDER BY item.id`, append(append([]any{sonic.ModelID, sonic.ModelVersion, sonic.ModelSHA256}, args...), SonicDefaultEnabled())...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var data []byte
		if err = rows.Scan(&data); err != nil {
			break
		}
		out.LibraryTracks++
		if _, vectorErr := sonic.FromBytes(data); vectorErr == nil {
			out.AnalyzedLibraryTracks++
		}
	}
	if next := rows.Err(); err == nil {
		err = next
	}
	rows.Close()
	if err != nil {
		return out, err
	}
	if out.Model != nil {
		for _, cluster := range clusters.Groups {
			out.Mixes = append(out.Mixes, MusicMix{ID: musicMixID(viewer, *out.Model, cluster.Tracks, clusters.HistoryDigest+":"+cluster.HistoryDigest), Cluster: cluster})
		}
	}
	return out, nil
}

// MusicMixKeys resolves only a currently produced selection for this profile.
// A changed model, visibility or member order expires the old identifier. The
// ordinary queue/playlist writer still reauthorizes every returned item.
func (s *Service) MusicMixKeys(viewer Viewer, at time.Time, id string) ([]string, error) {
	if !strings.HasPrefix(id, "mix-") || len(id) != 47 {
		return nil, sql.ErrNoRows
	}
	mixes, err := s.MusicMixes(viewer, at)
	if err != nil {
		return nil, err
	}
	if !musicMixesEligible(mixes) {
		return nil, sql.ErrNoRows
	}
	for _, mix := range mixes.Mixes {
		if mix.ID == id && musicMixEligible(mix) {
			return append([]string(nil), mix.Cluster.Tracks...), nil
		}
	}
	return nil, sql.ErrNoRows
}
