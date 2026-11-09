package read

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

// BEGIN_RB_CONTAINER_METRICS_JOURNAL
func rbContainerMetricsPublic(id int64) []byte {
	public := make([]byte, 16)
	binary.LittleEndian.PutUint64(public, uint64(id))
	return public
}

func newRBContainerMetricsSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newBrowseSource(t)
	for _, q := range []string{
		`INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(3,'season',0,1,1,1,0),(4,'episode',1,0,1,1,0),(5,'artist',0,1,1,1,1),(6,'album',0,1,1,1,1),(7,'song',1,0,1,1,1),(8,'book',0,1,1,1,0),(9,'audiobook_file',1,0,1,1,1),(10,'collection',0,1,1,1,0),(20,'ebook',0,0,1,1,0),(21,'comic',0,0,1,1,0)`,
	} {
		f.exec(t, q)
	}
	subject := func(id, kind, library int64) []byte {
		public := rbContainerMetricsPublic(id)
		f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,?,?)`, id, public, library, kind)
		return public
	}
	entity := func(id, kind, library int64, title string) {
		public := subject(id, kind, library)
		f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,admission_own_age,duration_key,community_rating_key,critic_rating_key,file_size_key,resolution_key,latest_aired_key)
VALUES(?,?,?,?,?,?,?,?,?,0,?,?,0,0,0,0,0,NULL,'')`, id, public, library, kind, title, title, title, title, title, public, public)
	}
	// A container's own metrics are never inherited by its aggregate.
	f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(2001,?,1,2)`, rbContainerMetricsPublic(2001))
	f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,admission_own_age,duration_key,community_rating_key,critic_rating_key,file_size_key,resolution_key,latest_aired_key)
VALUES(2001,?,1,2,'Show','show','s','Show','S',0,?,?,99,9999.0,9.9,9.9,9999,4000,'2099-01-01T00:00:00Z')`, rbContainerMetricsPublic(2001), rbContainerMetricsPublic(2001), rbContainerMetricsPublic(2001))
	seasonPublic := subject(2002, 3, 1)
	f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,show_id,season_number) VALUES(2002,?,1,3,'Season','season','s','Season','S',0,?,?,2001,1)`, seasonPublic, seasonPublic, seasonPublic)
	artistPublic := subject(1999, 5, 1)
	f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,artist_id) VALUES(1999,?,1,5,'Artist','artist','a','Artist','A',0,?,?,NULL)`, artistPublic, artistPublic, artistPublic)
	albumPublic := subject(2003, 6, 1)
	f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,artist_id) VALUES(2003,?,1,6,'Album','album','a','Album','A',0,?,?,1999)`, albumPublic, albumPublic, albumPublic)
	entity(2004, 8, 1, "Book")
	entity(2005, 10, 1, "Collection")
	entity(2006, 10, 1, "Nested collection")
	entity(2007, 2, 1, "Empty show")
	f.exec(t, `UPDATE catalog_entities SET duration_key=9999,community_rating_key=9.9,file_size_key=9999 WHERE id=2007`)
	entity(2100, 2, 1, "Large show")
	// Show 2001's own episodes; 2100 is the separate Large show below.
	for n := int64(0); n < 5; n++ {
		id := int64(2200) + n
		public := rbContainerMetricsPublic(id)
		retired := int64(0)
		if id == 2204 {
			retired = 1
		}
		resolution := []int64{720, 1080, 2160, 480, 4000}[n]
		f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,1,4)`, id, public)
		f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,retired,admission_own_age,duration_key,community_rating_key,critic_rating_key,file_size_key,resolution_key,latest_aired_key,show_id,parent_id,numbering,episode_number)
VALUES(?,?,1,4,?,?,?,?,?,0,?,?,?,1,?,?,?,?,?,?,2001,2002,'seasonal',?)`, id, public, fmt.Sprintf("Episode %d", id), fmt.Sprintf("episode %d", id), "e", fmt.Sprintf("Episode %d", id), "E", public, public, retired, float64(100*(n+1)), float64(n+1), float64(n+1), int64(1000*(n+1)), resolution, fmt.Sprintf("2026-01-%02dT00:00:00Z", n+1), n+1)
		f.exec(t, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,rating,play_count,last_played_at) VALUES('p',?,?,1,?,?,?)`, public, id, int64(2*(n+1)), int64(n+1), fmt.Sprintf("2026-02-%02dT00:00:00Z", n+1))
	}
	// The firehose container for multi-quantum and sparse continuation tests.
	for n := int64(0); n < 600; n++ {
		id := int64(3000) + n
		public := rbContainerMetricsPublic(id)
		f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,1,4)`, id, public)
		f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,duration_key,file_size_key,latest_aired_key,show_id,numbering,episode_number)
VALUES(?,?,1,4,?,?,?,?,?,0,?,?,?,?,?,2100,'absolute',?)`, id, public, fmt.Sprintf("Bulk %d", id), fmt.Sprintf("bulk %d", id), "b", fmt.Sprintf("Bulk %d", id), "B", public, public, float64(n+1), 1000+n, fmt.Sprintf("2026-04-%02dT00:00:00Z", n%28+1), n+1)
	}
	for n := int64(0); n < 3; n++ {
		id := int64(2300) + n
		public := rbContainerMetricsPublic(id)
		f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,1,7)`, id, public)
		f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,duration_key,album_id,disc_number,track_number) VALUES(?,?,1,7,?,?,?,?,?,0,?,?,?,2003,1,?)`, id, public, fmt.Sprintf("Track %d", id), fmt.Sprintf("track %d", id), "t", fmt.Sprintf("Track %d", id), "T", public, public, float64(60*(n+1)), n+1)
	}
	for n := int64(0); n < 2; n++ {
		id := int64(2400) + n
		public := rbContainerMetricsPublic(id)
		f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,1,9)`, id, public)
		f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,duration_key,book_id,disc_number,part_number) VALUES(?,?,1,9,?,?,?,?,?,0,?,?,?,2004,1,?)`, id, public, fmt.Sprintf("Part %d", id), fmt.Sprintf("part %d", id), "p", fmt.Sprintf("Part %d", id), "P", public, public, float64(300*(n+1)), n+1)
	}
	f.exec(t, `UPDATE catalog_entities SET duration_key=40,file_size_key=400 WHERE id=1`)
	f.exec(t, `UPDATE catalog_entities SET duration_key=30,file_size_key=300 WHERE id=2`)
	f.exec(t, `UPDATE catalog_entities SET duration_key=50,file_size_key=500 WHERE id=3`)
	// Authored order differs from entity id, so the page keyset is exercised.
	f.exec(t, `INSERT INTO catalog_collection_members(collection_id,item_id,order_key) VALUES(2005,3,'c'),(2005,1,'a'),(2005,2,'b')`)
	f.exec(t, `INSERT INTO catalog_collection_members(collection_id,item_id,order_key) VALUES(2006,2001,'a')`)
	return f
}

// END_RB_CONTAINER_METRICS_JOURNAL

func rbContainerMetricsFixtureHash(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("rb_container_metrics_test.go")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(data), "// BEGIN_RB_CONTAINER_METRICS_JOURNAL\n")
	end := strings.Index(string(data), "// END_RB_CONTAINER_METRICS_JOURNAL\n")
	if start < 0 || end <= start {
		t.Fatal("fixture journal")
	}
	return fmt.Sprintf("%x", sha256.Sum256(append([]byte(browsePlanFixtureHash(t)), data[start:end]...)))
}

func rbContainerMetricsConfig() governor.Config {
	cfg := readfixture.LiveConfig(time.Now)
	cfg.Foreground.AssignedBytes += 20 << 20
	cfg.Foreground.EffectiveBytes += 20 << 20
	cfg.Foreground.NonBrowseBytes += 16 << 20
	protected := cfg.Partitions[governor.Foreground][governor.MemoryBytes]
	cfg.Partitions[governor.Foreground][governor.MemoryBytes] += 80 << 20
	cfg.Partitions[governor.Foreground][governor.WALBytes] += 1 << 20
	cfg.Partitions[governor.Background] = governor.Amounts{governor.MemoryBytes: 4096, governor.WALBytes: 4096}
	cfg.Preparation = &governor.PreparationProfile{MaxActive: 4, MaxBytes: 16 << 20, MaxJobBytes: 16 << 20, ProtectedBytes: protected}
	return cfg
}

func newRBContainerMetricsFixture(t *testing.T) (*browseFixture, *RBContainerMetricsReader) {
	t.Helper()
	f := composeReadFixture(t, newRBContainerMetricsSource(t), RBContainerMetricsDefinitions(), rbContainerMetricsConfig(), false, rbContainerMetricsFixtureHash(t))
	r, err := NewRBContainerMetricsReader(f.snapshots, f.authority)
	if err != nil {
		t.Fatal(err)
	}
	return f, r
}

func rbContainerMetricsRequest() governor.Request {
	return governor.Request{Key: governor.JobKey{Domain: 398, ID: "rb-container-metrics-quantum"}, Owner: 1, Class: governor.Foreground, Need: governor.Amounts{governor.Reader: 1, governor.MemoryBytes: governor.ForegroundReadBytes}, Budget: governor.UnitBudget{RawRows: 2048, VMOps: 100000, Pages: 256}, Deadline: time.Now().Add(time.Second)}
}

func rbContainerMetricsRun(t *testing.T, job *RBContainerMetricsPreparation) RBContainerMetricsAdvance {
	t.Helper()
	var last RBContainerMetricsAdvance
	for n := 0; n < 64; n++ {
		advance, err := job.Advance(t.Context(), rbContainerMetricsRequest(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		last.Restarts += advance.Restarts
		last.RawRows += advance.RawRows
		last.Statements += advance.Statements
		if advance.Ready {
			last.Ready = true
			return last
		}
	}
	t.Fatal("container metrics preparation did not finish")
	return last
}

func rbContainerMetricsDeliver(t *testing.T, f *browseFixture, job *RBContainerMetricsPreparation, view BrowseInput) (RBContainerMetricsResult, error) {
	t.Helper()
	live, request, output := liveRequests("rb-container-metrics-page")
	var page RBContainerMetricsResult
	_, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, live, output, func(ctx context.Context, out BrowseOutput) error {
		var e error
		page, _, e = job.Metrics(ctx, request, time.Second, out, view)
		return e
	})
	if u := f.authority.ForegroundUsage(); u.Live != 0 || u.Waiters != 0 {
		t.Fatal("container metrics retained LIVE custody", u)
	}
	return page, err
}

func rbContainerMetricsOracle(t *testing.T, f *browseFixture, container int64, source string) (members int64, duration, community, critic float64, fileSize, resolution int64, latest string, rating float64, played string, plays int64) {
	t.Helper()
	// The aggregate counts only leaf children; a hierarchy walk also passes
	// intermediate containers, which are never folded.
	where := "e.show_id=" + fmt.Sprint(container) + " AND e.kind=4"
	switch source {
	case "season":
		where = "e.parent_id=" + fmt.Sprint(container) + " AND e.kind=4"
	case "album":
		where = "e.album_id=" + fmt.Sprint(container) + " AND e.kind=7"
	case "book":
		where = "e.book_id=" + fmt.Sprint(container) + " AND e.kind=9"
	}
	row := f.conn.QueryRowContext(t.Context(), `SELECT count(e.id),COALESCE(max(e.duration_key),0),COALESCE(max(e.community_rating_key),0),COALESCE(max(e.critic_rating_key),0),COALESCE(max(e.file_size_key),0),COALESCE(max(e.resolution_key),0),COALESCE(max(e.latest_aired_key),''),COALESCE(max(p.rating),0),COALESCE(max(p.last_played_at),''),COALESCE(sum(p.play_count),0)
FROM catalog_entities AS e LEFT JOIN personal_items AS p ON p.profile_id='p' AND p.item_id=e.id AND p.item_public_id=e.public_id
WHERE `+where+` AND e.library_id=1 AND e.retired=0`)
	if err := row.Scan(&members, &duration, &community, &critic, &fileSize, &resolution, &latest, &rating, &played, &plays); err != nil {
		t.Fatal(err)
	}
	return
}

func TestRBContainerMetricsShowAggregates(t *testing.T) {
	f, r := newRBContainerMetricsFixture(t)
	view := f.input
	view.Kind = 2
	job, err := r.BeginContainerMetrics(t.Context(), 1, 1<<20, RBContainerMetricsInput{View: view, Subject: 2001, Metrics: rbContainerMetricsAll &^ RBContainerFileSize})
	if err != nil {
		t.Fatal(err)
	}
	defer job.Close()
	done := rbContainerMetricsRun(t, job)
	if !done.Ready || done.RawRows == 0 {
		t.Fatalf("show aggregate did not complete: %+v", done)
	}
	page, err := rbContainerMetricsDeliver(t, f, job, view)
	if err != nil || !page.Complete {
		t.Fatalf("deliver: %v %+v", err, page)
	}
	members, duration, community, critic, _, resolution, latest, rating, played, plays := rbContainerMetricsOracle(t, f, 2001, "show")
	got := page.Metrics
	if page.Present != rbContainerMetricsAll&^RBContainerFileSize {
		t.Fatalf("presence mask: %#x", page.Present)
	}
	if got.Members != members || got.Duration != duration || got.CommunityRating != community || got.CriticRating != critic || got.Resolution != resolution || got.LatestAired != latest || got.PersonalRating != rating || got.LastPlayed != played || got.PlayCount != plays || got.EpisodeCount != members {
		t.Fatalf("aggregate differs from the independent oracle: %+v want %d %v %v %v %d %q %v %q %d", got, members, duration, community, critic, resolution, latest, rating, played, plays)
	}
	if got.FileSize != 0 {
		t.Fatal("unrequested owner-only file size escaped")
	}
}

func TestRBContainerMetricsHiddenChildExcluded(t *testing.T) {
	f, r := newRBContainerMetricsFixture(t)
	view := f.input
	view.Kind = 2
	job, err := r.BeginContainerMetrics(t.Context(), 1, 1<<20, RBContainerMetricsInput{View: view, Subject: 2001, Metrics: RBContainerDuration | RBContainerLatestAired | RBContainerPersonalRating | RBContainerPlayCount})
	if err != nil {
		t.Fatal(err)
	}
	defer job.Close()
	rbContainerMetricsRun(t, job)
	page, err := rbContainerMetricsDeliver(t, f, job, view)
	if err != nil {
		t.Fatal(err)
	}
	// Episode 2204 is retired and carries the newest, largest and most-rated
	// values; none of them may leak into the permitted aggregate.
	if page.Metrics.Members != 4 || page.Metrics.Duration != 400 || page.Metrics.LatestAired != "2026-01-04T00:00:00Z" || page.Metrics.PersonalRating != 8 || page.Metrics.PlayCount != 10 {
		t.Fatalf("hidden child influenced the aggregate: %+v", page.Metrics)
	}
}

func TestRBContainerMetricsOwnerOnlyFileSize(t *testing.T) {
	f, r := newRBContainerMetricsFixture(t)
	view := f.input
	view.Kind = 2
	metrics := RBContainerFileSize | RBContainerDuration
	job, err := r.BeginContainerMetrics(t.Context(), 1, 1<<20, RBContainerMetricsInput{View: view, Subject: 2001, Metrics: metrics})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := job.Advance(t.Context(), rbContainerMetricsRequest(), time.Second); !errors.Is(err, ErrDenied) {
		t.Fatalf("member read owner-only file size: %v", err)
	}
	if !job.entry.closed {
		t.Fatal("denied file size kept its lease")
	}
	f.exec(t, `UPDATE direct_memberships SET role='owner' WHERE account_id='a'`)
	job, err = r.BeginContainerMetrics(t.Context(), 1, 1<<20, RBContainerMetricsInput{View: view, Subject: 2001, Metrics: metrics})
	if err != nil {
		t.Fatal(err)
	}
	defer job.Close()
	rbContainerMetricsRun(t, job)
	page, err := rbContainerMetricsDeliver(t, f, job, view)
	if err != nil {
		t.Fatal(err)
	}
	// 2204 is retired, so the owner sees the largest permitted child (4000).
	if page.Metrics.FileSize != 4000 || page.Metrics.Members != 4 {
		t.Fatalf("owner file size: %+v", page.Metrics)
	}
}

func TestRBContainerMetricsRestrictedPolicyRefused(t *testing.T) {
	f, r := newRBContainerMetricsFixture(t)
	view := f.input
	view.Kind = 2
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('p',12,1)`)
	job, err := r.BeginContainerMetrics(t.Context(), 1, 1<<20, RBContainerMetricsInput{View: view, Subject: 2001, Metrics: RBContainerDuration})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := job.Advance(t.Context(), rbContainerMetricsRequest(), time.Second); !errors.Is(err, ErrUnqualified) || !errors.Is(err, errRBContainerMetricsUSPolicy) {
		t.Fatalf("restricted policy was not refused by name: %v", err)
	}
	if !job.entry.closed {
		t.Fatal("restricted policy kept its lease")
	}
	f.exec(t, `UPDATE profile_restrictions SET maximum_age=-1,allow_unrated=1,blocked_labels='["adult"]' WHERE profile_id='p'`)
	job, err = r.BeginContainerMetrics(t.Context(), 1, 1<<20, RBContainerMetricsInput{View: view, Subject: 2001, Metrics: RBContainerDuration})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := job.Advance(t.Context(), rbContainerMetricsRequest(), time.Second); !errors.Is(err, ErrUnqualified) {
		t.Fatalf("blocked labels admitted a child aggregate: %v", err)
	}
}

func TestRBContainerMetricsOwnAgeOnlyMutant(t *testing.T) {
	f, r := newRBContainerMetricsFixture(t)
	view := f.input
	view.Kind = 2
	job, err := r.BeginContainerMetrics(t.Context(), 1, 1<<20, RBContainerMetricsInput{View: view, Subject: 2007, Metrics: RBContainerDuration | RBContainerCommunityRating})
	if err != nil {
		t.Fatal(err)
	}
	defer job.Close()
	rbContainerMetricsRun(t, job)
	page, err := rbContainerMetricsDeliver(t, f, job, view)
	if err != nil {
		t.Fatal(err)
	}
	if page.Metrics.Members != 0 || page.Metrics.Duration != 0 || page.Metrics.CommunityRating != 0 {
		t.Fatalf("container's own metric/age became membership: %+v", page.Metrics)
	}
}

func TestRBContainerMetricsCollectionLeafOnly(t *testing.T) {
	f, r := newRBContainerMetricsFixture(t)
	view := f.input
	view.Kind = 10
	job, err := r.BeginContainerMetrics(t.Context(), 1, 1<<20, RBContainerMetricsInput{View: view, Subject: 2005, Metrics: RBContainerDuration})
	if err != nil {
		t.Fatal(err)
	}
	rbContainerMetricsRun(t, job)
	page, err := rbContainerMetricsDeliver(t, f, job, view)
	if err != nil {
		t.Fatal(err)
	}
	if page.Metrics.Members != 3 || page.Metrics.Duration != 50 {
		t.Fatalf("authored collection aggregate: %+v", page.Metrics)
	}
	if err := job.Close(); err != nil {
		t.Fatal(err)
	}
	// A nested container has no complete leaf proof here.
	nested, err := r.BeginContainerMetrics(t.Context(), 1, 1<<20, RBContainerMetricsInput{View: view, Subject: 2006, Metrics: RBContainerDuration})
	if err != nil {
		t.Fatal(err)
	}
	defer nested.Close()
	if _, err := nested.Advance(t.Context(), rbContainerMetricsRequest(), time.Second); !errors.Is(err, ErrUnqualified) {
		t.Fatalf("nested container member was flattened: %v", err)
	}
}

func TestRBContainerMetricsAutomaticCollectionRefused(t *testing.T) {
	f, r := newRBContainerMetricsFixture(t)
	view := f.input
	view.Kind = 10
	// One automatic row still joins to exactly one entity row, so a row-count
	// check alone would publish the three authored members as the full set.
	f.exec(t, `INSERT INTO catalog_automatic_collections(library_id,provider,provider_id,entity_id) VALUES(1,'provider','automatic',2005)`)
	job, err := r.BeginContainerMetrics(t.Context(), 1, 1<<20, RBContainerMetricsInput{View: view, Subject: 2005, Metrics: RBContainerDuration})
	if err != nil {
		t.Fatal(err)
	}
	advance, err := job.Advance(t.Context(), rbContainerMetricsRequest(), time.Second)
	if !errors.Is(err, ErrRBContainerMetricsAutomatic) || !errors.Is(err, ErrUnqualified) || advance.Ready {
		t.Fatalf("automatic collection was published as an authored-only exact set: %v %+v", err, advance)
	}
	if !job.entry.closed {
		t.Fatal("automatic collection kept its preparation lease")
	}
	// Two automatic rows for the same entity still refuse through the exact
	// point's row-count guard.
	f.exec(t, `INSERT INTO catalog_automatic_collections(library_id,provider,provider_id,entity_id) VALUES(1,'provider2','automatic2',2005)`)
	two, err := r.BeginContainerMetrics(t.Context(), 1, 1<<20, RBContainerMetricsInput{View: view, Subject: 2005, Metrics: RBContainerDuration})
	if err != nil {
		t.Fatal(err)
	}
	defer two.Close()
	if advance, err := two.Advance(t.Context(), rbContainerMetricsRequest(), time.Second); !errors.Is(err, ErrUnqualified) || advance.Ready {
		t.Fatalf("two automatic rows did not refuse: %v %+v", err, advance)
	}
}

func TestRBContainerMetricsRestartBudgetPerJob(t *testing.T) {
	f, r := newRBContainerMetricsFixture(t)
	view := f.input
	view.Kind = 2
	job, err := r.BeginContainerMetrics(t.Context(), 1, 1<<20, RBContainerMetricsInput{View: view, Subject: 2100, Metrics: RBContainerDuration})
	if err != nil {
		t.Fatal(err)
	}
	defer job.Close()
	// The six-hundred-child show cannot finish inside one bounded poll, so each
	// revision churn forces a content restart without completing the job.
	if advance, err := job.Advance(t.Context(), rbContainerMetricsRequest(), time.Second); err != nil || advance.Ready {
		t.Fatalf("first poll: %v %+v", err, advance)
	}
	for poll := 0; poll < 2; poll++ {
		f.exec(t, `UPDATE catalog_entities SET tv_hierarchy_revision=COALESCE(tv_hierarchy_revision,1)+1 WHERE id=2100`)
		advance, err := job.Advance(t.Context(), rbContainerMetricsRequest(), time.Second)
		if err != nil || advance.Ready || advance.Restarts != 1 {
			t.Fatalf("poll %d did not restart once: %v %+v", poll+1, err, advance)
		}
	}
	// The two job-wide restarts are spent; a third content change is refused
	// instead of being restarted again.
	f.exec(t, `UPDATE catalog_entities SET tv_hierarchy_revision=COALESCE(tv_hierarchy_revision,1)+1 WHERE id=2100`)
	advance, err := job.Advance(t.Context(), rbContainerMetricsRequest(), time.Second)
	if !errors.Is(err, ErrStale) || advance.Ready {
		t.Fatalf("third restart was not refused: %v %+v", err, advance)
	}
	if !job.entry.closed {
		t.Fatal("over-budget restart kept its preparation lease")
	}
}

func TestRBContainerMetricsDeletedLastMember(t *testing.T) {
	f, r := newRBContainerMetricsFixture(t)
	view := f.input
	view.Kind = 10
	f.exec(t, `DELETE FROM catalog_collection_members WHERE collection_id=2005`)
	job, err := r.BeginContainerMetrics(t.Context(), 1, 1<<20, RBContainerMetricsInput{View: view, Subject: 2005, Metrics: RBContainerDuration})
	if err != nil {
		t.Fatal(err)
	}
	defer job.Close()
	rbContainerMetricsRun(t, job)
	page, err := rbContainerMetricsDeliver(t, f, job, view)
	if err != nil {
		t.Fatal(err)
	}
	if page.Metrics.Members != 0 || page.Metrics.Duration != 0 {
		t.Fatalf("deleted last member left a stale aggregate: %+v", page.Metrics)
	}
}

func TestRBContainerMetricsReparentRestart(t *testing.T) {
	f, r := newRBContainerMetricsFixture(t)
	view := f.input
	view.Kind = 2
	job, err := r.BeginContainerMetrics(t.Context(), 1, 1<<20, RBContainerMetricsInput{View: view, Subject: 2100, Metrics: RBContainerDuration})
	if err != nil {
		t.Fatal(err)
	}
	defer job.Close()
	if _, err := job.Advance(t.Context(), rbContainerMetricsRequest(), time.Second); err != nil {
		t.Fatal(err)
	}
	original := job.entry.lease.Deadline()
	bytes := f.authority.Usage()[governor.Foreground][governor.MemoryBytes]
	// Reparent one bulk episode and move the show's hierarchy revision: the walk
	// must restart from the beginning, never append a mixed set.
	f.exec(t, `UPDATE catalog_entities SET show_id=2001 WHERE id=3000`)
	f.exec(t, `UPDATE catalog_entities SET tv_hierarchy_revision=COALESCE(tv_hierarchy_revision,1)+1 WHERE id=2100`)
	done := rbContainerMetricsRun(t, job)
	if !done.Ready || done.Restarts != 1 {
		t.Fatalf("content invalidation did not restart exactly once: %+v", done)
	}
	if !job.entry.lease.Deadline().Equal(original) || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != bytes {
		t.Fatal("restart renewed the deadline or re-admitted physical bytes")
	}
	page, err := rbContainerMetricsDeliver(t, f, job, view)
	if err != nil {
		t.Fatal(err)
	}
	members, _, _, _, _, _, _, _, _, _ := rbContainerMetricsOracle(t, f, 2100, "show")
	if page.Metrics.Members != members || members != 599 {
		t.Fatalf("restart published a mixed member set: %d want %d", page.Metrics.Members, members)
	}
}

func TestRBContainerMetricsSourceDisableDenied(t *testing.T) {
	f, r := newRBContainerMetricsFixture(t)
	view := f.input
	view.Kind = 2
	job, err := r.BeginContainerMetrics(t.Context(), 1, 1<<20, RBContainerMetricsInput{View: view, Subject: 2100, Metrics: RBContainerDuration})
	if err != nil {
		t.Fatal(err)
	}
	defer job.Close()
	if _, err := job.Advance(t.Context(), rbContainerMetricsRequest(), time.Second); err != nil {
		t.Fatal(err)
	}
	f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id=2100`)
	if _, err := job.Advance(t.Context(), rbContainerMetricsRequest(), time.Second); !errors.Is(err, ErrDenied) {
		t.Fatalf("retired container did not deny: %v", err)
	}
	if !job.entry.closed || f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
		t.Fatal("denied source retained its lease or bytes")
	}
}

func TestRBContainerMetricsDeliveryFence(t *testing.T) {
	f, r := newRBContainerMetricsFixture(t)
	view := f.input
	view.Kind = 2
	ready := func(t *testing.T) *RBContainerMetricsPreparation {
		t.Helper()
		job, err := r.BeginContainerMetrics(t.Context(), 1, 1<<20, RBContainerMetricsInput{View: view, Subject: 2001, Metrics: RBContainerDuration})
		if err != nil {
			t.Fatal(err)
		}
		rbContainerMetricsRun(t, job)
		return job
	}
	job := ready(t)
	f.exec(t, `UPDATE authorization_family_tokens SET expires_at='2026-11-05T00:00:00Z' WHERE token_hash='token'`)
	if _, err := rbContainerMetricsDeliver(t, f, job, view); !errors.Is(err, ErrDenied) {
		t.Fatalf("expired delivery fence: %v", err)
	}
	f.exec(t, `UPDATE authorization_family_tokens SET expires_at='2026-11-07T00:00:00Z' WHERE token_hash='token'`)
	job = ready(t)
	f.exec(t, `UPDATE catalog_entities SET tv_hierarchy_revision=COALESCE(tv_hierarchy_revision,1)+1 WHERE id=2001`)
	if _, err := rbContainerMetricsDeliver(t, f, job, view); !errors.Is(err, ErrStale) {
		t.Fatalf("container source drift published an old aggregate: %v", err)
	}
	_ = job.Close()
}

func TestRBContainerMetricsBudgetRefusal(t *testing.T) {
	f, r := newRBContainerMetricsFixture(t)
	view := f.input
	view.Kind = 2
	job, err := r.BeginContainerMetrics(t.Context(), 1, 1<<20, RBContainerMetricsInput{View: view, Subject: 2001, Metrics: RBContainerDuration})
	if err != nil {
		t.Fatal(err)
	}
	defer job.Close()
	request := rbContainerMetricsRequest()
	request.Need[governor.MemoryBytes] = r.pool.NativeRowReservation() + 512
	if _, err := job.Advance(t.Context(), request, time.Second); !errors.Is(err, governor.ErrSnapshotBudget) {
		t.Fatalf("small admitted copy budget did not refuse: %v", err)
	}
	if job.complete {
		t.Fatal("refused window published a ready aggregate")
	}
}

func TestRBContainerMetricsDisposal(t *testing.T) {
	f, r := newRBContainerMetricsFixture(t)
	view := f.input
	view.Kind = 3
	job, err := r.BeginContainerMetrics(t.Context(), 1, 1<<20, RBContainerMetricsInput{View: view, Subject: 2002, Metrics: RBContainerDuration})
	if err != nil {
		t.Fatal(err)
	}
	rbContainerMetricsRun(t, job)
	page, err := rbContainerMetricsDeliver(t, f, job, view)
	if err != nil || page.Metrics.Members == 0 {
		t.Fatalf("season aggregate: %v %+v", err, page.Metrics)
	}
	if err := job.Close(); err != nil {
		t.Fatal(err)
	}
	if f.authority.Usage()[governor.Foreground][governor.MemoryBytes] != 0 {
		t.Fatal("disposal retained physical bytes")
	}
	if _, err := job.Advance(t.Context(), rbContainerMetricsRequest(), time.Second); !errors.Is(err, ErrInput) {
		t.Fatalf("closed preparation advanced: %v", err)
	}
}
