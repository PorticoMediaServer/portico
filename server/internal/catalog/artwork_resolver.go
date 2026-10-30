package catalog

import (
	"context"
	"encoding/json"
	"net/url"
	"github.com/PorticoMediaServer/portico/server/internal/artworkref"
)

type artworkTarget struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// StillURL is an episode's own still (the still or thumbnail role on the item
// itself), never inherited: an episode without one has none, and clients draw
// a placeholder rather than repeat the show's art (Spec — Page Content §0.2).
type resolvedArtwork struct{ PosterURL, BackdropURL, LogoURL, StillURL string }

// selectedArtworkURL describes a source already selected by an authorized
// read: its identity (v) and both slots' actual width, height, bytes and
// rendition token, so a client picks a slot without another request.
func selectedArtworkURL(kind, id, role, subject, source string, d artworkref.Descriptor) string {
	if source == "" {
		return ""
	}
	path := "/v1/metadata/" + url.PathEscape(kind) + "/" + url.PathEscape(id) + "/art/" + role + "?" + d.Query(source)
	if subject != "" {
		path += "&subject=" + url.QueryEscape(subject)
	}
	return path
}

// describeItemArt rewrites a page's bare item art routes into described,
// cacheable URLs, with one keyed read per role.
func (s *Service) describeItemArt(urls ...*string) error {
	return artworkref.DescribeItemURLs(s.Context(), s.read(), urls...)
}

// describeItemArtIn is describeItemArt for one field of every element.
func describeItemArtIn[T any](s *Service, list []T, field func(*T) *string) error {
	urls := make([]*string, 0, len(list))
	for i := range list {
		urls = append(urls, field(&list[i]))
	}
	return s.describeItemArt(urls...)
}

// describeItemArtMap is describeItemArt for the values of a map.
func describeItemArtMap(s *Service, m map[string]string) error {
	keys := make([]string, 0, len(m))
	urls := make([]string, 0, len(m))
	for k, v := range m {
		keys, urls = append(keys, k), append(urls, v)
	}
	if err := describeItemArtIn(s, urls, func(u *string) *string { return u }); err != nil {
		return err
	}
	for i, k := range keys {
		m[k] = urls[i]
	}
	return nil
}

// resolveArtwork resolves a page, including container inheritance, in one read.
// Targets have already passed the caller's visibility checks; no library scan
// or per-item lookup is hidden inside the resolver.
func (s *Service) resolveArtwork(targets []artworkTarget) (map[artworkTarget]resolvedArtwork, error) {
	return s.resolveArtworkContext(s.Context(), targets)
}

func (s *Service) resolveArtworkContext(ctx context.Context, targets []artworkTarget) (map[artworkTarget]resolvedArtwork, error) {
	out := map[artworkTarget]resolvedArtwork{}
	if len(targets) == 0 {
		return out, nil
	}
	raw, _ := json.Marshal(targets)
	rows, err := s.read().QueryContext(ctx, artworkResolutionQuery, string(raw))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var t artworkTarget
		var kind, id, role, digest string
		var d artworkref.Descriptor
		if err = rows.Scan(append([]any{&t.Kind, &t.ID, &kind, &id, &role, &digest}, d.ScanTargets()...)...); err != nil {
			return nil, err
		}
		path := selectedArtworkURL(kind, id, role, "", digest, d)
		art := out[t]
		if role == "backdrop" {
			art.BackdropURL = path
		} else if role == "still" || role == "thumbnail" {
			art.StillURL = path
		} else if role == "logo" {
			art.LogoURL = path
		} else {
			art.PosterURL = path
		}
		out[t] = art
	}
	return out, rows.Err()
}

// Pin the ready-object lookup after each selected artwork. Allowing the planner
// to put ready objects first walks the whole artwork cache for every target,
// even though selection and object identities both have keyed indexes.
const artworkResolutionQuery = `WITH requested AS MATERIALIZED (
 SELECT json_extract(value,'$.kind') kind,json_extract(value,'$.id') id FROM json_each(?)
 ), resolved AS MATERIALIZED (
 SELECT r.kind kind,r.id id,e.id entity_id FROM requested r CROSS JOIN catalog_entities e ON e.public_id=pid_blob(r.id)
 ), candidates AS (
 -- Every branch starts from the requested entities and reaches their parents
 -- by key; left to the planner, a branch scanned all episodes or songs.
 SELECT kind,id,kind target_kind,entity_id target_id,0 priority FROM resolved
 UNION ALL SELECT r.kind,r.id,'season',parent.id,1 FROM resolved r CROSS JOIN catalog_episodes ep NOT INDEXED ON r.kind='item' AND ep.entity_id=r.entity_id CROSS JOIN catalog_entities parent ON parent.id=ep.season_id
 UNION ALL SELECT r.kind,r.id,'show',parent.id,2 FROM resolved r CROSS JOIN catalog_episodes ep NOT INDEXED ON r.kind='item' AND ep.entity_id=r.entity_id CROSS JOIN catalog_entities parent ON parent.id=ep.show_id
 UNION ALL SELECT r.kind,r.id,'show',parent.id,1 FROM resolved r CROSS JOIN catalog_seasons se ON r.kind='season' AND se.entity_id=r.entity_id CROSS JOIN catalog_entities parent ON parent.id=se.show_id
 UNION ALL SELECT r.kind,r.id,'album',parent.id,1 FROM resolved r CROSS JOIN catalog_songs x ON r.kind='item' AND x.entity_id=r.entity_id CROSS JOIN catalog_entities parent ON parent.id=x.album_id
 UNION ALL SELECT r.kind,r.id,'book',parent.id,1 FROM resolved r CROSS JOIN catalog_book_files x ON r.kind='item' AND x.entity_id=r.entity_id CROSS JOIN catalog_entities parent ON parent.id=x.book_id
 ), ranked AS (
 SELECT c.kind,c.id,a.kind target_kind,a.entity_id target_id,a.role,
 a.digest digest,
 row_number() OVER(PARTITION BY c.kind,c.id,CASE a.role WHEN 'backdrop' THEN 'backdrop' WHEN 'logo' THEN 'logo' WHEN 'still' THEN 'still' WHEN 'thumbnail' THEN 'still' ELSE 'poster' END ORDER BY c.priority,CASE a.role WHEN 'poster' THEN 0 WHEN 'still' THEN 0 ELSE 1 END,a.role) rank
 FROM candidates c CROSS JOIN artwork_selections a ON a.kind=c.target_kind AND a.entity_id=c.target_id AND a.subject='' AND a.role IN('poster','cover','portrait','square','backdrop','logo','still','thumbnail') AND (a.role NOT IN('still','thumbnail') OR c.priority=0)
 CROSS JOIN artwork_sources o ON o.digest=a.digest
 ) SELECT r.kind,r.id,r.target_kind,pid(t.public_id),r.role,r.digest,` + artworkref.Columns + ` FROM ranked r CROSS JOIN catalog_entities t ON t.id=r.target_id CROSS JOIN artwork_sources src ON src.digest=r.digest CROSS JOIN artwork_blobs sb ON sb.digest=src.small_blob CROSS JOIN artwork_blobs lb ON lb.digest=src.large_blob WHERE r.rank=1`
