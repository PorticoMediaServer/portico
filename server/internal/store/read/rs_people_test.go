package read

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

func newRSPeopleFixture(t *testing.T, live bool) (*browseFixture, *PersonReader) {
	t.Helper()
	f := composeReadFixture(t, newRSPeopleSource(t), append(browseDefinitions(), PersonCreditsDefinitions()...), readfixture.LiveConfig(time.Now), !live, rsPeopleHash(t))
	r, err := NewPersonReader(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	return f, r
}
func TestRSPeopleCompleteCreditTraversal(t *testing.T) {
	f, r := newRSPeopleFixture(t, false)
	for _, class := range []PersonCreditClass{PersonAllCredits, PersonCastCredits, PersonCrewCredits} {
		in := PersonCreditsInput{View: f.input, Person: 1, Class: class}
		in.View.Limit = 7
		var got []int64
		for n := 0; n < 100; n++ {
			p, cost, err := r.credits(t.Context(), foregroundRequest(), time.Second, f.output, in)
			if err != nil {
				t.Fatal(err)
			}
			if p.RawRows > MaxRaw || p.RawPositions > p.RawRows || len(p.Credits) > 7 || cost.LookupStatements > 204 || cost.PublicationStatements != 6 {
				t.Fatalf("unbounded %+v %+v", p, cost)
			}
			if len(p.Credits) > 0 && (p.Person == nil || p.Person.Name != "Person" || p.Person.Biography != "Accepted biography") {
				t.Fatal("missing visible witness", p)
			}
			for _, c := range p.Credits {
				if c.Image != c.Item.ID || c.CreditedName != "Credited person" {
					t.Fatal("obsolete image escaped", c)
				}
				if class == PersonCastCredits && c.Department != "Cast" || class == PersonCrewCredits && c.Department != "Directing" {
					t.Fatal("classification", c)
				}
				got = append(got, c.Item.ID*10+c.Ordinal)
			}
			if p.Exhausted {
				break
			}
			if p.Next == nil || n == 99 {
				t.Fatal("missing/nonterminating raw cursor")
			}
			in.After = p.Next
		}
		var want []int64
		for id := int64(100); id >= 1; id-- {
			if id%9 == 0 || id == 30 {
				continue
			}
			if class != PersonCastCredits {
				want = append(want, id*10+1)
			}
			if class != PersonCrewCredits {
				want = append(want, id*10)
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("class%d got%v want%v", class, got, want)
		}
	}
	// No person facts escape for an orphan or a hidden pending raw window.
	in := PersonCreditsInput{View: f.input, Person: 2}
	p, _, err := r.credits(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || p.Person != nil || len(p.Credits) != 0 || !p.Exhausted {
		t.Fatal("orphan facts escaped", p, err)
	}
	f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id<=100`)
	in.Person = 1
	p, _, err = r.credits(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || p.Person != nil || len(p.Credits) != 0 || p.Exhausted || p.Next == nil || p.RawPositions != MaxRaw {
		t.Fatal("hidden raw page", p, err)
	}
	in.After = p.Next
	f.exec(t, `UPDATE catalog_people SET revision=revision+1,name='Renamed' WHERE id=1`)
	p, _, err = r.credits(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(err, ErrStale) || !reflect.DeepEqual(p, PersonCreditsPage{}) {
		t.Fatal("stale person source", err)
	}
}
func TestRSPeoplePublicationLIVEAndRestriction(t *testing.T) {
	f, r := newRSPeopleFixture(t, true)
	in := PersonCreditsInput{View: f.input, Person: 1}
	q, request, output := liveRequests("rs-people")
	p, _, err := r.Credits(t.Context(), request, time.Second, BrowseOutput{}, in)
	if !errors.Is(err, ErrInput) || !reflect.DeepEqual(p, PersonCreditsPage{}) {
		t.Fatal("unadmitted read", err)
	}
	_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		p, _, err := r.Credits(ctx, request, time.Second, out, in)
		if err != nil || len(p.Credits) == 0 || p.Person == nil || f.authority.ForegroundUsage().Live != 1 {
			t.Fatal("public people", p, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	assertLiveUsage(t, f, 0, 0)
	// Final authority, not a returned person source row, controls disclosure.
	r.snapshots = &rsByteBoundarySnapshots{Snapshots: f.snapshots, after: func() { f.exec(t, `UPDATE authorization_session_families SET revoked=1`) }}
	q, request, output = liveRequests("rs-people-revoked")
	_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		p, _, err := r.Credits(ctx, request, time.Second, out, in)
		if !errors.Is(err, ErrDenied) || !reflect.DeepEqual(p, PersonCreditsPage{}) {
			t.Fatal("revoked person leaked", err)
		}
		return err
	})
	if !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	assertLiveUsage(t, f, 0, 0)
}
func TestRSPeopleRestrictedWitnessAndCursorBinding(t *testing.T) {
	f, r := newRSPeopleFixture(t, false)
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('p',12,0)`)
	in := PersonCreditsInput{View: f.input, Person: 1}
	p, _, err := r.credits(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || p.Person == nil || p.Next == nil {
		t.Fatal("restricted page", p, err)
	}
	for _, c := range p.Credits {
		if c.Item.ID%3 == 0 || c.Item.ID%20 > 12 {
			t.Fatal("hidden credit leaked", c)
		}
	}
	in.After = p.Next
	in.Class = PersonCrewCredits
	p, _, err = r.credits(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(err, ErrStale) || !reflect.DeepEqual(p, PersonCreditsPage{}) {
		t.Fatal("class cursor retarget", err)
	}
	// The selected image must still belong to the current credit-owner lifetime.
	f.exec(t, `UPDATE catalog_entities SET credit_owner_revision=credit_owner_revision+1,credit_owner_next_revision=credit_owner_next_revision+1 WHERE id<=100`)
	in.After = nil
	p, _, err = r.credits(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || p.Person != nil || len(p.Credits) != 0 || p.Next == nil || p.Exhausted {
		t.Fatal("obsolete owner image escaped", p, err)
	}

}
