// Package artworkref builds the artwork URLs pages carry. A page URL names the
// selected source (v) and describes both of its slots (sd, ld) with their
// actual width, height and bytes, plus the rendition token (r) a client sends
// back with the slot it chose:
//
//	…/art/{role}?v={source}&sd={w}x{h}.{bytes}.{r}&ld={w}x{h}.{bytes}.{r}
//
// The client requests …?v={source}&size=small|large&r={r}. The token r is the
// first 16 hex digits of the slot file's SHA-256, so a URL that names it
// always names the same bytes.
package artworkref

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

// TokenLength is the number of hex digits of a blob digest in r.
const TokenLength = 16

// Slot is one published representation of a source.
type Slot struct {
	Blob          string
	Width, Height int
	Bytes         int
}

func (s Slot) descriptor() string {
	token := s.Blob
	if len(token) > TokenLength {
		token = token[:TokenLength]
	}
	return strconv.Itoa(s.Width) + "x" + strconv.Itoa(s.Height) + "." + strconv.Itoa(s.Bytes) + "." + token
}

// Descriptor is a source's two slots.
type Descriptor struct {
	Small, Large Slot
}

// Query is the query string naming source and describing its slots.
func (d Descriptor) Query(source string) string {
	return "v=" + source + "&" + d.Slots()
}

// Slots describes both slots without naming a source, for routes whose
// authority is not a selected source (programme and recording images).
func (d Descriptor) Slots() string {
	return "sd=" + d.Small.descriptor() + "&ld=" + d.Large.descriptor()
}

// Querier is a database handle or transaction.
type Querier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// Load describes every listed source in one keyed read. A source that is not
// published is absent from the result.
func Load(ctx context.Context, q Querier, sources []string) (map[string]Descriptor, error) {
	out := map[string]Descriptor{}
	if len(sources) == 0 {
		return out, nil
	}
	raw, err := json.Marshal(sources)
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `SELECT s.digest,sb.digest,sb.width,sb.height,sb.bytes,lb.digest,lb.width,lb.height,lb.bytes FROM (SELECT DISTINCT value digest FROM json_each(?)) r CROSS JOIN artwork_sources s ON s.digest=r.digest CROSS JOIN artwork_blobs sb ON sb.digest=s.small_blob CROSS JOIN artwork_blobs lb ON lb.digest=s.large_blob`, string(raw))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var source string
		var d Descriptor
		if err = rows.Scan(&source, &d.Small.Blob, &d.Small.Width, &d.Small.Height, &d.Small.Bytes, &d.Large.Blob, &d.Large.Width, &d.Large.Height, &d.Large.Bytes); err != nil {
			return nil, err
		}
		out[source] = d
	}
	return out, rows.Err()
}

// Columns selects a source's two slots for Scan into ScanTargets, given joins
// "JOIN artwork_blobs sb ON sb.digest=<source>.small_blob JOIN artwork_blobs lb
// ON lb.digest=<source>.large_blob".
const Columns = "sb.digest,sb.width,sb.height,sb.bytes,lb.digest,lb.width,lb.height,lb.bytes"

// NullableColumns is Columns for LEFT JOINed blobs: an absent source scans
// as an empty descriptor.
const NullableColumns = "COALESCE(sb.digest,''),COALESCE(sb.width,0),COALESCE(sb.height,0),COALESCE(sb.bytes,0),COALESCE(lb.digest,''),COALESCE(lb.width,0),COALESCE(lb.height,0),COALESCE(lb.bytes,0)"

// Joins joins a source's two slot blobs, given the column naming the source.
func Joins(source string) string {
	return " CROSS JOIN artwork_sources src ON src.digest=" + source + " CROSS JOIN artwork_blobs sb ON sb.digest=src.small_blob CROSS JOIN artwork_blobs lb ON lb.digest=src.large_blob"
}

// ScanTargets returns the Scan destinations matching Columns.
func (d *Descriptor) ScanTargets() []any {
	return []any{&d.Small.Blob, &d.Small.Width, &d.Small.Height, &d.Small.Bytes, &d.Large.Blob, &d.Large.Width, &d.Large.Height, &d.Large.Bytes}
}

// Ref is the source an item's art route serves for one role, described.
type Ref struct {
	Source string
	Slots  Descriptor
}

// ItemURL is the item art route for role. With a published source it names
// and describes it; without one it is the bare route, which the server answers
// from local evidence or as pending, as before.
func ItemURL(escapedID, role string, refs map[string]Ref, id string) string {
	path := "/v1/items/" + escapedID + "/art/" + role
	if ref, ok := refs[id]; ok {
		return path + "?" + ref.Slots.Query(ref.Source)
	}
	return path
}

// ItemSources resolves, for a page of item public ids, the source the item
// art route would serve for role, in one keyed read. It mirrors the route's
// selection exactly: the item's own selection, then for posters and covers
// its album's cover, its own cover and its book's cover, then for posters and
// backdrops its season's and show's. It grants no visibility: callers pass
// items their viewer may already see.
func ItemSources(ctx context.Context, q Querier, ids []string, role string) (map[string]Ref, error) {
	out := map[string]Ref{}
	if len(ids) == 0 {
		return out, nil
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, itemSourcesSQL, string(raw), role)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var ref Ref
		if err = rows.Scan(append([]any{&id, &ref.Source}, ref.Slots.ScanTargets()...)...); err != nil {
			return nil, err
		}
		out[id] = ref
	}
	return out, rows.Err()
}

const itemSourcesSQL = `WITH requested AS MATERIALIZED (
 SELECT DISTINCT r.value pid,e.id entity FROM json_each(?1) r CROSS JOIN catalog_entities e ON e.public_id=pid_blob(r.value)
 ), candidates(pid,digest,priority) AS (
 SELECT r.pid,a.digest,0 FROM requested r CROSS JOIN artwork_selections a ON a.kind='item' AND a.entity_id=r.entity AND a.role=?2 AND a.subject=''
 UNION ALL SELECT r.pid,a.digest,1 FROM requested r CROSS JOIN catalog_songs cs ON ?2 IN('poster','cover') AND cs.entity_id=r.entity CROSS JOIN artwork_selections a ON a.kind='album' AND a.entity_id=cs.album_id AND a.role='cover' AND a.subject=''
 UNION ALL SELECT r.pid,a.digest,2 FROM requested r CROSS JOIN artwork_selections a ON ?2 IN('poster','cover') AND a.kind='item' AND a.entity_id=r.entity AND a.role='cover' AND a.subject=''
 UNION ALL SELECT r.pid,a.digest,3 FROM requested r CROSS JOIN catalog_book_files bf ON ?2 IN('poster','cover') AND bf.entity_id=r.entity CROSS JOIN artwork_selections a ON a.kind='book' AND a.entity_id=bf.book_id AND a.role='cover' AND a.subject=''
 UNION ALL SELECT r.pid,a.digest,4 FROM requested r CROSS JOIN catalog_episodes ep NOT INDEXED ON ?2 IN('poster','backdrop') AND ep.entity_id=r.entity CROSS JOIN artwork_selections a ON a.kind='season' AND a.entity_id=ep.season_id AND a.role=?2 AND a.subject=''
 UNION ALL SELECT r.pid,a.digest,5 FROM requested r CROSS JOIN catalog_episodes ep NOT INDEXED ON ?2 IN('poster','backdrop') AND ep.entity_id=r.entity CROSS JOIN artwork_selections a ON a.kind='show' AND a.entity_id=ep.show_id AND a.role=?2 AND a.subject=''
 UNION ALL SELECT r.pid,a.digest,5 FROM requested r CROSS JOIN catalog_seasons se ON ?2 IN('poster','backdrop') AND se.entity_id=r.entity CROSS JOIN artwork_selections a ON a.kind='show' AND a.entity_id=se.show_id AND a.role=?2 AND a.subject=''
 ), chosen AS (
 SELECT c.pid,c.digest,row_number() OVER(PARTITION BY c.pid ORDER BY c.priority) rank FROM candidates c CROSS JOIN artwork_sources o ON o.digest=c.digest
 ) SELECT chosen.pid,chosen.digest,` + Columns + ` FROM chosen CROSS JOIN artwork_sources src ON src.digest=chosen.digest CROSS JOIN artwork_blobs sb ON sb.digest=src.small_blob CROSS JOIN artwork_blobs lb ON lb.digest=src.large_blob WHERE chosen.rank=1`

// DescribeItemURLs rewrites every bare item art route among urls
// ("/v1/items/{id}/art/{role}" with no query) into its described form, with
// one keyed read per role for the whole page. A route whose item has no
// published source is left bare. Callers pass URLs their viewer may already
// see; nothing here grants access.
func DescribeItemURLs(ctx context.Context, q Querier, urls ...*string) error {
	type bare struct {
		target *string
		id     string
	}
	byRole := map[string][]bare{}
	for _, u := range urls {
		if u == nil {
			continue
		}
		id, role, ok := bareItemRoute(*u)
		if ok {
			byRole[role] = append(byRole[role], bare{u, id})
		}
	}
	for role, list := range byRole {
		ids := make([]string, 0, len(list))
		for _, b := range list {
			ids = append(ids, b.id)
		}
		refs, err := ItemSources(ctx, q, ids, role)
		if err != nil {
			return err
		}
		for _, b := range list {
			if ref, ok := refs[b.id]; ok {
				*b.target += "?" + ref.Slots.Query(ref.Source)
			}
		}
	}
	return nil
}

func bareItemRoute(u string) (string, string, bool) {
	rest, ok := strings.CutPrefix(u, "/v1/items/")
	if !ok || strings.ContainsAny(rest, "?#") {
		return "", "", false
	}
	escaped, role, ok := strings.Cut(rest, "/art/")
	if !ok || escaped == "" || role == "" || strings.Contains(role, "/") {
		return "", "", false
	}
	id, err := url.PathUnescape(escaped)
	if err != nil {
		return "", "", false
	}
	return id, role, true
}
