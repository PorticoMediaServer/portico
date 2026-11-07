package read

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

func TestRSPeoplePageExactGroupingKnownForAndContinuation(t *testing.T) {
	f, r := newRSPeoplePageFixture(t, false)
	p := beginRSPage(t, f, r, RSPersonPageInput{Limit: 2})
	out := finishRSPage(t, f, r, p)
	if out.Person.Token != "nfo-person" || out.Total != 4 || len(out.Credits) != 2 || out.Next == nil || out.Portrait == nil || out.Portrait.MIME != "image/png" {
		t.Fatal(out)
	}
	if out.Credits[0].Item.ID != 2 || out.Credits[1].Item.ID != 3 || len(out.Credits[0].Roles) != 2 {
		t.Fatal("NOCASE title order/grouping", out.Credits)
	}
	if len(out.KnownFor) != 4 || out.KnownFor[0].ID != 4 || out.KnownFor[1].ID != 1 || !reflect.DeepEqual(out.Roles, []string{"Cast", "Directing"}) {
		t.Fatal("billing/added/department ordering", out)
	}
	if len(out.Groups) != 2 || out.Groups[0].Total != 4 || out.Groups[1].Total != 4 {
		t.Fatal("role positions counted as works", out.Groups)
	}
	in := RSPersonPageInput{Limit: 2, After: out.Next}
	out = finishRSPage(t, f, r, beginRSPage(t, f, r, in))
	if out.Total != 4 || len(out.Credits) != 2 || out.Credits[0].Item.ID != 4 || out.Credits[1].Item.ID != 1 || out.Next != nil || len(out.Credits[1].Roles) != 2 {
		t.Fatal("missing/repeated grouped continuation", out)
	}
	for _, class := range []PersonCreditClass{PersonCastCredits, PersonCrewCredits} {
		out = finishRSPage(t, f, r, beginRSPage(t, f, r, RSPersonPageInput{Class: class}))
		if out.Total != 4 {
			t.Fatal(out)
		}
		for _, credit := range out.Credits {
			if len(credit.Roles) != 1 || rsPeopleCast(credit.Roles[0].Department) != (class == PersonCastCredits) {
				t.Fatal("classification", credit)
			}
		}
	}
	// Exact single-department filtering keeps global groups/known-for intact.
	out = finishRSPage(t, f, r, beginRSPage(t, f, r, RSPersonPageInput{Department: "missing"}))
	if out.Total != 0 || len(out.Credits) != 0 || len(out.Groups) != 2 || len(out.KnownFor) != 4 {
		t.Fatal(out)
	}
}

func TestRSPeoplePagePureReductionHasNoThousandTotalCap(t *testing.T) {
	// Tiny pure reduction: no native database or timing/scale claim.
	f, r := newRSPeoplePageFixture(t, false)
	p := beginRSPage(t, f, r, RSPersonPageInput{})
	p.current = rsPersonReduction{facts: &PersonFacts{ID: 1, Token: "local"}, works: map[int64]*rsPersonWork{}, departments: map[string]int{"Cast": 1001}}
	for id := int64(1); id <= 1001; id++ {
		p.current.works[id] = &rsPersonWork{item: TitleItem{BrowseItem: BrowseItem{ID: id, Title: "Same"}, Kind: 1}, roles: []RSPersonRole{{Role: "Actor", Department: "Cast"}}, ordinal: id}
	}
	out, err := p.personPage(8 << 20)
	if err != nil || out.Total != 1001 || len(out.Credits) != 40 || out.Next == nil || len(out.Groups) != 1 || out.Groups[0].Total != 1001 || out.KnownFor[0].ID != 1 {
		t.Fatal(out, err)
	}
}

func TestRSPeoplePagePendingLeaseAndLIVE(t *testing.T) {
	f, r := newRSPeoplePageFixture(t, true)
	p := beginRSPage(t, f, r, RSPersonPageInput{})
	_, _, err := r.Page(t.Context(), foregroundRequest(), time.Second, BrowseOutput{}, p)
	if !errors.Is(err, ErrInput) {
		t.Fatal(err)
	}
	p = beginRSPage(t, f, r, RSPersonPageInput{})
	var result RSPersonPage
	for n := 0; n < 30; n++ {
		q, request, output := liveRequests("prepared-person")
		_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
			var cost BrowseReadResult
			var e error
			result, cost, e = r.Page(ctx, request, time.Second, out, p)
			if e != nil {
				return e
			}
			for _, w := range cost.LookupWindows {
				if w.Ended.Sub(w.Started) > time.Second {
					t.Fatal("widened snapshot", w)
				}
			}
			if f.authority.ForegroundUsage().Live != 1 {
				t.Fatal("consumer lost LIVE")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.Complete {
			break
		}
		if !result.Pending {
			t.Fatal(result)
		}
		if err = f.authority.CheckPreparation(p.lease); err != nil {
			t.Fatal("poll released protection", err)
		}
	}
	if !result.Complete || !p.closed {
		t.Fatal(result)
	}
	assertLiveUsage(t, f, 0, 0)
}

func TestRSPeoplePageSourceDriftExpiryAndCapacity(t *testing.T) {
	for _, mode := range []string{"person", "head", "authority", "expired", "capacity", "orphan"} {
		t.Run(mode, func(t *testing.T) {
			clock := time.Now()
			f, r := newRSPeoplePageFixture(t, false, func() time.Time { return clock })
			clock = time.Now()
			p := beginRSPage(t, f, r, RSPersonPageInput{})
			if mode == "orphan" {
				p.input.Person = 2
				p.current.person = 2
			}
			_, _, err := r.page(t.Context(), foregroundRequest(), time.Second, f.output, p, f.input.Now)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = r.page(t.Context(), foregroundRequest(), time.Second, f.output, p, f.input.Now)
			if mode == "orphan" {
				if !errors.Is(err, ErrDenied) {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "person":
				f.exec(t, `UPDATE catalog_people SET revision=revision+1 WHERE id=1`)
			case "head":
				f.exec(t, `UPDATE catalog_entities SET credit_owner_revision=credit_owner_revision+1,credit_owner_next_revision=credit_owner_next_revision+1 WHERE id=4`)
			case "authority":
				f.exec(t, `UPDATE authorization_session_families SET revoked=1`)
			case "expired":
				clock = clock.Add(13 * time.Second)
			case "capacity":
				p.used = p.lease.PayloadBytes()
			}
			var out RSPersonPage
			for n := 0; n < 30; n++ {
				out, _, err = r.page(t.Context(), foregroundRequest(), time.Second, f.output, p, f.input.Now)
				if err != nil {
					break
				}
				if out.Complete {
					t.Fatal("invalid preparation published", mode, out)
				}
			}
			if err == nil || !reflect.DeepEqual(out, RSPersonPage{}) {
				t.Fatal(mode, out, err)
			}
			if mode == "person" && !errors.Is(err, ErrStale) || mode == "authority" && !errors.Is(err, ErrDenied) || mode == "capacity" && !errors.Is(err, governor.ErrSnapshotBudget) || mode == "orphan" && !errors.Is(err, ErrDenied) || mode == "expired" && !errors.Is(err, governor.ErrPreparationExpired) {
				t.Fatal(mode, err)
			}
		})
	}
}

func TestRSPeoplePageMultipleKindsAndDefaultContainerPolicy(t *testing.T) {
	f, r := newRSPeoplePageFixture(t, false)
	f.exec(t, `UPDATE catalog_entities SET kind=2 WHERE id=4`)
	second := f.input
	second.Kind = 2
	in := RSPersonPageInput{Views: []BrowseInput{f.input, second}, Person: 1, Limit: 40, Kind: 2}
	p := beginRSPage(t, f, r, in)
	var out RSPersonPage
	var err error
	for n := 0; n < 30; n++ {
		out, _, err = r.page(t.Context(), foregroundRequest(), time.Second, f.output, p, f.input.Now)
		if err != nil {
			t.Fatal(err)
		}
		if out.Complete {
			break
		}
	}
	if !out.Complete || out.Total != 1 || len(out.Credits) != 1 || out.Credits[0].Item.ID != 4 || out.Credits[0].Item.Kind != 2 || len(out.KnownFor) != 4 {
		t.Fatal("incomplete multi-kind scope", out)
	}
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('p',12,0)`)
	p = beginRSPage(t, f, r, in)
	out, _, err = r.page(t.Context(), foregroundRequest(), time.Second, f.output, p, f.input.Now)
	if !errors.Is(err, ErrUnqualified) || !reflect.DeepEqual(out, RSPersonPage{}) {
		t.Fatal("complete container policy was bypassed", out, err)
	}
}

func TestRSPeoplePageBytePointsAndFinalRevocation(t *testing.T) {
	f, r := newRSPeoplePageFixture(t, true)
	for _, point := range []struct {
		name string
		args []any
	}{{"rs.people.page.credit", []any{int64(1), int64(0), int64(1), int64(1), int64(1)}}, {"rs.people.page.portrait", []any{int64(1), "matched"}}} {
		_, err := f.snapshots.ReadForecasted(t.Context(), foregroundRequest(), time.Second, governor.SnapshotForecast{NativeRowBytes: 1, MaxQueries: 2}, func(scope *governor.ReadScope) error {
			rows, _, err := queryReadScope(scope, point.name, point.args...)
			if rows != nil || !errors.Is(err, governor.ErrSnapshotBudget) {
				t.Fatal("payload copied without actual-byte permission", rows, err)
			}
			return err
		})
		if !errors.Is(err, governor.ErrSnapshotBudget) {
			t.Fatal(err)
		}
	}
	p := beginRSPage(t, f, r, RSPersonPageInput{})
	r.snapshots = &rsByteBoundarySnapshots{Snapshots: f.snapshots, after: func() { f.exec(t, `UPDATE authorization_session_families SET revoked=1`) }}
	q, request, output := liveRequests("prepared-final-revoke")
	_, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		v, _, err := r.Page(ctx, request, time.Second, out, p)
		if !reflect.DeepEqual(v, RSPersonPage{}) || !errors.Is(err, ErrDenied) {
			t.Fatal("fresh denial lost", v, err)
		}
		return err
	})
	if !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	assertLiveUsage(t, f, 0, 0)
}
