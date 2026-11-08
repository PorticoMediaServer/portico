package read

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	rsCollectionComputePolicy  readPolicyID = 43
	rsCollectionDeliveryPolicy readPolicyID = 44
)

func init() {
	for _, id := range []readPolicyID{rsCollectionComputePolicy, rsCollectionDeliveryPolicy} {
		registerReadPolicy(readPolicyRecipe{id: id, domain: "rs-complete-collection", kinds: []int64{10}, stateful: true, validate: rsCollectionEligibility})
	}
}

func (*CollectionPreparation) readPolicyState() {}

// Only Advance selects compute; it exposes no members, root metadata, count or
// continuation. Detail selects delivery after COMPLETE membership reduction.
// Both paths still run the unchanged common authority/birth/library fence and
// prove the captured root's accepted admission tuple/publication again.
func rsCollectionEligibility(q browseQuery, in BrowseInput, f browseFence) (string, error) {
	p, ok := in.policy.state.(*CollectionPreparation)
	if !ok || p == nil || p.closed || p.authority == nil || p.lease == nil || in.policy.subject != p.in.Collection || in.Library != p.in.View.Library || in.Database != p.in.View.Database || in.TokenHash != p.in.View.TokenHash || in.Kind != 10 || f.container != 1 {
		return "", ErrInput
	}
	if err := p.authority.CheckPreparation(p.lease); err != nil {
		return "", err
	}
	if p.fence != nil && !rsCollectionSameFence(*p.fence, f) {
		return "", ErrStale
	}
	switch in.policy.recipe {
	case rsCollectionComputePolicy:
		if p.ready {
			return "", ErrInput
		}
	case rsCollectionDeliveryPolicy:
		if !p.ready || p.complete != rsCollectionDone || p.fence == nil || p.source == nil || p.proof != rsCollectionProof(p) {
			return "", ErrUnqualified
		}
		// Truly empty detail is permitted, just as EntityRestrictionSQL permits it.
		// A nonempty logical collection whose complete viewer set is empty is hidden.
		if p.nonempty && len(p.nodes) == 0 {
			return "", ErrDenied
		}
	default:
		return "", ErrInput
	}
	// The complete enumeration captured the current root. Its accepted admission
	// tuple and active publication are rechecked here, in the same snapshot as
	// every other first/final fence. Before the first capture the root read owns
	// this proof.
	if p.source != nil {
		if _, err := rsCollectionRootProof(q, p.source, f); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%#v/%x", p.source, p.proof)))), nil
}

// rsCollectionRootProof applies the merged container admission band to the
// captured current source row and, when one is installed, its active accepted
// aggregate publication. The root's own age never certifies membership; this
// only proves that the container itself is currently viewable.
func rsCollectionRootProof(q browseQuery, source []any, f browseFence) (string, error) {
	if len(source) < 15 || source[1].(int64) != 0 {
		return "", ErrDenied
	}
	count, unrated := source[10].(int64), source[11].(int64)
	if count < 0 || unrated < 0 || unrated > count {
		return "", ErrUnqualified
	}
	if count > unrated && source[12] == nil {
		return "", ErrUnqualified
	}
	if count == unrated && source[12] != nil {
		return "", ErrUnqualified
	}
	rows := source
	if source[9] != nil {
		publication, err := q("rs.subject.publication", source[9], source[0])
		if err != nil {
			return "", err
		}
		if len(publication) != 1 {
			return "", ErrUnqualified
		}
		p := publication[0]
		if p[0] != "published" && p[0] != "folding" {
			return "", ErrUnqualified
		}
		if !equalSource(source[10:15], p[3:8]) {
			return "", ErrUnqualified
		}
		rows = append(append([]any(nil), source...), p...)
	}
	permitted := count == 0 || f.allowUnrated != 0 && unrated > 0 || source[12] != nil && (f.maximumAge < 0 || source[12].(int64) <= f.maximumAge)
	if !permitted {
		return "", ErrDenied
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%#v", rows)))), nil
}

func rsCollectionSameFence(a, b browseFence) bool {
	a.policy, b.policy = 0, 0
	a.policySubject, b.policySubject = 0, 0
	a.policyWitness, b.policyWitness = "", ""
	return a == b
}

// rsCollectionProof binds the exact ordered viewer set to the ready tree. A
// changed, reordered, retired or newly visible member changes this digest.
func rsCollectionProof(p *CollectionPreparation) [32]byte {
	h := sha256.New()
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(len(p.nodes)))
	_, _ = h.Write(buf[:])
	if p.nonempty {
		_, _ = h.Write([]byte{1})
	} else {
		_, _ = h.Write([]byte{0})
	}
	for _, n := range p.nodes {
		binary.LittleEndian.PutUint64(buf[:], uint64(n.id))
		_, _ = h.Write(buf[:])
		binary.LittleEndian.PutUint64(buf[:], uint64(n.kind))
		_, _ = h.Write(buf[:])
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// Authored members use their own complete accepted subject tuple, never the
// collection's own age or a child container's own scalar label. Inferred R21
// members are automatic and refuse here. Unsupported nested automatic/Reading
// proofs refuse the whole operation rather than reporting a false empty or
// incomplete exact set.
func rsCollectionMemberEligible(q browseQuery, row []any, in BrowseInput, f browseFence) (bool, error) {
	if row[1].(int64) != 0 {
		return false, nil
	}
	kind := row[3].(int64)
	if kind < 1 || kind > 13 {
		return false, ErrUnqualified
	}
	child := in
	child.Kind = kind
	child.policy = readPolicyBinding{recipe: rsDetailSubjectPolicy, subject: row[0].(int64)}
	f.kind = kind
	f.container = row[4].(int64)
	_, err := rsDetailSubjectEligibility(q, child, f)
	if errors.Is(err, ErrDenied) {
		return false, nil
	}
	return err == nil, err
}
