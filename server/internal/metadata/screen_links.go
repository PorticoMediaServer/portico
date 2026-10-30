package metadata

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

// A work's provider links: its similar-title lists (catalog_similar) and every
// provider id it is known by (catalog_external_ids), both read by the
// recommendation engine and resolved by provider id when read.
//
// They follow the accepted identity and nothing else. A work has one accepted
// screen identity at a time, so publishing it replaces the whole set: every
// external id the entity has, and every similar list from a screen provider
// (other sources, such as a Portico dataset, are not this lane's rows). That
// is what keeps a refresh or a re-identification from leaving an id or a list
// behind: nothing is merged, so nothing stale can survive. Clearing or
// changing the identity drops the set with it. These are not descriptive
// fields: owner locks, manual metadata and fill_missing do not apply, and a
// candidate that was never accepted never reaches them.

// screenLinkSources are the catalog_similar sources a screen publication owns.
const screenLinkSources = `'tmdb','tvdb','anilist'`

// screenLinkKind maps the screen identity vocabulary onto the kinds the link
// tables store: tmdb movie|tv, tvdb series|movie, anilist anime, imdb title.
var screenLinkKinds = map[string]string{"tmdb:movie": "movie", "tmdb:show": "tv", "tvdb:show": "series", "tvdb:movie": "movie", "anilist:anime": "anime", "imdb:title": "title"}

func screenLinkKind(id metadataprovider.ScreenID) (string, bool) {
	kind, ok := screenLinkKinds[id.Provider+":"+id.Type]
	return kind, ok && metadataprovider.ValidScreenID(id)
}

// screenWorkLinked reports whether a screen target is a work the link tables
// describe: a movie item or a show, never an episode.
func screenWorkLinked(b screenBase) bool {
	return b.TargetKind == "show" || b.TargetKind == "item" && b.ItemKind == "movie"
}

type screenLink struct{ source, provider, kind, id string }

// publishScreenLinks replaces the work's links with the accepted record's. An
// unchanged set writes nothing: a republished stored document (every wake
// inside the freshness window) must not churn rows it would rewrite as-is.
func publishScreenLinks(ctx context.Context, tx *sql.Tx, entity int64, r metadataprovider.ScreenRecord) error {
	similar := []screenLink{}
	for _, target := range r.Similar {
		if kind, ok := screenLinkKind(target); ok {
			similar = append(similar, screenLink{r.Identity.Provider, target.Provider, kind, target.ID})
		}
	}
	external := []screenLink{}
	seen := map[screenLink]bool{}
	for _, id := range append([]metadataprovider.ScreenID{r.Identity}, r.Crosswalk...) {
		kind, ok := screenLinkKind(id)
		link := screenLink{"", id.Provider, kind, id.ID}
		if ok && !seen[link] {
			seen[link] = true
			external = append(external, link)
		}
	}
	current, err := readScreenLinks(ctx, tx, `SELECT source,provider,provider_kind,provider_id FROM catalog_similar WHERE entity_id=? AND source IN(`+screenLinkSources+`) ORDER BY source,rank`, entity)
	if err != nil {
		return err
	}
	if !sameScreenLinks(current, similar) {
		if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_similar WHERE entity_id=? AND source IN(`+screenLinkSources+`)`, entity); err != nil {
			return err
		}
		for i, v := range similar {
			if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_similar(entity_id,source,rank,provider,provider_kind,provider_id) VALUES(?,?,?,?,?,?)`, entity, v.source, i+1, v.provider, v.kind, v.id); err != nil {
				return err
			}
		}
	}
	current, err = readScreenLinks(ctx, tx, `SELECT '',provider,provider_kind,provider_id FROM catalog_external_ids WHERE entity_id=?`, entity)
	if err != nil {
		return err
	}
	if sameScreenLinkSet(current, external) {
		return nil
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_external_ids WHERE entity_id=?`, entity); err != nil {
		return err
	}
	for _, v := range external {
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_external_ids(entity_id,provider,provider_kind,provider_id) VALUES(?,?,?,?)`, entity, v.provider, v.kind, v.id); err != nil {
			return err
		}
	}
	// The work has just been given this identity. Another title of the library
	// that already carries it is the same work (a second folder of one show, a
	// film catalogued under two names): it becomes part of this one, which
	// keeps its id, and state kept for the identity from an earlier removal is
	// given back.
	if kind, ok := screenLinkKind(r.Identity); ok {
		if _, err = catalog.AdoptProviderIdentityTx(ctx, tx, entity, compactcatalog.ProviderIdentity{Provider: r.Identity.Provider, Kind: kind, ID: r.Identity.ID}); err != nil {
			return err
		}
	}
	return nil
}

// clearScreenLinks drops a work's provider links: its identity was cleared, or
// is about to be replaced by one that has not been published yet.
func clearScreenLinks(ctx context.Context, tx *sql.Tx, entity int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_similar WHERE entity_id=? AND source IN(`+screenLinkSources+`)`, entity); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM catalog_external_ids WHERE entity_id=?`, entity)
	return err
}

// retireScreenLinks clears a work's links when an owner decision moves its
// identity away from the one on record. The previous identity is rejected at
// that moment, so its lists must not stand in while the new one is fetched;
// re-choosing the same identity (a new episode order, say) keeps them.
func retireScreenLinks(ctx context.Context, tx *sql.Tx, targetKind string, entity int64, next metadataprovider.ScreenID) error {
	var current metadataprovider.ScreenID
	err := tx.QueryRowContext(ctx, `SELECT provider,provider_type,provider_id FROM screen_metadata_work WHERE target_kind=? AND target_id=?`, targetKind, entity).Scan(&current.Provider, &current.Type, &current.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && current == next {
		return nil
	}
	return clearScreenLinks(ctx, tx, entity)
}

func readScreenLinks(ctx context.Context, tx *sql.Tx, query string, entity int64) ([]screenLink, error) {
	rows, err := tx.QueryContext(ctx, query, entity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []screenLink{}
	for rows.Next() {
		var v screenLink
		if err = rows.Scan(&v.source, &v.provider, &v.kind, &v.id); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// sameScreenLinks compares ranked lists; a record's list has one source, so
// the stored rows ordered by source then rank are that list in rank order.
func sameScreenLinks(a, b []screenLink) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// sameScreenLinkSet compares id sets; both sides are duplicate-free (the
// table's key and the publisher's dedupe), so equal size and containment is
// equality.
func sameScreenLinkSet(a, b []screenLink) bool {
	if len(a) != len(b) {
		return false
	}
	in := map[screenLink]bool{}
	for _, v := range a {
		in[v] = true
	}
	for _, v := range b {
		if !in[v] {
			return false
		}
	}
	return true
}
