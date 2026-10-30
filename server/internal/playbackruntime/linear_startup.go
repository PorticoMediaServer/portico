package playbackruntime

import (
	"log"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/linearbuffer"
	"github.com/PorticoMediaServer/portico/server/internal/linearinput"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
)

type linearStartupPhase string

const (
	linearStartupAdmission linearStartupPhase = "admission"
	linearStartupOpening   linearStartupPhase = "opening-input"
	linearStartupProbing   linearStartupPhase = "probing"
	linearStartupPlanning  linearStartupPhase = "planning"
	linearStartupOutput    linearStartupPhase = "preparing-output"
	linearStartupCapture   linearStartupPhase = "capture-requested"
)

// This observer belongs to one actual live producer invocation. It reports at
// most three delayed-start snapshots, without changing readiness or admission.
// No locator, command, source metadata or transport error enters this record.
type linearStartup struct {
	mu                         sync.Mutex
	started                    time.Time
	phase                      linearStartupPhase
	gateway                    *linearinput.Gateway
	planKnown                  bool
	convertVideo, convertAudio bool
	fragmented                 bool
	reports                    int
}

func (s *linearStartup) enter(phase linearStartupPhase, gateway *linearinput.Gateway) {
	s.mu.Lock()
	s.phase = phase
	if gateway != nil {
		s.gateway = gateway
	}
	s.mu.Unlock()
}

func (s *linearStartup) planned(plan decoder.LinearEncoding) {
	s.mu.Lock()
	s.phase = linearStartupOutput
	s.planKnown = true
	s.convertVideo, s.convertAudio, s.fragmented = plan.ConvertVideo, plan.ConvertAudio, plan.Fragmented
	s.mu.Unlock()
}

func (s *linearStartup) report(w playback.LinearWork, window linearbuffer.Window) {
	if s == nil || window.Ready {
		return
	}
	now := time.Now()
	s.mu.Lock()
	// 15, 45 and 75 seconds, only while this invocation is not ready.
	if s.reports >= 3 || now.Sub(s.started) < 15*time.Second+time.Duration(s.reports)*30*time.Second {
		s.mu.Unlock()
		return
	}
	s.reports++
	started, phase, gateway := s.started, s.phase, s.gateway
	known, video, audio, fragmented := s.planKnown, s.convertVideo, s.convertAudio, s.fragmented
	s.mu.Unlock()
	var input linearinput.Span
	if gateway != nil {
		input = gateway.Snapshot()
	}
	idleMS := int64(-1)
	if !input.Last.IsZero() {
		idleMS = max(0, now.Sub(input.Last).Milliseconds())
	}
	// Input counts include the probe and capture through the same gateway;
	// capture-requested does not claim a decoder process has started.
	log.Printf("Channel playback %s: producer %d startup phase=%s elapsed_ms=%d input_observation_known=%t input_bytes=%d input_gaps=%d input_idle_ms=%d plan_known=%t convert_video=%t convert_audio=%t fragmented=%t window_start_ms=%d window_end_ms=%d window_span_ms=%d",
		shortPlaybackID(w.PlaybackID), w.ProducerOrdinal, phase, now.Sub(started).Milliseconds(), gateway != nil, input.Bytes, input.Gaps, idleMS,
		known, video, audio, fragmented, window.StartUS/1000, window.EndUS/1000, max(0, window.EndUS-window.StartUS)/1000)
}
