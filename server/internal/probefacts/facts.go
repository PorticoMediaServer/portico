// Package probefacts defines bounded media observations. These values do not
// grant publication authority; the root publisher supplies current catalog and
// attempt fences and chooses the presentation clock mapping.
package probefacts

import "github.com/PorticoMediaServer/portico/server/internal/producerinput"

const (
	SchemaVersion   = 1
	MaxOutputBytes  = 1 << 20
	MaxStreams      = 8192
	MaxFormatNames  = 8
	MaxNameBytes    = 64
	MaxVersionBytes = 4096
)

// Rational is reduced, with a positive denominator. An absent observation uses
// nil rather than zero. Publishers must check overflow when mapping timestamps.
type Rational struct{ Numerator, Denominator int64 }

// ToolIdentity is supplied by the qualified runtime, never source metadata.
// ExecutableSHA256 is lowercase 64-character hex; Version is bounded UTF-8.
type ToolIdentity struct{ ExecutableSHA256, Version string }

type FormatFacts struct {
	Names                         []string
	StartSeconds, DurationSeconds *Rational
}

type StreamFacts struct {
	Index                             int64
	Kind, Codec                       string
	TimeBase                          *Rational
	StartTimestamp, DurationTimestamp *int64
	Default                           bool
	Video                             *VideoFacts
	Audio                             *AudioFacts
}

type VideoFacts struct{ Width, Height int64 }
type AudioFacts struct{ SampleRate, Channels int64 }

type ProbeFacts struct {
	Format  FormatFacts
	Streams []StreamFacts
}

// AnalyzedInput is constructed only after actual successful probe exit and
// bridge Finalize inside the current PreparationInputBorrow callback. Facts are
// bounded parser output, not arbitrary unmarshaled decoder JSON. Source chunks
// record what was retained and made available, not proof of decoder consumption.
type AnalyzedInput struct {
	Version int
	Tool    ToolIdentity
	Facts   ProbeFacts
	Source  producerinput.Evidence
}
