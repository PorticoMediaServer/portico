package read

import (
	"context"
	"fmt"
	"slices"

	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

// CHANGE-013 supplies the dedicated birth witness. The point compares that
// witness and the real revision; artwork and token never enter this proof.
func nativePersonSource(q browseQuery, scope *fullQueryScope, id int64) (*nativeSource, error) {
	rows, err := q("full.native.person.accepted", id, scope.bank)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		raw, e := q("full.native.person.source.raw", id)
		if e != nil {
			return nil, e
		}
		if len(raw) == 0 {
			return nil, nil
		}
		return nil, errFullSearchPending
	}
	if len(rows) != 1 || len(rows[0]) != 8 {
		return nil, ErrUnqualified
	}
	p := rows[0]
	publication, a := p[0].(int64)
	generation, b := p[1].(int64)
	title, c := p[2].(string)
	fold, d := p[3].(string)
	birth, e := p[4].([]byte)
	revision, g := p[5].(int64)
	name, h := p[6].(string)
	sortKey, sk := p[7].(string)
	if !a || !b || !c || !d || !e || !g || !h || !sk || len(sortKey) > 65536 || publication <= 0 || generation <= 0 || len(birth) != 16 || revision <= 0 || title != name {
		return nil, ErrUnqualified
	}
	if err = nativeSealed(q, publication); err != nil {
		return nil, err
	}
	return &nativeSource{scope: scope, key: search.FieldKey{Subject: search.ManifestPerson, SubjectID: id, PublicationID: publication, BankID: scope.bank, InputGeneration: generation}, title: title, fold: fold, sortKey: sortKey}, nil
}

// A People card needs one CURRENT visible credited work. A positive witness
// can stop; a filled or exhausted work budget cannot claim a negative witness.
func nativePersonVisible(q browseQuery, scope *fullQueryScope, in BrowseInput, f browseFence, person int64) (bool, error) {
	_, allowed, err := nativePersonVisibility(q, scope, in, f, person)
	return allowed, err
}
func nativePersonVisibility(q browseQuery, scope *fullQueryScope, in BrowseInput, f browseFence, person int64) (nativePolicyWitness, bool, error) {
	if scope == nil {
		return nativePolicyWitness{}, false, ErrUnqualified
	}
	var after *ownerCursor
	for raw := 0; raw < 512; {
		page, err := readOwnerPage(q, personWorks, person, after, 64)
		if err != nil {
			return nativePolicyWitness{}, false, err
		}
		for _, p := range page.positions {
			raw++
			work, err := nativeEntitySource(q, scope, p.Work)
			if err != nil {
				return nativePolicyWitness{}, false, err
			}
			if work == nil {
				continue
			}
			allowed, _, err := nativeEligible(q, in, f, work)
			if err != nil {
				return nativePolicyWitness{}, false, err
			}
			if !allowed {
				continue
			}
			if work.item[11] != p.Image {
				continue
			}
			rows, err := q("full.native.credit.point", p.Work, p.Image, p.Ordinal, person)
			if err != nil {
				return nativePolicyWitness{}, false, err
			}
			if len(rows) > 1 {
				return nativePolicyWitness{}, false, ErrUnqualified
			}
			if len(rows) == 1 {
				proof := work.permission
				proof.person, proof.image, proof.ordinal = person, p.Image, p.Ordinal
				return proof, true, nil
			}
		}
		if page.exhausted {
			return nativePolicyWitness{}, false, nil
		}
		after = page.after
	}
	return nativePolicyWitness{}, false, errFullSearchPending
}

func nativePersonCandidate(ctx context.Context, q browseQuery, scope *fullQueryScope, f browseFence, in BrowseInput, person int64, lead exactTermPopulation) (canonicalCandidateProof, FullSearchHit, bool, error) {
	var proof canonicalCandidateProof
	var hit FullSearchHit
	if in.Kind != 0 {
		return proof, hit, false, nil
	}
	source, err := nativePersonSource(q, scope, person)
	if err != nil || source == nil {
		return proof, hit, false, err
	}
	permission, allowed, err := nativePersonVisibility(q, scope, in, f, person)
	if err != nil || !allowed {
		return proof, hit, false, err
	}
	fields, err := nativeSourceFields(ctx, q, scope, source, true, fullRouteKey{}, slices.Index(scope.terms, lead.term))
	if err != nil {
		return proof, hit, false, err
	}
	proof, match, err := proveCandidate(scope, fullResultKey{search.ManifestPerson, person, 0}, lead, fields, true)
	if err != nil || !match {
		return proof, hit, false, err
	}
	hit = FullSearchHit{Subject: search.ManifestPerson, ID: person, Tier: proof.tier, PeopleSubtier: proof.subtier, PersonName: source.title, SortKey: source.sortKey, permission: permission}
	return proof, hit, true, nil
}

func nativeCreditOwners(ctx context.Context, q browseQuery, scope *fullQueryScope, f browseFence, in BrowseInput, source *nativeSource, lead int) ([]candidateSource, error) {
	if source.item[11] == nil {
		return nil, nil
	}
	image, ok := source.item[11].(int64)
	if !ok || image <= 0 {
		return nil, ErrUnqualified
	}
	var out []candidateSource
	after := int64(-1)
	for raw := 0; raw <= 128; {
		statement := "full.native.credit.first"
		args := []any{source.key.SubjectID, image}
		if after >= 0 {
			statement = "full.native.credit.next"
			args = append(args, after)
		}
		rows, err := q(statement, append(args, int64(64))...)
		if err != nil {
			return nil, err
		}
		if len(rows) > 64 {
			return nil, ErrUnqualified
		}
		for _, row := range rows {
			if len(row) != 6 {
				return nil, ErrUnqualified
			}
			ord, a := row[0].(int64)
			person, b := row[1].(int64)
			if !a || ord <= after {
				return nil, ErrUnqualified
			}
			after = ord
			raw++
			if raw > 128 {
				return nil, fmt.Errorf("%w: complete credit cohort exceeds workspace", ErrUnqualified)
			}
			if row[1] == nil {
				continue
			}
			if !b || person <= 0 || row[2] != image {
				return nil, ErrUnqualified
			}
			owner, err := nativePersonSource(q, scope, person)
			if err != nil {
				return nil, err
			}
			if owner == nil {
				continue
			}
			fields, err := nativeSourceFields(ctx, q, scope, owner, false, fullRouteKey{relation: personWorks, owner: person, ordinal: ord, image: image}, lead)
			if err != nil {
				return nil, err
			}
			if len(out)+len(fields) > 512 {
				return nil, ErrUnqualified
			}
			out = append(out, fields...)
		}
		if len(rows) < 64 {
			return out, nil
		}
	}
	return nil, ErrUnqualified
}
