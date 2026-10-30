package downloads

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// Title is what a download is of. It is copied onto the claim when the claim is
// made, so a list of downloads reads as titles, in order, with no second
// request, and keeps reading that way after the title has left the library.
//
// Parent is the show, album or book; Group is the season. GroupNumber is the
// season or disc, Number the episode, track or part.
type Title struct {
	Kind            string  `json:"kind"`
	Name            string  `json:"title"`
	Year            int     `json:"year,omitempty"`
	DurationSeconds float64 `json:"durationSeconds,omitempty"`
	ParentID        string  `json:"parentId,omitempty"`
	ParentTitle     string  `json:"parentTitle,omitempty"`
	GroupID         string  `json:"groupId,omitempty"`
	GroupTitle      string  `json:"groupTitle,omitempty"`
	GroupNumber     *int    `json:"groupNumber,omitempty"`
	Number          *int    `json:"number,omitempty"`
	Artist          string  `json:"artist,omitempty"`
	// OnServer is false once the title has left this server's library. The
	// facts above are then the last ones the server knew.
	OnServer bool `json:"onServer"`
}

func number(v sql.NullInt64) *int {
	if !v.Valid {
		return nil
	}
	n := int(v.Int64)
	return &n
}

// readTitle reads a title's listing facts by entity. Each kind is one seek
// from the entity to its side table and one to each parent.
func readTitle(ctx context.Context, tx *sql.Tx, entity int64) (Title, error) {
	var out Title
	var kind int
	e := tx.QueryRowContext(ctx, `SELECT e.kind,e.title,e.year,COALESCE((SELECT max(a.duration) FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=e.id),0) FROM catalog_entities e WHERE e.id=?`, entity).Scan(&kind, &out.Name, &out.Year, &out.DurationSeconds)
	if e != nil {
		return out, e
	}
	out.Kind, _ = compactcatalog.Kind(kind).Name()
	out.OnServer = true
	var group, number sql.NullInt64
	switch compactcatalog.Kind(kind) {
	case compactcatalog.Episode:
		var numbering string
		e = tx.QueryRowContext(ctx, `SELECT pid(show.public_id),show.title,COALESCE(pid(season.public_id),''),COALESCE(s.title,''),s.number,ep.number,ep.numbering
 FROM catalog_episodes ep JOIN catalog_entities show ON show.id=ep.show_id
 LEFT JOIN catalog_entities season ON season.id=ep.season_id LEFT JOIN catalog_seasons s ON s.entity_id=ep.season_id
 WHERE ep.entity_id=?`, entity).Scan(&out.ParentID, &out.ParentTitle, &out.GroupID, &out.GroupTitle, &group, &number, &numbering)
		// An episode filed by air date has no number a person would recognise.
		if numbering == "date" {
			number = sql.NullInt64{}
		}
	case compactcatalog.Track:
		var provider string
		e = tx.QueryRowContext(ctx, `SELECT pid(album.public_id),album.title,cs.disc_number,cs.track_number,COALESCE(artist.title,''),cs.provider_artist
 FROM catalog_songs cs JOIN catalog_entities album ON album.id=cs.album_id
 LEFT JOIN catalog_albums ca ON ca.entity_id=cs.album_id LEFT JOIN catalog_entities artist ON artist.id=ca.artist_id
 WHERE cs.entity_id=?`, entity).Scan(&out.ParentID, &out.ParentTitle, &group, &number, &out.Artist, &provider)
		if provider != "" {
			out.Artist = provider
		}
	case compactcatalog.Part:
		e = tx.QueryRowContext(ctx, `SELECT pid(book.public_id),book.title,bf.disc_number,bf.part_number,COALESCE(cb.author,'')
 FROM catalog_book_files bf JOIN catalog_entities book ON book.id=bf.book_id LEFT JOIN catalog_books cb ON cb.entity_id=bf.book_id
 WHERE bf.entity_id=?`, entity).Scan(&out.ParentID, &out.ParentTitle, &group, &number, &out.Artist)
	}
	if errors.Is(e, sql.ErrNoRows) {
		// The side row is still being written by a scan: the title's own facts
		// are enough to list it, and the claim refreshes them when it is ready.
		e = nil
	}
	out.GroupNumber, out.Number = numberOf(group), numberOf(number)
	return out, e
}

func numberOf(v sql.NullInt64) *int { return number(v) }

// titleSnapshot renders a title for storage on a claim.
func titleSnapshot(t Title) string {
	raw, e := json.Marshal(t)
	if e != nil {
		return ""
	}
	return string(raw)
}

// storedTitle reads a claim's stored title. onServer is today's answer, not the
// stored one: a claim made while the title existed must say so when it is gone.
func storedTitle(raw string, onServer bool) Title {
	var out Title
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	out.OnServer = onServer
	return out
}

// refreshTitle rewrites a claim's stored title from the library as it is now.
// It runs when a claim becomes ready, so a title matched or renamed while its
// download was being prepared is listed under its settled name.
func refreshTitle(ctx context.Context, tx *sql.Tx, id string) error {
	var entity int64
	var public string
	e := tx.QueryRowContext(ctx, `SELECT d.item_id,d.item_public FROM download_preparations d JOIN catalog_entities e ON e.id=d.item_id AND e.public_id=pid_blob(d.item_public) WHERE d.id=?`, id).Scan(&entity, &public)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	title, e := readTitle(ctx, tx, entity)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE download_preparations SET title_json=? WHERE id=?`, titleSnapshot(title), id)
	return e
}
