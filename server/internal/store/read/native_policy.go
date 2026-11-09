package read

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"time"
)

// Search chooses a compiled domain from the actual source kind, never from a
// request's selector. A domain opts in at init; catalogue is the closed fallback.
func bindNativeSearchPolicy(kind, subject int64) (readPolicyBinding, error) {
	if subject <= 0 {
		return readPolicyBinding{}, ErrInput
	}
	readPolicies.Lock()
	defer readPolicies.Unlock()
	readPolicies.sealed = true
	for _, p := range readPolicies.recipes {
		if p.nativeSearch && slices.Contains(p.kinds, kind) {
			return readPolicyBinding{recipe: p.id, subject: subject, nativeSearch: true}, nil
		}
	}
	if p, ok := readPolicies.recipes[0]; ok && slices.Contains(p.kinds, kind) {
		return readPolicyBinding{subject: subject, nativeSearch: true}, nil
	}
	return readPolicyBinding{}, ErrUnqualified
}

// Comparable bounded values only. No physical rows, query executor or caller
// proof flag escapes the read window. The policy witness is capped at4096 by
// validateReadPolicy; all other fields have fixed width.
type nativePolicyWitness struct {
	valid                                          bool
	id, kind, library, revision, ownAge, container int64
	kindSource                                     [32]byte
	creditSource                                   [32]byte
	source                                         [32]byte
	birth, public                                  [16]byte
	binding                                        readPolicyBinding
	witness                                        string
	personSource                                   [32]byte
	personBank                                     int64
	person, image, ordinal                         int64 // People additionally prove a current credit.
}

func nativePolicyForSource(q browseQuery, in BrowseInput, f browseFence, source *nativeSource) (nativePolicyWitness, string, bool, error) {
	var proof nativePolicyWitness
	if source == nil || source.library != in.Library {
		return proof, "", false, nil
	}
	r := source.item
	if len(r) != 19 {
		return proof, "", false, ErrUnqualified
	}
	id, a := r[0].(int64)
	retired, b := r[4].(int64)
	revision, c := r[5].(int64)
	kind, d := r[7].(int64)
	library, e := r[8].(int64)
	birth, g := r[9].([]byte)
	public, h := r[2].([]byte)
	if !a || !b || !c || !d || !e || !g || !h || id != source.key.SubjectID || kind != source.kind || library != source.library || revision <= 0 || len(birth) != 16 || len(public) != 16 {
		return proof, "", false, ErrUnqualified
	}
	if retired != 0 {
		return proof, "", false, nil
	}
	age := int64(-1)
	if r[6] != nil {
		var ok bool
		age, ok = r[6].(int64)
		if !ok || age < 0 {
			return proof, "", false, ErrUnqualified
		}
	}
	rows, err := q("full.native.kind", kind)
	if err != nil {
		return proof, "", false, err
	}
	if len(rows) != 1 || len(rows[0]) != 3 || rows[0][0] != kind {
		return proof, "", false, ErrUnqualified
	}
	name, ok := rows[0][1].(string)
	container, ok2 := rows[0][2].(int64)
	if !ok || !ok2 || container < 0 || container > 1 {
		return proof, "", false, ErrUnqualified
	}
	in.Kind = kind
	in.policy, err = bindNativeSearchPolicy(kind, id)
	if err != nil {
		return proof, "", false, err
	}
	f.kind, f.container = kind, container
	if err = validateReadPolicy(q, in, &f); err != nil {
		// The compiled per-item recipe refuses an item this viewer may not see
		// (retired, over the profile age, lacking attribution, or without a
		// native-format edition). Native search treats that exactly like the
		// admittedItem rejection below: the item is not a hit, and one hidden
		// item cannot refuse the page. Retained and final rechecks still turn
		// an inadmissible admitted item into ErrDenied in validateNativePolicy.
		if errors.Is(err, ErrDenied) {
			return proof, "", false, nil
		}
		return proof, "", false, err
	}
	// Containers require their domain's complete eligibility proof. Their own
	// age is neither permission nor a substitute for visible-member semantics.
	if container == 0 && !admittedItem(r, f) {
		return proof, "", false, nil
	}
	proof = nativePolicyWitness{valid: true, id: id, kind: kind, library: library, revision: revision, ownAge: age, container: container, binding: in.policy, witness: strings.Clone(f.policyWitness)}
	proof.kindSource, err = nativeItemIdentity(rows[0])
	if err != nil {
		return nativePolicyWitness{}, "", false, err
	}
	proof.source, err = nativeItemIdentity(r)
	if err != nil {
		return nativePolicyWitness{}, "", false, err
	}
	if r[11] != nil {
		var valid bool
		proof.creditSource, valid, err = nativeCreditImageProof(q, r)
		if err != nil {
			return nativePolicyWitness{}, "", false, err
		}
		if !valid {
			return nativePolicyWitness{}, "", false, errFullSearchPending
		}
	}
	copy(proof.birth[:], birth)
	copy(proof.public[:], public)
	return proof, name, true, nil
}

func validateNativePolicy(q browseQuery, in BrowseInput, f browseFence, prior nativePolicyWitness) error {
	if !prior.valid {
		return ErrUnqualified
	}
	rows, err := q("full.native.item", prior.id)
	if err != nil {
		return err
	}
	if len(rows) != 1 || len(rows[0]) != 19 {
		return ErrDenied
	}
	source := &nativeSource{kind: prior.kind, library: prior.library, item: rows[0]}
	source.key.SubjectID = prior.id
	current, _, allowed, err := nativePolicyForSource(q, in, f, source)
	if errors.Is(err, errFullSearchPending) {
		return ErrStale
	}
	if err != nil {
		return err
	}
	if !allowed {
		return ErrDenied
	}
	current.person, current.image, current.ordinal = prior.person, prior.image, prior.ordinal
	current.personSource, current.personBank = prior.personSource, prior.personBank
	if current != prior {
		return ErrStale
	}
	if prior.person > 0 {
		person, err := nativePersonSource(q, &fullQueryScope{bank: prior.personBank}, prior.person)
		if errors.Is(err, errFullSearchPending) {
			return ErrStale
		}
		if err != nil {
			return err
		}
		if person == nil || person.personSource != prior.personSource {
			return ErrStale
		}
		if rows[0][11] != prior.image {
			return ErrStale
		}
		credit, err := q("full.native.credit.point", prior.id, prior.image, prior.ordinal, prior.person)
		if err != nil {
			return err
		}
		if len(credit) != 1 {
			return ErrDenied
		}
	}
	return nil
}

func nativeHitFacts(hit *FullSearchHit, source *nativeSource) error {
	if source == nil || len(source.item) != 19 {
		return ErrUnqualified
	}
	key, a := source.item[1].(string)
	year, b := source.item[17].(int64)
	if !a || !b || len(key) > 65536 || year < 0 {
		return ErrUnqualified
	}
	added := ""
	if source.item[18] != nil {
		var ok bool
		added, ok = source.item[18].(string)
		if !ok || len(added) != 30 {
			return ErrUnqualified
		}
		if _, err := time.Parse(time.RFC3339Nano, added); err != nil {
			return ErrUnqualified
		}
	}
	hit.SortKey, hit.Year, hit.AddedAt = strings.Clone(key), year, strings.Clone(added)
	hit.permission = source.permission
	return nil
}

// Bound total retained witnesses independently of stamp count. Prior stamps
// are comparison inputs; this read still performs all current checks itself.
func nativeRetainedPolicies(stamps []*FullSearchReadStamp, fence nativeSearchFence, query string, pool *NativeFullSearchReader) ([]nativePolicyWitness, []nativeStampedHit, error) {
	if len(stamps) > MaxNativeRetained {
		return nil, nil, ErrInput
	}
	var proofs []nativePolicyWitness
	var hits []nativeStampedHit
	for _, stamp := range stamps {
		if stamp == nil || stamp.pool != pool.pool || nativeCommonFence(stamp.fence) != nativeCommonFence(fence) || stamp.query != query {
			return nil, nil, ErrStale
		}
		if stamp.MetadataBytes() > MaxNativeStampBytes {
			return nil, nil, ErrInput
		}
		for _, hit := range stamp.hits {
			if !slices.Contains(hits, hit) {
				hits = append(hits, hit)
			}
			if len(hits) > MaxNativeRetained {
				return nil, nil, ErrInput
			}
		}
		for _, p := range stamp.policies {
			if !p.valid || len(p.witness) > 4096 {
				return nil, nil, ErrInput
			}
			if !slices.Contains(proofs, p) {
				proofs = append(proofs, p)
			}
			if len(proofs) > MaxNativeRetained {
				return nil, nil, ErrInput
			}
		}
	}
	return proofs, hits, nil
}

// A separate full PK point avoids a tiny joined-header scan. The returned
// header is checked against the current same-snapshot item's logical identity,
// immutable birth and owner revision before any credit can authorize a match.
func nativeCreditImageProof(q browseQuery, item []any) ([32]byte, bool, error) {
	if len(item) != 19 {
		return [32]byte{}, false, ErrUnqualified
	}
	image, ok := item[11].(int64)
	if !ok || image <= 0 {
		return [32]byte{}, false, ErrUnqualified
	}
	rows, err := q("full.native.credit.image", image)
	if err != nil {
		return [32]byte{}, false, err
	}
	if len(rows) == 0 {
		return [32]byte{}, false, nil
	}
	if len(rows) != 1 || len(rows[0]) != 6 {
		return [32]byte{}, false, ErrUnqualified
	}
	r := rows[0]
	birth, a := r[2].([]byte)
	expected, b := item[9].([]byte)
	revision, c := r[5].(int64)
	if !a || !b || !c || len(birth) != 16 || len(expected) != 16 || revision <= 0 {
		return [32]byte{}, false, ErrUnqualified
	}
	if r[0] != image || r[1] != item[0] || !bytes.Equal(birth, expected) || r[3] != item[12] || r[4] != int64(1) {
		return [32]byte{}, false, nil
	}
	digest, err := nativeItemIdentity(r)
	return digest, err == nil, err
}
