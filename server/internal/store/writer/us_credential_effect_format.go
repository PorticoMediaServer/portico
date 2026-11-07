package writer

import "fmt"

func (usCredentialPostResult) String() string   { return "usCredentialPostResult{redacted}" }
func (usCredentialPostResult) GoString() string { return "usCredentialPostResult{redacted}" }
func (usCredentialPostResult) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usCredentialPostResult")
}
