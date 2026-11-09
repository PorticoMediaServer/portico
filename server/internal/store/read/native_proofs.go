package read

import (
	"bytes"
	"context"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
	"slices"
)

func nativeEligible(q browseQuery, in BrowseInput, f browseFence, source *nativeSource) (bool, string, error) {
	if source != nil && source.key.Subject == search.ManifestPerson {
		allowed, err := nativePersonVisible(q, source.scope, in, f, source.key.SubjectID)
		return allowed, "person", err
	}
	proof, name, allowed, err := nativePolicyForSource(q, in, f, source)
	if err == nil && allowed {
		source.permission = proof
	}
	return allowed, name, err
}

func nativeSealed(q browseQuery, publication int64) error {
	rows, err := q("full.native.seal", publication)
	if err != nil {
		return err
	}
	if len(rows) != 1 || len(rows[0]) != 8 {
		return ErrUnqualified
	}
	r := rows[0]
	expected, ok := r[4].([]byte)
	chain, ok2 := r[5].([]byte)
	if r[0] != search.ManifestAlgorithm || r[1] != int64(1) || r[2] != r[3] || r[6] != r[7] || !ok || !ok2 || len(expected) != 32 || !bytes.Equal(expected, chain) {
		return ErrUnqualified
	}
	return nil
}
func nativeSourceFields(ctx context.Context, q browseQuery, scope *fullQueryScope, s *nativeSource, own bool, route fullRouteKey, lead int) ([]candidateSource, error) {
	maximum := uint16(4)
	if own && s.key.Subject == search.ManifestEntity {
		maximum = 9
	}
	var out []candidateSource
	for field := uint16(1); field <= maximum; field++ {
		proof, _, err := nativeLogicalField(ctx, q, scope, s, field, lead)
		if err != nil {
			return nil, err
		}
		if proof.scope != nil {
			if !own {
				proof.primary = ""
			}
			out = append(out, candidateSource{proof: proof, own: own, route: route})
		}
	}
	return out, nil
}

// Inverse relations are consumed completely before the conjunction or winner
// is proved. A work bound refuses; it never proves an unseen owner absent.
func nativeCandidate(ctx context.Context, q browseQuery, scope *fullQueryScope, f browseFence, in BrowseInput, id int64, lead nativeSearchLead) (canonicalCandidateProof, FullSearchHit, bool, error) {
	var empty canonicalCandidateProof
	var hit FullSearchHit
	if scope != nil && scope.peopleOnly {
		// A People action admits person candidates only. Refusing here, before
		// any source or inverse-owner read, keeps a work's complete credit
		// cohort out of the read even if a future route reaches this proof.
		return empty, hit, false, nil
	}
	source, err := nativeEntitySource(q, scope, id)
	if err != nil || source == nil {
		return empty, hit, false, err
	}
	eligible, name, err := nativeEligible(q, in, f, source)
	if err != nil || !eligible {
		return empty, hit, false, err
	}
	if in.Kind != 0 && in.Kind != source.kind {
		return empty, hit, false, nil
	}
	leadIndex := slices.Index(scope.terms, lead.term())
	sources, err := nativeSourceFields(ctx, q, scope, source, true, fullRouteKey{}, leadIndex)
	if err != nil {
		return empty, hit, false, err
	}
	workspace := func(fields []candidateSource) int64 {
		var n int64
		for _, field := range fields {
			n += 256 + int64(len(field.proof.key.Revision)+len(field.proof.primary)+len(field.proof.matched))
		}
		return n
	}
	owned := workspace(sources)
	addOwner := func(owner int64, route fullRouteKey) error {
		if owner == 0 {
			return nil
		}
		if owner < 0 {
			return ErrUnqualified
		}
		s, e := nativeEntitySource(q, scope, owner)
		if e != nil {
			return e
		}
		if s == nil {
			return nil
		}
		allowed, _, e := nativeEligible(q, in, f, s)
		if e != nil {
			return e
		}
		if !allowed {
			return nil
		}
		fields, e := nativeSourceFields(ctx, q, scope, s, false, route, leadIndex)
		if e != nil {
			return e
		}
		if len(sources)+len(fields) > 512 {
			return fmt.Errorf("%w: complete owner proof exceeds bounded workspace", ErrUnqualified)
		}
		owned += workspace(fields)
		if owned > 1<<20 {
			return fmt.Errorf("%w: complete owner proof workspace bytes", ErrUnqualified)
		}
		sources = append(sources, fields...)
		return nil
	}
	integer := func(index int) (int64, error) {
		if source.item[index] == nil {
			return 0, nil
		}
		v, ok := source.item[index].(int64)
		if !ok || v <= 0 {
			return 0, ErrUnqualified
		}
		return v, nil
	}
	for _, index := range []int{13, 14, 15} {
		if index == 15 && name != "album" {
			continue
		}
		owner, e := integer(index)
		if e != nil {
			return empty, hit, false, e
		}
		if owner == 0 {
			continue
		}
		relation := showChildren
		if index == 13 {
			relation = albumTracks
		}
		if index == 15 {
			relation = artistAlbums
		}
		if e = addOwner(owner, fullRouteKey{relation: relation, owner: owner}); e != nil {
			return empty, hit, false, e
		}
		if index == 13 {
			album, e := nativeEntitySource(q, scope, owner)
			if e != nil {
				return empty, hit, false, e
			}
			if album == nil {
				continue
			}
			artist, ok := album.item[15].(int64)
			if album.item[15] != nil && !ok {
				return empty, hit, false, ErrUnqualified
			}
			if artist > 0 {
				if e = addOwner(artist, fullRouteKey{relation: artistAlbums, owner: artist, via: owner}); e != nil {
					return empty, hit, false, e
				}
			}
		}
	}
	creditOwners, e := nativeCreditOwners(ctx, q, scope, f, in, source, leadIndex)
	if e != nil {
		return empty, hit, false, e
	}
	owned += workspace(creditOwners)
	if len(sources)+len(creditOwners) > 512 || owned > 1<<20 {
		return empty, hit, false, ErrUnqualified
	}
	sources = append(sources, creditOwners...)

	for _, relation := range []struct {
		name  string
		route ownerRelation
	}{{"song_artist", artistSongs}, {"authored_collection", authoredMembers}} {
		after := int64(0)
		count := 0
		for {
			statement := "full.native." + relation.name + ".first"
			args := []any{id}
			if after != 0 {
				statement = "full.native." + relation.name + ".next"
				args = append(args, after)
			}
			rows, e := q(statement, append(args, int64(64))...)
			if e != nil {
				return empty, hit, false, e
			}
			if len(rows) > 64 {
				return empty, hit, false, ErrUnqualified
			}
			for _, row := range rows {
				if len(row) != 1 {
					return empty, hit, false, ErrUnqualified
				}
				owner, ok := row[0].(int64)
				if !ok || owner <= after {
					return empty, hit, false, ErrUnqualified
				}
				count++
				if count > 128 {
					return empty, hit, false, fmt.Errorf("%w: complete owner cohort exceeds bounded workspace", ErrUnqualified)
				}
				if e = addOwner(owner, fullRouteKey{relation: relation.route, owner: owner}); e != nil {
					return empty, hit, false, e
				}
				after = owner
			}
			if len(rows) < 64 {
				break
			}
		}
	}
	automatic, err := nativeAutomaticOwners(q, in.Library, f.library, id)
	if err != nil {
		return empty, hit, false, err
	}
	for _, owner := range automatic {
		if err = addOwner(owner.Collection, fullRouteKey{relation: automaticMembers, owner: owner.Collection, automatic: owner.Route}); err != nil {
			return empty, hit, false, err
		}
	}
	proof, match, err := lead.prove(scope, fullResultKey{search.ManifestEntity, id, source.library}, sources, true)
	if err != nil || !match {
		return empty, hit, false, err
	}
	item, err := itemValue(source.item[:7])
	if err != nil {
		return empty, hit, false, err
	}
	hit = FullSearchHit{Subject: search.ManifestEntity, ID: id, Library: source.library, Kind: source.kind, Tier: proof.tier, Item: item}
	if err = nativeHitFacts(&hit, source); err != nil {
		return empty, FullSearchHit{}, false, err
	}
	return proof, hit, true, nil
}

// A distinct route: never substitute disc/track order for ID-search order.
const albumTracks ownerRelation = authoredMembers + 1
const automaticMembers ownerRelation = albumTracks + 1

func nativeOwnerPage(q browseQuery, relation ownerRelation, owner int64, after *ownerCursor) (ownerPage, error) {
	if relation != albumTracks {
		return readOwnerPage(q, relation, owner, after, 1)
	}
	if owner <= 0 || after != nil && (after.owner != owner || after.relation != relation || after.position.Work <= 0) {
		return ownerPage{}, ErrInput
	}
	statement := "full.native.album.first"
	args := []any{owner}
	if after != nil {
		statement = "full.native.album.next"
		args = append(args, after.position.Work)
	}
	rows, err := q(statement, append(args, int64(1))...)
	if err != nil {
		return ownerPage{}, err
	}
	if len(rows) > 1 {
		return ownerPage{}, ErrUnqualified
	}
	if len(rows) == 0 {
		return ownerPage{exhausted: true}, nil
	}
	if len(rows[0]) != 1 {
		return ownerPage{}, ErrUnqualified
	}
	id, ok := rows[0][0].(int64)
	if !ok || id <= 0 || after != nil && id >= after.position.Work {
		return ownerPage{}, ErrUnqualified
	}
	pos := ownerPosition{Work: id}
	return ownerPage{positions: []ownerPosition{pos}, after: &ownerCursor{relation, owner, pos}}, nil
}
func nativeExpandOne(ctx context.Context, q browseQuery, scope *fullQueryScope, f browseFence, in BrowseInput, lead nativeSearchLead, e *nativeExpansion, top *fullSearchTop) (bool, error) {
	// A People walk proves direct person candidates only. Entity sources and
	// the inverse work owners of a person can only yield entity hits, which
	// the People page never selects, so their credit cohorts are not walked.
	if scope.peopleOnly && e.source.Subject != search.ManifestPerson {
		return true, nil
	}
	// Reverse fan-out must not bypass the same preview budget merely because
	// one People/collection MATCH posting can own a million work positions.
	if scope.nativeWork != nil && e.direct {
		if err := scope.nativeWork.consume(1); err != nil {
			return false, err
		}
	}
	var source *nativeSource
	var err error
	if e.source.Subject == search.ManifestPerson {
		source, err = nativePersonSource(q, scope, e.source.SubjectID)
	} else {
		source, err = nativeEntitySource(q, scope, e.source.SubjectID)
	}
	if err != nil {
		return false, err
	}
	if source == nil {
		return false, ErrStale
	}
	if source.key.PublicationID != e.source.PublicationID || source.key.InputGeneration != e.source.InputGeneration {
		return false, ErrStale
	}
	allowed, name, err := nativeEligible(q, in, f, source)
	if err != nil {
		return false, err
	}
	if !allowed {
		return true, nil
	}
	emit := func(id int64, route fullRouteKey) error {
		if route.relation != 0 {
			work, err := nativeEntitySource(q, scope, id)
			if err != nil {
				return err
			}
			if work == nil {
				return nil
			}
			allowed, kind, err := nativeEligible(q, in, f, work)
			if err != nil {
				return err
			}
			if !allowed {
				return nil
			}
			switch route.relation {
			case personWorks:
				if work.item[11] != route.image {
					return nil
				}
				rows, err := q("full.native.credit.point", id, route.image, route.ordinal, route.owner)
				if err != nil {
					return err
				}
				if len(rows) == 0 {
					return nil
				}
				if len(rows) != 1 {
					return ErrUnqualified
				}
			case artistAlbums:
				if route.via == 0 {
					if kind != "album" || work.item[15] != route.owner {
						return nil
					}
				} else {
					if kind != "song" || work.item[13] != route.via {
						return nil
					}
				}
			case artistSongs:
				if kind != "song" {
					return nil
				}
			case albumTracks:
				if kind != "song" || work.item[13] != route.owner {
					return nil
				}
			case showChildren:
				if kind != "episode" && kind != "season" || work.item[14] != route.owner {
					return nil
				}
			}
		}
		var proof canonicalCandidateProof
		var hit FullSearchHit
		var matched bool
		var err error
		if route.relation == 0 && e.source.Subject == search.ManifestPerson {
			proof, hit, matched, err = nativePersonCandidate(ctx, q, scope, f, in, id, lead)
		} else {
			proof, hit, matched, err = nativeCandidate(ctx, q, scope, f, in, id, lead)
		}
		if err != nil {
			return err
		}
		if !matched {
			return nil
		}
		return top.observe(proof, e.source, route, hit)
	}
	if !e.direct {
		e.direct = true
		return false, emit(source.key.SubjectID, fullRouteKey{})
	}
	if scope.peopleOnly {
		return true, nil
	}
	if e.field > 4 {
		return true, nil
	}
	var relations []ownerRelation
	switch name {
	case "person":
		relations = []ownerRelation{personWorks}
	case "show":
		relations = []ownerRelation{showChildren}
	case "artist":
		relations = []ownerRelation{artistAlbums, artistSongs}
	case "album":
		relations = []ownerRelation{albumTracks}
	case "collection":
		relations = []ownerRelation{authoredMembers, automaticMembers}
	}
	if e.relation >= len(relations) {
		return true, nil
	}
	relation := relations[e.relation]
	if relation == automaticMembers {
		position, next, done, err := nativeAutomaticNext(q, in.Library, f.library, source.key.SubjectID, e.automatic)
		if err != nil {
			return false, err
		}
		e.automatic = next
		if done {
			e.relation++
			e.automatic = nil
			return e.relation >= len(relations), nil
		}
		// Work==0 still advances the raw mapping/evidence/override cursor. It
		// consumes this work quantum and all its native rows remain charged.
		if position.Work > 0 {
			if err = emit(position.Work, fullRouteKey{relation: automaticMembers, owner: source.key.SubjectID, automatic: position.Route}); err != nil {
				return false, err
			}
		}
		return false, nil
	}
	if e.album > 0 {
		page, err := nativeOwnerPage(q, albumTracks, e.album, e.albumCursor)
		if err != nil {
			return false, err
		}
		if len(page.positions) > 0 {
			if err = emit(page.positions[0].Work, fullRouteKey{relation: artistAlbums, owner: source.key.SubjectID, via: e.album}); err != nil {
				return false, err
			}
		}
		if page.exhausted {
			e.album = 0
			e.albumCursor = nil
		} else {
			e.albumCursor = page.after
		}
		return false, nil
	}
	page, err := nativeOwnerPage(q, relation, source.key.SubjectID, e.cursor)
	if err != nil {
		return false, err
	}
	if len(page.positions) > 0 {
		p := page.positions[0]
		if err = emit(p.Work, fullRouteKey{relation: relation, owner: source.key.SubjectID, ordinal: p.Ordinal, image: p.Image}); err != nil {
			return false, err
		}
		if name == "artist" && relation == artistAlbums {
			album, err := nativeEntitySource(q, scope, p.Work)
			if err != nil {
				return false, err
			}
			if album != nil {
				allowed, kind, err := nativeEligible(q, in, f, album)
				if err != nil {
					return false, err
				}
				if allowed && kind == "album" && album.item[15] == source.key.SubjectID {
					e.album = p.Work
				}
			}
		}
	}
	if page.exhausted {
		e.relation++
		e.cursor = nil
	} else {
		e.cursor = page.after
	}
	return e.relation >= len(relations) && e.album == 0, nil
}
