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
// Both paths still run the unchanged common authority/birth/library fence.
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
	rows, err := q("rs.auto.source", in.policy.subject, in.Library)
	if err != nil {
		return "", err
	}
	if len(rows) != 1 || rows[0][8] != nil && rows[0][8].(int64) != 0 {
		return "", ErrDenied
	}
	if p.source != nil && (len(p.source) < len(rows[0]) || !equalSource(p.source[:len(rows[0])], rows[0])) {
		return "", ErrStale
	}
	switch in.policy.recipe {
	case rsCollectionComputePolicy:
		if p.ready {
			return "", ErrInput
		}
	case rsCollectionDeliveryPolicy:
		if !p.ready || p.cursor.stage != rsAutoDone || p.fence == nil || p.source == nil || p.proof != rsCollectionProof(p) {
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
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%#v/%x", rows, p.proof)))), nil
}

func rsCollectionSameFence(a, b browseFence) bool {
	a.policy, b.policy = 0, 0
	a.policySubject, b.policySubject = 0, 0
	a.policyWitness, b.policyWitness = "", ""
	return a == b
}
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
	for n := range p.nodes {
		binary.LittleEndian.PutUint64(buf[:], uint64(rsNodeRank(p.nodes, p.root, n)))
		_, _ = h.Write(buf[:])
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// Authored members use their own complete accepted subject tuple, never the
// collection's own age or a child container's own scalar label. Inferred R21
// members are movies. Unsupported nested automatic/Reading proofs refuse the
// whole operation rather than reporting a false empty or incomplete exact set.
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
