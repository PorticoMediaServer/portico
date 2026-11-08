package read

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

//go:embed rs_art_fixture_test.go
var rsArtJournal string

func rsArtHash(t *testing.T) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(rsDetailHash(t)+rsArtJournal)))
}
func rsArtDigest(n int) string { return fmt.Sprintf("%064x", n+1000) }
func rsArtSelect(t *testing.T, f *browseFixture, id int64, role, choice string, n int) {
	t.Helper()
	digest := rsArtDigest(n)
	f.exec(t, `INSERT OR IGNORE INTO artwork_blobs(digest,mime,width,height,bytes,created_at) VALUES(?,'image/jpeg',600,900,1234,'now')`, digest)
	f.exec(t, `INSERT OR IGNORE INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at) VALUES(?,'image/jpeg',600,900,?,?,?,1920,0,0,1,'now')`, digest, digest, digest, digest)
	// Deliberately hostile remote origins are accepted writer evidence, never output URLs.
	f.exec(t, `INSERT INTO artwork_selections(kind,entity_id,role,choice,digest,provider,image_id,origin,attribution,source_fence,actor,observed_at) VALUES('entity',?,?,?,?, 'fixture','image',?,'attribution','accepted-fence','fixture','now')`, id, role, choice, digest, "https://private.invalid/"+strings.Repeat("secret", 100))
}
func newRSArtSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newRSDetailSource(t)
	f.exec(t, `UPDATE catalog_entities SET season_id=1103 WHERE id=1104`)
	rsArtSelect(t, f, 1103, "poster", "matched", 1)
	rsArtSelect(t, f, 1103, "poster", "locked", 2)
	rsArtSelect(t, f, 1102, "backdrop", "matched", 3)
	rsArtSelect(t, f, 1102, "logo", "matched", 4)
	rsArtSelect(t, f, 1102, "still", "matched", 5)
	rsArtSelect(t, f, 1104, "still", "matched", 6)
	rsArtSelect(t, f, 1105, "portrait", "matched", 7)
	rsArtSelect(t, f, 1106, "cover", "matched", 8)
	rsArtSelect(t, f, 1107, "cover", "matched", 9)
	rsArtSelect(t, f, 1108, "cover", "matched", 10)
	rsArtSelect(t, f, 1110, "poster", "matched", 11)
	// An explicit locked-empty point gives the NULL-digest witness its own row.
	rsArtSelectEmpty(t, f, 1109, "banner")
	f.exec(t, `INSERT INTO library_sources(id,library_id,configured_root,root,incarnation) VALUES('art-source','movies','/media','/media','root-a')`)
	f.exec(t, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES('art-object','art-source','asset','root-a','cover.jpg','r1','{}',1000,1)`)
	f.exec(t, `UPDATE artwork_selections SET source_object_id='art-object',source_object_revision='r1' WHERE entity_id=1103 AND choice='matched'`)
	return f
}
func newRSArtFixture(t *testing.T, live bool) (*browseFixture, *ForegroundReader) {
	t.Helper()
	f := composeReadFixture(t, newRSArtSource(t), append(HarnessDefinitions(), ArtworkDefinitions()...), readfixture.LiveConfig(time.Now), !live, rsArtHash(t))
	r, err := NewForegroundReader(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	return f, r
}
