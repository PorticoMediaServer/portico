package mediaengine

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/live"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"github.com/PorticoMediaServer/portico/server-next/internal/streaming"
)

// liveMeasure is how much of a channel is read to measure it: enough for ffprobe to see
// every track of a broadcast, and never longer than liveMeasureFor.
const (
	liveMeasure    = 3 << 20
	liveMeasureFor = 8 * time.Second
)

// measuredInput is what was measured of one channel's input, kept while the input runs.
type measuredInput struct {
	in    *live.Input
	facts mediafacts.Facts
}

// StartLive decides how a device plays a live channel, with the same planner as a title: the
// source's own MPEG-TS when the device's document says it plays that as it is, with the
// source's codecs (nil, nothing started); otherwise HLS made for it, repackaged when the
// codecs fit and converted when not, held to the owner's limits like any conversion.
func (e *Engine) StartLive(ctx context.Context, r live.Conversion) (*live.Play, error) {
	facts, err := e.measureLive(ctx, r.Input)
	if err != nil {
		return nil, err
	}
	tools := e.tools()
	converter, fragmented, err := tools.wait(ctx)
	if err != nil {
		return nil, err
	}
	owner := e.Delivery()
	request := streaming.PlanRequest{
		File: streaming.FileFacts{ID: "live", Container: "mpegts", VideoCodec: facts.VideoCodec, AudioCodec: facts.AudioCodec,
			Width: facts.Width, Height: facts.Height, DetailVersion: mediafacts.StreamDetailVersion, StreamsCurrent: true, Streams: facts.Streams},
		Settings: owner, TranscodingEnabled: owner.ConversionMode != "never", FragmentedOutput: fragmented, NoConverter: !converter,
		Preferences: saved(r.Preferences),
	}
	connection(&request.Connection, playback.PlanRequest{Network: r.Network})
	holdToLimits(&request, r.Limits)
	if len(r.Capabilities) > 0 {
		if device, err := streaming.ParseCapabilityDocument(r.Capabilities); err == nil {
			request.Device = &device
		}
	}
	request.Choice.AssetID, request.Choice.QualityID = "live", playback.QualityOriginal
	plan, err := e.planWithHardware(ctx, tools, request)
	switch {
	case errors.Is(err, streaming.ErrTranscodingDisabled):
		return nil, playback.ErrConversionOff
	case err != nil:
		return nil, playback.ErrUnsupported
	case plan.Strategy == streaming.DeliveryOriginal:
		return nil, nil
	}
	in, _ := e.enter(r.SessionID)
	// A viewing has one conversion: one left in an older conversion folder stops first.
	if old := e.liveIn(r.SessionID, in); old != nil {
		old.streams.StopLive(r.SessionID)
	}
	err = in.streams.StartLive(ctx, streaming.LiveRequest{SessionID: r.SessionID, Plan: plan, Input: r.Input})
	switch {
	case errors.Is(err, streaming.ErrConversionCapacity), errors.Is(err, streaming.ErrConversionSpace):
		e.leave(r.SessionID, in)
		return nil, playback.ErrBusy
	case errors.Is(err, streaming.ErrNoConverter):
		e.leave(r.SessionID, in)
		return nil, playback.ErrConversionOff
	case err != nil:
		e.leave(r.SessionID, in)
		return nil, err
	}
	return &live.Play{Route: routes[plan.Strategy], Engine: plan.Engine, Container: plan.OutputContainer}, nil
}

// liveIn finds an older place that still holds a viewing's conversion.
func (e *Engine) liveIn(sessionID string, current *place) *place {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, p := range e.retired {
		if p != current {
			if _, ok := p.streams.LiveStatusOf(sessionID); ok {
				return p
			}
		}
	}
	return nil
}

// ServeLive answers one request for a viewing's HLS: its playlist or a file it names.
func (e *Engine) ServeLive(w http.ResponseWriter, r *http.Request, sessionID, file string) {
	e.place(sessionID).streams.ServeLive(w, r, sessionID, file)
}

// StopLive ends a viewing's HLS, if it has one.
func (e *Engine) StopLive(sessionID string) {
	in := e.place(sessionID)
	in.streams.StopLive(sessionID)
	e.leave(sessionID, in)
}

// LiveFailed says a viewing's conversion failed for good.
func (e *Engine) LiveFailed(sessionID string) bool {
	status, ok := e.place(sessionID).streams.LiveStatusOf(sessionID)
	return ok && status.Failure != nil
}

// measureLive reads the first seconds of a channel's input from live and measures them, once
// for each input: every viewer of the channel shares what was found.
func (e *Engine) measureLive(ctx context.Context, in *live.Input) (mediafacts.Facts, error) {
	e.mu.Lock()
	for _, m := range e.live {
		if m.in == in {
			e.mu.Unlock()
			return m.facts, nil
		}
	}
	e.mu.Unlock()
	file, err := os.CreateTemp(e.folder, ".live-measure-*.ts")
	if err != nil {
		return mediafacts.Facts{}, err
	}
	defer os.Remove(file.Name())
	read, cancel := context.WithTimeout(ctx, liveMeasureFor)
	in.Read(read, time.Time{}, &capped{w: file, left: liveMeasure, full: cancel})
	cancel()
	file.Close()
	if info, statErr := os.Stat(file.Name()); statErr != nil || info.Size() == 0 {
		// The channel sent nothing in that time, or stopped.
		if ctx.Err() != nil {
			return mediafacts.Facts{}, ctx.Err()
		}
		return mediafacts.Facts{}, playback.ErrSourceUnavailable
	}
	facts, err := e.probe.Inspect(ctx, file.Name())
	if err != nil || facts.VideoCodec == "" && facts.AudioCodec == "" {
		return mediafacts.Facts{}, playback.ErrSourceUnavailable
	}
	e.mu.Lock()
	kept := e.live[:0]
	for _, m := range e.live {
		if m.in.Stopped() == nil {
			kept = append(kept, m)
		}
	}
	e.live = append(kept, measuredInput{in: in, facts: facts})
	e.mu.Unlock()
	return facts, nil
}

// capped writes until it has written left bytes, then ends the reading.
type capped struct {
	w    io.Writer
	left int
	full context.CancelFunc
}

func (c *capped) Write(p []byte) (int, error) {
	if len(p) > c.left {
		p = p[:c.left]
	}
	n, err := c.w.Write(p)
	c.left -= n
	if c.left <= 0 {
		c.full()
	}
	return n, err
}
