package subtitlevideo

import (
	"context"
	"errors"
	"io"

	"github.com/PorticoMediaServer/portico/server/internal/audiofacts"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

// MeasureAudio measures one audio asset for client-side decoding (spec §18.7):
// the same root/configuration-fenced input and confined ffprobe as every other
// media read, with one packet-and-frame pass. The input is revalidated after the
// pass, so a file that changed underneath is not measured.
func (r *Runtime) MeasureAudio(ctx context.Context, item, asset string) (audiofacts.Facts, error) {
	f, e := r.measureAudio(ctx, item, asset)
	// The shared input reports a file that no longer matches its scanned
	// version as a subtitle revision conflict; for audio facts it's a changed
	// source (NEW-34), not an unreadable file.
	if errors.Is(e, subtitles.ErrConflict) {
		e = audiofacts.ErrSourceChanged
	}
	return f, e
}

func (r *Runtime) measureAudio(ctx context.Context, item, asset string) (audiofacts.Facts, error) {
	var f audiofacts.Facts
	if r.ffprobe == "" {
		return f, subtitles.ErrRendererConfiguration
	}
	input, e := r.OpenSubtitleInput(ctx, item, asset, "")
	if e != nil {
		return f, e
	}
	defer input.Close()
	ledger := &extentLedger{}
	b, e := openBridge(ctx, input, ledger)
	if e != nil {
		return f, e
	}
	defer b.Close()
	var parsed error
	e = decoder.RunStreamingProbe(ctx, r.storage.Supervisor, identity.Token(), r.ffprobe, b.url, audiofacts.Args(b.url), b.reservation, func(out io.Reader) error {
		f, parsed = audiofacts.Parse(out)
		_, _ = io.Copy(io.Discard, out)
		return nil
	}, r.libraries...)
	if e != nil {
		return f, e
	}
	if parsed != nil {
		return f, parsed
	}
	if f.Codec == "mpc7" || f.Codec == "mpc8" {
		prefix, err := input.ReadExtent(ctx, 0, min(input.Size(), 1<<20))
		if err != nil {
			return f, err
		}
		if err = ledger.check(0, prefix); err != nil {
			return f, err
		}
		f, e = audiofacts.CompleteCodecHeaders(f, prefix)
		if e != nil {
			return f, e
		}
	}
	// An MP3, ADTS or Ogg file also gets its seek index: one more read of the
	// same fenced input, headers only, nothing decoded. A file that can't be
	// indexed exactly simply has none.
	if audiofacts.Indexed(f) {
		index, e := audiofacts.BuildSeekIndex(ctx, input.ReadExtent, input.Size(), f)
		if e != nil {
			return f, e
		}
		f.SeekIndex = audiofacts.EncodeSeekIndex(index)
	}
	return f, input.Validate(ctx)
}
