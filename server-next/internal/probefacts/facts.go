// Package probefacts defines bounded media observations: exact clocks and the
// few stream facts a recording or a channel needs, parsed strictly from
// ffprobe's output. Whoever stores them chooses the presentation clock mapping.
package probefacts

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

// AnalyzedInput is what one probe run found, with the tool that found it. It
// is made only after the probe has really exited successfully. Facts are
// bounded parser output, not arbitrary decoder JSON.
type AnalyzedInput struct {
	Version int
	Tool    ToolIdentity
	Facts   ProbeFacts
}
