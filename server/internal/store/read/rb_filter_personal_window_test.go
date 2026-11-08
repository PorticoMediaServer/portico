package read

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func rbPersonalWindowFixture(t *testing.T) *browseFixture {
	t.Helper()
	f := newBrowseSource(t)
	f.exec(t, `DELETE FROM personal_items`)
	f.exec(t, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,favorite,watchlisted,rating) SELECT 'p',public_id,id,library_id,1,id%2,CASE WHEN id%3=0 THEN NULL ELSE id%11 END FROM catalog_entities WHERE id%4=0 AND library_id=1`)
	f.exec(t, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,favorite,watchlisted,rating) SELECT 'q',public_id,id,library_id,1,1,10 FROM catalog_entities WHERE id%5=0 AND library_id=1`)
	defs := append(browseDefinitions(), RBPersonalTitleFilterDefinitions()...)
	f = composeReadFixture(t, f, defs, readfixture.LiveConfig(time.Now), false)
	return f
}
func rbPersonalWindowRead(t *testing.T, f *browseFixture, in PersonalTitleFilterInput) (PersonalTitleFilterPage, BrowseReadResult, error) {
	t.Helper()
	live, req, out := liveRequests("rb-personal-window")
	req.Budget.RawRows = 2*MaxRaw + 10
	var page PersonalTitleFilterPage
	var cost BrowseReadResult
	_, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, live, out, func(ctx context.Context, out BrowseOutput) error {
		var err error
		page, cost, err = f.reader.PersonalTitleFilterWindow(ctx, req, time.Second, out, in)
		if err == nil {
			assertLiveUsage(t, f, 1, 0)
		}
		return err
	})
	if u := f.authority.ForegroundUsage(); u.Live != 0 || u.Waiters != 0 {
		t.Fatal("personal source retained LIVE custody", u)
	}
	return page, cost, err
}

func TestRBPersonalTitleWindowProgress(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := rbPersonalWindowFixture(t)
		in := PersonalTitleFilterInput{View: f.input, Query: json.RawMessage(`{"variant":"predicate","field":"favorite","operator":"equals","value":true}`)}
		var got []int64
		for n := 0; n < 40; n++ {
			p, _, err := rbPersonalWindowRead(t, f, in)
			if err != nil {
				t.Fatal(err)
			}
			if p.RawRows > MaxRaw || p.RawPositions > p.RawRows {
				t.Fatal("raw source cap", p)
			}
			for _, item := range p.Items {
				if item.ID%4 != 0 || item.ID%9 == 0 || item.ID == 1040 {
					t.Fatal("wrong personal membership", item.ID)
				}
				got = append(got, item.ID)
			}
			if p.Exhausted {
				in.After = nil
				break
			}
			if p.After == nil {
				t.Fatal("lost raw continuation")
			}
			in.After = p.After
		}
		if in.After != nil {
			t.Fatal("source never exhausted")
		}
		want := 0
		for id := 1; id <= 1100; id++ {
			if id%4 == 0 && id%9 != 0 && id != 1040 {
				want++
			}
		}
		if len(got) != want {
			t.Fatal("source filtered or capped membership", len(got), want)
		}
	})
}

func TestRBPersonalTitleWindowFreshState(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := rbPersonalWindowFixture(t)
		in := PersonalTitleFilterInput{View: f.input, Query: json.RawMessage(`{"variant":"predicate","field":"favorite","operator":"equals","value":true}`)}
		// No revision bump: the raw personal tuple itself must be revalidated.
		hook := &savedViewWindowHook{base: f.snapshots, hook: func(n int) {
			if n == 3 {
				f.exec(t, `UPDATE personal_items SET favorite=0 WHERE profile_id='p'`)
			}
		}}
		f.reader.snapshots = hook
		got, _, err := rbPersonalWindowRead(t, f, in)
		if !errors.Is(err, ErrStale) || !reflect.DeepEqual(got, PersonalTitleFilterPage{}) {
			t.Fatal("unversioned personal drift escaped", got, err)
		}
		savedViewSameDeadline(t, hook)
		f.reader.snapshots = f.snapshots
		empty, _, err := rbPersonalWindowRead(t, f, in)
		if err != nil || len(empty.Items) != 0 || empty.Exhausted || empty.After == nil || empty.RawPositions != 64 {
			t.Fatal("filtered empty batch lost raw continuation or used another profile", empty, err)
		}
	})
}

func TestRBPersonalTitleBytesBeforePayload(t *testing.T) {
	s := &savedViewSession{copyLimit: 4 << 20, nativeLimit: 2 << 20}
	in := PersonalTitleFilterInput{View: BrowseInput{Library: 1, Kind: 1}}
	calls := 0
	q := func(name string, args ...any) ([][]any, error) {
		calls++
		if calls != 1 || name != "rb.filter.personal.title.ignore.asc.first.bytes" {
			t.Fatal("unfunded payload was requested", name)
		}
		return [][]any{{int64(1), int64(1), int64(3 << 20)}}, nil
	}
	rows, err := readRBPersonalTitleRows(s, q, in, browseFence{profile: "p"})
	if !errors.Is(err, governor.ErrSnapshotBudget) || rows != nil || calls != 1 {
		t.Fatal("byte frontier did not refuse before payload", rows, err, calls)
	}
}

func TestRBPersonalTitleWindowCursorAndDenial(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := rbPersonalWindowFixture(t)
		in := PersonalTitleFilterInput{View: f.input, Query: json.RawMessage(`{"variant":"predicate","field":"favorite","operator":"equals","value":true}`)}
		first, _, err := rbPersonalWindowRead(t, f, in)
		if err != nil || first.After == nil {
			t.Fatal("missing initial continuation", err)
		}
		in.After = first.After
		in.Query = json.RawMessage(`{"variant":"predicate","field":"favorite","operator":"equals","value":false}`)
		got, _, err := rbPersonalWindowRead(t, f, in)
		if !errors.Is(err, ErrStale) || !reflect.DeepEqual(got, PersonalTitleFilterPage{}) {
			t.Fatal("cursor accepted another personal query", got, err)
		}
		in.After = nil
		hook := &savedViewWindowHook{base: f.snapshots, hook: func(n int) {
			if n == 3 {
				f.exec(t, `UPDATE authorization_session_families SET revoked=1`)
			}
		}}
		f.reader.snapshots = hook
		got, _, err = rbPersonalWindowRead(t, f, in)
		if !errors.Is(err, ErrDenied) || !reflect.DeepEqual(got, PersonalTitleFilterPage{}) {
			t.Fatal("revoked personal page escaped", got, err)
		}
		savedViewSameDeadline(t, hook)
	})
}

func TestRBPersonalTitleWindowCapture(t *testing.T) {
	f := rbPersonalWindowFixture(t)
	defs := RBPersonalTitleFilterDefinitions()
	if len(defs) != 24 {
		t.Fatal("source/byte inventory changed", len(defs))
	}
	for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if stats == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		g := captureBrowse(t, f, defs, stats)
		if _, err := stmt.New(defs, g.Receipt()); err != nil {
			t.Fatal(err)
		}
	}
}
