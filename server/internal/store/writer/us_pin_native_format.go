package writer

import "fmt"

func (usPINKeepProof) String() string                 { return "usPINKeepProof{redacted}" }
func (usPINKeepProof) GoString() string               { return "usPINKeepProof{redacted}" }
func (usPINKeepProof) Format(state fmt.State, _ rune) { formatUSPreparation(state, "usPINKeepProof") }

func (usPINFenceScope) String() string                 { return "usPINFenceScope{redacted}" }
func (usPINFenceScope) GoString() string               { return "usPINFenceScope{redacted}" }
func (usPINFenceScope) Format(state fmt.State, _ rune) { formatUSPreparation(state, "usPINFenceScope") }
