package dbwork

import (
	"database/sql"
	"log"
	"sync"
)

// AccountingWALPhase tags existing granted writers; it never qualifies a WAL
// reservation or changes admission, caps, checkpointing or transaction plans.
type AccountingWALPhase uint8

const (
	AccountingFlip AccountingWALPhase = 1 << iota
	AccountingFold
	accountingWALWindow        = 64
	accountingFlipWarningBytes = 8 << 20
)

// ObserveAccountingWAL records the whole granted writer containing this phase.
// Untracked fixture transactions have no trustworthy writer meter and are skipped.
func ObserveAccountingWAL(tx *sql.Tx, phase AccountingWALPhase) {
	if phase != AccountingFlip && phase != AccountingFold {
		return
	}
	if value, ok := liveWrites.Load(tx); ok {
		w := value.(*Write)
		w.mu.Lock()
		if !w.done {
			w.accountingWAL |= phase
		}
		w.mu.Unlock()
	}
}

type AccountingWALPhaseStats struct {
	Samples           uint64 `json:"samples"`
	UnknownWAL        uint64 `json:"unknownWal"`
	UnknownFrames     uint64 `json:"unknownFrames"`
	UnknownCachePages uint64 `json:"unknownCachePages"`
	WindowSamples     int    `json:"windowSamples"`
	MaxWALBytes       uint64 `json:"maxWalBytes"`
	MaxFrames         uint64 `json:"maxFrames"`
	MaxCachePages     int64  `json:"maxCachePages"`
	PeakWALBytes      uint64 `json:"peakWalBytes"`
	PeakFrames        uint64 `json:"peakFrames"`
	PeakCachePages    int64  `json:"peakCachePages"`
}
type AccountingWALStats struct {
	Window int                     `json:"window"`
	Flip   AccountingWALPhaseStats `json:"flip"`
	Fold   AccountingWALPhaseStats `json:"fold"`
}
type accountingWALSample struct {
	bytes, frames                         uint64
	pages                                 int64
	writtenKnown, framesKnown, pagesKnown bool
}
type accountingWALRing struct {
	samples                                        [accountingWALWindow]accountingWALSample
	total, unknownWAL, unknownFrames, unknownPages uint64
	count, next                                    int
	peakBytes, peakFrames                          uint64
	peakPages                                      int64
}

var accountingWAL struct {
	sync.Mutex
	flip, fold accountingWALRing
}

func (r *accountingWALRing) add(s accountingWALSample) {
	r.total++
	if s.writtenKnown {
		r.peakBytes = max(r.peakBytes, s.bytes)
	}
	if s.framesKnown {
		r.peakFrames = max(r.peakFrames, s.frames)
	}
	if s.pagesKnown {
		r.peakPages = max(r.peakPages, s.pages)
	}
	if !s.writtenKnown {
		r.unknownWAL++
	}
	if !s.framesKnown {
		r.unknownFrames++
	}
	if !s.pagesKnown {
		r.unknownPages++
	}
	r.samples[r.next] = s
	r.next = (r.next + 1) % accountingWALWindow
	r.count = min(r.count+1, accountingWALWindow)
}
func (r *accountingWALRing) snapshot() AccountingWALPhaseStats {
	out := AccountingWALPhaseStats{Samples: r.total, UnknownWAL: r.unknownWAL, UnknownFrames: r.unknownFrames, UnknownCachePages: r.unknownPages, WindowSamples: r.count, PeakWALBytes: r.peakBytes, PeakFrames: r.peakFrames, PeakCachePages: r.peakPages}
	for _, s := range r.samples[:r.count] {
		if s.pagesKnown {
			out.MaxCachePages = max(out.MaxCachePages, s.pages)
		}
		if s.writtenKnown {
			out.MaxWALBytes = max(out.MaxWALBytes, s.bytes)
		}
		if s.framesKnown {
			out.MaxFrames = max(out.MaxFrames, s.frames)
		}
	}
	return out
}

// AccountingWAL reports rolling maxima over the last64 committed phase writers
// and process peaks since reset; neither is a physical frame bound.
// WAL bytes/frames are successful native writes, including spill/rewrite/padding;
// cache pages come from SQLite's own DB-status counter. Unknown is never zero proof.
func AccountingWAL() AccountingWALStats {
	accountingWAL.Lock()
	defer accountingWAL.Unlock()
	return AccountingWALStats{Window: accountingWALWindow, Flip: accountingWAL.flip.snapshot(), Fold: accountingWAL.fold.snapshot()}
}
func resetAccountingWAL() {
	accountingWAL.Lock()
	defer accountingWAL.Unlock()
	accountingWAL.flip, accountingWAL.fold = accountingWALRing{}, accountingWALRing{}
}
func noteAccountingWAL(phase AccountingWALPhase, s accountingWALSample) {
	accountingWAL.Lock()
	if phase&AccountingFlip != 0 {
		accountingWAL.flip.add(s)
	}
	if phase&AccountingFold != 0 {
		accountingWAL.fold.add(s)
	}
	accountingWAL.Unlock()
	// This is deliberately after commit AND gate release, with no error/refusal.
	if phase&AccountingFlip != 0 && s.writtenKnown && s.bytes > accountingFlipWarningBytes {
		log.Printf("warning: MusicBrainz accounting flip wrote %d WAL bytes (%d cache-page writes); observed telemetry only", s.bytes, s.pages)
	}
}
func observedWALFrames(bytes, headers uint64, pageSize int64) (uint64, bool) {
	if pageSize <= 0 || headers > bytes/32 {
		return 0, false
	}
	frameSize := uint64(pageSize) + 24
	payload := bytes - 32*headers
	if payload%frameSize != 0 {
		return 0, false
	}
	return payload / frameSize, true
}
