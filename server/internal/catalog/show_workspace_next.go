package catalog

import (
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/personalstate"
)

// workspaceNextUp uses the same started-show projection as Continue Watching.
// An unstarted show falls back to its first visible, playable episode. The
// episode is hydrated once through mediaPage, including season/show poster
// inheritance and the profile's current progress.
func (s *Service) workspaceNextUp(r ShowWorkspaceRequest, show string) (*ContentEntry, string, error) {
	request := HomeRequest{Viewer: r.Viewer, Profile: r.Profile, Libraries: []string{r.Library}, Restrictions: r.Viewer.EffectiveRestrictions()}
	cutoffs, premieres, _, err := s.continueWatchingPolicy(request.Libraries, request.now())
	if err != nil {
		return nil, "", err
	}
	cutoffs = idsCutoffs(request.Libraries, "0001-01-01T00:00:00.000Z")
	base, args := homeShowContinuation(request, cutoffs, premieres, 1, show, nil, true)
	args = append(args, show)
	var id string
	err = s.read().QueryRow(`SELECT candidate.id FROM (`+base+`) candidate JOIN catalog_entities item ON item.public_id=pid_blob(candidate.id) JOIN catalog_episodes ep ON ep.entity_id=item.id JOIN catalog_entities sh ON sh.id=ep.show_id WHERE sh.public_id=pid_blob(?) LIMIT 1`, args...).Scan(&id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, "", err
	}
	if errors.Is(err, sql.ErrNoRows) {
		restriction, bound := ItemRestrictionSQL("item.id", request.Restrictions)
		fallbackArgs := append([]any{r.Profile, show, r.Library}, bound...)
		fallbackArgs = append(fallbackArgs, r.Profile, show)
		err = s.read().QueryRow(`SELECT pid(item.public_id) FROM catalog_episodes ep JOIN catalog_entities item ON item.id=ep.entity_id JOIN catalog_entities sh ON sh.id=ep.show_id
 LEFT JOIN catalog_entities se ON se.id=ep.season_id LEFT JOIN catalog_seasons sd ON sd.entity_id=se.id
 CROSS JOIN (SELECT ? AS profile) ps
 JOIN catalog_libraries l ON l.id=item.library_id
 WHERE sh.public_id=pid_blob(?) AND l.library_id=? AND `+personalstate.CompactSQL("ps.profile", "item.id")+`=0
 AND `+homeVisible("item.id")+` AND `+restriction+`
 AND NOT EXISTS(SELECT 1 FROM profile_show_activity started WHERE started.profile_id=? AND started.show_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)))
	ORDER BY COALESCE(sd.number,0)=0,COALESCE(sd.number,0),ep.number,item.id LIMIT 1`, fallbackArgs...).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, "", nil
		}
		if err != nil {
			return nil, "", err
		}
	}
	entries, err := s.homeEntries(r.Profile, []string{id})
	if err != nil {
		return nil, "", err
	}
	if len(entries) != 1 {
		return nil, "", ErrStaleContinuation
	}
	var season sql.NullString
	if err = s.read().QueryRow(`SELECT pid(se.public_id) FROM catalog_entities item JOIN catalog_episodes ep ON ep.entity_id=item.id LEFT JOIN catalog_entities se ON se.id=ep.season_id WHERE item.public_id=pid_blob(?)`, id).Scan(&season); err != nil {
		return nil, "", err
	}
	return &entries[0], season.String, nil
}

func idsCutoffs(libraries []string, stamp string) string {
	values := map[string]string{}
	for _, library := range libraries {
		values[library] = stamp
	}
	raw, _ := json.Marshal(values)
	return string(raw)
}
