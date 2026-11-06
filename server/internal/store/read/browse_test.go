package read

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/schematest"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

func TestMain(m *testing.M) { os.Exit(schematest.Run(m)) }

type browseFixture struct {
	path      string
	conn      *sql.Conn
	snapshots *governor.Snapshots
	input     BrowseInput
	facts     []browseFact
}
type browseFact struct {
	id        int64
	key, keep string
	retired   bool
	age       *int64
}

func newBrowseFixture(t *testing.T) *browseFixture {
	t.Helper()
	f := &browseFixture{path: schematest.Clone(t), input: BrowseInput{Database: "fixture-db-1", TokenHash: "token", Library: 1, Kind: 1, Limit: 40, Now: time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)}}
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	db, _, err := sqliteconn.Open(t.Context(), f.path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	f.conn, err = db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.conn.Close(); err != nil {
			t.Error(err)
		}
	})
	seedBrowse(t, f)
	registry, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	reader := sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal, ReadLimits: sqliteconn.ReadLimits{LengthBytes: 65536, Columns: 32}}
	connector, err := sqliteconn.Connector(f.path, reader)
	if err != nil {
		t.Fatal(err)
	}
	gc := governor.Config{Now: time.Now, MaxGrantsPerClass: 2}
	gc.Partitions[governor.Foreground] = governor.Amounts{governor.Reader: 1, governor.MemoryBytes: 16 << 20}
	g, a, err := governor.New(gc)
	if err != nil {
		t.Fatal(err)
	}
	f.snapshots, err = governor.NewSnapshots(connector, reader, g, a, registry)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.snapshots.Close(); err != nil {
			t.Error(err)
		}
	})
	return f
}

func (f *browseFixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := f.conn.ExecContext(t.Context(), q, args...); err != nil {
		t.Fatal(err)
	}
}

func seedBrowse(t *testing.T, f *browseFixture) {
	t.Helper()
	for _, q := range []string{
		`INSERT INTO identity_state(singleton,sign_in_kind) VALUES(1,'local')`,
		`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('a','alice',x'00','p'),('b','bob',x'00','q')`,
		`INSERT INTO direct_memberships(account_id,role,allowed_libraries) VALUES('a','member','["movies"]'),('b','member','["other"]')`,
		`INSERT INTO direct_profiles(id,account_id,name) VALUES('p','a','Alice'),('q','b','Bob')`,
		`INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES('d','a','i','fixture','test','test','1','2026-11-05T00:00:00Z','2026-11-05T00:00:00Z')`,
		`INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,device_id) VALUES('f','s','a','p','local','member',1,'2026-11-07T00:00:00Z',1,'d')`,
		`INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at) VALUES('token','f',1,'2026-11-07T00:00:00Z')`,
		`INSERT INTO libraries(id,name,kind,root) VALUES('movies','Movies','movie','/media'),('other','Other','movie','/media')`,
		`INSERT INTO catalog_libraries(id,library_id,root) VALUES(1,'movies','/media'),(2,'other','/media')`,
		`INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0),(2,'show',0,1,1,1,0)`,
	} {
		f.exec(t, q)
	}
	f.exec(t, "BEGIN")
	for id := int64(1); id <= 1100; id++ {
		key, keep := "a", "The A"
		if id%2 == 0 {
			key = "A"
			keep = "the a"
		}
		if id > 1050 {
			key = "b"
			keep = "B"
		}
		if id <= 5 {
			key = ""
			keep = ""
		}
		retired := id%9 == 0
		var age any
		var agePtr *int64
		if id%3 != 0 {
			v := int64(id % 20)
			age = v
			agePtr = &v
		}
		f.facts = append(f.facts, browseFact{id: id, key: key, keep: keep, retired: retired, age: agePtr})
		library := int64(1)
		if id == 1040 {
			library = 2
		}
		public := make([]byte, 16)
		binary.LittleEndian.PutUint64(public, uint64(id))
		f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,?,1)`, id, public, library)
		f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,retired,admission_own_age) VALUES(?,?,?,1,?,?,?,?,?,0,?,?,?,?)`, id, public, library, fmt.Sprintf("Title %d", id), key, key, keep, keep, public, public, retired, age)
	}
	f.exec(t, "COMMIT")
}

func (f *browseFixture) window(t *testing.T, ctx context.Context, in BrowseInput) (BrowsePage, governor.SnapshotResult, error) {
	t.Helper()
	r := governor.Request{Key: governor.JobKey{Domain: 71, ID: "browse-test"}, Owner: 1, Class: governor.Foreground, Need: governor.Amounts{governor.Reader: 1, governor.MemoryBytes: 4 << 20}, Budget: governor.UnitBudget{RawRows: 100, VMOps: 100000, Pages: 256}, Deadline: time.Now().Add(time.Second)}
	var page BrowsePage
	result, err := f.snapshots.Read(ctx, r, time.Second, func(s *governor.ReadScope) error {
		var e error
		page, e = BrowseWindow(s, in)
		return e
	})
	// Test-only output is consumed immediately. A serving adapter needs separate
	// output custody, not a refund of the snapshot's memory reservation.
	return page, result, err
}

func TestBrowseTraversalMatchesIndependentFacts(t *testing.T) {
	f := newBrowseFixture(t)
	for _, restricted := range []bool{false, true} {
		if restricted {
			f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('p',12,0)`)
		}
		for _, keep := range []bool{false, true} {
			for _, desc := range []bool{false, true} {
				in := f.input
				in.KeepArticles = keep
				in.Descending = desc
				in.Limit = 7
				var got []int64
				for windows := 0; ; windows++ {
					if windows > 1100 {
						t.Fatal("cursor did not terminate")
					}
					page, result, err := f.window(t, t.Context(), in)
					if err != nil || result.Refusal != nil {
						t.Fatalf("read: %v %+v", err, result)
					}
					if page.RawRows > MaxRaw || page.RawPositions > page.RawRows || page.Statements > 5 || len(page.Items) > in.Limit {
						t.Fatalf("unbounded window: %+v", page)
					}
					if result.Ended.IsZero() || result.Ended.Sub(result.Started) > time.Second {
						t.Fatal("snapshot physical lifetime")
					}
					for _, item := range page.Items {
						got = append(got, item.ID)
						if item.Title != fmt.Sprintf("Title %d", item.ID) {
							t.Fatal("wrong hydration")
						}
					}
					if page.Exhausted {
						if page.Next != nil {
							t.Fatal("terminal cursor")
						}
						break
					}
					if page.Next == nil {
						t.Fatal("unfinished page has no raw continuation")
					}
					in.After = page.Next
				}
				facts := slices.Clone(f.facts)
				sort.Slice(facts, func(i, j int) bool {
					x, y := facts[i], facts[j]
					a, b := x.key, y.key
					if keep {
						a, b = x.keep, y.keep
					}
					cmp := strings.Compare(strings.ToLower(a), strings.ToLower(b))
					if cmp == 0 {
						if desc {
							return x.id > y.id
						}
						return x.id < y.id
					}
					if desc {
						return cmp > 0
					}
					return cmp < 0
				})
				var want []int64
				for _, fact := range facts {
					if fact.id == 1040 || fact.retired || restricted && (fact.age == nil || *fact.age > 12) {
						continue
					}
					want = append(want, fact.id)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("restricted=%v keep=%v desc=%v: got %d want %d", restricted, keep, desc, len(got), len(want))
				}
			}
		}
	}
}

func TestBrowseRejectsCurrentAuthorityChanges(t *testing.T) {
	for _, tt := range []struct {
		name, sql string
		want      error
	}{
		{"revoked", `UPDATE authorization_session_families SET revoked=1`, ErrDenied},
		{"epoch", `UPDATE accounts SET epoch=2 WHERE id='a'`, ErrDenied},
		{"missing-global", `DELETE FROM identity_state`, ErrDenied},
		{"global-generation", `UPDATE identity_state SET authority_generation=2`, ErrDenied},
		{"token-generation", `UPDATE authorization_session_families SET current_generation=2`, ErrDenied},
		{"token-retired", `UPDATE authorization_family_tokens SET retired=1`, ErrDenied},
		{"pin-gate", `UPDATE authorization_session_families SET pin_gate=1`, ErrDenied},
		{"disabled", `UPDATE direct_memberships SET disabled=1 WHERE account_id='a'`, ErrDenied},
		{"deleted", `UPDATE direct_profiles SET deleted=1 WHERE id='p'`, ErrDenied},
		{"cross-account", `UPDATE authorization_session_families SET profile_id='q'`, ErrDenied},
		{"cross-device", `UPDATE identity_devices SET account_id='b'`, ErrDenied},
		{"member-library", `UPDATE direct_memberships SET allowed_libraries='[]' WHERE account_id='a'`, ErrDenied},
		{"profile-library", `UPDATE direct_profiles SET allowed_libraries='[]' WHERE id='p'`, ErrDenied},
		{"retired-library", `UPDATE catalog_libraries SET retired=1 WHERE id=1`, ErrDenied},
		{"removed-configuration", `DELETE FROM libraries WHERE id='movies'`, ErrDenied},
		{"expired", `UPDATE authorization_family_tokens SET expires_at='2026-11-06T00:00:00Z'`, ErrDenied},
		{"horizon", `UPDATE authorization_session_families SET authorization_horizon='2026-11-06T00:00:00Z'`, ErrDenied},
		{"member-policy", `INSERT INTO access_limits(account_id,policy_present) VALUES('a',1)`, ErrUnqualified},
		{"labels", `INSERT INTO profile_restrictions(profile_id,blocked_labels) VALUES('p','["hidden"]')`, ErrUnqualified},
		{"pending-age", `UPDATE libraries SET age_pending_operations=1 WHERE id='movies'`, ErrUnqualified},
		{"stale-age", `UPDATE libraries SET age_input_revision=2 WHERE id='movies'`, ErrUnqualified},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newBrowseFixture(t)
			f.exec(t, tt.sql)
			page, _, err := f.window(t, t.Context(), f.input)
			if !errors.Is(err, tt.want) || len(page.Items) != 0 || page.Next != nil {
				t.Fatalf("page=%+v err=%v want %v", page, err, tt.want)
			}
		})
	}
}

func TestBrowseContinuationFencesAndRawRejections(t *testing.T) {
	f := newBrowseFixture(t)
	page, _, err := f.window(t, t.Context(), f.input)
	if err != nil {
		t.Fatal(err)
	}
	if page.Next == nil {
		t.Fatal("missing continuation")
	}
	for _, change := range []func(*BrowseInput){func(i *BrowseInput) { i.Descending = true }, func(i *BrowseInput) { i.KeepArticles = true }, func(i *BrowseInput) { i.Database = "another-db" }, func(i *BrowseInput) { i.Kind = 2 }} {
		in := f.input
		in.After = page.Next
		change(&in)
		if _, _, err := f.window(t, t.Context(), in); !errors.Is(err, ErrStale) {
			t.Fatalf("query fence: %v", err)
		}
	}
	f.exec(t, `UPDATE libraries SET data_order_revision=2 WHERE id='movies'`)
	in := f.input
	in.After = page.Next
	if _, _, err := f.window(t, t.Context(), in); !errors.Is(err, ErrStale) {
		t.Fatalf("order fence: %v", err)
	}
	f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE library_id=1`)
	in.After = nil
	page, _, err = f.window(t, t.Context(), in)
	if err != nil || len(page.Items) != 0 || page.Exhausted || page.RawPositions != MaxRaw || page.Next == nil {
		t.Fatalf("hidden window falsely exhausted: %+v %v", page, err)
	}
	in.After = page.Next
	next, _, err := f.window(t, t.Context(), in)
	if err != nil || next.RawPositions != MaxRaw || next.Next.id <= page.Next.id {
		t.Fatalf("raw cursor failed to advance: %+v %v", next, err)
	}
}

func TestBrowseCancellationAndLimits(t *testing.T) {
	f := newBrowseFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if p, _, err := f.window(t, ctx, f.input); !errors.Is(err, context.Canceled) || len(p.Items) != 0 {
		t.Fatal("cancelled result escaped", err)
	}
	in := f.input
	in.Limit = MaxPage + 1
	if _, _, err := f.window(t, t.Context(), in); !errors.Is(err, ErrInput) {
		t.Fatal("limit accepted", err)
	}
	if _, _, err := f.window(t, t.Context(), f.input); err != nil {
		t.Fatal("reader was not returned", err)
	}
}
