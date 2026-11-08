package read

import (
	"context"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"reflect"
	"testing"
	"time"
)

func TestRSReadingInventoryMatchesCanonicalOracle(t *testing.T) {
	f, r := rsReadingFixture(t)
	in := ReadingFactsInput{View: f.input, Item: 1101}
	in.View.Kind = 20
	cases := []struct{ name, sql string }{
		{"ready", `UPDATE library_sources SET health='ready',enabled=1,root='/media',configured_root='/media',incarnation='inc'; UPDATE inventory_objects SET retired=0,state='available',unsupported_reason='',root_incarnation='inc'`},
		{"offline-existing-meaning", `UPDATE library_sources SET health='offline'`},
		{"root-changed", `UPDATE library_sources SET health='root_changed'`},
		{"disabled", `UPDATE library_sources SET health='ready',enabled=0`},
		{"stale-object", `UPDATE library_sources SET enabled=1,incarnation='new-inc'`},
		{"unsupported", `UPDATE library_sources SET incarnation='inc'; UPDATE inventory_objects SET unsupported_reason='unsupported'`},
		{"missing", `UPDATE inventory_objects SET unsupported_reason='',state='missing'`},
		{"retired-object", `UPDATE inventory_objects SET state='available',retired=1`},
		{"fallback-matching-good", `DELETE FROM inventory_objects`},
		{"fallback-matching-disabled", `UPDATE library_sources SET enabled=0`},
		{"fallback-no-match-all-bad", `UPDATE library_sources SET root='/elsewhere',configured_root='/elsewhere'`},
		{"fallback-no-match-one-good", `UPDATE library_sources SET enabled=1`},
		{"no-sources", `DELETE FROM library_sources`},
		{"unavailable-asset", `UPDATE catalog_assets SET available=0 WHERE id=1`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f.exec(t, c.sql)
			var want int64
			if err := f.conn.QueryRowContext(t.Context(), `SELECT available FROM inventory_item_availability WHERE item_id=1101 AND asset_id='asset'`).Scan(&want); err != nil {
				t.Fatal(err)
			}
			p, _, err := r.facts(t.Context(), foregroundRequest(), time.Second, f.output, in)
			if err != nil || p.Available != (want != 0) || p.ID != 1101 {
				t.Fatal("canonical availability mismatch", p, want, err)
			}
		})
	}
}
func TestRSReadingFreshDenialAndInventoryDrift(t *testing.T) {
	for _, c := range []struct {
		name, sql string
		denied    bool
	}{{"revoke", `UPDATE authorization_session_families SET revoked=1`, true}, {"asset-path", `UPDATE catalog_assets SET path='/other/title.mkv' WHERE id=1`, false}, {"source-root", `UPDATE library_sources SET root='/other',configured_root='/other' WHERE id='source'`, false}, {"inventory-retire", `UPDATE inventory_objects SET retired=1 WHERE id='object'`, false}} {
		t.Run(c.name, func(t *testing.T) {
			f, r := rsReadingFixture(t)
			r.snapshots = &rsReadingBoundary{Snapshots: f.snapshots, mutate: func(phase int) {
				if phase == 2 {
					f.exec(t, c.sql)
				}
			}}
			in := ReadingFactsInput{View: f.input, Item: 1101}
			in.View.Kind = 20
			p, cost, err := r.facts(t.Context(), foregroundRequest(), time.Second, f.output, in)
			if c.denied {
				if !errors.Is(err, ErrDenied) || !reflect.DeepEqual(p, ReadingFacts{}) {
					t.Fatal("late denial escaped", p, err)
				}
			} else if err != nil || cost.Attempts != 2 || p.ID != 1101 {
				t.Fatal("whole inventory retry", p, cost.Attempts, err)
			}
		})
	}
}
func TestRSReadingClosedLeafEligibility(t *testing.T) {
	f, r := rsReadingFixture(t)
	in := ReadingFactsInput{View: f.input, Item: 1101}
	in.View.Kind = 20
	for _, c := range []struct{ name, sql string }{{"age", `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated)VALUES('p',12,0)`}, {"unaccepted", `DELETE FROM profile_restrictions; UPDATE catalog_entities SET has_reading_attribution_members=0 WHERE id=1101`}, {"format", `UPDATE catalog_entities SET has_reading_attribution_members=1 WHERE id=1101; UPDATE reading_publications SET format='mobi' WHERE item_id=1101`}} {
		t.Run(c.name, func(t *testing.T) {
			f.exec(t, c.sql)
			p, _, err := r.facts(t.Context(), foregroundRequest(), time.Second, f.output, in)
			if !errors.Is(err, ErrDenied) || !reflect.DeepEqual(p, ReadingFacts{}) {
				t.Fatal("closed eligibility failed", p, err)
			}
		})
	}
	// The native constructor does not infer configured conversion from a filename.
	withMobi, err := newRSReadingReaderWithMobi(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	p, _, err := withMobi.facts(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || p.ID != 1101 {
		t.Fatal("trusted compiled conversion domain", p, err)
	}
}

type rsReadingBoundary struct {
	*governor.Snapshots
	mutate func(int)
	calls  int
}

func (s *rsReadingBoundary) Read(ctx context.Context, r governor.Request, b time.Duration, fn func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	out, err := s.Snapshots.Read(ctx, r, b, fn)
	s.calls++
	s.mutate(s.calls)
	return out, err
}
func (s *rsReadingBoundary) ReadForecasted(ctx context.Context, r governor.Request, b time.Duration, f governor.SnapshotForecast, fn func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	out, err := s.Snapshots.ReadForecasted(ctx, r, b, f, fn)
	s.calls++
	s.mutate(s.calls)
	return out, err
}
