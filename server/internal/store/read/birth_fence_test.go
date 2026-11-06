package read

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

// Exercise EVERY public entry, not only the shared private predicate. The two
// phases isolate admission-time rejection from fresh-final-view rejection.
func TestPublicReadBirthFences(t *testing.T) {
	for _, entry := range []string{"browse", "detail", "playback", "continue", "search"} {
		for _, phase := range []string{"initial", "final"} {
			t.Run(entry+"/"+phase, func(t *testing.T) {
				f, reader := newLiveFixture(t)
				var member, profile string
				if err := f.conn.QueryRowContext(t.Context(), `SELECT command_member_incarnation,command_profile_lifetime FROM authorization_session_families WHERE id='f'`).Scan(&member, &profile); err != nil {
					t.Fatal(err)
				}
				reset := func() {
					f.exec(t, `UPDATE authorization_session_families SET command_member_incarnation=?,command_profile_lifetime=? WHERE id='f'`, member, profile)
					f.exec(t, `INSERT INTO direct_memberships(account_id,role,allowed_libraries,incarnation) VALUES('a','member','["movies"]',?) ON CONFLICT(account_id) DO UPDATE SET incarnation=excluded.incarnation`, member)
					f.exec(t, `INSERT INTO direct_profiles(id,account_id,name,lifetime) VALUES('p','a','Alice',?) ON CONFLICT(id) DO UPDATE SET lifetime=excluded.lifetime`, profile)
					f.reader.snapshots, reader.snapshots = f.snapshots, f.snapshots
				}
				invoke := func(t *testing.T, change func()) (BrowseReadResult, error) {
					t.Helper()
					q, request, output := liveRequests("birth-" + entry)
					if entry == "search" {
						requests, e := search.NewSearchRequests(1)
						if e != nil {
							t.Fatal(e)
						}
						defer requests.Close()
						d, e := NewSearchDispatcher(requests, f.governor, f.authority, f.snapshots, func(context.Context, governor.Request, time.Duration, BrowseOutput, BrowseInput, search.Query) (TitleSearchPage, *BrowseReadResult, error) {
							if change != nil {
								change()
							}
							return TitleSearchPage{Disposition: SearchResults, Items: []BrowseItem{{ID: 1, Title: "fixture"}}}, nil, nil
						})
						if e != nil {
							t.Fatal(e)
						}
						published := false
						result, e := d.Consume(t.Context(), q, request, time.Second, output, dispatcherInput(f, "a"), func(context.Context, SearchResponse) (func(context.Context) error, error) {
							return func(context.Context) error { published = true; return nil }, nil
						})
						if e != nil && (published || result.Published) {
							t.Fatal("denied search published")
						}
						if e == nil && !published {
							t.Fatal("valid search did not publish")
						}
						if result.Read == nil {
							t.Fatal("missing search fence evidence", e)
						}
						return *result.Read, e
					}
					if change != nil {
						boundary := &browseBoundarySnapshots{Snapshots: f.snapshots, afterBusiness: change}
						f.reader.snapshots, reader.snapshots = boundary, boundary
					}
					var cost BrowseReadResult
					_, e := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
						var err error
						var page, zero any
						switch entry {
						case "browse":
							page, cost, err = f.reader.Window(ctx, request, time.Second, out, f.input)
							zero = BrowsePage{}
						case "detail":
							page, cost, err = reader.Detail(ctx, request, time.Second, out, TitleInput{View: f.input, Item: 1, Asset: 1})
							zero = TitleDetail{}
						case "playback":
							page, cost, err = reader.Playback(ctx, request, time.Second, out, TitleInput{View: f.input, Item: 1, Asset: 1})
							zero = PlaybackFile{}
						case "continue":
							page, cost, err = reader.ContinueWatching(ctx, request, time.Second, out, ContinueInput{View: f.input})
							zero = ContinuePage{}
						}
						if err != nil && !reflect.DeepEqual(page, zero) {
							t.Fatalf("denied %s leaked payload: %+v", entry, page)
						}
						if err == nil && reflect.DeepEqual(page, zero) {
							t.Fatal("valid entry returned zero payload")
						}
						return err
					})
					return cost, e
				}
				if _, e := invoke(t, nil); e != nil {
					t.Fatal("matching original births denied", e)
				}
				for _, mutation := range []struct {
					name string
					sql  []string
				}{
					{"changed-family-member", []string{`UPDATE authorization_session_families SET command_member_incarnation='11111111111111111111111111111111' WHERE id='f'`}},
					{"changed-family-profile", []string{`UPDATE authorization_session_families SET command_profile_lifetime='22222222222222222222222222222222' WHERE id='f'`}},
					{"missing-member", []string{`DELETE FROM direct_memberships WHERE account_id='a'`}},
					{"missing-profile", []string{`DELETE FROM direct_profiles WHERE id='p'`}},
					{"equal-id-member-recreated", []string{`DELETE FROM direct_memberships WHERE account_id='a'`, `INSERT INTO direct_memberships(account_id,role,allowed_libraries,incarnation) VALUES('a','member','["movies"]','33333333333333333333333333333333')`}},
					{"equal-id-profile-recreated", []string{`DELETE FROM direct_profiles WHERE id='p'`, `INSERT INTO direct_profiles(id,account_id,name,lifetime) VALUES('p','a','Alice','44444444444444444444444444444444')`}},
				} {
					t.Run(mutation.name, func(t *testing.T) {
						change := func() {
							for _, sql := range mutation.sql {
								f.exec(t, sql)
							}
						}
						var boundary func()
						if phase == "initial" {
							change()
						} else {
							boundary = change
						}
						cost, e := invoke(t, boundary)
						if !errors.Is(e, ErrDenied) {
							t.Fatalf("birth mismatch did not DENY: cost=%+v err=%v", cost, e)
						}
						if phase == "initial" && (cost.LookupStatements != 2 || cost.PublicationStatements != 0) {
							t.Fatal("initial denial entered business", cost)
						}
						if phase == "final" && cost.PublicationStatements != 2 {
							t.Fatal("missing fresh principal denial", cost)
						}
						assertLiveUsage(t, f, 0, 0)
					})
					reset()
				}
			})
		}
	}
}

func TestSearchBirthFenceAfterEncoding(t *testing.T) {
	f, _, d := searchDispatcherFixture(t, 1, func(context.Context, governor.Request, time.Duration, BrowseOutput, BrowseInput, search.Query) (TitleSearchPage, *BrowseReadResult, error) {
		return TitleSearchPage{Disposition: SearchResults, Items: []BrowseItem{{ID: 1, Title: "fixture"}}}, nil, nil
	})
	q, r, o := liveRequests("encode-birth-change")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	published := false
	result, err := d.Consume(ctx, q, r, time.Second, o, dispatcherInput(f, "a"), func(context.Context, SearchResponse) (func(context.Context) error, error) {
		f.exec(t, `UPDATE direct_profiles SET lifetime=? WHERE id='p'`, strings.Repeat("9", 32))
		cancel()
		return func(context.Context) error { published = true; return nil }, nil
	})
	if !errors.Is(err, ErrDenied) || published || result.Published || result.Read.PublicationStatements != 2 {
		t.Fatal("encoding/cancel bypassed current principal", result, err)
	}
	assertLiveUsage(t, f, 0, 0)
}
