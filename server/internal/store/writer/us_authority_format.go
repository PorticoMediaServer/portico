package writer

import "fmt"

func (usOwnerAuthorityWitness) String() string   { return "usOwnerAuthorityWitness{redacted}" }
func (usOwnerAuthorityWitness) GoString() string { return "usOwnerAuthorityWitness{redacted}" }
func (usOwnerAuthorityWitness) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usOwnerAuthorityWitness")
}

func (usOwnerAuthorityProof) String() string   { return "usOwnerAuthorityProof{redacted}" }
func (usOwnerAuthorityProof) GoString() string { return "usOwnerAuthorityProof{redacted}" }
func (usOwnerAuthorityProof) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usOwnerAuthorityProof")
}

func (usOwnerAuthorityBudget) String() string   { return "usOwnerAuthorityBudget{redacted}" }
func (usOwnerAuthorityBudget) GoString() string { return "usOwnerAuthorityBudget{redacted}" }
func (usOwnerAuthorityBudget) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usOwnerAuthorityBudget")
}

func (usOwnerAuthorization) String() string   { return "usOwnerAuthorization{redacted}" }
func (usOwnerAuthorization) GoString() string { return "usOwnerAuthorization{redacted}" }
func (usOwnerAuthorization) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usOwnerAuthorization")
}
