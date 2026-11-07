package writer

import "fmt"

func (usInvitationScope) String() string   { return "usInvitationScope{redacted}" }
func (usInvitationScope) GoString() string { return "usInvitationScope{redacted}" }
func (usInvitationScope) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usInvitationScope")
}

func (usInvitationAccess) String() string   { return "usInvitationAccess{redacted}" }
func (usInvitationAccess) GoString() string { return "usInvitationAccess{redacted}" }
func (usInvitationAccess) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usInvitationAccess")
}

func (usInvitationCreateInput) String() string   { return "usInvitationCreateInput{redacted}" }
func (usInvitationCreateInput) GoString() string { return "usInvitationCreateInput{redacted}" }
func (usInvitationCreateInput) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usInvitationCreateInput")
}

func (usInvitationEditInput) String() string   { return "usInvitationEditInput{redacted}" }
func (usInvitationEditInput) GoString() string { return "usInvitationEditInput{redacted}" }
func (usInvitationEditInput) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usInvitationEditInput")
}

func (usInvitationPrepared) String() string   { return "usInvitationPrepared{redacted}" }
func (usInvitationPrepared) GoString() string { return "usInvitationPrepared{redacted}" }
func (usInvitationPrepared) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usInvitationPrepared")
}

func (usInvitationCommand) String() string   { return "usInvitationCommand{redacted}" }
func (usInvitationCommand) GoString() string { return "usInvitationCommand{redacted}" }
func (usInvitationCommand) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usInvitationCommand")
}
