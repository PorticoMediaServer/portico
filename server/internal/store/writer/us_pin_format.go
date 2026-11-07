package writer

import "fmt"

func (usPINRequest) String() string                 { return "usPINRequest{redacted}" }
func (usPINRequest) GoString() string               { return "usPINRequest{redacted}" }
func (usPINRequest) Format(state fmt.State, _ rune) { formatUSPreparation(state, "usPINRequest") }

func (usPINPrepared) String() string                 { return "usPINPrepared{redacted}" }
func (usPINPrepared) GoString() string               { return "usPINPrepared{redacted}" }
func (usPINPrepared) Format(state fmt.State, _ rune) { formatUSPreparation(state, "usPINPrepared") }

func (usPINCommandRequest) String() string   { return "usPINCommandRequest{redacted}" }
func (usPINCommandRequest) GoString() string { return "usPINCommandRequest{redacted}" }
func (usPINCommandRequest) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usPINCommandRequest")
}

func (usPINCommand) String() string                 { return "usPINCommand{redacted}" }
func (usPINCommand) GoString() string               { return "usPINCommand{redacted}" }
func (usPINCommand) Format(state fmt.State, _ rune) { formatUSPreparation(state, "usPINCommand") }
