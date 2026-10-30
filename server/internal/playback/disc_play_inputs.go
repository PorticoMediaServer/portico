package playback

import (
	"context"
	"path/filepath"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/disc"
	"github.com/PorticoMediaServer/portico/server/internal/ownedcapture"
	"github.com/PorticoMediaServer/portico/server/internal/playbackdisc"
)

type discPlayInputs struct {
	store       *playbackdisc.Store
	captures    *ownedcapture.Service
	tool        disc.Tool
	tools       func(context.Context) (playbackdisc.ToolPins, error)
	conditional DiscConditionalSource
	cleanup     sync.Mutex
	after       string // bounded maintenance cursor; advances only after COMMIT
}

// ConfigureDiscInputs installs no start producer or preparation202 branch. It
// only connects exact already-active private bindings to their ordinary reader.
// Call once during composition after the real probe/HLS/capture dependencies.
func (s *Service) ConfigureDiscInputs(serverID string, captures *ownedcapture.Service) error {
	if s == nil || s.db == nil || s.hls == nil || s.probe == nil || captures == nil || s.discInputs != nil {
		return disc.ErrRemoteUnqualified
	}
	var actual string
	if err := s.db.QueryRow(`SELECT value FROM configuration WHERE key='id'`).Scan(&actual); err != nil {
		return err
	}
	if actual != serverID {
		return playbackdisc.ErrCurrent
	}
	store, err := playbackdisc.New(serverID, captures)
	if err != nil {
		return err
	}
	tool := disc.Tool{Binary: filepath.Join(filepath.Dir(s.hls.binary), "portico-disc")}
	tools, err := discPlayToolWitness(tool.Binary, s.probe.Binary, s.hls.binary)
	if err != nil {
		return err
	}
	s.discInputs = &discPlayInputs{store: store, captures: captures, tool: tool, tools: tools}
	if s.Subtitles != nil {
		s.Subtitles.DiscPresentations = s
	}
	return nil
}

// Detect an exact legacy binding before considering any local/global disc pin.
// A private binding with a missing generation, pending state or unavailable
// adapter is handled by refusal, never by falling through to mutable inventory.
func (h *HLS) openPrivateDiscInput(ctx context.Context, id string) (*assets.PlaybackInput, bool, error) {
	tx, release, err := dbwork.BeginRead(ctx, h.db)
	if err != nil {
		return nil, true, err
	}
	key, snapshot, private, err := privateDiscBindingTx(ctx, tx, id)
	release()
	if err != nil {
		return nil, true, err
	}
	if !private {
		return nil, false, nil
	}
	if dbwork.Snapshot(ctx) != nil || h.discPlayback == nil || h.discPlayback.discInputs == nil {
		return nil, true, disc.ErrRemoteUnqualified
	}
	s, inputs := h.discPlayback, h.discPlayback.discInputs
	input, err := openPrivateDiscPresentation(ctx, discPlayReaderOptions{
		DB: h.db, Store: inputs.store, Captures: inputs.captures,
		Key: key, Snapshot: snapshot,
		Tool: inputs.tool, Tools: inputs.tools, Current: s.checkActiveDiscSnapshotTx, Conditional: inputs.conditional,
	})
	return input, true, err
}
