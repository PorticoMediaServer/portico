package read

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

func fullScope(t *testing.T, text string) *fullQueryScope {
	t.Helper()
	q, e := search.PlanQuery(text, true)
	if e != nil {
		t.Fatal(e)
	}
	s, e := newFullQueryScope(q, 1, 1, 0)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func fieldProof(t *testing.T, s *fullQueryScope, family search.ManifestSubject, id int64, field uint16, text string) logicalSourceProof {
	t.Helper()
	title := search.PrepareTitle(text)
	k := search.FieldKey{Subject: family, SubjectID: id, PublicationID: id + 100, BankID: 1, InputGeneration: 1, Field: field, Revision: "09"}
	w, e := newLogicalFieldWalk(s, k, title.Fold())
	if e != nil {
		t.Fatal(e)
	}
	if e = w.observe(t.Context(), k, search.PreparePersonName(text), title.Fold()); e != nil {
		t.Fatal(e)
	}
	p, e := w.finish(t.Context(), true)
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func populations(t *testing.T, s *fullQueryScope, proofs ...logicalSourceProof) []exactTermPopulation {
	t.Helper()
	proofs = slices.Clone(proofs)
	slices.SortFunc(proofs, func(a, b logicalSourceProof) int { return compareSource(a.key, b.key) })
	var out []exactTermPopulation
	for i := range s.terms {
		p := logicalPopulation{scope: s, term: i}
		var matched []logicalSourceProof
		for _, v := range proofs {
			if v.matched[i] {
				matched = append(matched, v)
			}
		}
		if e := logicalPopulationStep(t.Context(), &p, matched, len(proofs), true); e != nil {
			t.Fatal(e)
		}
		exact, e := finishExactPopulation(t.Context(), &p)
		if e != nil {
			t.Fatal(e)
		}
		out = append(out, exact)
	}
	return out
}

func TestFullLogicalPopulationAndLead(t *testing.T) {
	s := fullScope(t, "Matrix keanu matrix")
	work := fieldProof(t, s, search.ManifestEntity, 10, 1, "Matrix")
	person := fieldProof(t, s, search.ManifestPerson, 2, 1, "Keanu")
	other := fieldProof(t, s, search.ManifestEntity, 11, 1, "Matrix Reloaded")
	p := logicalPopulation{scope: s, term: 0}
	if e := logicalPopulationStep(t.Context(), &p, []logicalSourceProof{person, person}, 2, false); e != nil {
		t.Fatal(e)
	}
	if _, e := finishExactPopulation(t.Context(), &p); !errors.Is(e, errFullSearchPending) {
		t.Fatal("partial count minted exact", e)
	}
	if e := logicalPopulationStep(t.Context(), &p, nil, 0, true); e != nil {
		t.Fatal(e)
	}
	exact, e := finishExactPopulation(t.Context(), &p)
	if e != nil || exact.count != 1 {
		t.Fatal("physical siblings counted as logical sources", exact, e)
	}
	lead, e := chooseLead(s, populations(t, s, work, person, other))
	if e != nil || lead.term != "keanu" || lead.count != 1 {
		t.Fatal(lead, e)
	}
	if _, e = chooseLead(s, []exactTermPopulation{exact}); !errors.Is(e, errFullSearchPending) {
		t.Fatal("omitted term accepted", e)
	}
	tie := fullScope(t, "beta alpha")
	a := fieldProof(t, tie, search.ManifestEntity, 1, 1, "alpha beta")
	lead, e = chooseLead(tie, populations(t, tie, a))
	if e != nil || lead.term != "alpha" {
		t.Fatal("not BINARY tie", lead, e)
	}
}

func TestFullMixedOwnOwnerProofAndRanking(t *testing.T) {
	s := fullScope(t, "Matrix Keanu")
	work := fieldProof(t, s, search.ManifestEntity, 10, 1, "Matrix")
	person := fieldProof(t, s, search.ManifestPerson, 2, 1, "Keanu Reeves")
	direct := fieldProof(t, s, search.ManifestEntity, 20, 1, "Matrix Keanu")
	lead, e := chooseLead(s, populations(t, s, work, person, direct))
	if e != nil {
		t.Fatal(e)
	}
	foreign := candidateSource{proof: person, route: fullRouteKey{relation: personWorks, owner: 2, ordinal: 1, image: 1}}
	proof, yes, e := proveCandidate(s, fullResultKey{search.ManifestEntity, 10, 1}, lead, []candidateSource{{proof: work, own: true}, foreign}, true)
	if e != nil || !yes || proof.tier != 5 {
		t.Fatal("mixed conjunction lost", proof, yes, e)
	}
	best, yes, e := proveCandidate(s, fullResultKey{search.ManifestEntity, 20, 1}, lead, []candidateSource{{proof: direct, own: true}, foreign}, true)
	if e != nil || !yes || best.tier != 0 || compareFullRank(best, proof) >= 0 {
		t.Fatal("foreign route outranked direct title", best, e)
	}
	if _, yes, e = proveCandidate(s, fullResultKey{search.ManifestEntity, 10, 1}, lead, []candidateSource{{proof: work, own: true}}, true); e != nil || yes {
		t.Fatal("hidden owner supplied proof", yes, e)
	}
	if _, _, e = proveCandidate(s, fullResultKey{search.ManifestEntity, 10, 1}, lead, []candidateSource{{proof: work, own: true}, foreign}, false); !errors.Is(e, errFullSearchPending) {
		t.Fatal("partial inverse proved canonical winner", e)
	}
	appearance := person
	appearance.key.Field = 6
	if _, yes, e = proveCandidate(s, fullResultKey{search.ManifestEntity, 10, 1}, lead, []candidateSource{{proof: work, own: true}, {proof: appearance}}, true); e != nil || yes {
		t.Fatal("appearance became foreign NAME", yes, e)
	}
}

func TestFullMultiOwnerCanonicalRouteAndIdentity(t *testing.T) {
	s := fullScope(t, "Keanu Nolan")
	a := fieldProof(t, s, search.ManifestPerson, 2, 1, "Keanu")
	b := fieldProof(t, s, search.ManifestPerson, 3, 1, "Nolan")
	lead, e := chooseLead(s, populations(t, s, a, b))
	if e != nil {
		t.Fatal(e)
	}
	sources := []candidateSource{{proof: b, route: fullRouteKey{relation: personWorks, owner: 3, ordinal: 2, image: 10}}, {proof: a, route: fullRouteKey{relation: personWorks, owner: 2, ordinal: 5, image: 10}}, {proof: a, route: fullRouteKey{relation: personWorks, owner: 2, ordinal: 1, image: 10}}}
	first, yes, e := proveCandidate(s, fullResultKey{search.ManifestEntity, 1, 1}, lead, sources, true)
	if e != nil || !yes || first.tier != 5 || first.lead != a.key || first.route.ordinal != 1 {
		t.Fatal("not complete/minimum canonical route", first, e)
	}
	slices.Reverse(sources)
	again, yes, e := proveCandidate(s, first.key, lead, sources, true)
	if e != nil || !yes || first != again {
		t.Fatal("arrival order changed winner", again, e)
	}
	wrong := a
	wrong.key.PublicationID++
	if _, _, e = proveCandidate(s, first.key, lead, append(sources, candidateSource{proof: wrong}), true); !errors.Is(e, ErrStale) {
		t.Fatal("mixed accepted heads", e)
	}
	people := fullScope(t, "Keanu")
	p := fieldProof(t, people, search.ManifestPerson, 1, 1, "Keanu")
	pl, _ := chooseLead(people, populations(t, people, p))
	card, yes, e := proveCandidate(people, fullResultKey{search.ManifestPerson, 1, 0}, pl, []candidateSource{{proof: p, own: true}}, true)
	if e != nil || !yes || card.tier != 3 || card.subtier != 0 || card.key == first.key {
		t.Fatal(card, e)
	}
}

func TestFullLogicalSiblingAndForeignFence(t *testing.T) {
	s := fullScope(t, "辛哫")
	k := search.FieldKey{Subject: search.ManifestEntity, SubjectID: 1, PublicationID: 1, BankID: 1, InputGeneration: 1, Field: 4, Revision: "09"}
	w, e := newLogicalFieldWalk(s, k, "")
	if e != nil {
		t.Fatal(e)
	}
	// Prefix candidate has no normalized hit. The accepted normalized sibling
	// does; multiple physical windows still produce ONE logical source proof.
	if e = w.observe(t.Context(), k, "", strings.Repeat("甲", 200)); e != nil {
		t.Fatal(e)
	}
	if e = w.observe(t.Context(), k, "", "辛哫"); e != nil {
		t.Fatal(e)
	}
	p, e := w.finish(t.Context(), true)
	if e != nil || !p.matched[0] {
		t.Fatal(p, e)
	}
	counts := populations(t, s, p, p)
	if counts[0].count != 1 {
		t.Fatal("extra logical alias", counts)
	}
	w, _ = newLogicalFieldWalk(s, k, "")
	bad := k
	bad.PublicationID++
	if e = w.observe(t.Context(), bad, "", "辛哫"); !errors.Is(e, search.ErrFieldContinuation) {
		t.Fatal(e)
	}
	if _, e = w.finish(t.Context(), true); e == nil {
		t.Fatal("foreign sibling not poisoned")
	}
	w, _ = newLogicalFieldWalk(s, k, "")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if e = w.observe(ctx, k, "", "辛哫"); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	bad = k
	bad.BankID = 2
	if _, e = newLogicalFieldWalk(s, bad, ""); e == nil {
		t.Fatal("cross-bank proof")
	}
}

func TestFullCanonicalPostingPopulation(t *testing.T) {
	s := fullScope(t, "Keanu")
	a := fieldProof(t, s, search.ManifestPerson, 100, 1, "Keanu")
	b := fieldProof(t, s, search.ManifestEntity, 2, 4, "Keanu")
	pa := canonicalPostingProof{source: a, term: 0, first: fullPostingKey{1, 10}}
	pb := canonicalPostingProof{source: b, term: 0, first: fullPostingKey{1, 11}}
	p := logicalPopulation{scope: s, term: 0}
	// Logical keys decrease, while true native rowids increase. Repeated
	// fragments/slots must not require sorting or a query-wide seen-ID set.
	if e := canonicalPopulationStep(t.Context(), &p, 1, []logicalPostingDelta{{fullPostingKey{1, 10}, &pa}, {fullPostingKey{1, 11}, &pb}, {fullPostingKey{1, 12}, nil}}, true); e != nil {
		t.Fatal(e)
	}
	if _, e := finishExactPopulation(t.Context(), &p); !errors.Is(e, errFullSearchPending) {
		t.Fatal("one slot mistaken for complete population", e)
	}
	if e := canonicalPopulationStep(t.Context(), &p, 2, []logicalPostingDelta{{fullPostingKey{2, 1}, &pa}}, true); e != nil {
		t.Fatal(e)
	}
	for slot := uint8(3); slot <= 5; slot++ {
		if e := canonicalPopulationStep(t.Context(), &p, slot, nil, true); e != nil {
			t.Fatal(e)
		}
	}
	exact, e := finishExactPopulation(t.Context(), &p)
	if e != nil || exact.count != 2 {
		t.Fatal("physical/native order polluted logical count", exact, e)
	}
	p = logicalPopulation{scope: s, term: 0}
	if e = canonicalPopulationStep(t.Context(), &p, 1, []logicalPostingDelta{{fullPostingKey{1, 10}, &pa}}, false); e != nil {
		t.Fatal(e)
	}
	if _, e = finishExactPopulation(t.Context(), &p); !errors.Is(e, errFullSearchPending) {
		t.Fatal("partial native traversal was exact", e)
	}
	if e = canonicalPopulationStep(t.Context(), &p, 1, []logicalPostingDelta{{fullPostingKey{1, 10}, &pa}}, true); !errors.Is(e, ErrStale) {
		t.Fatal("raw cursor repeated", e)
	}
}

func TestFullCandidatePreservesPerFieldRevisions(t *testing.T) {
	scope := fullScope(t, "alpha beta")
	title := fieldProof(t, scope, search.ManifestEntity, 1, 1, "alpha")
	alias := fieldProof(t, scope, search.ManifestEntity, 1, 4, "beta")
	title.key.Revision = "native-title-09"
	alias.key.Revision = "native-alias-17"
	lead, err := chooseLead(scope, populations(t, scope, title, alias))
	if err != nil {
		t.Fatal(err)
	}
	sources := []candidateSource{{proof: title, own: true}, {proof: alias, own: true}}
	proof, yes, err := proveCandidate(scope, fullResultKey{search.ManifestEntity, 1, 1}, lead, sources, true)
	if err != nil || !yes || proof.tier != 4 {
		t.Fatal("distinct field witnesses lost", proof, yes, err)
	}
	changed := title
	changed.key.Revision = "another-title"
	if _, _, err = proveCandidate(scope, proof.key, lead, append(sources, candidateSource{proof: changed, own: true}), true); !errors.Is(err, ErrStale) {
		t.Fatal("same-field witness drift accepted", err)
	}
}
