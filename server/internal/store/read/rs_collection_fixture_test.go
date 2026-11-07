package read

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
	"testing"
)

//go:embed rs_collection_fixture_test.go
var rsCollectionJournal string

func rsCollectionHash(t *testing.T) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(browsePlanFixtureHash(t)+rsCollectionJournal)))
}

func newRSCollectionSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newBrowseSource(t)
	f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(10,'collection',0,1,1,1,0)`)
	f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(1101,x'0080000000000000000000000000044d',1,10),(1102,x'0080000000000000000000000000044e',2,10)`)
	f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,collection_name_key) SELECT id,public_id,library_id,kind,'Collection','collection','c','collection','c',0,public_id,public_id,'collection' FROM catalog_subjects WHERE id IN(1101,1102)`)
	f.exec(t, `INSERT INTO catalog_collection_members(collection_id,item_id,order_key) SELECT 1101,id,CASE WHEN id<=100 THEN 'a' WHEN id<=200 THEN 'b' ELSE 'c' END FROM catalog_entities WHERE id<=201 OR id=1040`)
	f.exec(t, `INSERT INTO catalog_collection_members(collection_id,item_id,order_key) VALUES(1102,1040,'a')`)
	f.input.Kind = 10
	return f
}
