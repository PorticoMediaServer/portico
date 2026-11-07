package writer

import "fmt"

func (usProfileTarget) String() string                 { return "usProfileTarget{redacted}" }
func (usProfileTarget) GoString() string               { return "usProfileTarget{redacted}" }
func (usProfileTarget) Format(state fmt.State, _ rune) { formatUSPreparation(state, "usProfileTarget") }

func (usProfilePrepared) String() string   { return "usProfilePrepared{redacted}" }
func (usProfilePrepared) GoString() string { return "usProfilePrepared{redacted}" }
func (usProfilePrepared) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usProfilePrepared")
}
