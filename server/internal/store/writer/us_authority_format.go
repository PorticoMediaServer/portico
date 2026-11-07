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

func (usPolicyAuthorityBudget) String() string   { return "usPolicyAuthorityBudget{redacted}" }
func (usPolicyAuthorityBudget) GoString() string { return "usPolicyAuthorityBudget{redacted}" }
func (usPolicyAuthorityBudget) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usPolicyAuthorityBudget")
}

func (usPolicyAuthorization) String() string   { return "usPolicyAuthorization{redacted}" }
func (usPolicyAuthorization) GoString() string { return "usPolicyAuthorization{redacted}" }
func (usPolicyAuthorization) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usPolicyAuthorization")
}

func (usPolicyAuthorityStamp) String() string   { return "usPolicyAuthorityStamp{redacted}" }
func (usPolicyAuthorityStamp) GoString() string { return "usPolicyAuthorityStamp{redacted}" }
func (usPolicyAuthorityStamp) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usPolicyAuthorityStamp")
}

func (usPolicyNativePermit) String() string   { return "usPolicyNativePermit{redacted}" }
func (usPolicyNativePermit) GoString() string { return "usPolicyNativePermit{redacted}" }
func (usPolicyNativePermit) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usPolicyNativePermit")
}
