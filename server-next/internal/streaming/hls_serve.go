package streaming

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func (h *HLS) Ready(ctx context.Context, id string) error {
	if e := h.start(ctx, id, 0); e != nil {
		return e
	}
	wait, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		h.mu.Lock()
		s := h.streams[id]
		if s == nil {
			h.mu.Unlock()
			return ErrGrantEnded
		}
		state, status, changed := s.state, s.output, s.changed
		h.mu.Unlock()
		if e := finishedStreamError(state); e != nil {
			return e
		}
		if status == "failed" {
			return errMediaConversionFailed
		}
		if info, e := os.Stat(filepath.Join(h.outputDir(id), "master.m3u8")); e == nil && info.Size() > 0 {
			stamp, err := os.ReadFile(filepath.Join(h.outputDir(id), "generation"))
			if err == nil && string(stamp) == outputStamp {
				return nil
			}
		}
		select {
		case <-wait.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return ErrSegmentPreparing
		case <-changed:
		case <-tick.C:
		}
	}
}

// outputStamp is written last when a stream's playlists are published. Output
// without it was interrupted while being set up and is produced again.
const outputStamp = "1"

// ErrHLSArtifactNotFound hides names outside this presentation's artifact set.
var ErrHLSArtifactNotFound = errors.New("stream artifact not found")

var artifactName = regexp.MustCompile(
	`^(master\.m3u8|video\.m3u8|text-[0-9]{1,2}\.m3u8|audio-[0-9]{1,2}\.m3u8|init\.mp4|segment-[0-9]{6}\.(?:ts|m4s)|audio-[0-9]{1,2}-init\.mp4|audio-[0-9]{1,2}-[0-9]{6}\.(?:ts|m4s))$`,
)

// Membership comes from the presentation plan, not from output readiness. An
// undeclared name must never wait for, restart or read a producer's output.
func hlsArtifactDeclared(name string, p *DeliveryPlan) bool {
	if strings.HasPrefix(name, "audio-") {
		return renditionArtifactDeclared(name, p)
	}
	if strings.HasPrefix(name, "text-") {
		if p != nil {
			for ordinal := range manifestTextRenditions(p) {
				if name == "text-"+strconv.Itoa(ordinal)+".m3u8" {
					return true
				}
			}
		}
		return false
	}
	fragmented := p != nil && p.OutputContainer == "fmp4_hls"
	switch name {
	case "master.m3u8":
		return true
	case "video.m3u8":
		return p != nil && (len(p.Renditions) > 0 || len(p.TextRenditions) > 0 || fragmented)
	case "init.mp4":
		return fragmented
	}
	_, segment := hlsSegmentIndex(name)
	return segment && (fragmented && strings.HasSuffix(name, ".m4s") || !fragmented && strings.HasSuffix(name, ".ts"))
}

// FileContext resolves one artifact and, for a segment the converter has not
// reached, moves the converter. A seek is nothing more than a request for a
// slot further along the grid: if it already exists the read is instant, and if
// it does not the session's own producer restarts there rather than converting
// everything in between.
func (h *HLS) FileContext(ctx context.Context, id, name string) (string, error) {
	if !artifactName.MatchString(name) {
		return "", ErrHLSArtifactNotFound
	}
	live, e := h.liveStream(id)
	if e != nil || live.mode != "hls" {
		// A stream that stopped or failed between the grant's lookup and here is
		// the grant's end, not a server error.
		return "", ErrGrantEnded
	}
	duration := live.duration
	delivery := h.planCopy(id)
	if delivery != nil && (strings.HasPrefix(name, "text-") || name == "video.m3u8") {
		if err := h.loadTextRenditions(ctx, id, delivery); err != nil {
			return "", err
		}
	}
	if !hlsArtifactDeclared(name, delivery) {
		return "", ErrHLSArtifactNotFound
	}
	if err := h.waitOutputMove(ctx, id); err != nil {
		return "", err
	}
	index, segment := hlsSegmentIndex(name)
	if strings.HasPrefix(name, "segment-") && !segment {
		return "", ErrHLSArtifactNotFound
	}
	stamp, stampErr := os.ReadFile(filepath.Join(h.outputDir(id), "generation"))
	if stampErr != nil || string(stamp) != outputStamp {
		// Retire interrupted owned initial publication, then use the same absent
		// output restart path as eviction.
		h.retireIncompleteOutput(id)
		if os.IsNotExist(stampErr) || !validOutputGeneration(h.outputDir(id)) {
			if _, err := os.Stat(h.outputDir(id)); os.IsNotExist(err) {
				from, segment := hlsSegmentIndex(name)
				if !segment {
					from = 0
				}
				if segment && from >= hlsSegmentCount(duration) {
					return "", ErrHLSArtifactNotFound
				}
				if err := h.start(ctx, id, from); err != nil {
					return "", err
				}
			}
		}
		return "", ErrSegmentPreparing
	}
	if strings.HasPrefix(name, "audio-") {
		return h.renditionFile(ctx, id, name, delivery)
	}
	path := filepath.Join(h.outputDir(id), name)
	if name == "init.mp4" {
		return h.awaitSegment(ctx, id, -1, func(ctx context.Context) (string, error) {
			wait, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			tick := time.NewTicker(100 * time.Millisecond)
			defer tick.Stop()
			for {
				current := filepath.Join(h.outputDir(id), name)
				if current != path {
					path = current
					if err := h.start(ctx, id, 0); err != nil {
						return "", err
					}
				}
				if segmentReady(path) {
					return path, nil
				}
				select {
				case <-wait.Done():
					if ctx.Err() != nil {
						return "", ctx.Err()
					}
					return "", ErrSegmentPreparing
				case <-tick.C:
				}
			}
		})
	}
	if !segment || hlsSegmentCount(duration) == 0 {
		return path, nil
	}
	count := hlsSegmentCount(duration)
	if timeline := h.sessionTimeline(ctx, id); timeline != nil {
		count = timeline.count()
	}
	if index >= count {
		// A well-formed segment slot at or past a known grid is the end of this
		// presentation, not an unknown artifact: a short title's last read can
		// land here while the session is still live. Answer the grant's own
		// hidden ended 404 rather than an internal 500. Malformed names and
		// generation mismatches keep their own refusals above.
		if count > 0 {
			return "", ErrGrantEnded
		}
		// A timeline that reports no slots is a broken plan, so it stays a fault.
		return "", errors.New("empty stream timeline")
	}
	cfg := h.configuration()
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		h.noteServed(ctx, id, index, cfg)
		return path, nil
	}
	if e = h.start(ctx, id, index); e != nil {
		return "", e
	}
	// Everybody waiting for this segment waits once. Ten reconnecting clients
	// asking for the same segment used to run ten poll loops between them.
	return h.awaitSegment(ctx, id, index, func(ctx context.Context) (string, error) {
		wait, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			h.mu.Lock()
			s := h.streams[id]
			if s == nil {
				h.mu.Unlock()
				return "", ErrGrantEnded
			}
			state, status, changed := s.state, s.output, s.changed
			h.mu.Unlock()
			if e := finishedStreamError(state); e != nil {
				return "", e
			}
			if status == "failed" {
				return "", errMediaConversionFailed
			}
			current := filepath.Join(h.outputDir(id), name)
			if current != path {
				path = current
				if err := h.start(ctx, id, index); err != nil {
					return "", err
				}
			}
			if segmentReady(path) {
				h.noteServed(ctx, id, index, cfg)
				return path, nil
			}
			select {
			case <-wait.Done():
				// A request that was cancelled says so; only the wait's own
				// budget running out is "still preparing".
				if ctx.Err() != nil {
					return "", ctx.Err()
				}
				return "", ErrSegmentPreparing
			case <-changed:
				// The window this request was waiting on may have ended without
				// reaching the segment. Asking again is free when one covers it.
				if err := h.start(ctx, id, index); err != nil {
					return "", err
				}
			case <-tick.C:
			}
		}
	})
}

// noteServed records the viewer's real position in the conversion, which is what
// both the throttle and the retention window are measured against.
func (h *HLS) noteServed(ctx context.Context, id string, index int, cfg DeliveryConfiguration) {
	h.noteSessionServed(id)
	h.mu.Lock()
	// A throttled window is already being torn down: its producer was cancelled
	// and the window is only waiting to be unregistered. Serving from it never
	// restarts production. That would spend a second restart and re-produce
	// segments already on disk. A throttled session resumes when a request
	// finds its segment absent.
	if window := h.windows[id]; window != nil && index > window.served {
		window.served = index
	}
	if s := h.streams[id]; s != nil && index > s.served {
		s.served = index
	}
	from := h.freed[id]
	moving := h.moving[id] != nil
	h.mu.Unlock()
	if moving {
		return
	}
	dir, release := h.retainOutput(id)
	if release == nil {
		return
	}
	defer release()
	if _, freed := prunePlayedSegments(dir, from, index, cfg.PlayedRetentionSeconds); freed > from {
		h.mu.Lock()
		if h.freed[id] < freed {
			h.freed[id] = freed
		}
		h.mu.Unlock()
	}
}
