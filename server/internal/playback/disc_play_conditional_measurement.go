package playback

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/disc"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/playbackdisc"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// This constructor accepts only an original, already admitted conditional
// object. It supplies no weak-version promotion or full-capture fallback.
type conditionalDiscMeasure struct {
	DB        *sql.DB
	Object    storage.RemoteObject
	Tool      disc.Tool
	Probe     assets.Probe
	Template  playbackdisc.Snapshot
	Requested catalog.DiscTitleRef
	TitleKey  string
	Current   func(context.Context) error
	AcceptTx  func(context.Context, *sql.Tx) error
	Tools     func(context.Context) (playbackdisc.ToolPins, error)
}

type conditionalDiscFactProof struct {
	caller   context.Context
	object   storage.RemoteObject
	version  mediasource.Version
	snapshot storage.Snapshot
}

// These are in-memory identity checks, not provider IO inside an accepting
// writer. The measurement's mandatory original AcceptTx separately rechecks the
// actual source/actor/operation/queue in that writer. Rows cannot recreate this
// original retained object or its actual measured facts.
func (p *conditionalDiscFactProof) check(ctx context.Context) error {
	if p == nil || ctx == nil || p.caller == nil || p.object == nil {
		return disc.ErrProtocol
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := p.caller.Err(); err != nil {
		return err
	}
	if p.object.Access() != "finite_random" || p.object.Version().ID() != p.version.ID() || p.object.Snapshot() != p.snapshot {
		return mediasource.ErrSourceChanged
	}
	return nil
}

func (p *conditionalDiscFactProof) CheckTx(ctx context.Context, tx *sql.Tx) error {
	if tx == nil {
		return disc.ErrProtocol
	}
	if err := p.check(ctx); err != nil {
		return err
	}
	retained, ok := p.object.(interface {
		CheckTx(context.Context, *sql.Tx) error
	})
	if !ok {
		return disc.ErrRemoteUnqualified
	}
	return retained.CheckTx(ctx, tx)
}

// measureConditionalDiscPlay consumes Object only after the outside-SQL check.
// No preparation/session is accepted here. The returned private proof owns the
// exact object until its caller's original accepting commit or cleanup.
func measureConditionalDiscPlay(ctx context.Context, o conditionalDiscMeasure) (_ *discPlayMeasurement, err error) {
	if ctx == nil || dbwork.Snapshot(ctx) != nil || o.Object == nil {
		return nil, disc.ErrProtocol
	}
	if err = discPlayPreparationContext(ctx); err != nil {
		return nil, err
	}
	var input *assets.PlaybackInput
	defer func() {
		if err != nil {
			if input != nil {
				input.Close()
			} else {
				o.Object.Close()
			}
		}
	}()
	if o.DB == nil || o.Current == nil || o.AcceptTx == nil || o.Tools == nil || !discPlayConfiguredInputs(o.Tool, o.Probe) || o.Template.ID != "" || o.Template.SessionID != "" || o.Template.Generation != 0 || o.Template.Input.Captured != nil || o.Template.Input.Conditional != nil {
		return nil, disc.ErrProtocol
	}
	version, snapshot := o.Object.Version(), o.Object.Snapshot()
	evidence := version.Evidence()
	if version.ID() == "" || evidence.Kind != mediasource.StrongETag && evidence.Kind != mediasource.ImmutableProviderVersion ||
		evidence.Size <= 0 || evidence.Size != snapshot.Size || snapshot.Directory || snapshot.ErrorCode != "" || o.Object.Access() != "finite_random" {
		return nil, disc.ErrRemoteUnqualified
	}
	proof := &conditionalDiscFactProof{caller: ctx, object: o.Object, version: version, snapshot: snapshot}
	current := func(check context.Context) error {
		if err := proof.check(check); err != nil {
			return err
		}
		if err := o.Current(check); err != nil {
			return err
		}
		return proof.check(check)
	}
	if err = current(ctx); err != nil {
		return nil, err
	}
	view, err := dbwork.BeginSnapshot(ctx, o.DB)
	if err != nil {
		return nil, err
	}
	err = o.AcceptTx(view.Context(), view.Tx())
	view.Rollback()
	if err != nil {
		return nil, err
	}
	pins, err := o.Tools(ctx)
	if err != nil {
		return nil, err
	}
	structure, err := o.Tool.ProbeRemoteImageForPlay(ctx, o.Object, current)
	if err != nil {
		return nil, err
	}
	title, err := measuredRequestedDiscTitle(o.Template, structure, o.TitleKey, o.Requested)
	if err != nil {
		return nil, err
	}
	retained, err := conditionalDiscInput(ctx, o.Tool, o.Object, structure, title, current)
	if err != nil {
		return nil, err
	}
	input = retained.Input
	facts, err := o.Probe.InspectDiscPlayback(ctx, input, structure, title)
	if err != nil {
		return nil, err
	}
	// A preparation claim changes only with no original helper still reading
	// under the old claim. Quiesce joins that child and freshly observes this
	// same object; the private input retains custody, not an active byte pump.
	if err = retained.Quiesce(ctx); err != nil {
		return nil, err
	}
	after, err := o.Tools(ctx)
	if err != nil {
		return nil, err
	}
	if pins != after {
		return nil, ErrStaleOffer
	}
	if err = current(ctx); err != nil {
		return nil, err
	}
	// The version scopes the actual conditional origin/configuration. This
	// measured hash carries no receipt, viewer permission or substitute object.
	raw, err := json.Marshal(struct {
		Version   string
		Structure disc.Probe
	}{version.ID(), structure})
	if err != nil {
		return nil, err
	}
	revision := identity.Digest("disc-play-conditional-structure-v1\x00" + string(raw))
	measured, err := measuredDiscSnapshot(o.Template, playbackdisc.Input{Conditional: &playbackdisc.Conditional{Version: version}}, pins, structure, title, revision, facts)
	if err != nil {
		return nil, err
	}
	if o.Requested.ID != "" {
		ref, e := measuredDiscTitleRef(measured)
		if e != nil {
			return nil, e
		}
		if *ref != o.Requested {
			return nil, ErrStaleOffer
		}
	}
	return &discPlayMeasurement{ctx: ctx, input: input, probe: proof, original: o.AcceptTx, snapshot: measured}, nil
}

func measuredDiscTitle(structure disc.Probe, key string) (disc.Title, error) {
	if key == "" {
		if title, ok := structure.MainTitle(); ok {
			return title, nil
		}
		return disc.Title{}, ErrStaleOffer
	}
	var title disc.Title
	matches := 0
	for _, candidate := range structure.Titles {
		if candidate.Key(structure.Kind) == key {
			title, matches = candidate, matches+1
		}
	}
	if matches != 1 {
		return disc.Title{}, ErrStaleOffer
	}
	return title, nil
}

func measuredDiscSnapshot(template playbackdisc.Snapshot, input playbackdisc.Input, pins playbackdisc.ToolPins, structure disc.Probe, title disc.Title, revision string, facts assets.Facts) (playbackdisc.Snapshot, error) {
	// Root65 supplies no disc-to-audio/publication adapter or inferred readiness.
	if facts.VideoCodec == "" || len(facts.Streams) == 0 {
		return playbackdisc.Snapshot{}, ErrIncompatible
	}
	snapshot := template
	snapshot.Tools, snapshot.Input = pins, input
	snapshot.Media = playbackdisc.MediaFacts{Container: facts.Container, VideoCodec: facts.VideoCodec, AudioCodec: facts.AudioCodec,
		Width: facts.Width, Height: facts.Height, DurationMs: title.DurationMs, OriginUS: facts.OriginUS, TimingKnown: facts.TimingKnown,
		Streams: append([]assets.Stream(nil), facts.Streams...), Chapters: append([]assets.Chapter(nil), facts.Chapters...)}
	snapshot.Disc = playbackdisc.DiscFacts{Structure: structure, SelectedTitleKey: title.Key(structure.Kind), StructureRevision: revision}
	// Stream's public JSON intentionally omits planning facts. Hash the actual
	// measured detail/breadth and dispositions too; the private rows retain them.
	type measuredStream struct {
		assets.Stream
		Detail          assets.StreamDetail
		HearingImpaired bool
		Commentary      bool
	}
	streams := make([]measuredStream, 0, len(snapshot.Media.Streams))
	for _, stream := range snapshot.Media.Streams {
		streams = append(streams, measuredStream{stream, stream.Detail, stream.HearingImpaired, stream.Commentary})
	}
	fingerprint := func(value any) (string, error) {
		raw, err := json.Marshal(value)
		if err != nil {
			return "", err
		}
		return identity.Digest("disc-play-facts-v1\x00" + revision + "\x00" + snapshot.Disc.SelectedTitleKey + "\x00" + string(raw)), nil
	}
	var err error
	if snapshot.Disc.StreamRevision, err = fingerprint(streams); err != nil {
		return playbackdisc.Snapshot{}, err
	}
	if snapshot.Disc.ChapterRevision, err = fingerprint(snapshot.Media.Chapters); err != nil {
		return playbackdisc.Snapshot{}, err
	}
	if snapshot.Disc.FactsRevision, err = fingerprint(struct {
		Media           playbackdisc.MediaFacts
		StreamRevision  string
		ChapterRevision string
	}{snapshot.Media, snapshot.Disc.StreamRevision, snapshot.Disc.ChapterRevision}); err != nil {
		return playbackdisc.Snapshot{}, err
	}
	return snapshot, nil
}

func discPlayPreparationContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline, finite := ctx.Deadline()
	if !finite || deadline.After(time.Now().Add(playbackdisc.MaxPreparationLifetime)) {
		return disc.ErrProtocol
	}
	return nil
}

func discPlayConfiguredInputs(tool disc.Tool, probe assets.Probe) bool {
	for _, binary := range []string{tool.Binary, probe.Binary} {
		if !filepath.IsAbs(binary) || filepath.Clean(binary) != binary {
			return false
		}
	}
	return true
}

func measuredRequestedDiscTitle(template playbackdisc.Snapshot, structure disc.Probe, key string, requested catalog.DiscTitleRef) (disc.Title, error) {
	if requested.ID != "" {
		selected, err := catalog.MeasuredDiscTitleKey(template.Source.AssetID, template.Source.InventoryObjectID, template.Source.InventoryRevision, structure, requested.ID)
		if err != nil {
			return disc.Title{}, ErrStaleOffer
		}
		if key != "" && key != selected {
			return disc.Title{}, ErrStaleOffer
		}
		key = selected
	} else if requested.Revision != "" {
		return disc.Title{}, ErrStaleOffer
	}
	return measuredDiscTitle(structure, key)
}
