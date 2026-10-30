// Package segmentmarkers holds the viewer-facing shape of detected media
// segments (intro, recap, credits, commercial, outro) and the single server-side
// rule that decides whether a client may skip one without asking. Clients never
// infer skip safety from confidence or provenance: they read automaticSafe.
//
// This package is deliberately a leaf. Analysis owns detection and storage,
// catalog owns item detail and playback owns offers; all three publish this
// same shape.
package segmentmarkers

import "strings"

// Marker is one projected segment on the viewer clock of the played source.
// Positions are seconds from the start of the logical item, never file offsets.
type Marker struct {
	ID            string  `json:"id"`
	Kind          string  `json:"kind"`
	StartSeconds  float64 `json:"startSeconds"`
	EndSeconds    float64 `json:"endSeconds"`
	AutomaticSafe bool    `json:"automaticSafe"`
}

// Set fences a marker projection to the exact source and marker revision it was
// read from, the way chapters are fenced. A client that has moved to another
// source or missed an owner edit must refetch rather than seek on stale facts.
type Set struct {
	SourceID string   `json:"sourceId"`
	Revision string   `json:"revision"`
	Markers  []Marker `json:"markers"`
}

// ViewerKind reports the kinds a viewer client may act on. Owner-only kinds
// (for example a manual "chapter" marker) never reach the viewer path.
func ViewerKind(kind string) bool {
	switch kind {
	case "intro", "recap", "credits", "commercial", "outro":
		return true
	}
	return false
}

// AutomaticConfidence is the measured-confidence floor an unapproved detection
// must clear before the server will authorize an unattended skip.
const AutomaticConfidence = 0.85

// ViewerConfidence is the floor below which an unapproved detection stays with
// the owner for review: viewers get no button for a weak guess.
const ViewerConfidence = 0.5

// MeasuredFamily is the provenance prefix of detections confirmed by
// measurement across files: an intro or closing credits whose audio other
// episodes of the same show share. Kept in step with segmentdetect's
// SeasonProvenance (a test holds them together); this package stays a leaf.
const MeasuredFamily = "season_audio_match"

// MeasuredProvenance reports whether a provenance names a cross-episode
// measurement. Everything else is a single file's guess: a chapter title is
// someone else's labelling, and one file's picture or sound can be fooled by a
// dark scene or a reused cue. Owner-made markers carry their own approval. No
// single-file guess may skip without asking, whatever confidence it claims.
func MeasuredProvenance(provenance string) bool {
	return strings.HasPrefix(provenance, MeasuredFamily+":")
}

// AutomaticSafe is the whole skip policy. An owner-approved marker is safe
// because a person accepted it. An unapproved marker is safe only when other
// episodes confirmed it with confidence at or above the floor. Dismissed
// markers are never projected and so never reach this function.
func AutomaticSafe(approved bool, confidence float64, provenance string) bool {
	if approved {
		return true
	}
	return DefaultConfidencePolicy().AutomaticSafe(false, confidence, provenance)
}

// ViewerVisible reports whether a viewer is offered a marker at all: approved,
// or a detection at or above ViewerConfidence that did not record its own
// semantic ambiguity (a stretch between black frames may be an advert or a
// scene). The download bundle and every viewer projection apply this one rule.
func ViewerVisible(approved bool, confidence float64, provenance string) bool {
	return DefaultConfidencePolicy().ViewerVisible(approved, confidence, provenance)
}

// Candidate is a stored marker with the facts the projection ranks by.
type Candidate struct {
	Marker
	Approved   bool
	Confidence float64
}

// Strongest keeps, of markers of one kind that overlap, the one a viewer
// should act on: an approved one, then one safe to skip unattended, then the
// most confident, then the earliest. A chapter title and a measurement of the
// same intro become one button, not two. Input and output are in start order.
func Strongest(candidates []Candidate) []Marker {
	rank := func(c Candidate) (int, float64) {
		switch {
		case c.Approved:
			return 2, c.Confidence
		case c.AutomaticSafe:
			return 1, c.Confidence
		}
		return 0, c.Confidence
	}
	keep := make([]bool, len(candidates))
	for i := range candidates {
		keep[i] = true
	}
	for i, a := range candidates {
		for j := i + 1; j < len(candidates); j++ {
			b := candidates[j]
			if !keep[i] || !keep[j] || a.Kind != b.Kind || a.StartSeconds >= b.EndSeconds || b.StartSeconds >= a.EndSeconds {
				continue
			}
			ra, ca := rank(a)
			rb, cb := rank(b)
			if rb > ra || rb == ra && cb > ca {
				keep[i] = false
			} else {
				keep[j] = false
			}
		}
	}
	out := []Marker{}
	for i, c := range candidates {
		if keep[i] {
			out = append(out, c.Marker)
		}
	}
	return out
}
