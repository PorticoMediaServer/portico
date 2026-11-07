package writer

import "fmt"

func (usSessionPins) String() string                 { return "usSessionPins{redacted}" }
func (usSessionPins) GoString() string               { return "usSessionPins{redacted}" }
func (usSessionPins) Format(state fmt.State, _ rune) { formatUSPreparation(state, "usSessionPins") }

func (usSessionPreparedOutput) String() string   { return "usSessionPreparedOutput{redacted}" }
func (usSessionPreparedOutput) GoString() string { return "usSessionPreparedOutput{redacted}" }
func (usSessionPreparedOutput) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usSessionPreparedOutput")
}

func (usSessionRotationRequest) String() string   { return "usSessionRotationRequest{redacted}" }
func (usSessionRotationRequest) GoString() string { return "usSessionRotationRequest{redacted}" }
func (usSessionRotationRequest) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usSessionRotationRequest")
}

func (usSessionClocks) String() string                 { return "usSessionClocks{redacted}" }
func (usSessionClocks) GoString() string               { return "usSessionClocks{redacted}" }
func (usSessionClocks) Format(state fmt.State, _ rune) { formatUSPreparation(state, "usSessionClocks") }

func (usSessionRotation) String() string   { return "usSessionRotation{redacted}" }
func (usSessionRotation) GoString() string { return "usSessionRotation{redacted}" }
func (usSessionRotation) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usSessionRotation")
}

func (usSessionNativeResult) String() string   { return "usSessionNativeResult{redacted}" }
func (usSessionNativeResult) GoString() string { return "usSessionNativeResult{redacted}" }
func (usSessionNativeResult) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usSessionNativeResult")
}
