package read

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestRSSubjectDetailAdmissionBands(t *testing.T) {
	f, r := newRSDetailFixture(t, false)
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated)VALUES('p',12,0)`)
	in := TitleFactsInput{View: f.input, Item: 1110}
	in.View.Kind = 10
	for _, c := range []struct {
		name             string
		members, unrated int64
		minimum          any
		allow            int
		want             bool
	}{{"empty", 0, 0, nil, 0, true}, {"all-unrated-block", 2, 2, nil, 0, false}, {"unrated-allow", 2, 2, nil, 1, true}, {"all-overage", 2, 0, int64(18), 1, false}, {"mixed-age", 3, 1, int64(8), 0, true}, {"known-allow", 1, 0, int64(12), 0, true}} {
		t.Run(c.name, func(t *testing.T) {
			f.exec(t, `UPDATE profile_restrictions SET allow_unrated=? WHERE profile_id='p'`, c.allow)
			f.exec(t, `UPDATE catalog_entities SET admission_member_count=?,admission_unrated_members=?,admission_min_known_age=?,admission_own_age=99 WHERE id=1110`, c.members, c.unrated, c.minimum)
			d, _, err := r.facts(t.Context(), foregroundRequest(), time.Second, f.output, in)
			if c.want {
				if err != nil || d.Item.ID != 1110 {
					t.Fatal("accepted member band", d, err)
				}
			} else if !errors.Is(err, ErrDenied) || !reflect.DeepEqual(d, TitleFacts{}) {
				t.Fatal("hidden band escaped", d, err)
			}
		})
	}
	// The default browse recipe still refuses restricted containers.
	_, _, err := f.reader.window(t.Context(), foregroundRequest(), time.Second, f.output, in.View)
	if !errors.Is(err, ErrUnqualified) {
		t.Fatal("default browse changed", err)
	}
	f.exec(t, `UPDATE libraries SET age_input_revision=age_input_revision+1 WHERE id='movies'`)
	d, _, err := r.facts(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(err, ErrUnqualified) || !reflect.DeepEqual(d, TitleFacts{}) {
		t.Fatal("unpublished age tuple escaped", d, err)
	}
}
func TestRSSubjectDetailKindsAndFreshProof(t *testing.T) {
	f, r := newRSDetailFixture(t, false)
	for _, kind := range []int64{11, 12, 13} {
		container := 0
		if kind == 13 {
			container = 1
		}
		f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening)VALUES(?, ?,0,?,1,1,0)`, kind, map[int64]string{11: "extra", 12: "disc", 13: "author"}[kind], container)
		public := []byte{0xf1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, byte(kind)}
		f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind)VALUES(?,?,1,?)`, 1200+kind, public, kind)
		f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation)VALUES(?,?,1,?,'Typed root','typed','t','typed','t',0,?,?)`, 1200+kind, public, kind, public, public)
		in := TitleFactsInput{View: f.input, Item: 1200 + kind}
		in.View.Kind = kind
		d, _, err := r.facts(t.Context(), foregroundRequest(), time.Second, f.output, in)
		if err != nil || d.Item.ID != in.Item || d.Item.Kind != kind {
			t.Fatal("shipped root kind", d, err)
		}
	}
	in := TitleFactsInput{View: f.input, Item: 1110}
	in.View.Kind = 10
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated)VALUES('p',12,0)`)
	boundary := &rsByteBoundarySnapshots{Snapshots: f.snapshots, after: func() {
		f.exec(t, `UPDATE catalog_entities SET admission_member_count=1,admission_unrated_members=0,admission_min_known_age=18 WHERE id=1110`)
	}}
	r.snapshots = boundary
	d, cost, err := r.facts(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(err, ErrDenied) || !reflect.DeepEqual(d, TitleFacts{}) || cost.Attempts != 1 {
		t.Fatal("fresh final band failed", d, cost.Attempts, err)
	}
}
func TestRSSubjectRejectsUnsealedPublication(t *testing.T) {
	// Native foreign keys prevent installing a nonexistent publication. Exercise
	// the closed predicate's exact point response without manufacturing an invalid
	// accepted image or disabling foreign keys in a database fixture.
	in := BrowseInput{Library: 1, Kind: 10, policy: readPolicyBinding{recipe: rsDetailSubjectPolicy, subject: 1}}
	source := []any{int64(1), int64(1), int64(10), int64(0), nil, int64(1), int64(0), int64(8), nil, nil, int64(2), int64(2), int64(9), make([]byte, 16), int64(1), nil, nil, int64(1), int64(1), int64(1), int64(1)}
	for _, phase := range []string{"building", "reducing", "retired"} {
		_, err := rsDetailSubjectEligibility(func(name string, args ...any) ([][]any, error) {
			if name == "rs.subject.automatic" {
				return nil, nil
			}
			if name == "rs.subject.eligibility" {
				return [][]any{source}, nil
			}
			return [][]any{{phase, int64(1), int64(1), int64(1), int64(0), int64(8), nil, nil}}, nil
		}, in, browseFence{container: 1, maximumAge: 12, allowUnrated: 0})
		if !errors.Is(err, ErrUnqualified) {
			t.Fatal("unsealed admission accepted", phase, err)
		}
	}
}

func TestRSSubjectAuthoredTupleCannotProveAutomaticMembers(t *testing.T) {
	f, r := newRSDetailFixture(t, false)
	f.exec(t, `INSERT INTO catalog_automatic_collections(library_id,provider,provider_id,entity_id)VALUES(1,'nfo','automatic',1110)`)
	in := TitleFactsInput{View: f.input, Item: 1110}
	in.View.Kind = 10
	d, _, err := r.facts(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if !errors.Is(err, ErrUnqualified) || !reflect.DeepEqual(d, TitleFacts{}) {
		t.Fatal("authored empty tuple admitted inferred membership", d, err)
	}
}
