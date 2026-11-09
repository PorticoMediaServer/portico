package read

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

// BEGIN_RB_DIRECTORY_JOURNAL
func newRBDirectorySource(t *testing.T) *browseFixture {
	t.Helper()
	f := newBrowseSource(t)
	for _, q := range []string{
		`UPDATE direct_memberships SET allowed_libraries='["movies","other","books","reading"]' WHERE account_id='a'`,
		`UPDATE direct_memberships SET allowed_libraries='["other"]' WHERE account_id='b'`,
		`INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES('e','b','ib','Bob','test','test','1','2026-11-05T00:00:00Z','2026-11-05T00:00:00Z')`,
		`INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,device_id,command_member_incarnation,command_profile_lifetime,command_incarnation,security_kind) SELECT 'fb','s','b','q','local','member',1,'2026-11-07T00:00:00Z',1,'e',m.incarnation,p.lifetime,x'00800000000000000000000000000002',0 FROM direct_memberships AS m JOIN direct_profiles AS p ON p.account_id=m.account_id WHERE m.account_id='b' AND p.id='q'`,
		`INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at) VALUES('tokenb','fb',1,'2026-11-07T00:00:00Z')`,
		`INSERT INTO libraries(id,name,kind,root,position) VALUES('gone','Gone','tv','/gone',2),('secret','Secret','tv','/secret',3),('books','Books','audiobook','/books',4),('reading','Reading','book','/reading',5)`,
		`INSERT INTO catalog_libraries(id,library_id,root,retired) VALUES(3,'gone','/gone',1),(4,'secret','/secret',0),(5,'books','/books',0),(6,'reading','/reading',0)`,
	} {
		f.exec(t, q)
	}
	// Two authorized libraries deliberately share the same root ("/media" above)
	// and hold equal folded title keys, so a correct page cannot deduplicate by
	// root or collapse cross-library ties.
	type show struct {
		id, library int64
		key, keep   string
		added       any
		content     any
		year        int64
		duration    float64
		retired     int64
	}
	shows := []show{
		{2001, 1, "Alpha", "The Alpha", "2026-01-02T00:00:00.000000000Z", "2026-03-01T00:00:00.000000000Z", 2001, 30.5, 0},
		{2002, 1, "alpha", "alpha", "2026-01-01T00:00:00.000000000Z", "2026-02-01T00:00:00.000000000Z", 2002, 12.0, 0},
		{2003, 2, "Alpha", "Alpha", "2026-01-01T00:00:00.000000000Z", "2026-02-01T00:00:00.000000000Z", 2002, 20.0, 0},
		{2004, 2, "Beta", "Beta", nil, nil, 1999, 11.5, 0},
		{2005, 1, "beta", "beta", nil, "2026-01-01T00:00:00.000000000Z", 1999, 9.0, 0},
		{2006, 2, "Gamma", "Gamma", "2026-01-01T00:00:00.000000000Z", "2026-02-01T00:00:00.000000000Z", 2005, 99.0, 1},
		{2007, 1, "delta", "The Delta", "2026-01-03T00:00:00.000000000Z", "2026-04-01T00:00:00.000000000Z", 2010, 44.0, 0},
		{2008, 2, "delta", "the delta", "2026-01-04T00:00:00.000000000Z", "2026-05-01T00:00:00.000000000Z", 2011, 45.0, 0},
		// Library 3 is a retired mapping and library 4 is not shared with the
		// viewer at all; neither may surface from the directory or a merge.
		{3001, 3, "Hidden", "Hidden", "2026-01-01T00:00:00.000000000Z", "2026-01-01T00:00:00.000000000Z", 1990, 5.0, 0},
		{3002, 4, "Denied", "Denied", "2026-01-01T00:00:00.000000000Z", "2026-01-01T00:00:00.000000000Z", 1991, 6.0, 0},
	}
	for _, s := range shows {
		public := make([]byte, 16)
		binary.LittleEndian.PutUint64(public, uint64(s.id))
		f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,?,2)`, s.id, public, s.library)
		f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,year,added_at,last_content_added_at,retired,duration_key,admission_own_age,metadata_incarnation,artwork_incarnation) VALUES(?,?,?,2,?,?,?,?,?,?,?,?,?,?,?,NULL,?,?)`,
			s.id, public, s.library, fmt.Sprintf("Show %d", s.id), s.key, s.key, s.keep, s.keep, s.id, s.year, s.added, s.content, s.retired, s.duration, public, public)
	}
	return f
}

// END_RB_DIRECTORY_JOURNAL

func rbDirectoryFixtureHash(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("rb_directory_test.go")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(data), "// BEGIN_RB_DIRECTORY_JOURNAL\n")
	end := strings.Index(string(data), "// END_RB_DIRECTORY_JOURNAL\n")
	if start < 0 || end <= start {
		t.Fatal("fixture journal")
	}
	return fmt.Sprintf("%x", sha256.Sum256(append([]byte(browsePlanFixtureHash(t)), data[start:end]...)))
}

func rbDirectoryConfig() governor.Config {
	return readfixture.LiveConfig(time.Now)
}

func newRBDirectoryFixture(t *testing.T) *browseFixture {
	t.Helper()
	return composeReadFixture(t, newRBDirectorySource(t), RBDirectoryDefinitions(), rbDirectoryConfig(), true, rbDirectoryFixtureHash(t))
}

func (f *browseFixture) directory(t *testing.T, in RBDirectoryInput) (RBDirectoryPage, BrowseReadResult, error) {
	t.Helper()
	request := governor.Request{Key: governor.JobKey{Domain: 95, ID: "directory"}, Owner: 1, Class: governor.Foreground, Need: governor.Amounts{governor.Reader: 1, governor.MemoryBytes: governor.ForegroundReadBytes}, Budget: governor.UnitBudget{RawRows: 2048, VMOps: 100000, Pages: 256}, Deadline: time.Now().Add(time.Second)}
	return f.reader.directoryWindow(t.Context(), request, time.Second, f.output, in)
}

func rbDirectoryIDs(page RBDirectoryPage) []string {
	out := []string{}
	for _, lib := range page.Libraries {
		out = append(out, lib.ID)
	}
	return out
}

func TestRBDirectoryAuthorizedLibrariesAndPivots(t *testing.T) {
	f := newRBDirectoryFixture(t)
	in := RBDirectoryInput{View: f.input}
	page, cost, err := f.directory(t, in)
	if err != nil {
		t.Fatal(err)
	}
	if got := rbDirectoryIDs(page); !slices.Equal(got, []string{"movies", "other", "books", "reading"}) {
		t.Fatalf("authorized libraries: %v", got)
	}
	if page.Pending || !page.Exhausted || page.Next != nil || len(page.CrossPivots) != 3 {
		t.Fatalf("directory envelope: %+v", page)
	}
	// The mapped scope also holds a retired and an unshared mapping; the raw
	// evidence counts the four authorized libraries only.
	if page.RawRows != 4 || page.RawPositions != 4 {
		t.Fatalf("authorized raw evidence: rows=%d positions=%d", page.RawRows, page.RawPositions)
	}
	if cost.LookupStatements == 0 || page.Statements != cost.LookupStatements+cost.PublicationStatements {
		t.Fatal("statement accounting", page.Statements, cost)
	}
	movie := page.Libraries[0]
	if movie.Kind != "movie" || len(movie.Pivots) == 0 {
		t.Fatal("movie pivots", movie)
	}
	states := map[string]RBDirectoryPivot{}
	for _, pivot := range movie.Pivots {
		states[pivot.ID] = pivot
	}
	if states["movies"].State != RBDirectorySupported || len(states["movies"].Sorts) == 0 {
		t.Fatal("movies pivot is not supported", states["movies"])
	}
	if states["collections"].State != RBDirectoryPending || states["collections"].Source != RBGapGroupLift {
		t.Fatal("collections pivot does not name its gap", states["collections"])
	}
	if states["categories"].Source != RBGapGroupPrepare {
		t.Fatal("categories pivot does not name its gap", states["categories"])
	}
	books := page.Libraries[2]
	audiobook := map[string]RBDirectoryPivot{}
	for _, pivot := range books.Pivots {
		audiobook[pivot.ID] = pivot
	}
	if books.Kind != "audiobook" || audiobook["authors"].State != RBDirectorySupported || audiobook["books"].State != RBDirectorySupported {
		t.Fatal("audiobook pivots are not supported", books)
	}
	reading := page.Libraries[3]
	if reading.Kind != "book" || len(reading.Pivots) == 0 || reading.Pivots[0].State != RBDirectoryPending || reading.Pivots[0].Source != RBGapReading {
		t.Fatal("reading pivots are not explicit pending", reading)
	}
	// A different principal sees only its own share, never the first viewer's
	// overlapping-root libraries.
	other := in
	other.View.TokenHash = "tokenb"
	page, _, err = f.directory(t, other)
	if err != nil {
		t.Fatal(err)
	}
	if got := rbDirectoryIDs(page); !slices.Equal(got, []string{"other"}) {
		t.Fatalf("other principal saw %v", got)
	}
	if page.RawRows != 1 || page.RawPositions != 1 {
		t.Fatalf("other principal raw evidence: rows=%d positions=%d", page.RawRows, page.RawPositions)
	}
}

// TestRBDirectoryCapacityBoundary holds the real 192-row complete-scope ceiling:
// exactly the documented capacity is a complete list, one more mapped row is
// explicit pending preparation rather than a truncated page.
func TestRBDirectoryCapacityBoundary(t *testing.T) {
	f := newRBDirectoryFixture(t)
	bulk := func(t *testing.T, first, last int64) {
		t.Helper()
		f.exec(t, `INSERT INTO libraries(id,name,kind,root,position) SELECT 'bulk'||x,'Bulk '||x,'movie','/bulk/'||x,x FROM (WITH RECURSIVE c(x) AS (SELECT ? UNION ALL SELECT x+1 FROM c WHERE x<?) SELECT x FROM c)`, first, last)
		f.exec(t, `INSERT INTO catalog_libraries(id,library_id,root) SELECT x,'bulk'||x,'/bulk/'||x FROM (WITH RECURSIVE c(x) AS (SELECT ? UNION ALL SELECT x+1 FROM c WHERE x<?) SELECT x FROM c)`, first, last)
	}
	if rbDirectoryCapacity != 192 {
		t.Fatalf("capacity contract moved: %d", rbDirectoryCapacity)
	}
	// Existing non-retired mappings are ids 1,2,4,5,6 (five rows), so ids
	// 7..193 reach the 192 ceiling and one more crosses it.
	bulk(t, 7, 6+rbDirectoryCapacity-5)
	page, _, err := f.directory(t, RBDirectoryInput{View: f.input})
	if err != nil {
		t.Fatal(err)
	}
	if page.Pending || !page.Exhausted || len(page.Libraries) != 4 {
		t.Fatalf("capacity page: %+v", page)
	}
	bulk(t, 194, 194)
	page, _, err = f.directory(t, RBDirectoryInput{View: f.input})
	if err != nil {
		t.Fatal(err)
	}
	if !page.Pending || page.PendingSource != RBGapDirectoryScope || page.Exhausted || len(page.Libraries) != 0 {
		t.Fatalf("over-capacity page: %+v", page)
	}
}

func TestRBDirectoryNoLibraryViewer(t *testing.T) {
	f := newRBDirectoryFixture(t)
	f.exec(t, `UPDATE direct_memberships SET allowed_libraries='[]' WHERE account_id='a'`)
	page, _, err := f.directory(t, RBDirectoryInput{View: f.input})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Libraries) != 0 || !page.Exhausted || page.Pending || page.Next != nil {
		t.Fatalf("no-library viewer: %+v", page)
	}
	// Raw evidence counts only authorized mapping rows, never the wider mapped
	// scope used to prove the share.
	if page.RawRows != 0 || page.RawPositions != 0 {
		t.Fatalf("no-library viewer raw evidence: rows=%d positions=%d", page.RawRows, page.RawPositions)
	}
}

func TestRBDirectoryPaginationAndScopeDrift(t *testing.T) {
	f := newRBDirectoryFixture(t)
	in := RBDirectoryInput{View: f.input}
	in.View.Limit = 1
	var got []string
	for n := 0; n < 4; n++ {
		page, _, err := f.directory(t, in)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, rbDirectoryIDs(page)...)
		if page.Exhausted {
			if page.Next != nil {
				t.Fatal("exhausted cursor")
			}
			break
		}
		if page.Next == nil {
			t.Fatal("lost continuation")
		}
		in.After = page.Next
	}
	if !slices.Equal(got, []string{"movies", "other", "books", "reading"}) {
		t.Fatalf("paged libraries: %v", got)
	}
	// A new authorized mapping is a scope change, not a page continuation.
	firstIn := RBDirectoryInput{View: f.input}
	firstIn.View.Limit = 1
	page, _, err := f.directory(t, firstIn)
	if err != nil || page.Next == nil {
		t.Fatal(page, err)
	}
	changed := RBDirectoryInput{View: f.input, After: page.Next}
	f.exec(t, `UPDATE catalog_libraries SET retired=1 WHERE id=2`)
	if gotPage, _, err := f.directory(t, changed); !errors.Is(err, ErrStale) || len(gotPage.Libraries) != 0 {
		t.Fatal("retired library scope drift", gotPage, err)
	}
	// The same cursor is not another principal's continuation.
	f2 := newRBDirectoryFixture(t)
	secondIn := RBDirectoryInput{View: f2.input}
	secondIn.View.Limit = 1
	first, _, err := f2.directory(t, secondIn)
	if err != nil || first.Next == nil {
		t.Fatal(first, err)
	}
	foreign := RBDirectoryInput{View: f2.input, After: first.Next}
	foreign.View.TokenHash = "tokenb"
	if gotPage, _, err := f2.directory(t, foreign); !errors.Is(err, ErrStale) || len(gotPage.Libraries) != 0 {
		t.Fatal("cursor crossed principals", gotPage, err)
	}
}

func TestRBDirectoryRefusesAfterCancellation(t *testing.T) {
	f := newRBDirectoryFixture(t)
	request := governor.Request{Key: governor.JobKey{Domain: 95, ID: "directory"}, Owner: 1, Class: governor.Foreground, Need: governor.Amounts{governor.Reader: 1, governor.MemoryBytes: governor.ForegroundReadBytes}, Budget: governor.UnitBudget{RawRows: 2048, VMOps: 100000, Pages: 256}, Deadline: time.Now().Add(time.Second)}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	page, _, err := f.reader.directoryWindow(ctx, request, time.Second, f.output, RBDirectoryInput{View: f.input})
	if err == nil || len(page.Libraries) != 0 || page.Statements != 0 {
		t.Fatal("cancelled directory returned bytes", page, err)
	}
}

func TestRBDirectoryPublicLIVE(t *testing.T) {
	f := composeReadFixture(t, newRBDirectorySource(t), RBDirectoryDefinitions(), rbDirectoryConfig(), false, rbDirectoryFixtureHash(t))
	live, request, output := liveRequests("directory")
	_, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, live, output, func(ctx context.Context, out BrowseOutput) error {
		page, _, err := f.reader.Directory(ctx, request, time.Second, out, RBDirectoryInput{View: f.input})
		if err != nil {
			return err
		}
		if len(page.Libraries) == 0 {
			t.Fatal(page)
		}
		assertLiveUsage(t, f, 1, 0)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	assertLiveUsage(t, f, 0, 0)
}

func TestRBAcrossCapabilitiesDeclarePendingSources(t *testing.T) {
	supported := map[RBAcrossSort]bool{}
	pending := map[RBAcrossSort]RBSourceGap{}
	for _, capability := range RBAcrossCapabilities() {
		switch capability.State {
		case RBDirectorySupported:
			supported[capability.Sort] = true
		case RBDirectoryPending:
			if capability.Source == "" {
				t.Fatal("pending capability without a named source", capability)
			}
			pending[capability.Sort] = capability.Source
		default:
			t.Fatal("unknown capability state", capability)
		}
	}
	for _, want := range []RBAcrossSort{RBAcrossTitle, RBAcrossAdded, RBAcrossContentAdded, RBAcrossSort(SortYear), RBAcrossSort(SortFileSize)} {
		if !supported[want] {
			t.Fatalf("supported sort missing: %s", want)
		}
	}
	for _, want := range []RBAcrossSort{RBAcrossSort("random"), RBAcrossSort("forYou"), RBAcrossSort("groupOrder")} {
		if pending[want] == "" {
			t.Fatalf("pending sort lacks a source: %s", want)
		}
	}
	if _, err := rbAcrossSpecFor(RBAcrossSort("random"), 1, false); !errors.Is(err, ErrUnqualified) || !strings.Contains(err.Error(), "rb.random") {
		t.Fatal("named pending refusal", err)
	}
	if _, err := rbAcrossSpecFor(RBAcrossContentAdded, 1, false); err != nil {
		t.Fatal("contentAdded should fall back to accepted date order", err)
	}
}
