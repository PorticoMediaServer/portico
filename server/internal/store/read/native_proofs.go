package read

import (
	"bytes"
	"context"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
	"slices"
)

func nativeEligible(q browseQuery, in BrowseInput, f browseFence, source *nativeSource) (bool, string, error) {
	if source == nil || source.library != in.Library {
		return false, "", nil
	}
	if len(source.item) != 17 || !admittedItem(source.item, f) {
		return false, "", nil
	}
	rows, err := q("full.native.kind", source.kind)
	if err != nil {
		return false, "", err
	}
	if len(rows) != 1 || len(rows[0]) != 3 || rows[0][0] != source.kind {
		return false, "", ErrUnqualified
	}
	name, ok := rows[0][1].(string)
	container, ok2 := rows[0][2].(int64)
	if !ok || !ok2 {
		return false, "", ErrUnqualified
	}
	f.kind, f.container = source.kind, container
	in.Kind = source.kind
	if err := validateReadPolicy(q, in, &f); err != nil {
		return false, "", err
	}
	return true, name, nil
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
	if own {
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
func nativeCandidate(ctx context.Context, q browseQuery, scope *fullQueryScope, f browseFence, in BrowseInput, id int64, lead exactTermPopulation) (canonicalCandidateProof, FullSearchHit, bool, error) {
	var empty canonicalCandidateProof
	var hit FullSearchHit
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
	if name == "movie" || name == "collection" {
		return empty, hit, false, fmt.Errorf("%w: automatic collection inverse proof not composed", ErrUnqualified)
	}
	leadIndex := slices.Index(scope.terms, lead.term)
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
	image, e := integer(11)
	if e != nil {
		return empty, hit, false, e
	}
	if image > 0 {
		rows, e := q("full.native.credit.first", id, image, int64(1))
		if e != nil {
			return empty, hit, false, e
		}
		if len(rows) > 0 {
			return empty, hit, false, fmt.Errorf("%w: People search birth carrier is not adopted", ErrUnqualified)
		}
	}
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
	proof, match, err := proveCandidate(scope, fullResultKey{search.ManifestEntity, id, source.library}, lead, sources, true)
	if err != nil || !match {
		return empty, hit, false, err
	}
	item, err := itemValue(source.item[:7])
	if err != nil {
		return empty, hit, false, err
	}
	hit = FullSearchHit{Subject: search.ManifestEntity, ID: id, Library: source.library, Kind: source.kind, Tier: proof.tier, Item: item}
	return proof, hit, true, nil
}

// A distinct route: never substitute disc/track order for ID-search order.
const albumTracks ownerRelation = authoredMembers + 1

func nativeExpandOne(ctx context.Context, q browseQuery, scope *fullQueryScope, f browseFence, in BrowseInput, lead exactTermPopulation, e *nativeExpansion, top *fullSearchTop) (bool, error) {
	source, err := nativeEntitySource(q, scope, e.source.SubjectID)
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
		proof, hit, matched, err := nativeCandidate(ctx, q, scope, f, in, id, lead)
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
	if e.field > 4 {
		return true, nil
	}
	var relations []ownerRelation
	switch name {
	case "show":
		relations = []ownerRelation{showChildren}
	case "artist":
		relations = []ownerRelation{artistAlbums, artistSongs}
	case "album":
		return false, fmt.Errorf("%w: album ID-search index is not adopted", ErrUnqualified)
	case "collection":
		return false, fmt.Errorf("%w: automatic collection forward proof not composed", ErrUnqualified)
	}
	if e.relation >= len(relations) {
		return true, nil
	}
	relation := relations[e.relation]
	page, err := readOwnerPage(q, relation, source.key.SubjectID, e.cursor, 1)
	if err != nil {
		return false, err
	}
	if len(page.positions) > 0 {
		p := page.positions[0]
		if err = emit(p.Work, fullRouteKey{relation: relation, owner: source.key.SubjectID, ordinal: p.Ordinal, image: p.Image}); err != nil {
			return false, err
		}
		if name == "artist" && relation == artistAlbums {
			return false, fmt.Errorf("%w: album ID-search index is not adopted", ErrUnqualified)
		}
	}
	if page.exhausted {
		e.relation++
		e.cursor = nil
	} else {
		e.cursor = page.after
	}
	return e.relation >= len(relations), nil
}
