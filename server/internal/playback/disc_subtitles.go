package playback

import (
	"context"
	"io"
	"os"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"time"
)

func (s *Service) DiscSubtitleAvailable() bool {
	return s != nil && s.hls != nil && s.hls.binary != "" && s.hls.ffprobe != "" && s.hls.SourceStorage != nil && s.hls.SourceStorage.Supervisor != nil
}
func (s *Service) OpenDiscSubtitleInput(ctx context.Context, item, source, session string) (subtitles.DiscInput, error) {
	invocation, ok := subtitles.DiscExtractionInvocationFromContext(ctx)
	if !ok || !s.DiscSubtitleAvailable() {
		return nil, subtitles.ErrUnavailable
	}
	facts := invocation.Facts()
	if facts.ItemID != item || facts.AssetID != source || facts.SessionID != session {
		return nil, subtitles.ErrConflict
	}
	tx, done, err := dbwork.BeginRead(ctx, s.db)
	if err != nil {
		return nil, err
	}
	err = invocation.CheckTx(ctx, tx)
	if err == nil {
		snapshot, private, e := s.activePrivateDiscSnapshotTx(ctx, tx, session)
		if e != nil {
			err = e
		} else if private && (facts.SnapshotID != snapshot.ID || facts.AssetID != snapshot.Source.AssetID) || !private && facts.SnapshotID != "" {
			err = subtitles.ErrConflict
		}
	}
	done()
	if err != nil {
		return nil, err
	}
	input, handled, err := s.hls.openDiscInput(ctx, session)
	if err != nil {
		return nil, err
	}
	if !handled || input == nil {
		return nil, subtitles.ErrUnsupported
	}
	out := &discSubtitleInput{service: s, input: input, invocation: invocation}
	if err = out.Validate(ctx); err != nil {
		input.Close()
		return nil, err
	}
	if err = invocation.RetainInput(out.Validate, input.Close); err != nil {
		input.Close()
		return nil, err
	}
	return out, nil
}

type discSubtitleInput struct {
	service    *Service
	input      *assets.PlaybackInput
	invocation subtitles.DiscExtractionInvocation
}

// The sealed extraction invocation owns the descriptor through publication.
func (d *discSubtitleInput) Close() error { return nil }
func (d *discSubtitleInput) Validate(ctx context.Context) error {
	tx, done, err := dbwork.BeginRead(ctx, d.service.db)
	if err != nil {
		return err
	}
	err = d.invocation.CheckTx(ctx, tx)
	done()
	if err != nil {
		return err
	}
	if d.input.Validate == nil {
		return subtitles.ErrUnavailable
	}
	return d.input.Validate(ctx)
}
func (d *discSubtitleInput) Run(ctx context.Context, executable string, before, after []string, limit int64) ([]byte, error) {
	h := d.service.hls
	if h == nil || (executable != h.binary && executable != h.ffprobe) || limit < 1 || limit > int64(subtitles.AssetLimitBytes()) {
		return nil, subtitles.ErrRendererConfiguration
	}
	if err := d.Validate(ctx); err != nil {
		return nil, err
	}
	run, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()
	options, url, _, err := d.input.InputAt(run, 0)
	if err != nil {
		return nil, err
	}
	args := append([]string{}, before...)
	args = append(args, options...)
	args = append(args, "-i", url)
	args = append(args, after...)
	files := append([]*os.File{}, d.input.Extra...)
	if d.input.MountFD == 0 {
		files = append(files, d.input.File)
	}
	for _, f := range files {
		if f != nil {
			if _, err = f.Seek(0, io.SeekStart); err != nil {
				return nil, err
			}
		}
	}
	cmd, err := mediaexec.CommandContext(run, mediaexec.Job{Executable: executable, Args: args, Files: d.input.ExtraFiles(), InputFD: d.input.MountFD, InputMount: d.input.MountPath, ReadPaths: d.input.ReadPaths(), MaxFileBytes: mediaexec.NoOutputFiles, MaxCPUSeconds: 300})
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	stop, stopped := make(chan struct{}), make(chan struct{})
	lost := make(chan error, 1)
	supervise.Go("disc-subtitle.authority", func() {
		defer close(stopped)
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-run.Done():
				return
			case <-tick.C:
				if e := d.Validate(run); e != nil {
					lost <- e
					cancel()
					return
				}
			}
		}
	})
	var raw []byte
	retired := make(chan struct{})
	err = h.SourceStorage.Supervisor.RunOwnedCompletion(run, "playback:disc-subtitle:"+identity.Token(), cmd, func(reader io.Reader) error {
		var e error
		raw, e = io.ReadAll(io.LimitReader(reader, limit+1))
		if int64(len(raw)) > limit {
			return subtitles.ErrCapacity
		}
		return e
	}, nil, func() { close(retired) })
	<-retired
	close(stop)
	<-stopped
	select {
	case err = <-lost:
	default:
	}
	if err != nil {
		return nil, err
	}
	if err = d.Validate(ctx); err != nil {
		return nil, err
	}
	return raw, nil
}
