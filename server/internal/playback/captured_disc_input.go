package playback

import (
	"context"
	"encoding/json"
	"reflect"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/disc"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/ownedcapture"
)

// capturedDiscInput consumes this reader after the outside-SQL context check.
// Invalid context leaves it with the caller for cleanup outside its transaction.
// The caller supplies the
// exact accepted structure/title and its original invocation or presentation
// authority check. Neither the descriptor nor matching bytes confer authority.
// The input retains the entire reader until Close, including its normalized
// source/actor, artifact and reader pins. No provider path is reopened.
func capturedDiscInput(ctx context.Context, tool disc.Tool, reader *ownedcapture.Reader, structure disc.Probe, title disc.Title, current func(context.Context) error) (_ *assets.PlaybackInput, err error) {
	if ctx == nil || dbwork.Snapshot(ctx) != nil || reader == nil {
		return nil, disc.ErrProtocol
	}
	defer func() {
		if err != nil {
			reader.Close()
		}
	}()
	if current == nil || reader.Context() == nil || reader.Context().Done() == nil {
		return nil, disc.ErrProtocol
	}
	// Decode a private copy and select only its exact title. In particular, an
	// owner preference or a title from another capture cannot retarget this input.
	raw, err := json.Marshal(structure)
	if err != nil {
		return nil, disc.ErrProtocol
	}
	structure, err = disc.DecodeProbe(raw)
	if err != nil || structure.Packaging != "iso" {
		return nil, disc.ErrProtocol
	}
	key := title.Key(structure.Kind)
	var selected disc.Title
	matches := 0
	for _, candidate := range structure.Titles {
		if candidate.Key(structure.Kind) == key {
			if !reflect.DeepEqual(candidate, title) {
				return nil, disc.ErrProtocol
			}
			selected, matches = candidate, matches+1
		}
	}
	if matches != 1 {
		return nil, disc.ErrProtocol
	}
	id, version := reader.CaptureIdentity()
	file, life := reader.File(), reader.Context()
	if file == nil || id == "" || version.ID() == "" || version.Evidence().Kind != mediasource.ContentSHA256 {
		return nil, disc.ErrProtocol
	}
	var input *assets.PlaybackInput
	validate := func(check context.Context) error {
		if err := check.Err(); err != nil {
			return err
		}
		if err := life.Err(); err != nil {
			return err
		}
		gotID, gotVersion := reader.CaptureIdentity()
		if gotID != id || gotVersion.ID() != version.ID() || reader.File() != file || input != nil && input.File != file {
			return mediasource.ErrSourceChanged
		}
		if err := reader.Check(check); err != nil {
			return err
		}
		if err := current(check); err != nil {
			return err
		}
		return life.Err()
	}
	input = &assets.PlaybackInput{File: file, Inherit: true, Validate: validate, Lifetime: life, ReleaseSource: reader.Close}
	run, release := input.BorrowContext(ctx)
	defer release()
	if err = validate(run); err != nil {
		return nil, err
	}
	actual, err := tool.ProbeDescriptor(run, file, false)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(actual, structure) {
		return nil, mediasource.ErrSourceChanged
	}
	input.InputAt = func(check context.Context, ms int64) ([]string, string, int64, error) {
		read, release := input.BorrowContext(check)
		defer release()
		if e := validate(read); e != nil {
			return nil, "", 0, e
		}
		var landing *disc.Seek
		if ms > 0 && structure.Kind == "bluray" {
			if selected.Playlist == nil {
				return nil, "", 0, disc.ErrProtocol
			}
			value, e := tool.SeekDescriptor(read, file, false, *selected.Playlist, ms)
			if e != nil {
				return nil, "", 0, e
			}
			landing = &value
		}
		value, e := structure.FFmpegInput(disc.DescriptorPath(false), selected, ms, landing)
		if e != nil {
			return nil, "", 0, e
		}
		if e = validate(read); e != nil {
			return nil, "", 0, e
		}
		return value.Options, value.URL, value.TimestampOffsetMs, nil
	}
	input.Options, input.Argument, _, err = input.InputAt(run, 0)
	if err != nil {
		return nil, err
	}
	return input, nil
}
