package writer

import "fmt"

func (usPolicyIssuer) String() string                 { return "usPolicyIssuer{redacted}" }
func (usPolicyIssuer) GoString() string               { return "usPolicyIssuer{redacted}" }
func (usPolicyIssuer) Format(state fmt.State, _ rune) { formatUSPreparation(state, "usPolicyIssuer") }

func (usLibraryPolicyRequest) String() string   { return "usLibraryPolicyRequest{redacted}" }
func (usLibraryPolicyRequest) GoString() string { return "usLibraryPolicyRequest{redacted}" }
func (usLibraryPolicyRequest) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usLibraryPolicyRequest")
}

func (usLibraryPolicyCommand) String() string   { return "usLibraryPolicyCommand{redacted}" }
func (usLibraryPolicyCommand) GoString() string { return "usLibraryPolicyCommand{redacted}" }
func (usLibraryPolicyCommand) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usLibraryPolicyCommand")
}
