package metadata

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

func TestMusicAlbumBackgroundEntersFencedWorkerAndKeepsOwner(t *testing.T) {
	for _, owner := range []bool{false, true} {
		t.Run(map[bool]string{false: "automatic", true: "owner"}[owner], func(t *testing.T) {
			c := catalogtest.Open(t)
			library := c.Library("music", "Music", "music", "/music")
			album := c.Album(c.Artist(library, "Pink Floyd"), "The Wall", 1979)
			c.Fields(album.ID, map[string]any{"backdrop_url": "local:album-background"})
			if owner {
				digest := strings.Repeat("a", 64)
				c.Exec(fixtureArtworkBlobSQL, digest)
				c.Exec(fixtureArtworkSourceSQL, digest)
				c.Exec(`INSERT INTO artwork_candidates(id,kind,entity_id,role,provider,image_id,origin,attribution,source_fence,observed_at) VALUES('owner-background','album',?,'backdrop','local','owner','local:owner-background','Local file','owner-fence','now')`, album.ID)
				c.Exec(`INSERT INTO artwork_selections(kind,entity_id,role,candidate_id,digest,locked,actor,observed_at) VALUES('album',?,'backdrop','owner-background',?,1,'owner','now')`, album.ID, digest)
			}
			c.Write(func(ctx context.Context, tx *sql.Tx) error {
				target := RepairTarget{"album", album.Public}
				fence, err := artworkFence(ctx, tx, target)
				if err != nil {
					return err
				}
				if err = seedMusicAlbumBackdrop(ctx, tx, target, album.ID, fence, "now"); err != nil {
					return err
				}
				var candidateFence, origin string
				if err = tx.QueryRowContext(ctx, `SELECT source_fence,origin FROM artwork_candidates WHERE kind='album' AND entity_id=? AND role='backdrop' AND origin='local:album-background'`, album.ID).Scan(&candidateFence, &origin); err != nil {
					return err
				}
				if candidateFence != fence || origin != "local:album-background" {
					t.Fatal("background lost current source fence", candidateFence, fence, origin)
				}
				var jobs int
				if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM artwork_jobs WHERE kind='album' AND entity_id=? AND role='backdrop' AND status='pending'`, album.ID).Scan(&jobs); err != nil {
					return err
				}
				if (!owner && jobs != 1) || (owner && jobs != 0) {
					t.Fatal("owner selection or automatic worker admission changed", owner, jobs)
				}
				return nil
			})
		})
	}
}
