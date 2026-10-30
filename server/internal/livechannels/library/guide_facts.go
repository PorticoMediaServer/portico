package librarychannels

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
)

// libraryFacts fills Library Channel programmes from their library items
// (Channels spec §8.1): episode numbers and title, rating, genres as
// categories, year, and the poster as image (a path this server serves).
// Only programmes the viewer may see get facts; a restricted or unavailable
// slot keeps its placeholder title and nothing else. One query per fact for a
// whole channel window, not one per programme.
// guideItemFactsSQL reads a page of programme items' facts. It LEFT JOINs the
// compact episode and season tables directly, never a whole-library scan
// (NEW-47).
const guideItemFactsSQL = `SELECT pid(i.public_id),i.title,k.name,i.year,d.poster_url<>'',COALESCE(s.number,0),COALESCE(CASE WHEN e.numbering='seasonal' THEN e.number END,0)
 FROM catalog_entities i JOIN catalog_kinds k ON k.id=i.kind LEFT JOIN catalog_item_details d ON d.entity_id=i.id LEFT JOIN catalog_episodes e ON e.entity_id=i.id LEFT JOIN catalog_seasons s ON s.entity_id=e.season_id WHERE i.public_id IN(SELECT pid_blob(value) FROM json_each(?)) AND i.retired=0`

func libraryFacts(ctx context.Context, tx *sql.Tx, programmes []livechannels.Programme, items map[string]string) error {
	ids := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		if item != "" && !seen[item] {
			seen[item] = true
			ids = append(ids, item)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	raw, _ := json.Marshal(ids)
	type facts struct {
		title, kind    string
		year           int
		poster         bool
		season, number int
		rating         string
		categories     []string
	}
	byItem := map[string]*facts{}
	rows, e := tx.QueryContext(ctx, guideItemFactsSQL, string(raw))
	if e != nil {
		return e
	}
	for rows.Next() {
		var id string
		f := &facts{}
		if e = rows.Scan(&id, &f.title, &f.kind, &f.year, &f.poster, &f.season, &f.number); e != nil {
			rows.Close()
			return e
		}
		byItem[id] = f
	}
	if e = rows.Close(); e != nil {
		return e
	}
	rows, e = tx.QueryContext(ctx, `SELECT pid(e.public_id),min(a.source_value) FROM json_each(?) j JOIN catalog_entities e ON e.public_id=pid_blob(j.value) JOIN catalog_item_attribute_edges a ON a.item_id=e.id JOIN catalog_attribute_terms term ON term.id=a.term_id WHERE term.field_id=1 GROUP BY e.id`, string(raw))
	if e != nil {
		return e
	}
	for rows.Next() {
		var id, value string
		if e = rows.Scan(&id, &value); e != nil {
			rows.Close()
			return e
		}
		if f := byItem[id]; f != nil {
			f.rating = value
		}
	}
	if e = rows.Close(); e != nil {
		return e
	}
	rows, e = tx.QueryContext(ctx, `SELECT DISTINCT pid(e.public_id),COALESCE(src.label_override,term.label) name FROM json_each(?) j JOIN catalog_entities e ON e.public_id=pid_blob(j.value) JOIN catalog_term_sources src ON src.entity_id=e.id JOIN catalog_terms term ON term.id=src.term_id WHERE term.vocab=1 ORDER BY pid(e.public_id),src.provider,name`, string(raw))
	if e != nil {
		return e
	}
	for rows.Next() {
		var id, name string
		if e = rows.Scan(&id, &name); e != nil {
			rows.Close()
			return e
		}
		if f := byItem[id]; f != nil && len(f.categories) < 16 {
			f.categories = append(f.categories, name)
		}
	}
	if e = rows.Close(); e != nil {
		return e
	}
	for i := range programmes {
		p := &programmes[i]
		f := byItem[items[p.ID]]
		if f == nil {
			continue
		}
		if f.year > 0 {
			p.Year = f.year
		}
		if f.kind == "episode" {
			if f.title != "" && f.title != p.Title {
				p.Subtitle = f.title
			}
			if f.season > 0 || f.number > 0 {
				p.Episode = &livechannels.ProgrammeEpisode{Season: f.season, Number: f.number}
			}
		}
		if f.rating != "" {
			p.Rating = &livechannels.ProgrammeRating{Value: f.rating}
		}
		p.Categories = f.categories
		if f.poster {
			p.Image = "/v1/items/" + items[p.ID] + "/art/poster"
		}
	}
	return nil
}
