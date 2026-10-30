package catalog

import (
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/personalstate"
)

// compactPageItems hydrates only the supplied candidate keys. These
// statements are bounded by the caller's page limit and share its request
// snapshot.
func (s *Service) compactPageItems(profile string, ids []string, moviesOnly bool) (map[string]*Item, error) {
	keyset := idsJSON(ids)
	items := make(map[string]*Item, len(ids))
	factsKindWhere := ""
	factsArgs := []any{profile, keyset, profile}
	if moviesOnly {
		factsKindWhere = ` AND e.kind=?`
		factsArgs = append(factsArgs, compactcatalog.Movie)
	}
	rows, err := s.read().Query(`SELECT pid(e.public_id),l.library_id,e.title,e.kind,e.year,d.overview,d.poster_url,d.backdrop_url,d.added_text,
	EXISTS(SELECT 1 FROM catalog_missing_titles missing WHERE missing.entity_id=e.id),COALESCE(p.position,0)/1000.0,`+personalstate.CompactSQL("?", "e.id")+`
 FROM json_each(?) requested JOIN catalog_entities e ON e.public_id=pid_blob(requested.value)
 JOIN catalog_libraries l ON l.id=e.library_id JOIN catalog_item_details d ON d.entity_id=e.id
 LEFT JOIN progress p ON p.profile_id=? AND p.item_id=e.id
 WHERE l.retired=0 AND e.retired=0
 AND NOT EXISTS(SELECT 1 FROM dvr_catalog_retirements gone WHERE gone.item_id=e.id)`+factsKindWhere, factsArgs...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		item := new(Item)
		var kind compactcatalog.Kind
		if err = rows.Scan(&item.ID, &item.LibraryID, &item.Title, &kind, &item.Year, &item.Overview, &item.PosterURL, &item.BackdropURL, &item.AddedAt, &item.Missing, &item.ProgressSeconds, &item.Watched); err != nil {
			rows.Close()
			return nil, err
		}
		if item.Kind, err = kind.Name(); err != nil {
			rows.Close()
			return nil, err
		}
		item.Sources = []assets.Source{}
		items[item.ID] = item
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = s.read().Query(`SELECT pid(e.public_id),a.token,a.container,a.video_codec,a.audio_codec,a.width,a.height,a.duration,link.available
 FROM json_each(?) requested JOIN catalog_entities e ON e.public_id=pid_blob(requested.value)
 JOIN catalog_asset_links link ON link.entity_id=e.id JOIN catalog_assets a ON a.id=link.asset_id
 ORDER BY e.id,link.part_index,a.token`, keyset)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var itemID string
		var source assets.Source
		var available bool
		if err = rows.Scan(&itemID, &source.ID, &source.Container, &source.VideoCodec, &source.AudioCodec, &source.Width, &source.Height, &source.Duration, &available); err != nil {
			rows.Close()
			return nil, err
		}
		if item := items[itemID]; item != nil {
			item.Sources = append(item.Sources, source)
			item.Available = item.Available || available
			if source.Duration > item.Duration {
				item.Duration = source.Duration
			}
		}
	}
	err = rows.Err()
	rows.Close()
	return items, err
}
