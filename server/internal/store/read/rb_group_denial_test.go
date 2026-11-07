package read

import (
	"errors"
	"reflect"
	"testing"
	"testing/synctest"
	"time"
)

func TestRBGroupEveryRecipeInitialDenial(t *testing.T) {
	for _, spec := range rbGroupSpecs() {
		t.Run(spec.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := rbGroupFixture(t, false)
				f.exec(t, `UPDATE authorization_session_families SET revoked=1`)
				in := RBGroupInput{View: f.input, Family: spec.family, Subject: rbGroupFixtureSubject(spec.family)}
				in.View.Kind = spec.kinds[0]
				p, result, e := f.reader.rbGroupWindow(t.Context(), rbGroupRequest(), time.Second, f.output, in)
				if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(p, RBGroupPage{}) {
					t.Fatal("initial authority denial leaked a source page", p, e)
				}
				if result.LookupStatements != 2 || result.PublicationStatements != 0 {
					t.Fatal("initial denial must stop after the authority points, before business or publication SQL", result.LookupStatements, result.PublicationStatements)
				}
			})
		})
	}
}

func TestRBGroupAuthorCandidatesRefuseWithoutEligibleBookProof(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := rbGroupFixture(t, false)
		f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE kind=8`)
		for _, family := range []RBGroupFamily{RBGroupCollectionMembers, RBGroupPlaylistEntries} {
			in := RBGroupInput{View: f.input, Family: family, Subject: rbGroupFixtureSubject(family)}
			in.View.Kind = 13
			p, _, e := f.reader.rbGroupWindow(t.Context(), rbGroupRequest(), time.Second, f.output, in)
			if !errors.Is(e, ErrUnqualified) || !reflect.DeepEqual(p, RBGroupPage{}) {
				t.Fatal("unproved author candidate escaped", family, p, e)
			}
		}
	})
}
