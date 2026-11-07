package read

import (
	"fmt"
	"slices"
	"sync"
)

// readPolicyID and its binding are private compiled purposes. Network callers
// cannot supply a policy, register a validator or turn a refusal into permission.
// Domain readers select their own registered recipe in their own source files.
type readPolicyID uint16

// Stateful recipes accept only private, compiled domain work. The marker is
// deliberately unexported; it is neither a caller assertion nor a wire value.
type readPolicyState interface{ readPolicyState() }
type readPolicyBinding struct {
	recipe  readPolicyID
	subject int64
	state   readPolicyState
}
type readPolicyRecipe struct {
	id       readPolicyID
	domain   string
	kinds    []int64
	stateful bool
	// validate runs AFTER all common authority, birth, library and policy checks,
	// in BOTH initial and final snapshots. It must prove the complete eligibility
	// obligation of its closed domain; no caller-supplied proof flag is accepted.
	// A bounded comparable witness joins the common continuation/publication fence.
	validate func(browseQuery, BrowseInput, browseFence) (string, error)
}

var readPolicies = struct {
	sync.Mutex
	sealed  bool
	recipes map[readPolicyID]readPolicyRecipe
}{recipes: make(map[readPolicyID]readPolicyRecipe)}

// registerReadPolicy is init-only. Each compiled domain owns one registration
// file; adding a recipe never edits a shared list or opens a runtime extension.
func registerReadPolicy(p readPolicyRecipe) {
	readPolicies.Lock()
	defer readPolicies.Unlock()
	if readPolicies.sealed {
		panic("read policy registration after first use")
	}
	if p.domain == "" || p.validate == nil || len(p.kinds) == 0 {
		panic("incomplete read policy recipe")
	}
	if _, exists := readPolicies.recipes[p.id]; exists {
		panic("duplicate read policy recipe")
	}
	p.kinds = slices.Clone(p.kinds)
	slices.Sort(p.kinds)
	for n, kind := range p.kinds {
		if kind < 1 || kind > 99 || n > 0 && p.kinds[n-1] == kind {
			panic("invalid read policy kinds")
		}
	}
	readPolicies.recipes[p.id] = p
}
func resolveReadPolicy(in BrowseInput) (readPolicyRecipe, error) {
	readPolicies.Lock()
	defer readPolicies.Unlock()
	readPolicies.sealed = true
	p, ok := readPolicies.recipes[in.policy.recipe]
	if !ok || !slices.Contains(p.kinds, in.Kind) {
		return readPolicyRecipe{}, ErrUnqualified
	}
	if p.stateful != (in.policy.state != nil) {
		return readPolicyRecipe{}, ErrInput
	}
	// The legacy/default path has no subject-specific eligibility proof.
	if p.id == 0 && in.policy.subject != 0 {
		return readPolicyRecipe{}, ErrInput
	}
	return p, nil
}
func validateReadPolicy(q browseQuery, in BrowseInput, f *browseFence) error {
	p, err := resolveReadPolicy(in)
	if err != nil {
		return err
	}
	witness, err := p.validate(q, in, *f)
	if err != nil {
		return err
	}
	if len(witness) > 4096 {
		return fmt.Errorf("%w: read policy witness budget", ErrUnqualified)
	}
	f.policy = p.id
	f.policySubject = in.policy.subject
	f.policyWitness = witness
	return nil
}
