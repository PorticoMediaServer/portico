package catalog

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"
)

// Local sidecar carriers (Spec — Page Content §0.5): the scan reads an
// entity's image files once, into the local artwork cache; the commit records
// the cache keys here, and the artwork worker serves them as provider='local'
// candidates. Rows are upserted per role, like movie posters; the insert
// trigger marks the entity's artwork dirty.

// artistSidecarCarriers maps an album scan's artist keys onto its album
// artist, skipping placeholder artists a folder portrait must never tattoo.
func artistSidecarCarriers(artistID int64, artist string, extra map[string]string) []sidecarCarrier {
	if len(extra) == 0 || artist == "" || artist == "Various Artists" || artist == "Unknown artist" {
		return nil
	}
	carriers := []sidecarCarrier{}
	if key := extra["artist/portrait"]; key != "" {
		carriers = append(carriers, sidecarCarrier{"artist", artistID, "portrait", key})
	}
	if key := extra["artist/backdrop"]; key != "" {
		carriers = append(carriers, sidecarCarrier{"artist", artistID, "backdrop", key})
	}
	return carriers
}

// sidecarCarrier is one resolved local image: its entity, role and cache key.
type sidecarCarrier struct {
	kind   string
	entity int64
	role   string
	key    string
}

// commitSidecars upserts one scan's sidecar keys as local_artwork carrier
// rows. Like movie posters, a file's removal is not actively detected: the
// next scan carrying the file refreshes the row, and the insert trigger marks
// the entity's artwork dirty.
func commitSidecars(ctx context.Context, tx *sql.Tx, library string, carriers []sidecarCarrier) error {
	now := time.Now().UTC().Format(time.RFC3339)
	for _, c := range carriers {
		if c.kind == "" || c.entity == 0 || c.role == "" || c.key == "" {
			continue
		}
		var old string
		err := tx.QueryRowContext(ctx, `SELECT art_key FROM local_artwork WHERE library_id=? AND kind=? AND entity_id=? AND role=?`, library, c.kind, c.entity, c.role).Scan(&old)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if old != "" && old != c.key {
			previous := c
			previous.key = old
			if err = releaseAutomaticSidecar(ctx, tx, previous); err != nil {
				return err
			}
		}
		if read, ok := ctx.Value(sidecarReadKey{}).(*sidecarRead); ok {
			read.carriers = append(read.carriers, c)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO local_artwork(library_id,kind,entity_id,role,art_key,observed_at) VALUES(?,?,?,?,?,?) ON CONFLICT(library_id,kind,entity_id,role) DO UPDATE SET art_key=excluded.art_key,observed_at=excluded.observed_at`, library, c.kind, c.entity, c.role, c.key, now); err != nil {
			return err
		}
	}
	return nil
}

// sidecarEntities resolves an asset's episode items to their show, and one
// season filename ("season/2/poster") to that show's season id.
func sidecarShowEntities(tx *sql.Tx, asset string) ([]int64, error) {
	return scanInt64s(tx.Query(`SELECT DISTINCT e.show_id FROM catalog_assets a JOIN catalog_asset_links l INDEXED BY catalog_asset_links_asset ON l.asset_id=a.id JOIN catalog_episodes e ON e.entity_id=l.entity_id WHERE a.token=?`, asset))
}

func sidecarSeasonNumber(key string) (int, bool) {
	name, role, ok := strings.Cut(key, "/")
	if !ok || name != "season" {
		return 0, false
	}
	number, poster, ok := strings.Cut(role, "/")
	if !ok || poster != "poster" {
		return 0, false
	}
	n, err := strconv.Atoi(number)
	if err != nil || n < 0 || n > 9999 {
		return 0, false
	}
	return n, true
}

// commitAssetSidecars writes one scanned asset's sidecar keys as carriers.
// tv/anime episodes resolve to their show (and numbered seasons); movies to
// their item (logo only); music is handled in commitAudio, which knows the
// album artist.
func (s *Service) commitAssetSidecars(ctx context.Context, tx *sql.Tx, library, kind, asset string, extra map[string]string) error {
	return s.visitAssetSidecars(ctx, tx, library, kind, asset, extra, commitSidecars)
}

// visitAssetSidecars resolves slots through the asset's actual catalogue links.
// Primary carriers and alternate candidates share this entity mapping, while
// only the primary caller writes local_artwork or its selection lifecycle.
func (s *Service) visitAssetSidecars(ctx context.Context, tx *sql.Tx, library, kind, asset string, extra map[string]string, commit func(context.Context, *sql.Tx, string, []sidecarCarrier) error) error {
	if len(extra) == 0 {
		return nil
	}
	switch kind {
	case "tv", "anime":
		if key := extra["item/still"]; key != "" {
			items, e := scanInt64s(tx.QueryContext(ctx, `SELECT l.entity_id FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id WHERE a.token=?`, asset))
			if e != nil {
				return e
			}
			carriers := []sidecarCarrier{}
			for _, item := range items {
				carriers = append(carriers, sidecarCarrier{"item", item, "still", key})
			}
			if e = commit(ctx, tx, library, carriers); e != nil {
				return e
			}
		}
		shows, err := sidecarShowEntities(tx, asset)
		if err != nil {
			return err
		}
		carriers := []sidecarCarrier{}
		for _, show := range shows {
			for key, cache := range extra {
				switch {
				case key == "show/poster":
					carriers = append(carriers, sidecarCarrier{"show", show, "poster", cache})
				case key == "show/backdrop":
					carriers = append(carriers, sidecarCarrier{"show", show, "backdrop", cache})
				case key == "show/logo":
					carriers = append(carriers, sidecarCarrier{"show", show, "logo", cache})
				default:
					if number, ok := sidecarSeasonNumber(key); ok {
						var season int64
						if err := tx.QueryRowContext(ctx, `SELECT entity_id FROM catalog_seasons WHERE show_id=? AND number=?`, show, number).Scan(&season); err != nil && err != sql.ErrNoRows {
							return err
						} else if err == nil {
							carriers = append(carriers, sidecarCarrier{"season", season, "poster", cache})
						}
					}
				}
			}
		}
		return commit(ctx, tx, library, carriers)
	case "movie":
		items, err := scanInt64s(tx.QueryContext(ctx, `SELECT l.entity_id FROM catalog_assets a JOIN catalog_asset_links l INDEXED BY catalog_asset_links_asset ON l.asset_id=a.id WHERE a.token=?`, asset))
		if err != nil {
			return err
		}
		carriers := []sidecarCarrier{}
		for _, item := range items {
			for _, role := range []string{"poster", "logo", "backdrop"} {
				if key := extra["item/"+role]; key != "" {
					carriers = append(carriers, sidecarCarrier{"item", item, role, key})
				}
			}
		}
		return commit(ctx, tx, library, carriers)
	}
	return nil
}
