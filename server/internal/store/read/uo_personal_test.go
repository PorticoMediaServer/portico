package read

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

func newUOPersonalFixture(t *testing.T, live bool) (*browseFixture, *ForegroundReader) {
	t.Helper()
	defs := append(browseDefinitions(), UOPersonalDefinitions()...)
	var f *browseFixture
	if live {
		f = composeReadFixture(t, newForegroundSource(t), defs, readfixture.LiveConfig(time.Now), false)
	} else {
		f = composeBrowseFixture(t, newForegroundSource(t), defs)
	}
	r, err := NewForegroundReader(f.snapshots)
	if err != nil {
		t.Fatal(err)
	}
	return f, r
}

func TestUOPersonalStateValues(t *testing.T) {
	f, r := newUOPersonalFixture(t, false)
	in := PersonalStateInput{View: f.input, Item: 1}
	read := func() PersonalState {
		t.Helper()
		v, result, err := r.uoPersonalState(t.Context(), foregroundRequest(), time.Second, f.output, in)
		if err != nil {
			t.Fatal(err)
		}
		if result.LookupStatements != 6 || result.PublicationStatements != 6 {
			t.Fatalf("unexpected point read work %+v", result)
		}
		return v
	}
	v := read()
	if v.PublicID[0] != 1 || v.ResumePosition != 500 || v.Revision != 1 || v.Rating != nil || v.NotInterested || v.Favorite || v.Watchlisted {
		t.Fatalf("initial state %+v", v)
	}
	f.exec(t, `UPDATE personal_items SET rating=0,favorite=1,watchlisted=1,not_interested=1,watched=1,resume_completed=1,play_count=3,revision=2 WHERE profile_id='p' AND item_id=1`)
	v = read()
	if v.Rating == nil || *v.Rating != 0 || !v.NotInterested || !v.Favorite || !v.Watchlisted || !v.Watched || !v.Completed || v.PlayCount != 3 || v.Revision != 2 {
		t.Fatalf("zero rating/flags lost %+v", v)
	}
	f.exec(t, `UPDATE personal_items SET rating=10 WHERE profile_id='p' AND item_id=1`)
	if v = read(); v.Rating == nil || *v.Rating != 10 {
		t.Fatal("rating boundary lost", v)
	}
	f.exec(t, `DELETE FROM personal_items WHERE profile_id='p' AND item_id=1`)
	v = read()
	if v.PublicID[0] != 1 || !reflect.DeepEqual(v.UserState, UserState{}) || v.Rating != nil || v.NotInterested {
		t.Fatalf("absent personal row is not default state %+v", v)
	}
	// Profile q still has resume=999 for the same title. It must never supply
	// this profile's missing state.
	f.exec(t, `UPDATE personal_items SET rating=8,favorite=1 WHERE profile_id='q' AND item_id=1`)
	if v = read(); v.Revision != 0 || v.ResumePosition != 0 || v.Rating != nil || v.Favorite {
		t.Fatal("cross-profile state leak", v)
	}
}

func TestUOPersonalStateDenials(t *testing.T) {
	f, r := newUOPersonalFixture(t, false)
	for _, item := range []int64{9, 1040, 999999} { // retired, other library, absent
		v, _, err := r.uoPersonalState(t.Context(), foregroundRequest(), time.Second, f.output, PersonalStateInput{View: f.input, Item: item})
		if !errors.Is(err, ErrDenied) || !reflect.DeepEqual(v, PersonalState{}) {
			t.Fatalf("item%d leaked %+v: %v", item, v, err)
		}
	}
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age) VALUES('p',5)`)
	v, _, err := r.uoPersonalState(t.Context(), foregroundRequest(), time.Second, f.output, PersonalStateInput{View: f.input, Item: 8})
	if !errors.Is(err, ErrDenied) || !reflect.DeepEqual(v, PersonalState{}) {
		t.Fatal("age-restricted personal state escaped", v, err)
	}
	f.exec(t, `DELETE FROM profile_restrictions WHERE profile_id='p'`)
	// The native subject FK prevents retained identity reassignment before a
	// read can observe it. Exercise that real boundary, without disabling FKs.
	if _, err := f.conn.ExecContext(t.Context(), `UPDATE personal_items SET item_public_id=zeroblob(16) WHERE profile_id='p' AND item_id=1`); err == nil {
		t.Fatal("native subject identity constraint accepted a mismatch")
	}
	v, _, err = r.uoPersonalState(t.Context(), foregroundRequest(), time.Second, f.output, PersonalStateInput{View: f.input, Item: 1})
	if err != nil || v.PublicID[0] != 1 || v.ResumePosition != 500 {
		t.Fatal("rejected identity mismatch damaged current state", v, err)
	}
}

func TestUOPersonalStatePublication(t *testing.T) {
	for _, scenario := range []string{"revoked", "unversioned-state", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			f, r := newUOPersonalFixture(t, false)
			boundary := &browseBoundarySnapshots{Snapshots: f.snapshots}
			switch scenario {
			case "revoked":
				boundary.afterBusiness = func() { f.exec(t, `UPDATE authorization_session_families SET revoked=1`) }
			case "unversioned-state":
				boundary.afterBusiness = func() { f.exec(t, `UPDATE personal_items SET rating=7 WHERE profile_id='p' AND item_id=1`) }
			case "canceled":
				boundary.cancelLookup = true
			}
			r.snapshots = boundary
			v, result, err := r.uoPersonalState(t.Context(), foregroundRequest(), time.Second, f.output, PersonalStateInput{View: f.input, Item: 1})
			if scenario == "unversioned-state" {
				if err != nil || v.Rating == nil || *v.Rating != 7 || result.Attempts != 2 {
					t.Fatalf("exact point state was not retried %+v %+v %v", v, result, err)
				}
				return
			}
			want := ErrDenied
			if scenario == "canceled" {
				want = context.Canceled
			}
			if !errors.Is(err, want) || !reflect.DeepEqual(v, PersonalState{}) {
				t.Fatalf("publication leaked %+v: %v", v, err)
			}
		})
	}
}

func TestUOPersonalStateLiveCustody(t *testing.T) {
	f, r := newUOPersonalFixture(t, true)
	q, request, output := liveRequests("uo-personal")
	in := PersonalStateInput{View: f.input, Item: 1}
	v, result, err := r.PersonalState(t.Context(), request, time.Second, BrowseOutput{}, in)
	if !errors.Is(err, ErrInput) || result.LookupStatements != 0 || !reflect.DeepEqual(v, PersonalState{}) {
		t.Fatal("unadmitted read ran", err)
	}
	_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		v, _, err := r.PersonalState(ctx, request, time.Second, out, in)
		if err != nil || v.ResumePosition != 500 {
			t.Fatal("live personal read", v, err)
		}
		assertLiveUsage(t, f, 1, 0)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	assertLiveUsage(t, f, 0, 0)
}

func TestUOPersonalStateRejectsInvalidInput(t *testing.T) {
	f, r := newUOPersonalFixture(t, false)
	for _, in := range []PersonalStateInput{
		{View: f.input}, {View: f.input, Item: -1},
		{View: func() BrowseInput { v := f.input; v.After = &BrowseCursor{}; return v }(), Item: 1},
	} {
		v, result, err := r.uoPersonalState(t.Context(), governor.Request{}, time.Second, f.output, in)
		if !errors.Is(err, ErrInput) || result.LookupStatements != 0 || !reflect.DeepEqual(v, PersonalState{}) {
			t.Fatal("invalid point read ran", err)
		}
	}
}
