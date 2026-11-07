package read

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// BEGIN_RB_HOME_JOURNAL
func newRBHomeSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newBrowseSource(t)
	f.exec(t, `DELETE FROM catalog_entities WHERE id>12 AND id<>1040`)
	f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(3,'season',0,1,1,1,0),(4,'episode',1,0,1,1,0),(5,'artist',0,1,1,1,1),(6,'album',0,1,1,1,1),(7,'song',1,0,1,1,1),(8,'book',0,1,1,1,1),(9,'book_file',1,0,1,1,1)`)
	f.exec(t, `UPDATE catalog_entities SET added_at='2026-10-01T00:00:00.000000000Z',admission_own_age=10`)
	homeFixtureEntity(t, f, 2000, 1, 2, nil, nil, nil, "2026-01-01T00:00:00.000000000Z")
	homeFixtureEntity(t, f, 2100, 1, 3, int64(2000), int64(2000), nil, "2026-01-01T00:00:00.000000000Z")
	homeFixtureEntity(t, f, 2200, 1, 3, int64(2000), int64(2000), nil, "2026-01-01T00:00:00.000000000Z")
	homeFixtureEntity(t, f, 3000, 1, 4, int64(2100), int64(2000), int64(2100), "2026-11-01T12:00:00.000000000Z")
	homeFixtureEntity(t, f, 3001, 1, 4, int64(2100), int64(2000), int64(2100), "2026-11-01T11:59:00.000000000Z")
	homeFixtureEntity(t, f, 3002, 1, 4, int64(2200), int64(2000), int64(2200), "2026-10-01T12:00:00.000000000Z")
	f.exec(t, `UPDATE catalog_entities SET last_content_added_at='2026-11-01T12:00:00.000000000Z' WHERE id IN(2000,2100)`)
	homeFixtureEntity(t, f, 9000, 1, 5, nil, nil, nil, nil)
	homeFixtureEntity(t, f, 9001, 1, 6, int64(9000), nil, nil, nil)
	homeFixtureEntity(t, f, 9100, 1, 8, nil, nil, nil, nil)
	homeFixtureEntity(t, f, 9200, 1, 7, int64(9001), nil, nil, nil)
	homeFixtureEntity(t, f, 9300, 1, 9, int64(9100), nil, nil, nil)
	f.exec(t, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,resume_position,activity_at,activity_state,resume_playback_id) SELECT 'p',public_id,id,library_id,5000,'2026-11-02T12:00:00.000000000Z','in_progress','play-1' FROM catalog_entities WHERE id IN(1,3000,9200,9300,9100)`)
	f.exec(t, `UPDATE personal_items SET resume_item_public_id=(SELECT public_id FROM catalog_entities WHERE id=9300) WHERE item_id=9100`)
	for _, id := range []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 1040, 3000, 3001, 3002, 9200, 9300} {
		homeFixtureAsset(t, f, id)
	}
	return f
}
func homeFixtureEntity(t *testing.T, f *browseFixture, id, library, kind int64, parent, show, season any, added any) {
	t.Helper()
	public := make([]byte, 16)
	binary.BigEndian.PutUint64(public[8:], uint64(id))
	f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,?,?)`, id, public, library, kind)
	var artist, album, book any
	if kind == 6 {
		artist = parent
	}
	if kind == 7 {
		album = parent
	}
	if kind == 9 {
		book = parent
	}
	f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,parent_id,show_id,season_id,season_number,episode_number,numbering,artist_id,album_id,book_id,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,added_at,admission_own_age) VALUES(?,?,?,?,?,?,?,?,?,'seasonal',?,?,?,?,'home','h','home','h',0,?,?,?,10)`, id, public, library, kind, parent, show, season, id, id, artist, album, book, fmt.Sprintf("Home %d", id), public, public, added)
}
func homeFixtureAsset(t *testing.T, f *browseFixture, id int64) {
	t.Helper()
	f.exec(t, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) SELECT id,printf('home-%d',id),printf('/media/home-%d',id),1000,1,'matroska','h264','aac',1920,1080,120.0,library_id FROM catalog_entities WHERE id=?`, id)
	f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id) SELECT id,id,library_id FROM catalog_entities WHERE id=?`, id)
}

// END_RB_HOME_JOURNAL

func rbHomeFixtureHash(t *testing.T) string {
	t.Helper()
	data, e := os.ReadFile("rb_home_test.go")
	if e != nil {
		t.Fatal(e)
	}
	start := strings.Index(string(data), "// BEGIN_RB_HOME_JOURNAL\n")
	end := strings.Index(string(data), "// END_RB_HOME_JOURNAL\n")
	if start < 0 || end <= start {
		t.Fatal("journal")
	}
	return fmt.Sprintf("%x", sha256.Sum256(append([]byte(browsePlanFixtureHash(t)), data[start:end]...)))
}
func newRBHomeFixture(t *testing.T) *browseFixture {
	t.Helper()
	return composeReadFixture(t, newRBHomeSource(t), RBHomeDefinitions(), readfixture.LiveConfig(time.Now), true, rbHomeFixtureHash(t))
}
func homeRequest() governor.Request {
	r := foregroundRequest()
	r.Budget.RawRows = 300
	r.Budget.VMOps = 100000
	r.Budget.Pages = 256
	return r
}
func homeTargetRead(t *testing.T, f *browseFixture, kind, id int64) (HomeRecentCard, BrowseReadResult, error) {
	t.Helper()
	in := HomeTargetInput{View: f.input, Work: id}
	in.View.Kind = kind
	return f.reader.homeRecentTarget(t.Context(), homeRequest(), time.Second, f.output, in)
}

func TestRBHomeTargetProjection(t *testing.T) {
	f := newRBHomeFixture(t)
	card, cost, e := homeTargetRead(t, f, 2, 2000)
	if e != nil || card.Work.ID != 2000 || card.Target.ID != 2100 || card.TargetKind != 3 || card.EligibleLeaves != 3 {
		t.Fatal(card, cost, e)
	}
	f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id=3001`)
	card, _, e = homeTargetRead(t, f, 2, 2000)
	if e != nil || card.Target.ID != 3000 || card.TargetKind != 4 {
		t.Fatal(card, e)
	}
	f.exec(t, `UPDATE catalog_entities SET added_at='2026-11-01T11:58:00.000000000Z' WHERE id=3002`)
	card, _, e = homeTargetRead(t, f, 2, 2000)
	if e != nil || card.Target.ID != 2000 || card.TargetKind != 2 {
		t.Fatal(card, e)
	}
	f.exec(t, `UPDATE catalog_entities SET retired=0 WHERE id=3001`)
	card, _, e = homeTargetRead(t, f, 3, 2100)
	if e != nil || card.Target.ID != 2100 || card.TargetKind != 3 {
		t.Fatal(card, e)
	}
}

func TestRBHomeRestrictedChronologyUsesEligibleLeaves(t *testing.T) {
	f := newRBHomeFixture(t)
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('p',12,0)`)
	f.exec(t, `UPDATE catalog_entities SET admission_own_age=18 WHERE id=3000`)
	f.exec(t, `UPDATE catalog_entities SET last_content_added_at='2026-11-06T12:00:00.000000000Z',admission_own_age=NULL WHERE id=2000`)
	card, _, e := homeTargetRead(t, f, 2, 2000)
	if e != nil || card.Target.ID != 3001 || card.Added != "2026-11-01T11:59:00.000000000Z" || card.EligibleLeaves != 2 {
		t.Fatal(card, e)
	}
	f.exec(t, `UPDATE catalog_assets SET available=0 WHERE id=3001`)
	card, _, e = homeTargetRead(t, f, 2, 2000)
	if e != nil || card.Target.ID != 3002 || card.Added != "2026-10-01T12:00:00.000000000Z" {
		t.Fatal(card, e)
	}
	f.exec(t, `INSERT INTO dvr_catalog_retirements(item_id,requested_ms) VALUES(3002,1)`)
	card, _, e = homeTargetRead(t, f, 2, 2000)
	if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(card, HomeRecentCard{}) {
		t.Fatal("empty work escaped", card, e)
	}
}

func TestRBHomeTargetRawCapFailsClosed(t *testing.T) {
	f := newRBHomeFixture(t)
	for n := int64(0); n < 256; n++ {
		homeFixtureEntity(t, f, 4000+n, 1, 4, int64(2100), int64(2000), int64(2100), nil)
	}
	card, _, e := homeTargetRead(t, f, 2, 2000)
	if !errors.Is(e, ErrUnqualified) || !reflect.DeepEqual(card, HomeRecentCard{}) {
		t.Fatal("truncated success", card, e)
	}
}

func TestRBHomeRecentExactMergeAndSourceRefusal(t *testing.T) {
	f := newRBHomeFixture(t)
	f.exec(t, `UPDATE direct_memberships SET allowed_libraries='["movies","other"]' WHERE account_id='a'`)
	f.exec(t, `UPDATE catalog_entities SET added_at='2026-11-02T12:00:00.000000000Z' WHERE id=1040`)
	in := HomeRecentInput{View: f.input, Sources: []HomeSource{{2, 1}, {1, 2}, {1, 1}}}
	row, cost, e := f.reader.homeRecentRow(t.Context(), homeRequest(), time.Second, f.output, in)
	if e != nil || !row.Exhausted || len(row.Items) != 13 || row.Items[0].Work.ID != 1040 || row.Items[1].Work.ID != 2000 {
		t.Fatal(row, cost, e)
	}

	if cost.PublicationStatements != 14 {
		t.Fatal("secondary library not freshly fenced", cost)
	}
	for n := int64(0); n < 64; n++ {
		homeFixtureEntity(t, f, 5000+n, 1, 1, nil, nil, nil, nil)
	}
	row, _, e = f.reader.homeRecentRow(t.Context(), homeRequest(), time.Second, f.output, in)
	if !errors.Is(e, ErrUnqualified) || !reflect.DeepEqual(row, HomeRecentRow{}) {
		t.Fatal("raw cap produced exhaustive shelf", row, e)
	}
}

func TestRBHomeManagedSourceProofAndFreshFence(t *testing.T) {
	f := newRBHomeFixture(t)
	f.exec(t, `INSERT INTO library_sources(id,library_id,configured_root,root,incarnation) VALUES('home-source','movies','/media','/media','home-incarnation')`)
	card, _, e := homeTargetRead(t, f, 2, 2000)
	if !errors.Is(e, ErrUnqualified) || card.Work.ID != 0 {
		t.Fatal("guessed managed/legacy fallback", card, e)
	}
	for _, id := range []int64{3000, 3001, 3002} {
		f.exec(t, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES(?,'home-source',?,'home-incarnation',?,'1','{}',1000,1)`, fmt.Sprint("object-", id), fmt.Sprint("home-", id), fmt.Sprint("file-", id))
	}
	f.exec(t, `UPDATE catalog_assets SET available=0 WHERE id=3000`)
	card, _, e = homeTargetRead(t, f, 2, 2000)
	if e != nil || card.Target.ID != 2100 {
		t.Fatal("accepted inventory proof lost", card, e)
	}
	f.exec(t, `UPDATE inventory_objects SET unsupported_reason=char(0) WHERE asset_id='home-3000'`)
	card, _, e = homeTargetRead(t, f, 2, 2000)
	if e != nil || card.Target.ID != 3001 {
		t.Fatal("NUL reason treated as empty", card, e)
	}
	f.reader.snapshots = &browseBoundarySnapshots{Snapshots: f.snapshots, afterBusiness: func() { f.exec(t, `UPDATE library_sources SET enabled=0 WHERE id='home-source'`) }}
	card, _, e = homeTargetRead(t, f, 2, 2000)
	if e == nil || card.Work.ID != 0 {
		t.Fatal("source disable escaped final exact root", card, e)
	}
}

func TestRBHomeRecentRestrictedGlobalMerge(t *testing.T) {
	f := newRBHomeFixture(t)
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('p',12,0)`)
	f.exec(t, `UPDATE catalog_entities SET admission_own_age=18 WHERE id=3000`)
	f.exec(t, `UPDATE catalog_entities SET last_content_added_at='2026-11-06T12:00:00.000000000Z' WHERE id=2000`)
	homeFixtureEntity(t, f, 2010, 1, 2, nil, nil, nil, "2026-01-01T00:00:00.000000000Z")
	homeFixtureEntity(t, f, 3010, 1, 4, nil, int64(2010), nil, "2026-11-02T12:00:00.000000000Z")
	homeFixtureAsset(t, f, 3010)
	f.exec(t, `UPDATE catalog_entities SET last_content_added_at='2026-11-02T12:00:00.000000000Z' WHERE id=2010`)
	row, _, e := f.reader.homeRecentRow(t.Context(), homeRequest(), time.Second, f.output, HomeRecentInput{View: f.input, Sources: []HomeSource{{1, 2}}})
	if e != nil || len(row.Items) != 2 || row.Items[0].Work.ID != 2010 || row.Items[1].Added != "2026-11-01T11:59:00.000000000Z" {
		t.Fatal("hidden child reordered shelf", row, e)
	}
}

type homeStageSnapshots struct {
	*governor.Snapshots
	reads  int
	after  int
	mutate func()
}

func (s *homeStageSnapshots) Read(ctx context.Context, request governor.Request, budget time.Duration, read func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	s.reads++
	result, e := s.Snapshots.Read(ctx, request, budget, read)
	if s.reads == s.after {
		s.mutate()
	}
	return result, e
}
func TestRBHomeRecentStageHandoffRechecksAuthority(t *testing.T) {
	f := newRBHomeFixture(t)
	f.reader.snapshots = &homeStageSnapshots{Snapshots: f.snapshots, after: 3, mutate: func() { f.exec(t, `UPDATE authorization_session_families SET revoked=1`) }}
	row, _, e := f.reader.homeRecentRow(t.Context(), homeRequest(), time.Second, f.output, HomeRecentInput{View: f.input, Sources: []HomeSource{{1, 2}}})
	if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(row, HomeRecentRow{}) {
		t.Fatal("source capture escaped the target-stage fence", row, e)
	}
}

func TestRBHomeRecentShelfCapFromTwoCompleteStreams(t *testing.T) {
	f := newRBHomeFixture(t)
	f.exec(t, `UPDATE direct_memberships SET allowed_libraries='["movies","other"]' WHERE account_id='a'`)
	for n := int64(0); n < 50; n++ {
		for _, library := range []int64{1, 2} {
			id := 10000 + library*100 + n
			homeFixtureEntity(t, f, id, library, 1, nil, nil, nil, "2026-11-01T12:00:00.000000000Z")
			homeFixtureAsset(t, f, id)
		}
	}
	row, cost, e := f.reader.homeRecentRow(t.Context(), homeRequest(), time.Second, f.output, HomeRecentInput{View: f.input, Sources: []HomeSource{{1, 1}, {2, 1}}})
	var want []int64
	for n := int64(0); n < 50; n++ {
		want = append(want, 10100+n, 10200+n)
	}
	wire := func(id int64) string {
		var b [16]byte
		binary.BigEndian.PutUint64(b[8:], uint64(id))
		return base64.RawURLEncoding.EncodeToString(b[:])
	}
	slices.SortFunc(want, func(a, b int64) int { return -strings.Compare(wire(a), wire(b)) })
	var got []int64
	for _, card := range row.Items {
		got = append(got, card.Work.ID)
	}
	if e != nil || !row.Exhausted || !slices.Equal(got, want) || len(cost.LookupWindows) > 34 {
		t.Fatal("exact shelf cap", row, e)
	}
}

func TestRBHomePublicationAndLIVE(t *testing.T) {
	for _, mode := range []string{"revoke", "secondary-revision", "secondary-viewer", "secondary-policy"} {
		t.Run(mode, func(t *testing.T) {
			f := newRBHomeFixture(t)
			f.exec(t, `UPDATE direct_memberships SET allowed_libraries='["movies","other"]' WHERE account_id='a'`)
			f.reader.snapshots = &browseBoundarySnapshots{Snapshots: f.snapshots, afterBusiness: func() {
				switch mode {
				case "revoke":
					f.exec(t, `UPDATE authorization_session_families SET revoked=1`)
				case "secondary-revision":
					f.exec(t, `UPDATE libraries SET data_revision=data_revision+1 WHERE id='other'`)
					f.exec(t, `UPDATE catalog_entities SET title='fresh secondary',revision=revision+1 WHERE id=1040`)
				case "secondary-viewer":
					f.exec(t, `INSERT INTO viewer_revisions(profile_id,library_id,revision) VALUES('p','other',2)`)
					f.exec(t, `UPDATE catalog_entities SET title='fresh secondary',revision=revision+1 WHERE id=1040`)
				case "secondary-policy":
					f.exec(t, `UPDATE direct_profiles SET allowed_libraries='["movies"]' WHERE id='p'`)
				}
			}}
			row, cost, e := f.reader.homeRecentRow(t.Context(), homeRequest(), time.Second, f.output, HomeRecentInput{View: f.input, Sources: []HomeSource{{1, 1}, {2, 1}}})
			if mode == "secondary-revision" || mode == "secondary-viewer" {
				if e != nil || cost.Attempts != 2 {
					t.Fatal("whole retry", row, cost, e)
				}
				fresh := false
				for _, card := range row.Items {
					if card.Work.ID == 1040 {
						fresh = card.Work.Title == "fresh secondary" && card.Work.Revision == 2
					}
				}
				if !fresh {
					t.Fatal("earlier private target escaped retry", row, cost, e)
				}
			} else if e == nil || !reflect.DeepEqual(row, HomeRecentRow{}) {
				t.Fatal("publication leak", row, e)
			}
		})
	}
	f := composeReadFixture(t, newRBHomeSource(t), RBHomeDefinitions(), readfixture.LiveConfig(time.Now), false, rbHomeFixtureHash(t))
	q, request, output := liveRequests("home")
	in := HomeTargetInput{View: f.input, Work: 2000}
	in.View.Kind = 2
	if card, cost, e := f.reader.HomeRecentTarget(t.Context(), request, time.Second, BrowseOutput{}, in); !errors.Is(e, ErrInput) || card.Work.ID != 0 || cost.LookupStatements != 0 {
		t.Fatal("LIVE bypass", card, cost, e)
	}
	_, e := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		card, _, e := f.reader.HomeRecentTarget(ctx, request, time.Second, out, in)
		if e != nil {
			return e
		}
		if card.Target.ID != 2100 {
			t.Fatal(card)
		}
		assertLiveUsage(t, f, 1, 0)
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	assertLiveUsage(t, f, 0, 0)
}

func TestRBHomeShelfCapAndTieOrder(t *testing.T) {
	cards := make([]HomeRecentCard, 140)
	for n := range cards {
		cards[n].Work.ID = int64(n)
		cards[n].Work.PublicID[15] = byte(n)
		cards[n].Added = "2026-11-01T12:00:00.000000000Z"
	}
	var want []int64
	for n := range cards {
		want = append(want, int64(n))
	}
	wire := func(id int64) string {
		var b [16]byte
		b[15] = byte(id)
		return base64.RawURLEncoding.EncodeToString(b[:])
	}
	slices.SortFunc(want, func(a, b int64) int { return -strings.Compare(wire(a), wire(b)) })
	sortHomeCards(cards)
	cards = cards[:min(HomeRecentMaximum, len(cards))]
	var got []int64
	for _, card := range cards {
		got = append(got, card.Work.ID)
	}
	if !slices.Equal(got, want[:100]) {
		t.Fatal("cap/wire tie", got, want[:100])
	}
	// Base64url lexical order differs from BLOB order: 'A' sorts after '0'.
	var aID, bID [16]byte
	bID[0] = 208
	adversary := []HomeRecentCard{{Work: BrowseItem{ID: 2, PublicID: bID}}, {Work: BrowseItem{ID: 1, PublicID: aID}}}
	sortHomeCards(adversary)
	if adversary[0].Work.ID != 1 {
		t.Fatal("wire tie was replaced by raw BLOB ordering", adversary)
	}

}

func TestRBHomeCapturePopulations(t *testing.T) {
	f := newRBHomeSource(t)
	for _, d := range append(rbHomeDefinitions(), append(rbHomeActivityDefinitions(), rbHomeMemberDefinitions()...)...) {
		for _, c := range d.Cohorts {
			rows, e := f.conn.QueryContext(t.Context(), d.SQL, c.Bindings...)
			if e != nil {
				t.Fatal(d.ID, e)
			}
			present := rows.Next()
			e = rows.Err()
			rows.Close()
			if e != nil || present != (c.Name == "present") {
				t.Fatal(d.ID, c.Name, present, e)
			}
		}
	}
	for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if stats == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		captureBrowse(t, f, append(rbHomeDefinitions(), append(rbHomeActivityDefinitions(), rbHomeMemberDefinitions()...)...), stats, rbHomeFixtureHash(t))
	}
}
