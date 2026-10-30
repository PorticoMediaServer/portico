package catalog

import (
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/personalstate"
)

// Item facts come from the compact catalogue, written synchronously: there is
// no pending state between a write and a read. Progress positions are stored
// in milliseconds (unit 0) and surface as seconds.
func (s *Service) itemFacts(profile, id string) (Item, error) {
	var item Item
	var kind compactcatalog.Kind
	err := s.read().QueryRow(`
 SELECT pid(e.public_id),l.library_id,e.title,e.kind,e.year,d.overview,d.poster_url,d.backdrop_url,d.added_text,
 COALESCE((SELECT position FROM progress WHERE profile_id=? AND item_id=e.id),0)/1000.0,
	`+personalstate.CompactSQL("?", "e.id")+`
 FROM catalog_entities e JOIN catalog_libraries l ON l.id=e.library_id JOIN catalog_item_details d ON d.entity_id=e.id
 WHERE e.public_id=pid_blob(?) AND l.retired=0 AND e.retired=0
 AND NOT EXISTS(SELECT 1 FROM dvr_catalog_retirements WHERE item_id=e.id)`,
		profile, profile, id).Scan(
		&item.ID, &item.LibraryID, &item.Title, &kind, &item.Year, &item.Overview, &item.PosterURL, &item.BackdropURL, &item.AddedAt, &item.ProgressSeconds, &item.Watched)
	if err == nil {
		item.Kind, err = kind.Name()
	}
	return item, err
}

func (s *Service) itemGenres(id, kind string) (*sql.Rows, error) {
	return s.read().Query(`SELECT p.source_id,COALESCE(p.label_override,t.label) AS name,p.provider
 FROM catalog_entities e JOIN catalog_term_sources p ON p.entity_id=e.id JOIN catalog_terms t ON t.id=p.term_id
 WHERE e.public_id=pid_blob(?) AND t.vocab=? ORDER BY p.provider,p.ordinal,p.source_id`, id, compactcatalog.VocabGenre)
}
