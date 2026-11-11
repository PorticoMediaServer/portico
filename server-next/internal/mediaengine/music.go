package mediaengine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/audiofacts"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"github.com/PorticoMediaServer/portico/server-next/internal/streaming"
)

// A song or an audiobook played by an app with a music engine of its own is not sent as an
// HLS stream. The app is given the file itself, or an exact conversion of it, with what to
// trim from each end and where it can seek, which is what lets one song run into the next
// without a gap. That plan needs the file measured exactly, by decoding it once.
//
// The scan makes the measurement when it reads the file and keeps it with the file
// (files.audio_exact), so a play has it at once. A file the scan could not measure that way
// (a very long one, a platform without the scan's worker, a file scanned before this was
// built) is measured the first time it is planned, and that is kept in memory until the
// file changes or the server restarts. A plan waits a short while for it; if it takes
// longer, this play is planned the ordinary way and the measurement carries on beside it,
// so the next play of the file has it.

// musicWait is how long a plan waits for a file to be measured.
const musicWait = 3 * time.Second

// measuredMusic is one file's measurement, done or under way.
type measuredMusic struct {
	done  chan struct{}
	facts audiofacts.Facts
	index *audiofacts.SeekIndex
	err   error
}

// measured returns a music file's exact facts, and false when they are not there in time
// or the file cannot be measured.
func (e *Engine) measured(ctx context.Context, path string, place db.FileOnDisk) (*measuredMusic, bool) {
	key := fmt.Sprint(path, "\x00", place.Size, "\x00", place.ModifiedNS)
	e.mu.Lock()
	m := e.music[key]
	if m == nil {
		if e.music == nil || len(e.music) > 2000 {
			e.music = map[string]*measuredMusic{}
		}
		m = &measuredMusic{done: make(chan struct{})}
		e.music[key] = m
		go func() {
			defer close(m.done)
			// Not this request's: it outlives a plan that stopped waiting.
			run, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			modified := time.Unix(0, place.ModifiedNS)
			if place.AudioExact != "" && json.Unmarshal([]byte(place.AudioExact), &m.facts) == nil && m.facts.SampleRate > 0 {
				// The scan measured it. Only the seek index is made now, which is quick.
				m.index, m.err = e.streams.AudioSeekIndex(run, path, place.Size, modified, &m.facts)
				return
			}
			m.facts, m.index, m.err = e.streams.MeasureAudio(run, path, place.Size, modified)
		}()
	}
	e.mu.Unlock()
	wait := time.NewTimer(musicWait)
	defer wait.Stop()
	select {
	case <-m.done:
		return m, m.err == nil
	case <-wait.C:
	case <-ctx.Done():
	}
	return nil, false
}

// musicPlan is the plan for a song or audiobook on a device whose own music engine plays
// it, or nil when the ordinary plan applies: another kind of title, a device without such
// an engine, a file on remote storage, a file not measured yet, a way of playing this file
// that failed on this device before, or a track the device cannot be given either way.
func (e *Engine) musicPlan(ctx context.Context, r playback.PlanRequest, request streaming.PlanRequest, path string, place db.FileOnDisk,
	remoteID int64, facts mediafacts.Facts, converter bool) *playback.Plan {
	if r.Item.Kind != db.KindTrack && r.Item.Kind != db.KindAudiobook || request.Device == nil || len(request.Device.AudioDecode) == 0 ||
		remoteID != 0 || len(r.Failures) > 0 {
		return nil
	}
	m, ok := e.measured(ctx, path, place)
	if !ok {
		return nil
	}
	choice := request.Choice
	choice.AudioDecode, choice.Remote = request.Device.DecodeCaps(), r.Network != playback.NetworkLocal
	source := streaming.SourceFromFacts(request.File)
	plan, err := streaming.PlanAudio(streaming.AudioPlanRequest{StreamID: r.Version.PublicID, Generation: 1, Facts: m.facts, Measured: true,
		Size: place.Size, Source: &source, Choice: &choice, Settings: request.Settings, Preferences: request.Preferences, TranscodingEnabled: converter})
	if err != nil || plan.Mode != "direct" && plan.Mode != "converted" {
		return nil
	}
	out := &playback.Plan{Mode: "direct", Player: streaming.EngineNative, Route: playback.RouteOriginal, Container: plan.Container, Quality: r.Choice.Quality,
		Audio:  &playback.Decision{Action: "direct"},
		Detail: &planned{audio: plan, index: m.index, path: path, place: place}}
	if plan.Mode == "converted" {
		out.Mode, out.Route = "stream", playback.RouteConverted
		out.Audio = &playback.Decision{Action: "transcode", To: plan.Codec, Channels: plan.Channels}
		if plan.Convert != nil {
			out.BitrateKbps = plan.Convert.BitrateBPS / 1000
		}
	}
	for _, s := range facts.Streams {
		if s.Type == "audio" {
			out.AudioTrackID = trackID(r.Version, facts, s.Index)
			break
		}
	}
	// What the app is shown before the stream has an address; the stream's own copy has them.
	out.AudioRender, _ = json.Marshal(plan)
	sum := sha256.Sum256(append(append([]byte("music\x00"), out.AudioRender...), []byte(path)...))
	out.Key = hex.EncodeToString(sum[:8])
	return out
}
