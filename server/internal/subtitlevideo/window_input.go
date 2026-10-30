package subtitlevideo

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/childprocess"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

type playbackWindow struct {
	runtime *Runtime
	input   subtitles.RenderInput
	bridge  *MediaBridge
	session string
}

func (r *Runtime) OpenPlaybackWindow(ctx context.Context, session string) (subtitles.WindowInput, error) {
	var item, source string
	if e := r.db.QueryRowContext(ctx, `SELECT COALESCE(pid(e.public_id),''),ps.asset_id FROM playback_sessions ps LEFT JOIN catalog_entities e ON e.id=ps.item_id WHERE ps.id=?`, session).Scan(&item, &source); e != nil {
		return nil, e
	}
	input, e := r.OpenSubtitleInput(ctx, item, source, session)
	if e != nil {
		return nil, e
	}
	var expected string
	if e = r.db.QueryRowContext(ctx, `SELECT evidence FROM subtitle_remote_sessions WHERE session_id=?`, session).Scan(&expected); e != nil || expected != persistentEvidence(input) {
		input.Close()
		return nil, subtitles.ErrConflict
	}
	return &playbackWindow{r, input, NewMediaBridge(input), session}, nil
}
func (w *playbackWindow) Close() error { return w.input.Close() }
func (w *playbackWindow) Run(ctx context.Context, dir string, build func(string) (subtitles.WindowCommand, error)) error {
	return w.bridge.With(ctx, func(url string, res *decoder.EndpointReservation) error {
		if e := w.input.Validate(ctx); e != nil {
			return e
		}
		command, e := build(url)
		if e != nil {
			return e
		}
		e = decoder.RunHLSWindow(childprocess.WithStarted(ctx, command.Started), w.runtime.storage.Supervisor, identity.Token(), w.runtime.ffmpeg, url, dir, res, command.Args, command.Hardware, w.runtime.libraries...)
		if e == nil {
			e = w.input.Validate(ctx)
		}
		return e
	})
}

// Probe observes the same retained version and extent ledger as HLS production.
func (w *playbackWindow) Probe(ctx context.Context, args []string) ([]byte, error) {
	var data []byte
	err := w.bridge.With(ctx, func(url string, res *decoder.EndpointReservation) error {
		if err := w.input.Validate(ctx); err != nil {
			return err
		}
		var err error
		data, err = decoder.RunProbeQuery(ctx, w.runtime.storage.Supervisor, identity.Token(), w.runtime.ffprobe, url, res, args, w.runtime.libraries...)
		if err == nil {
			err = w.input.Validate(ctx)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return data, nil
}
