package playbackruntime

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

// MediaInputs opens an item's asset the way every v1 media read does
// (subtitlevideo.Runtime.OpenSubtitleInput: local roots, mounts, remote storage,
// STRM, all fenced to the library's current configuration).
type MediaInputs func(ctx context.Context, item, asset string) (subtitles.RenderInput, error)

type directInputs struct {
	mu   sync.Mutex
	open MediaInputs
}

// UseMediaInputs sets how a v1 channel session's Library Channels source is
// opened (spec §18.6, B5). The composition root calls it before channels play.
func (l *LinearRuntime) UseMediaInputs(open MediaInputs) {
	l.inputs.mu.Lock()
	l.inputs.open = open
	l.inputs.mu.Unlock()
}

func (l *LinearRuntime) mediaInputs() MediaInputs {
	l.inputs.mu.Lock()
	defer l.inputs.mu.Unlock()
	return l.inputs.open
}

// produceLibraryDirect produces a scheduled title from the media input: the
// same producer and bridge as a prepared input, over the source every v1 read
// uses. The input is revalidated on every extent and before publication.
func (l *LinearRuntime) produceLibraryDirect(ctx context.Context, w playback.LinearWork, entry *linearEntry) error {
	open := l.mediaInputs()
	if open == nil || w.Selection.ItemID == "" || w.Selection.AssetID == "" {
		return channelFault("library_source_unavailable", 503)
	}
	in, err := open(ctx, w.Selection.ItemID, w.Selection.AssetID)
	if err != nil {
		return channelFault("library_source_unavailable", 503)
	}
	defer in.Close()
	if err = in.Validate(ctx); err != nil {
		return channelFault("library_source_unavailable", 503)
	}
	return l.produceLibrary(ctx, w, entry, &renderBorrow{in: in, ref: playback.SourceReference{Kind: playback.ObservedSourceReference, ID: "media:" + w.Selection.AssetID + ":" + in.Evidence()}})
}

// renderBorrow is a media input as the producer bridge borrows one: an observed
// source whose every read and validation carries fresh evidence of the same
// reference.
type renderBorrow struct {
	in  subtitles.RenderInput
	ref playback.SourceReference
	seq atomic.Int64
}

func (b *renderBorrow) evidence() playback.InputSourceEvidence {
	n := b.seq.Add(1)
	return playback.InputSourceEvidence{Reference: b.ref, ObservationInterval: &playback.ObservationInterval{First: n, Last: n}}
}
func (b *renderBorrow) Metadata() (playback.PreparedInputMetadata, error) {
	return playback.PreparedInputMetadata{Reference: b.ref, Length: playback.KnownInt64{Known: true, Value: b.in.Size()}, InitialEvidence: b.evidence()}, nil
}
func (b *renderBorrow) Validate(ctx context.Context) (playback.InputValidation, error) {
	if err := b.in.Validate(ctx); err != nil {
		return playback.InputValidation{}, err
	}
	return playback.InputValidation{Evidence: b.evidence()}, nil
}
func (b *renderBorrow) ReadExtent(ctx context.Context, offset, length int64) (playback.ObservedExtent, error) {
	data, err := b.in.ReadExtent(ctx, offset, length)
	if err != nil {
		return playback.ObservedExtent{}, err
	}
	return playback.ObservedExtent{Bytes: data, Evidence: b.evidence()}, nil
}
