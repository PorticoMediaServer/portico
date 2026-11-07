package writer

import "fmt"

func (usMemberTarget) String() string                 { return "usMemberTarget{redacted}" }
func (usMemberTarget) GoString() string               { return "usMemberTarget{redacted}" }
func (usMemberTarget) Format(state fmt.State, _ rune) { formatUSPreparation(state, "usMemberTarget") }

func (usMemberRequest) String() string                 { return "usMemberRequest{redacted}" }
func (usMemberRequest) GoString() string               { return "usMemberRequest{redacted}" }
func (usMemberRequest) Format(state fmt.State, _ rune) { formatUSPreparation(state, "usMemberRequest") }

func (usMemberPrepared) String() string   { return "usMemberPrepared{redacted}" }
func (usMemberPrepared) GoString() string { return "usMemberPrepared{redacted}" }
func (usMemberPrepared) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usMemberPrepared")
}

func (usAccountEpochPrepared) String() string   { return "usAccountEpochPrepared{redacted}" }
func (usAccountEpochPrepared) GoString() string { return "usAccountEpochPrepared{redacted}" }
func (usAccountEpochPrepared) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usAccountEpochPrepared")
}
