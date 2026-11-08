package read

import (
	"crypto/sha256"
	"fmt"
)

// rbContainerMetricsPolicy is a closed container-subject recipe. It is not a
// caller permission flag and it never opens the leaf/default browse recipe.
const rbContainerMetricsPolicy readPolicyID = 32560

// errRBContainerMetricsUSPolicy is the packet's named refusal. US owns accepted
// account-limit and blocked-label authority semantics. Until its read ABI lands,
// this recipe can only prove the default (unrestricted) container policy; it must
// never infer restricted member eligibility from a source dictionary, a child's
// own age or the container's own admission scalar.
var errRBContainerMetricsUSPolicy = fmt.Errorf("%w: US accepted account-limit/blocked-label policy facts are not landed", ErrUnqualified)

func init() {
	registerReadPolicy(readPolicyRecipe{id: rbContainerMetricsPolicy, domain: "rb-container-metrics", kinds: []int64{2, 3, 6, 8, 10}, validate: rbContainerMetricsEligibility})
}

// rbContainerMetricsEligibility proves the exact container subject and refuses
// every policy this recipe cannot yet evaluate. The common fence has already
// refused non-default labels, unsupported roles and torn libraries.
func rbContainerMetricsEligibility(q browseQuery, in BrowseInput, f browseFence) (string, error) {
	if in.policy.subject <= 0 || f.container == 0 {
		return "", ErrInput
	}
	if f.maximumAge >= 0 || f.allowUnrated == 0 {
		return "", errRBContainerMetricsUSPolicy
	}
	name := "rb.container_metrics.point"
	if in.Kind == 10 {
		// Authored collection members are only a complete source when no
		// automatic/inferred membership exists for the collection.
		name = "rb.container_metrics.point.collection"
	}
	rows, err := q(name, in.policy.subject, in.Library, in.Kind)
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", ErrDenied
	}
	if len(rows) != 1 {
		// A collection with automatic/inferred membership cannot be proved by
		// an authored-member walk; refuse rather than publish a partial set.
		return "", ErrUnqualified
	}
	r := rows[0]
	if r[3].(int64) != 0 {
		return "", ErrDenied
	}
	public, ok := r[4].([]byte)
	if !ok || len(public) != 16 {
		return "", ErrUnqualified
	}
	incarnation, ok := r[5].([]byte)
	if !ok || len(incarnation) != 16 {
		return "", ErrUnqualified
	}
	// The bounded comparable witness includes identity, incarnation and every
	// accepted source revision, never a child metric or a caller assertion.
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%#v", rows)))), nil
}
