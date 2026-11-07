package read

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestRSPolicyDefaultCatalogueEnvelope(t *testing.T) {
	for kind := int64(1); kind <= 10; kind++ {
		if _, err := resolveReadPolicy(BrowseInput{Kind: kind}); err != nil {
			t.Fatal("legacy kind refused", kind, err)
		}
	}
	for _, kind := range []int64{0, 11, 13, 20, 21, 99} {
		if _, err := resolveReadPolicy(BrowseInput{Kind: kind}); !errors.Is(err, ErrUnqualified) {
			t.Fatal("unregistered default kind admitted", kind, err)
		}
	}
	for _, in := range []BrowseInput{{Kind: 1, policy: readPolicyBinding{recipe: 64999}}, {Kind: 1, policy: readPolicyBinding{recipe: rsTestReadingPolicy}}, {Kind: 1, policy: readPolicyBinding{subject: 1}}} {
		if _, err := resolveReadPolicy(in); err == nil {
			t.Fatal("invalid recipe/binding admitted")
		}
	}
	for _, tc := range []struct {
		container, maximum, unrated int64
		want                        bool
	}{
		{0, -1, 1, true}, {0, 12, 0, true}, {1, -1, 1, true}, {1, 12, 1, false}, {1, -1, 0, false}, {1, 12, 0, false},
	} {
		_, err := validateCataloguePolicy(nil, BrowseInput{}, browseFence{container: tc.container, maximumAge: tc.maximum, allowUnrated: tc.unrated})
		if (err == nil) != tc.want {
			t.Fatal("default container refusal changed", tc, err)
		}
	}
}

func TestRSPolicyDefaultBrowseParity(t *testing.T) {
	f := newBrowseFixture(t)
	in := f.input
	p, _, err := f.window(t, t.Context(), in)
	if err != nil || len(p.Items) != 40 {
		t.Fatal("ordinary browse acceptance changed", p, err)
	}
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('p',12,0)`)
	p, _, err = f.window(t, t.Context(), in)
	if err != nil || len(p.Items) == 0 {
		t.Fatal("restricted leaf acceptance changed", p, err)
	}
	for _, item := range p.Items {
		if item.ID%3 == 0 || item.ID%20 > 12 {
			t.Fatal("hidden leaf admitted", item)
		}
	}
	in.Kind = 2
	p, _, err = f.window(t, t.Context(), in)
	if !errors.Is(err, ErrUnqualified) || !reflect.DeepEqual(p, BrowsePage{}) {
		t.Fatal("restricted container default changed", err)
	}
	in.Kind = 20
	p, cost, err := f.window(t, t.Context(), in)
	if !errors.Is(err, ErrUnqualified) || !reflect.DeepEqual(p, BrowsePage{}) || cost.LookupStatements != 0 {
		t.Fatal("unknown kind default changed", err, cost)
	}
}

func rsPolicyRead(t *testing.T, f *browseFixture, in BrowseInput, sources ...browseSnapshots) ([]int64, BrowseReadResult, error) {
	t.Helper()
	var source browseSnapshots = f.snapshots
	if len(sources) == 1 {
		source = sources[0]
	}
	return publishRead(t.Context(), source, foregroundRequest(), time.Second, f.output, in, []readStep[[]int64]{func(ctx context.Context, _ browseQuery, _ browseFence, out *[]int64) error {
		*out = []int64{in.policy.subject}
		return ctx.Err()
	}})
}
func rsPolicyReadingFixture(t *testing.T) *browseFixture {
	t.Helper()
	f, _ := newForegroundFixture(t)
	f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(20,'ebook',0,0,1,1,0),(21,'comic',0,0,1,1,0)`)
	// These are test-only source facts, not a real accepted Reading publication.
	rsPolicyRetype(t, f, 20)
	return f
}
func TestRSPolicyReadingRequiresItsClosedRecipe(t *testing.T) {
	f := rsPolicyReadingFixture(t)
	in := f.input
	in.Kind = 20
	out, _, err := rsPolicyRead(t, f, in)
	if !errors.Is(err, ErrUnqualified) || out != nil {
		t.Fatal("default enabled Reading", out, err)
	}
	in.policy = readPolicyBinding{recipe: rsTestReadingPolicy, subject: 1}
	out, _, err = rsPolicyRead(t, f, in)
	if err != nil || !reflect.DeepEqual(out, []int64{1}) {
		t.Fatal("registered bounded recipe rejected", out, err)
	}
	in.policy.subject = 0
	out, _, err = rsPolicyRead(t, f, in)
	if !errors.Is(err, ErrInput) || out != nil {
		t.Fatal("caller proof substituted for recipe", out, err)
	}
	in.policy.subject = 1
	f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id=1`)
	out, _, err = rsPolicyRead(t, f, in)
	if !errors.Is(err, ErrDenied) || out != nil {
		t.Fatal("ineligible subject admitted", out, err)
	}
}
func TestRSPolicyWitnessAndAuthorityRechecked(t *testing.T) {
	f := rsPolicyReadingFixture(t)
	in := f.input
	in.Kind = 20
	in.policy = readPolicyBinding{recipe: rsTestReadingPolicy, subject: 1}
	out, _, err := rsPolicyRead(t, f, in, &browseBoundarySnapshots{Snapshots: f.snapshots, afterBusiness: func() { f.exec(t, `UPDATE catalog_entities SET revision=revision+1 WHERE id=1`) }})
	if !errors.Is(err, ErrStale) || out != nil {
		t.Fatal("fresh policy witness was not compared", out, err)
	}
	out, _, err = rsPolicyRead(t, f, in, &browseBoundarySnapshots{Snapshots: f.snapshots, afterBusiness: func() { f.exec(t, `UPDATE authorization_session_families SET revoked=1`) }})
	if !errors.Is(err, ErrDenied) || out != nil {
		t.Fatal("registered recipe bypassed fresh authority", out, err)
	}
}

func TestRSPolicyContainerNeedsCompleteEligibility(t *testing.T) {
	f, _ := newForegroundFixture(t)
	rsPolicyRetype(t, f, 2)
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('p',12,0)`)
	in := f.input
	in.Kind = 2
	out, _, err := rsPolicyRead(t, f, in)
	if !errors.Is(err, ErrUnqualified) || out != nil {
		t.Fatal("legacy refusal changed", out, err)
	}
	in.policy = readPolicyBinding{recipe: rsTestContainerPolicy, subject: 1}
	out, _, err = rsPolicyRead(t, f, in)
	if !errors.Is(err, ErrUnqualified) || out != nil {
		t.Fatal("incomplete proof became permission", out, err)
	}
	f.exec(t, `UPDATE catalog_entities SET parent_id=NULL WHERE parent_id=1`)
	f.exec(t, `UPDATE catalog_entities SET parent_id=1 WHERE id IN(2,3)`)
	out, _, err = rsPolicyRead(t, f, in)
	if err != nil || !reflect.DeepEqual(out, []int64{1}) {
		t.Fatal("complete fixture proof refused", out, err)
	}
	f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id IN(2,3)`)
	out, _, err = rsPolicyRead(t, f, in)
	if !errors.Is(err, ErrDenied) || out != nil {
		t.Fatal("no visible member proof became permission", out, err)
	}
}
func TestRSPolicyRegistrySealedBeforeExecution(t *testing.T) {
	if _, err := resolveReadPolicy(BrowseInput{Kind: 1}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("runtime registration accepted")
		}
	}()
	registerReadPolicy(readPolicyRecipe{id: 64998, domain: "late", kinds: []int64{20}, validate: rsTestReadingEligibility})
}

func rsPolicyRetype(t *testing.T, f *browseFixture, kind int64) {
	t.Helper()
	f.exec(t, `BEGIN`)
	f.exec(t, `PRAGMA defer_foreign_keys=ON`)
	f.exec(t, `UPDATE catalog_subjects SET kind=? WHERE id=1`, kind)
	f.exec(t, `UPDATE catalog_entities SET kind=? WHERE id=1`, kind)
	f.exec(t, `COMMIT`)
}
func TestRSPolicyRecipeCannotBypassBirthChecks(t *testing.T) {
	f := rsPolicyReadingFixture(t)
	in := f.input
	in.Kind = 20
	in.policy = readPolicyBinding{recipe: rsTestReadingPolicy, subject: 1}
	var birth string
	if err := f.conn.QueryRowContext(t.Context(), `SELECT command_member_incarnation FROM authorization_session_families WHERE id='f'`).Scan(&birth); err != nil {
		t.Fatal(err)
	}
	f.exec(t, `UPDATE authorization_session_families SET command_member_incarnation='11111111111111111111111111111111' WHERE id='f'`)
	out, cost, err := rsPolicyRead(t, f, in)
	if !errors.Is(err, ErrDenied) || out != nil || cost.LookupStatements != 2 || cost.PublicationStatements != 0 {
		t.Fatal("initial birth check bypassed", out, cost, err)
	}
	f.exec(t, `UPDATE authorization_session_families SET command_member_incarnation=? WHERE id='f'`, birth)
	out, cost, err = rsPolicyRead(t, f, in, &browseBoundarySnapshots{Snapshots: f.snapshots, afterBusiness: func() {
		f.exec(t, `UPDATE authorization_session_families SET command_profile_lifetime='22222222222222222222222222222222' WHERE id='f'`)
	}})
	if !errors.Is(err, ErrDenied) || out != nil || cost.PublicationStatements != 2 {
		t.Fatal("fresh birth check bypassed", out, cost, err)
	}
}

func TestRSPolicyContinuationBindsRecipeSubject(t *testing.T) {
	f := rsPolicyReadingFixture(t)
	in := f.input
	in.Kind = 20
	in.policy = readPolicyBinding{recipe: rsTestReadingPolicy, subject: 1}
	fence, _, err := publishRead(t.Context(), f.snapshots, foregroundRequest(), time.Second, f.output, in, []readStep[browseFence]{func(ctx context.Context, _ browseQuery, current browseFence, out *browseFence) error {
		*out = current
		return ctx.Err()
	}})
	if err != nil {
		t.Fatal(err)
	}
	in.After = &BrowseCursor{fence: fence}
	f.exec(t, `BEGIN`)
	f.exec(t, `PRAGMA defer_foreign_keys=ON`)
	f.exec(t, `UPDATE catalog_subjects SET kind=20 WHERE id=2`)
	f.exec(t, `UPDATE catalog_entities SET kind=20 WHERE id=2`)
	f.exec(t, `COMMIT`)
	in.policy.subject = 2
	out, _, err := rsPolicyRead(t, f, in)
	if !errors.Is(err, ErrStale) || out != nil {
		t.Fatal("continuation retargeted a domain subject", out, err)
	}
	// Changing the recipe itself also cannot reuse a previously captured fence.
	in.policy = readPolicyBinding{recipe: rsTestReadingAliasPolicy, subject: 1}
	out, _, err = rsPolicyRead(t, f, in)
	if !errors.Is(err, ErrStale) || out != nil {
		t.Fatal("continuation changed its compiled recipe", out, err)
	}
}
