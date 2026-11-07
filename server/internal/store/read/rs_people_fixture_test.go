package read

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
	"testing"
)

//go:embed rs_people_fixture_test.go
var rsPeopleJournal string

func rsPeopleHash(t *testing.T) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(browsePlanFixtureHash(t)+rsPeopleJournal)))
}
func newRSPeopleSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newBrowseSource(t)
	f.exec(t, `INSERT INTO catalog_people(id,token,identity_key,name,sort_name,biography,artwork_incarnation) VALUES(1,'person','person','Person','person','Accepted biography',zeroblob(16)),(2,'orphan','orphan','Orphan','orphan','Private orphan biography',zeroblob(16))`)
	f.exec(t, `INSERT INTO catalog_credit_labels(id,label) VALUES(1,'Actor'),(2,'Cast'),(3,'Director'),(4,'Directing')`)
	f.exec(t, `BEGIN`)
	for id := int64(1); id <= 101; id++ {
		work := id
		if id == 101 {
			work = 1040
		}
		for _, image := range []int64{work, 10000 + work} {
			f.exec(t, `INSERT INTO catalog_credit_images(id,entity_id,entity_incarnation,source_revision,identity_revision,metadata_revision,owner_revision,policy_incarnation,policy_revision,consent_revision,metadata_language,source_manifest_digest,build_request_digest,manifest_algorithm,build_expected_count,build_expected_bytes,build_expected_digest,build_seed,build_chain,current_input_digest,command_incarnation,sealed,manifest_digest) SELECT ?,id,metadata_incarnation,1,1,1,1,zeroblob(16),0,0,'en',zeroblob(32),zeroblob(32),'test',0,0,zeroblob(32),zeroblob(32),zeroblob(32),zeroblob(32),zeroblob(16),1,zeroblob(32) FROM catalog_entities WHERE id=?`, image, work)
			if image == work {
				f.exec(t, `INSERT INTO catalog_credits(image_id,entity_id,ord,person_id,role_id,department_id,credited_name,provider,credit_id,source_ordinal) VALUES(?,?,0,1,1,2,'Credited person','fixture','cast',0),(?,?,1,1,3,4,'Credited person','fixture','crew',1)`, image, work, image, work)
			} else {
				f.exec(t, `INSERT INTO catalog_credits(image_id,entity_id,ord,person_id,role_id,department_id,credited_name,provider,credit_id,source_ordinal) VALUES(?,?,1,1,1,2,'Old name','fixture','obsolete',1)`, image, work)
			}
		}
		f.exec(t, `UPDATE catalog_entities SET credit_image_id=? WHERE id=?`, work, work)
	}
	f.exec(t, `UPDATE catalog_credit_images SET sealed=0 WHERE id=30`)
	f.exec(t, `COMMIT`)
	return f
}
