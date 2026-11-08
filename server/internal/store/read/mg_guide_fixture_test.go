package read

import (
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

// Independent facts: time ties, overlap boundaries, two immutable generations,
// wide multibyte display values, and a sealed locator never selected by reads.
const mgGuideSeed = `
INSERT INTO live_source_identities(id,settings_viewer_access) VALUES('mg-source','server-members');
INSERT INTO live_generations(id,source_id,name,tuner_count,channel_count,programme_count,created_at) VALUES
 ('mg-old','mg-source','Old guide',2,3,6,'2026-11-07T00:00:00Z'),
 ('mg-new','mg-source','New guide',2,1,1,'2026-11-07T00:00:00Z');
INSERT INTO live_sources(id,name,revision,state,tuner_count,active_generation,published_at) VALUES('mg-source','Guide',1,'active',2,'mg-old','2026-11-07T00:00:00Z');
INSERT INTO live_operations(request_id,input_hash,source_id,generation_id,expected_revision,status,command_incarnation,create_operation_id,birth_request_digest,expected_choice_count,manifest_digest,stage_digest) VALUES
 ('mg-import','digest','mg-source','mg-new',0,'staging',x'01010101010101010101010101010101',x'02020202020202020202020202020202',zeroblob(32),0,zeroblob(32),zeroblob(32));
INSERT INTO live_channel_versions(generation_id,channel_id,provider_key,name,number,group_name,position,locator_sealed,available_start,available_end,has_live_channel_logos,logo_url) VALUES
 ('mg-old','mg-channel','provider','Télé 📺','1','News',0,X'010203','2026-11-07T00:00:00Z','2026-11-07T06:00:00Z',1,'https://example.invalid/logo'),
 ('mg-old','mg-channel-1','provider-1','Second','2','News',1,X'010203','','',0,NULL),
 ('mg-old','mg-channel-2','provider-2','Third','3','News',2,X'010203','','',0,NULL),
 ('mg-new','mg-channel','provider','New channel','1','News',0,X'010203','','',0,NULL);
INSERT INTO live_programmes(generation_id,id,channel_id,provider_key,title,start_utc,end_utc,lineage,has_live_programme_metadata,metadata_series_id,metadata_episode_id,metadata_new_evidence,metadata_description,metadata_facts,has_live_programme_icons,icon_url) VALUES
 ('mg-old','mg-a','mg-channel','provider-a','Émission 📺','2026-11-07T00:00:00Z','2026-11-07T01:00:00Z','a',1,'series','episode','new','Résumé 漢字','{"rating":"TV-G"}',1,'https://example.invalid/icon'),
 ('mg-new','mg-a','mg-channel','provider-a','Replacement','2026-11-07T00:00:00Z','2026-11-07T01:00:00Z','a',0,NULL,NULL,NULL,NULL,'{}',0,NULL);
INSERT INTO live_programmes(generation_id,id,channel_id,provider_key,title,start_utc,end_utc,lineage) VALUES
 ('mg-old','mg-b','mg-channel','provider-b','Tie one','2026-11-07T01:00:00Z','2026-11-07T02:00:00Z','b'),
 ('mg-old','mg-c','mg-channel','provider-c','Tie two','2026-11-07T01:00:00Z','2026-11-07T02:00:00Z','c'),
 ('mg-old','mg-before','mg-channel','provider-before','Ends at start','2026-11-06T23:00:00Z','2026-11-07T00:00:00Z','before'),
 ('mg-old','mg-overlap','mg-channel','provider-overlap','Starts before','2026-11-06T23:30:00Z','2026-11-07T00:30:00Z','overlap'),
 ('mg-old','mg-after','mg-channel','provider-after','Starts at end','2026-11-07T06:00:00Z','2026-11-07T07:00:00Z','after');
`

// The fixture's member token must see every configured library: the guide is
// refused when any configured catalogue library is outside the viewer's set.
const mgGuideJournal = `
UPDATE direct_memberships SET allowed_libraries='["movies","other"]' WHERE account_id='a';
`

func mgGuideSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newForegroundSource(t)
	f.exec(t, mgGuideSeed)
	f.exec(t, mgGuideJournal)
	return f
}

func mgGuidePlanHash(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("%x", sha256.Sum256([]byte(foregroundFixtureHash(t)+mgGuideSeed+mgGuideJournal)))
}

func mgGuideFixture(t *testing.T) (*browseFixture, *MGGuideReader) {
	t.Helper()
	f := composeReadFixture(t, mgGuideSource(t), MGGuideHarnessDefinitions(), readfixture.LiveConfig(time.Now), true, mgGuidePlanHash(t))
	r, err := NewMGGuideReader(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	return f, r
}

func mgGuideInput(f *browseFixture, limit int) MGGuideInput {
	in := MGGuideInput{View: f.input, Source: "mg-source", Start: "2026-11-07T00:00:00Z", End: "2026-11-07T06:00:00Z"}
	in.View.Limit = limit
	return in
}
