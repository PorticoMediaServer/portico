package writer

import (
	"slices"
	"strings"
	"unicode/utf8"
)

// Preparation bounds, not a limit on play duration or an executable recipe.
// Oversized captures must retain their intent for smaller, stable requests;
// neither truncation nor report acknowledgement is performed here.
const wcMaxPlayingIntervals = 256
const wcMaxPlaybackIDBytes = 128
const wcQuarterMS int64 = 900_000

// One original occurrence/report proof and one trusted clock anchor. These
// claimed witnesses are not an attestation, disclosure or write capability.
// In particular a scalar credited/WatchedMs value cannot supply these spans.
type wcPlayingProof struct {
	playbackID                             string
	occurrenceGeneration, reportSequence   int64
	activityGeneration, rememberGeneration int64
	clockIncarnation                       [16]byte
}

type wcPlayingInterval struct{ fromMS, untilMS int64 }

type wcPlayingInput struct {
	proof wcPlayingProof
	span  wcPlayingInterval
}

// A preparation value owns its union and keeps independent occurrences and
// clock anchors separate. It is not durable: a producer must persist/resolve
// every continuous interval before acknowledging its whole captured report.
type wcPreparedPlaying struct {
	proof     wcPlayingProof
	intervals []wcPlayingInterval
	valid     bool
}

// prepareWCPlaying unions only one exact proof before any quarter splitting.
// It performs no SQL, receipt encoding, clock inference or runtime admission.
func prepareWCPlaying(raw []wcPlayingInput) (*wcPreparedPlaying, error) {
	if len(raw) == 0 {
		return nil, invalid("progress_playing_intervals")
	}
	if err := count("progress_playing_intervals", len(raw), wcMaxPlayingIntervals); err != nil {
		return nil, err
	}
	proof := raw[0].proof
	if proof.playbackID == "" || len(proof.playbackID) > wcMaxPlaybackIDBytes ||
		!utf8.ValidString(proof.playbackID) || strings.IndexByte(proof.playbackID, 0) >= 0 ||
		proof.occurrenceGeneration <= 0 || proof.reportSequence < 0 ||
		proof.activityGeneration <= 0 || proof.rememberGeneration <= 0 || proof.clockIncarnation == [16]byte{} {
		return nil, invalid("progress_playing_proof")
	}
	// Validate the complete raw envelope before copying/sorting. In particular
	// an oversized duplicate population cannot bypass the raw preparation bound.
	for _, in := range raw {
		if in.proof != proof || in.span.fromMS < 0 || in.span.untilMS <= in.span.fromMS {
			return nil, invalid("progress_playing_input")
		}
	}
	spans := make([]wcPlayingInterval, len(raw))
	for i, in := range raw {
		spans[i] = in.span
	}
	slices.SortFunc(spans, func(a, b wcPlayingInterval) int {
		if a.fromMS < b.fromMS || (a.fromMS == b.fromMS && a.untilMS < b.untilMS) {
			return -1
		}
		if a == b {
			return 0
		}
		return 1
	})
	union := spans[:0]
	for _, span := range spans {
		if len(union) == 0 || span.fromMS > union[len(union)-1].untilMS {
			union = append(union, span)
		} else {
			union[len(union)-1].untilMS = max(union[len(union)-1].untilMS, span.untilMS)
		}
	}
	proof.playbackID = strings.Clone(proof.playbackID)
	return &wcPreparedPlaying{proof: proof, intervals: union, valid: true}, nil
}

// pieceAt returns ONE nonempty quarter-contained piece. W must obtain/recheck
// the actual durable pending cursor and complete proof in its accepting turn;
// this arithmetic neither advances that cursor nor authorizes acceptance.
// A long interval remains one descriptor rather than allocating every quarter.
func (p *wcPreparedPlaying) pieceAt(interval int, cursor int64) (wcPlayingInterval, error) {
	if p == nil || !p.valid || interval < 0 || interval >= len(p.intervals) {
		return wcPlayingInterval{}, invalid("progress_playing_interval")
	}
	span := p.intervals[interval]
	if cursor < span.fromMS || cursor >= span.untilMS {
		return wcPlayingInterval{}, invalid("progress_playing_cursor")
	}
	until := span.untilMS
	// Compare positive differences before adding: the next boundary may lie
	// beyond MaxInt64 even though the complete source interval is valid.
	if step := wcQuarterMS - cursor%wcQuarterMS; step < until-cursor {
		until = cursor + step
	}
	return wcPlayingInterval{cursor, until}, nil
}
