package subtitles

import "io"

import "context"

type WindowCommand struct {
	// Started observes only the converter, after successful process Start.
	Started  func()
	Args     []string
	Hardware bool
	// Stderr, when set, receives what the converter writes to standard error, as a local
	// run's does: a converter that exits 0 having said something did not simply finish.
	Stderr io.Writer
}

// Window inputs retain the existing observed source and private endpoint
// confinement. Ordinary HLS owns the plan, command graph, output and admission.
type WindowInput interface {
	Probe(context.Context, []string) ([]byte, error)
	Run(context.Context, string, func(string) (WindowCommand, error)) error
	Close() error
}
type WindowInputs interface {
	OpenPlaybackWindow(context.Context, string) (WindowInput, error)
}
