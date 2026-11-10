package streaming

import (
	"context"
	"errors"

	"github.com/PorticoMediaServer/portico/server-next/internal/subtitles"
)

type timelineWindow struct {
	probe func(context.Context, []string) ([]byte, error)
}

func (w timelineWindow) Probe(ctx context.Context, args []string) ([]byte, error) {
	return w.probe(ctx, args)
}
func (timelineWindow) Run(context.Context, string, func(string) (subtitles.WindowCommand, error)) error {
	return errors.New("unexpected producer run")
}
func (timelineWindow) Close() error { return nil }
