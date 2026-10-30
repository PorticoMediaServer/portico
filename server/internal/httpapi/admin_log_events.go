package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/servicelog"
)

const adminLogMaxLife = 30 * time.Minute

type adminLogStreams struct {
	mu       sync.Mutex
	held     map[string]int
	lifetime time.Duration
}

func (s *adminLogStreams) admit(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.held == nil {
		s.held = map[string]int{}
	}
	if s.held[key] >= 2 {
		return false
	}
	s.held[key]++
	return true
}
func (s *adminLogStreams) release(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.held[key]--
	if s.held[key] == 0 {
		delete(s.held, key)
	}
}

func (d Dependencies) adminLogEvents(recorder *servicelog.Recorder, streams *adminLogStreams) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		lifetime := streams.lifetime
		if lifetime <= 0 {
			lifetime = adminLogMaxLife
		}
		ctx, cancelLife := context.WithTimeout(r.Context(), lifetime)
		defer cancelLife()
		r = r.WithContext(ctx)

		principal, err := d.administrator(r)
		if err != nil {
			accessFailure(w, err)
			return
		}
		level := r.URL.Query().Get("level")
		if level == "" {
			level = "info"
		}
		if !servicelog.ValidLevel(level) {
			accessFailure(w, access.ErrInvalid)
			return
		}
		var after *int64
		if raw := r.Header.Get("Last-Event-ID"); raw != "" {
			seq, err := recorder.EventSequence(raw)
			if err != nil {
				accessFailure(w, access.ErrInvalid)
				return
			}
			after = &seq
		}
		key := principal.Authority + ":" + principal.AccountID + ":" + principal.ProfileID
		if !streams.admit(key) {
			accessFailure(w, access.ErrCapacity)
			return
		}
		defer streams.release(key)
		if _, ok := w.(http.Flusher); !ok {
			policyError(w, "stream_unavailable")
			return
		}
		replay, reset, records, cancel := recorder.SubscribeAfter(level, after)
		defer cancel()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Accel-Buffering", "no")
		if err := frameWrite(w, func(io.Writer) (int, error) {
			w.WriteHeader(200)
			return 0, nil
		}); err != nil {
			return
		}
		frame := func(id, event string, body any) bool {
			if ctx.Err() != nil {
				return false
			}
			raw, _ := json.Marshal(body)
			if err := frameWrite(w, func(out io.Writer) (int, error) {
				return fmt.Fprintf(out, "id: %s\nevent: %s\ndata: %s\n\n", id, event, raw)
			}); err != nil {
				return false
			}
			return true
		}
		if reset {
			// The reset ID precedes every replayed record, even after ring eviction.
			seq := int64(0)
			if len(replay) > 0 {
				seq = replay[0].Sequence - 1
			}
			if !frame(recorder.EventID(seq), "reset", map[string]string{"reason": "log_window_expired"}) {
				return
			}
		}
		for _, record := range replay {
			if !frame(recorder.EventID(record.Sequence), "message", record) {
				return
			}
		}
		heartbeat := time.NewTicker(20 * time.Second)
		defer heartbeat.Stop()
		authorized := func() bool { current, err := d.administrator(r); return err == nil && current == principal }
		for {
			select {
			case <-r.Context().Done():
				return
			case <-heartbeat.C:
				if !authorized() {
					return
				}
				if err := frameWrite(w, func(out io.Writer) (int, error) {
					return fmt.Fprint(out, ": keep-alive\n\n")
				}); err != nil {
					return
				}
			case record, ok := <-records:
				if !ok || !authorized() {
					return
				}
				if !frame(recorder.EventID(record.Sequence), "message", record) {
					return
				}
			}
		}
	}
}
