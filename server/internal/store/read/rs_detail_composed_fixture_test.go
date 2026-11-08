package read

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

//go:embed rs_detail_composed_fixture_test.go
var rsDetailComposedJournal string

// rsDetailComposedHash keeps this composition's Capture bound to its exact
// fixture source without touching the merged family journals.
func rsDetailComposedHash(t *testing.T) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(rsArtHash(t)+rsDetailComposedJournal)))
}

func rsDetailComposedPublic(id int64) []byte {
	public := make([]byte, 16)
	public[0] = 0x90
	public[15] = byte(id)
	return public
}

func execComposed(t *testing.T, f *browseFixture, q string, args ...any) {
	t.Helper()
	if _, err := f.conn.ExecContext(t.Context(), q, args...); err != nil {
		t.Fatalf("composed fixture: %v: %s", err, q)
	}
}

func rsDetailComposedSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newRSArtSource(t)
	// A direct movie poster and a credit image make the kind-1 common windows
	// observable without inventing art or credits for other kinds.
	rsArtSelect(t, f, 1101, "poster", "matched", 12)
	execComposed(t, f, `INSERT INTO catalog_credit_images(id,entity_id,entity_incarnation,source_revision,identity_revision,metadata_revision,owner_revision,policy_incarnation,policy_revision,consent_revision,metadata_language,source_manifest_digest,build_request_digest,manifest_algorithm,build_expected_count,build_expected_bytes,build_expected_digest,build_seed,build_chain,current_input_digest,command_incarnation,sealed,manifest_digest) SELECT 1101,id,metadata_incarnation,1,1,1,1,zeroblob(16),0,0,'en',zeroblob(32),zeroblob(32),'test',0,0,zeroblob(32),zeroblob(32),zeroblob(32),zeroblob(32),zeroblob(16),1,zeroblob(32) FROM catalog_entities WHERE id=1101`)
	execComposed(t, f, `UPDATE catalog_entities SET credit_image_id=1101 WHERE id=1101`)
	execComposed(t, f, `INSERT INTO catalog_credits(image_id,entity_id,ord,person_id,role_id,department_id,credited_name,provider,credit_id,source_ordinal) VALUES(1101,1101,0,NULL,1,2,'Composed actor','fixture','cast',0),(1101,1101,1,NULL,3,4,'Composed director','fixture','crew',1)`)
	// One selected link gives the version window its exact asset/part/edition
	// facts without exposing any ready-file, stream or chapter values.
	execComposed(t, f, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id) VALUES(1107,1,1)`)
	// One show child window keeps the RD2 bounded continuation meaningful.
	for n, id := range []int64{1130, 1131} {
		public := rsDetailComposedPublic(id)
		execComposed(t, f, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,1,3)`, id, public)
		execComposed(t, f, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,parent_id,show_id,season_number) VALUES(?,?,1,3,?,?,?,?,?,0,?,?,1102,1102,?)`, id, public, fmt.Sprintf("Composed season %d", n+1), fmt.Sprintf("season %d", n+1), fmt.Sprintf("s%d", n+1), fmt.Sprintf("season %d", n+1), fmt.Sprintf("s%d", n+1), public, public, n+1)
	}
	// Reading leaves expose accepted attribution and current inventory without
	// borrowing any timed-media typed fact.
	execComposed(t, f, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(20,'ebook',0,0,1,1,0),(21,'comic',0,0,1,1,0)`)
	execComposed(t, f, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(2,'asset2','/media/comic.cbz',2000,2,'cbz','','',0,0,0,1)`)
	for _, entry := range []struct {
		id     int64
		kind   int64
		asset  int64
		title  string
		format string
	}{
		{1120, 20, 1, "Composed ebook", "epub"},
		{1121, 21, 2, "Composed comic", "cbz"},
	} {
		public := rsDetailComposedPublic(entry.id)
		execComposed(t, f, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,1,?)`, entry.id, public, entry.kind)
		execComposed(t, f, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,has_reading_attribution_members,reading_numbered_order,reading_number_value,reading_author_value,reading_author_key,reading_author_source,reading_series_value,reading_series_key,reading_series_id,reading_series_source,reading_publication_type,reading_publication_type_source) VALUES(?,?,1,?,?,?,?,?,?,0,?,?,1,1,0,'Composed author','composed author','owner','Composed series','composed series','composed-series','owner','book','owner')`, entry.id, public, entry.kind, entry.title, entry.title, entry.title, entry.title, entry.title, public, public)
		execComposed(t, f, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id) VALUES(?,?,1)`, entry.id, entry.asset)
		execComposed(t, f, `INSERT INTO reading_publications(item_id,asset_id,format) VALUES(?,?,?)`, entry.id, entry.asset, entry.format)
	}
	execComposed(t, f, `INSERT INTO library_sources(id,library_id,kind,root,configured_root,incarnation,health,generation) VALUES('composed-source','movies','local','/composed','/composed','composed-inc','ready',1)`)
	execComposed(t, f, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state) VALUES('composed-object','composed-source','asset','composed-inc','title.mkv','r','{}',1000,1,'available')`)
	return f
}

func newRSDetailComposedFixture(t *testing.T, live bool) (*browseFixture, *RSDetailComposedReader) {
	t.Helper()
	definitions := append(HarnessDefinitions(), TitleFactsDefinitions()...)
	definitions = append(definitions, ArtworkDefinitions()...)
	definitions = append(definitions, ReadingFactsDefinitions()...)
	definitions = append(definitions, RSDetailComposedDefinitions()...)
	f := composeReadFixture(t, rsDetailComposedSource(t), definitions, readfixture.LiveConfig(time.Now), !live, rsDetailComposedHash(t))
	r, err := NewRSDetailComposedReader(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	return f, r
}
