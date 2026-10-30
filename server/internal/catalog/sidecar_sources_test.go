package catalog

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"strings"
	"testing"
)

func TestRemovingSidecarsReleasesAutomaticArtButKeepsOwnerSelections(t *testing.T) {
	for _, owner := range []bool{false, true} {
		t.Run(map[bool]string{false: "automatic", true: "owner"}[owner], func(t *testing.T) {
			c := catalogtest.Open(t)
			c.Library("m", "Movies", "movie", "/m")
			asset, token := identityFile(t, c, "m", "/m/Film (2000).mkv", 1080)
			item := attachMovie(t, c, "m", asset, "/m/Film (2000).mkv", "Film", 2000)
			carrier := sidecarCarrier{kind: "item", entity: item, role: "poster", key: "sidecar-key"}
			c.Write(func(ctx context.Context, tx *sql.Tx) error {
				ctx, read := withSidecarRead(ctx, token)
				if err := commitSidecars(ctx, tx, "m", []sidecarCarrier{carrier}); err != nil {
					return err
				}
				if err := compactcatalog.SetFieldsTx(ctx, tx, item, compactcatalog.Automatic, map[string]any{"poster_url": "local:sidecar-key"}); err != nil {
					return err
				}
				return settleSidecarRead(ctx, tx, "m", read)
			})
			digest := strings.Repeat("a", 64)
			c.Exec(`INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at) VALUES(?,'image/jpeg',1,1,1,'now'); INSERT OR IGNORE INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at) SELECT digest,mime,width,height,digest,digest,digest,1920,0,0,1,created_at FROM artwork_blobs`, digest)
			c.Exec(`INSERT INTO artwork_candidates(id,kind,entity_id,role,provider,image_id,origin,attribution,source_fence,observed_at) VALUES('candidate','item',?,'poster','local','sidecar','local:sidecar-key','Local file','fence','now')`, item)
			actor := ""
			if owner {
				actor = "owner"
			}
			c.Exec(`INSERT INTO artwork_selections(kind,entity_id,role,candidate_id,digest,locked,actor,observed_at) SELECT column1,column2,column3,column4,column5,column7,column8,column9 FROM (VALUES('item',?,'poster','candidate',?,?,?,?,'now'))`, item, digest, digest, owner, actor)
			c.Write(func(ctx context.Context, tx *sql.Tx) error {
				ctx, read := withSidecarRead(ctx, token)
				return settleSidecarRead(ctx, tx, "m", read)
			})
			if n := rows(t, c, `SELECT count(*) FROM local_artwork WHERE entity_id=?`, item); n != 0 {
				t.Fatal("removed carrier remained")
			}
			want := 0
			if owner {
				want = 1
			}
			if n := rows(t, c, `SELECT count(*) FROM artwork_selections WHERE entity_id=?`, item); n != want {
				t.Fatalf("selections: %d want %d", n, want)
			}
			if n := rows(t, c, `SELECT count(*) FROM artwork_blobs`); n != 1 {
				t.Fatal("retained image bytes were removed")
			}
		})
	}
}
