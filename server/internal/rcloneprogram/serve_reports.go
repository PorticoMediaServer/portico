package rcloneprogram

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"
)

// PacerObservation contains only an actual asynchronous upstream-rclone pacer
// report. Backoff is not a provider quota, HTTP status or Retry-After deadline.
// It neither grants reads nor promises synchronous control of hidden retries.
type PacerObservation struct {
	Sequence   uint64
	Backoff    bool
	ObservedAt time.Time
}

const serveReportLineLimit = 64 << 10

// serveReports consumes private stderr without retaining or logging provider
// text, paths, URLs or credentials. Oversize lines are discarded through their
// newline so an untrusted tail cannot become a separate accepted JSON record.
type serveReports struct {
	mu       sync.Mutex
	line     []byte
	dropping bool
	latest   PacerObservation
}

func (r *serveReports) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(p)
	for len(p) > 0 {
		end := bytes.IndexByte(p, '\n')
		part, rest := p, []byte(nil)
		if end >= 0 {
			part, rest = p[:end], p[end+1:]
		}
		if !r.dropping {
			if len(part) > serveReportLineLimit-len(r.line) {
				clear(r.line)
				r.line = r.line[:0]
				r.dropping = true
			} else {
				r.line = append(r.line, part...)
			}
		}
		if end >= 0 {
			if !r.dropping {
				r.accept(r.line)
			}
			clear(r.line)
			r.line = r.line[:0]
			r.dropping = false
		}
		p = rest
	}
	return n, nil
}

func (r *serveReports) accept(line []byte) {
	var record struct {
		Level, Msg, Object, ObjectType, Source string
	}
	if json.Unmarshal(line, &record) != nil || record.Level != "debug" || record.Object != "pacer" || record.ObjectType != "string" {
		return
	}
	lineNumber, ok := strings.CutPrefix(record.Source, "fs/pacer.go:")
	if !ok {
		return
	}
	if n, err := strconv.ParseUint(lineNumber, 10, 32); err != nil || n == 0 {
		return
	}
	delay, ok := strings.CutPrefix(record.Msg, "Rate limited, increasing sleep to ")
	if !ok {
		return
	}
	if duration, err := time.ParseDuration(delay); err != nil || duration <= 0 {
		return
	}
	if r.latest.Sequence == ^uint64(0) {
		return
	}
	r.latest = PacerObservation{Sequence: r.latest.Sequence + 1, Backoff: true, ObservedAt: time.Now().UTC()}
}

func (r *serveReports) observation() PacerObservation {
	if r == nil {
		return PacerObservation{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.latest
}

func (r *serveReports) discardPartial() {
	r.mu.Lock()
	defer r.mu.Unlock()
	clear(r.line)
	r.line = nil
	r.dropping = false
}

func (r *serveReports) backendObserved(sequence uint64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// A successful request cannot clear a newer report that arrived during it.
	if r.latest.Sequence == sequence {
		r.latest.Backoff = false
	}
}

func (s *ServeProcess) PacerObservation() PacerObservation {
	if s == nil {
		return PacerObservation{}
	}
	return s.reports.observation()
}

// BackendObserved records a completed fresh upstream metadata observation,
// using the sequence captured before that I/O. Cached VFS responses, a shorter
// pacer delay or local capacity admission must never call this method.
func (s *ServeProcess) BackendObserved(sequence uint64) {
	if s != nil {
		s.reports.backendObserved(sequence)
	}
}
