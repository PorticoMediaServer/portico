// Package qualification is the sealed handoff between stmt and its native
// plan capturer. Go's internal boundary excludes domain registration callers.
package qualification

import "crypto/sha256"

// Receipt has no exported implementation or usable zero value. The concrete
// type is checked as well as the seal, so embedding this interface cannot forge
// a receipt. JSON/golden data never reconstructs a live capability.
type Receipt interface{ capturedStatementReceipt() }

type receipt struct {
	definitions map[string][32]byte
	plans       [32]byte
	sources     [32]byte
	sealed      bool
}

func (*receipt) capturedStatementReceipt() {}

// Mint is called only after Capture completes every native check. The separate
// stmttest fixture adapter may call it only inside a Go test binary; it issues
// unit-fixture evidence, never production qualification.
func Mint(definitions map[string][]byte, plans, sources []byte) Receipt {
	if len(definitions) == 0 || len(plans) == 0 || len(sources) == 0 {
		return nil
	}
	r := &receipt{definitions: make(map[string][32]byte, len(definitions)), plans: sha256.Sum256(plans), sources: sha256.Sum256(sources), sealed: true}
	for id, definition := range definitions {
		if id == "" || len(definition) == 0 {
			return nil
		}
		r.definitions[id] = sha256.Sum256(definition)
	}
	return r
}

func Allows(proof Receipt, id string, definition []byte) bool {
	r, ok := proof.(*receipt)
	if !ok || r == nil || !r.sealed {
		return false
	}
	want, ok := r.definitions[id]
	return ok && want == sha256.Sum256(definition)
}
