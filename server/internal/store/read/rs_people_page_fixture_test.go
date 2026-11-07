package read

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

//go:embed rs_people_page_fixture_test.go
var rsPeoplePageJournal string

func rsPeoplePageHash(t *testing.T) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(rsPeopleHash(t)+rsPeoplePageJournal)))
}
func rsPeoplePageDefinitions() []stmt.Definition {
	d := append(browseDefinitions(), PersonCreditsDefinitions()...)
	d = append(d, RSPeoplePageDefinitions()...)
	return append(d, RSPeopleDirectoryDefinitions()...)
}
func newRSPeoplePageSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newRSPeopleSource(t)
	f.exec(t, `DELETE FROM catalog_credits WHERE entity_id>4`)
	f.exec(t, `UPDATE catalog_people SET token='nfo-person',identity_key='local:nfo:person',sort_name='z',biography='é biography' WHERE id=1`)
	f.exec(t, `INSERT INTO catalog_people(id,token,identity_key,name,sort_name,artwork_incarnation) VALUES(3,'local-other','local:other','Another Person','a',zeroblob(16))`)
	f.exec(t, `INSERT INTO catalog_credits(image_id,entity_id,ord,person_id,role_id,department_id,credited_name,provider,credit_id,source_ordinal) VALUES(1,1,2,1,1,2,'Local person','','duplicate-role',2),(2,2,2,3,1,2,'Local other','','local-other',2)`)
	f.exec(t, `UPDATE catalog_entities SET title=CASE id WHEN 1 THEN 'Zulu' WHEN 2 THEN 'alpha' WHEN 3 THEN 'ALPHA' WHEN 4 THEN 'Beta' END,added_at=CASE id WHEN 4 THEN '2026-11-07T00:00:00.000000000Z' ELSE '2020-01-01T00:00:00.000000000Z' END WHERE id<=4`)
	f.exec(t, `INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at) VALUES(printf('%064d',1),'image/png',1,1,1,'2026-11-06T00:00:00Z')`)
	f.exec(t, `INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at) SELECT digest,'image/png',1,1,digest,digest,digest,1280,0,0,1,created_at FROM artwork_blobs`)
	f.exec(t, `INSERT INTO artwork_selections(kind,entity_id,role,choice,digest,provider,image_id,origin,attribution,source_fence,actor,observed_at) SELECT 'person',1,'portrait','matched',digest,'local','portrait','nfo','','f','test',created_at FROM artwork_sources`)
	return f
}
func newRSPeoplePageFixture(t *testing.T, live bool, clocks ...func() time.Time) (*browseFixture, *RSPeoplePageReader) {
	t.Helper()
	now := time.Now
	if len(clocks) > 0 {
		now = clocks[0]
	}
	gc := readfixture.LiveConfig(now)
	gc.Preparation = &governor.PreparationProfile{MaxActive: 4, MaxBytes: 16 << 20, MaxJobBytes: 4 << 20, ProtectedBytes: 32 << 20}
	f := composeReadFixture(t, newRSPeoplePageSource(t), rsPeoplePageDefinitions(), gc, !live, rsPeoplePageHash(t))
	r, err := NewRSPeoplePageReader(f.snapshots, f.authority)
	if err != nil {
		t.Fatal(err)
	}
	return f, r
}
func beginRSPage(t *testing.T, f *browseFixture, r *RSPeoplePageReader, in RSPersonPageInput) *RSPeoplePreparation {
	t.Helper()
	if len(in.Views) == 0 {
		in.Views = []BrowseInput{f.input}
	}
	if in.Limit == 0 {
		in.Limit = 40
	}
	if in.Person == 0 {
		in.Person = 1
	}
	p, ref, err := r.BeginPage(t.Context(), 1, time.Now().Add(12*time.Second), 4<<20, in)
	if err != nil || ref != nil {
		t.Fatal(err, ref)
	}
	t.Cleanup(func() {
		if e := p.Close(); e != nil {
			t.Error(e)
		}
	})
	return p
}
func finishRSPage(t *testing.T, f *browseFixture, r *RSPeoplePageReader, p *RSPeoplePreparation) RSPersonPage {
	t.Helper()
	for n := 0; n < 100; n++ {
		page, cost, err := r.page(t.Context(), foregroundRequest(), time.Second, f.output, p, f.input.Now)
		if err != nil {
			t.Fatal(err, cost)
		}
		if page.Complete {
			return page
		}
		if !page.Pending || page.Person != nil || page.Total != 0 || len(page.Groups) != 0 || len(page.Credits) != 0 {
			t.Fatal("partial result escaped", page)
		}
	}
	t.Fatal("preparation did not terminate")
	return RSPersonPage{}
}
