package writer

import (
	"fmt"
	"io"
)

// Logging preparation must never traverse credentials, protected wire or issuer
// witnesses. Ignore flags/precision for verbs routed through Format. Go fmt
// handles %p separately: use it only with pointers, never request values.
// This changes no canonical request encoding or wire DTO.
func formatUSPreparation(state fmt.State, label string) {
	_, _ = io.WriteString(state, label+"{redacted}")
}
