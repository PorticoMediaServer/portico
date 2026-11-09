package read

import (
	"context"
	"database/sql"
	"encoding/json"
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
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/schematest"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func TestMain(m *testing.M) { os.Exit(schematest.Run(m)) }

type browseFixture struct {
	governor      governor.Governor
	authority     *governor.Authority
	path          string
	conn          *sql.Conn
	snapshots     *governor.Snapshots
	reader        *BrowseReader
	output        BrowseOutput
	input         BrowseInput
	facts         []browseFact
	derivedSchema []string
}
type browseFact struct {
	id        int64
	key, keep string
	retired   bool
	age       *int64
}

// Functional controls do not qualify real-clock ready latency.
const functionalReadTimeout = 10 * time.Second

func newBrowseFixture(t *testing.T) *browseFixture {
	t.Helper()
	return composeBrowseFixture(t, newBrowseSource(t), browseDefinitions())
}

func composeBrowseFixture(t *testing.T, f *browseFixture, definitions []stmt.Definition) *browseFixture {
	t.Helper()
	gc := governor.Config{Now: time.Now, MaxGrantsPerClass: 2}
	gc.Partitions[governor.Foreground] = governor.Amounts{governor.Reader: 1, governor.MemoryBytes: governor.ForegroundRequestBytes}
	return composeReadFixture(t, f, definitions, gc, true)
}

func composeReadFixture(t *testing.T, f *browseFixture, definitions []stmt.Definition, gc governor.Config, reserveOutput bool, fixtureHashes ...string) *browseFixture {
	t.Helper()

	golden := captureBrowse(t, f, definitions, plan.Unanalyzed, fixtureHashes...)
	registry, err := stmt.New(definitions, golden.Receipt())
	if err != nil {
		t.Fatal(err)
	}
	reader := browseReaderConfig()
	connector, err := sqliteconn.Connector(f.path, reader)
	if err != nil {
		t.Fatal(err)
	}
	g, a, err := governor.New(gc)
	if err != nil {
		t.Fatal(err)
	}
	f.governor, f.authority = g, a
	f.snapshots, err = governor.NewSnapshots(connector, reader, g, a, registry)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.snapshots.Close(); err != nil {
			t.Error(err)
		}
	})
	f.reader, err = NewBrowseReader(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	if !reserveOutput {
		return f
	}

	outputRequest := governor.Request{Key: governor.JobKey{Domain: 72, ID: "browse-output"}, Owner: 1, Class: governor.Foreground, Need: governor.Amounts{governor.MemoryBytes: 8 << 20}, Deadline: time.Now().Add(time.Hour)}
	decision := g.Try(outputRequest)
	if decision.Refusal != nil {
		t.Fatal("retained-output admission", decision.Refusal)
	}
	f.output = BrowseOutput{Authority: a, Grant: decision.Grant, Request: outputRequest}
	t.Cleanup(func() {
		g.Cancel(decision.Grant)
		if err := a.Retired(decision.Grant); err != nil {
			t.Error(err)
		}
	})
	return f
}

func newBrowseSource(t *testing.T) *browseFixture {
	t.Helper()
	return attachReadSource(readfixture.Browse(t))
}
func attachReadSource(source *readfixture.Source) *browseFixture {
	f := &browseFixture{path: source.Path(), conn: source.Conn(), input: BrowseInput{Database: "fixture-db-1", TokenHash: "token", Library: 1, Kind: 1, Limit: 40, Now: time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)}}
	for _, fact := range source.Facts() {
		f.facts = append(f.facts, browseFact{fact.ID, fact.Key, fact.Keep, fact.Retired, fact.Age})
	}
	return f
}

func (f *browseFixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := f.conn.ExecContext(t.Context(), q, args...); err != nil {
		t.Fatal(err)
	}
}

func (f *browseFixture) window(t *testing.T, ctx context.Context, in BrowseInput) (BrowsePage, BrowseReadResult, error) {
	t.Helper()
	r := governor.Request{Key: governor.JobKey{Domain: 71, ID: "browse-test"}, Owner: 1, Class: governor.Foreground, Need: governor.Amounts{governor.Reader: 1, governor.MemoryBytes: governor.ForegroundReadBytes}, Budget: governor.UnitBudget{RawRows: 100, VMOps: 100000, Pages: 256}, Deadline: time.Now().Add(functionalReadTimeout)}
	// This fixture exercises the private fence kernel and retains its output
	// through the last assertion. TestLive covers mandatory public admission.
	return f.reader.window(ctx, r, functionalReadTimeout, f.output, in)
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
					if page.RawRows > MaxRaw || page.RawPositions > page.RawRows || result.LookupStatements > 7 || result.PublicationStatements != 5 || page.Statements != result.LookupStatements+result.PublicationStatements || page.Statements > 12 || len(page.Items) > in.Limit {
						t.Fatalf("unbounded window: %+v", page)
					}
					if result.Lookup.Ended.IsZero() || result.Publication.Ended.IsZero() || result.Lookup.Ended.Before(result.Lookup.Started) || result.Publication.Started.Before(result.Lookup.Ended) || result.Publication.Ended.Before(result.Publication.Started) {
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

// Fixed native column limits also govern schema reparse, not just result
// width. Canonical tables have up to127 columns; a32-column reader warmed
// before ANALYZE otherwise reports the new schema as malformed on reuse.
func TestBrowseReaderSurvivesStatisticsReload(t *testing.T) {
	f := newBrowseFixture(t)
	if _, _, err := f.window(t, t.Context(), f.input); err != nil {
		t.Fatal(err)
	}
	// This is declared test-fixture maintenance outside every read snapshot,
	// never an implicit foreground ANALYZE or a product statistics policy.
	f.exec(t, "ANALYZE")
	if _, _, err := f.window(t, t.Context(), f.input); err != nil {
		t.Fatal("reader cannot reparse canonical schema after statistics reload", err)
	}
}

// json_valid is not typed-array validation, and a valid nonempty policy must
// also refuse until the complete item predicate exists. These cases establish
// fail-closed admission only; they do NOT qualify that missing predicate.
func TestBrowseProfileLabelsFailClosed(t *testing.T) {
	f := newBrowseFixture(t)
	f.exec(t, `INSERT INTO profile_restrictions(profile_id) VALUES('p')`)
	tooMany := make([]string, 65)
	for i := range tooMany {
		tooMany[i] = "label"
	}
	list, err := json.Marshal(tooMany)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`null`, `{}`, `false`, `123`, `"label"`, `[null]`, `[1]`, `[""]`, `["Horror"]`, `["` + strings.Repeat("a", 121) + `"]`, string(list)} {
		f.exec(t, `UPDATE profile_restrictions SET blocked_labels=? WHERE profile_id='p'`, raw)
		page, _, err := f.window(t, t.Context(), f.input)
		if !errors.Is(err, ErrUnqualified) || len(page.Items) > 0 || page.Next != nil {
			t.Fatalf("unsupported/malformed profile%q admitted: %+v %v", raw, page, err)
		}
	}
	// Broken JSON syntax is refused by canonical storage itself.
	if _, err := f.conn.ExecContext(t.Context(), `UPDATE profile_restrictions SET blocked_labels='[' WHERE profile_id='p'`); err == nil {
		t.Fatal("canonical JSON syntax guard absent")
	}
	f.exec(t, `UPDATE profile_restrictions SET blocked_labels='[]' WHERE profile_id='p'`)
	if page, _, err := f.window(t, t.Context(), f.input); err != nil || len(page.Items) == 0 {
		t.Fatal("empty-label envelope should still browse", err)
	}
}
