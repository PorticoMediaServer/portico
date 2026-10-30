package playbackv1

import (
	"github.com/PorticoMediaServer/portico/server/internal/playback"
)

// Quality is what a viewer asks a presentation's quality to be (spec §5.2).
//
//   - original: the file as it is. This is the default.
//   - fixed: one step of the published ladder, named by id ("1080p-low"). A step is
//     a ceiling, not a target: a file already within it still plays as it is.
//   - automatic: starts at the original and moves down, and back up, as the
//     connection requires. The client asks for each move (step "down" or "up")
//     and the server re-plans the same session at the current position; id is
//     the ladder step the session is on now, empty while it is on the original.
//   - limit: ceilings a client states in numbers.
//
// Whatever is asked for, the owner's ceilings, the member's caps and the
// upload budget still apply.
type Quality struct {
	Mode                string `json:"mode"`
	ID                  string `json:"id,omitempty"`
	MaxVideoBitrateKbps int    `json:"maxVideoBitrateKbps,omitempty"`
	MaxHeight           int    `json:"maxHeight,omitempty"`
	MaxAudioBitrateKbps int    `json:"maxAudioBitrateKbps,omitempty"`
	// Step is a request, never state: with mode automatic, "down" or "up" moves
	// one ladder step from where the session is. It is never stored or echoed.
	Step string `json:"step,omitempty"`
}

// Quality modes.
const (
	QualityOriginal  = "original"
	QualityAutomatic = "automatic"
	QualityFixed     = "fixed"
	QualityLimit     = "limit"
	QualityCD        = "cd-quality"
)

// Validate bounds a quality request.
func (q Quality) Validate(path string) error {
	numbers := q.MaxVideoBitrateKbps != 0 || q.MaxHeight != 0 || q.MaxAudioBitrateKbps != 0
	_, known := playback.LadderPreset(q.ID)
	switch q.Mode {
	case QualityOriginal, QualityCD:
		if numbers || q.ID != "" || q.Step != "" {
			return &FieldError{Path: path}
		}
	case QualityFixed:
		if numbers || q.Step != "" {
			return &FieldError{Path: path}
		}
		if !known {
			return &FieldError{Path: path + ".id"}
		}
	case QualityAutomatic:
		if numbers {
			return &FieldError{Path: path}
		}
		// "original" names where Automatic starts: on no step at all.
		if q.ID != "" && q.ID != QualityOriginal && !known {
			return &FieldError{Path: path + ".id"}
		}
		if q.Step != "" && q.Step != "down" && q.Step != "up" {
			return &FieldError{Path: path + ".step"}
		}
	case QualityLimit:
		if q.ID != "" || q.Step != "" || q.MaxVideoBitrateKbps < 0 || q.MaxVideoBitrateKbps > 2_000_000 || q.MaxHeight < 0 || q.MaxHeight > 8640 || q.MaxAudioBitrateKbps < 0 || q.MaxAudioBitrateKbps > 10_000 {
			return &FieldError{Path: path}
		}
	default:
		return &FieldError{Path: path + ".mode"}
	}
	return nil
}

// apply writes the quality's ceilings into a planner choice.
func (q Quality) apply(c *playback.V1Choice) {
	c.Limit, c.MaxVideoBitrateBPS, c.MaxAudioBitrateBPS, c.MaxVideoHeight = false, 0, 0, 0
	c.AudioCDQuality = q.Mode == QualityCD
	step := func(id string) {
		if preset, ok := playback.LadderPreset(id); ok {
			c.Limit = true
			c.MaxVideoBitrateBPS, c.MaxAudioBitrateBPS, c.MaxVideoHeight = preset.VideoBitrateBPS, preset.MaxAudioBitrateBPS, preset.Height
		}
	}
	switch q.Mode {
	case QualityCD:
		c.QualityID = QualityCD
	case QualityFixed:
		c.QualityID = q.ID
		step(q.ID)
	case QualityAutomatic:
		c.QualityID = QualityAutomatic
		if q.ID != "" {
			c.QualityID = QualityAutomatic + ":" + q.ID
			step(q.ID)
		}
	case QualityLimit:
		c.QualityID = QualityLimit
		// A pure audio limit carries its kbps in its id, the rung id the offers publish.
		if q.MaxAudioBitrateKbps > 0 && q.MaxVideoBitrateKbps == 0 && q.MaxHeight == 0 {
			c.QualityID = playback.LimitQualityID(q.MaxAudioBitrateKbps)
		}
		c.Limit = true
		c.MaxVideoBitrateBPS, c.MaxAudioBitrateBPS, c.MaxVideoHeight = q.MaxVideoBitrateKbps*1000, q.MaxAudioBitrateKbps*1000, q.MaxHeight
	default:
		c.QualityID = QualityOriginal
	}
}

// savedQuality is the quality a saved default names: "original", "automatic"
// or a ladder id. Anything else is the original.
func savedQuality(name string) Quality {
	switch {
	case name == QualityAutomatic:
		return Quality{Mode: QualityAutomatic}
	case name == QualityOriginal || name == "":
		return Quality{Mode: QualityOriginal}
	}
	if _, ok := playback.LadderPreset(name); ok {
		return Quality{Mode: QualityFixed, ID: name}
	}
	return Quality{Mode: QualityOriginal}
}

// SourceFacts is what quality decisions need to know about the file: its
// picture height and its whole bitrate. Zero means unknown.
type SourceFacts struct {
	Height      int
	BitrateKbps int
}

// ladderFor returns the complete canonical menu. Automatic separately narrows
// these ceilings to genuinely smaller steps evidenced by source facts.
func ladderFor(source SourceFacts) []playback.QualityPreset {
	return playback.QualityLadder
}

// presetKbps is everything a ladder step delivers at most, picture and sound.
func presetKbps(p playback.QualityPreset) int {
	return (p.VideoBitrateBPS + p.MaxAudioBitrateBPS) / 1000
}

// stepAutomatic moves an automatic quality one step down or up for a source.
// Down from the original lands on the widest step that is actually smaller
// than the file; up from the widest such step returns to the original. A move
// that cannot be made (already at the smallest step, already at the original)
// returns the quality unchanged.
func stepAutomatic(current Quality, direction string, source SourceFacts) Quality {
	if source.Height <= 0 || source.BitrateKbps <= 0 {
		return Quality{Mode: QualityAutomatic}
	}
	steps := ladderFor(source)
	// Steps that would not make the stream smaller than the original are not
	// steps down at all.
	useful := steps[:0:0]
	for _, preset := range steps {
		if source.Height > 0 && preset.Height > source.Height && !(source.Height < playback.QualityLadder[len(playback.QualityLadder)-1].Height && preset.ID == playback.QualityLadder[len(playback.QualityLadder)-1].ID) || source.BitrateKbps > 0 && presetKbps(preset) >= source.BitrateKbps {
			continue
		}
		useful = append(useful, preset)
	}
	if len(useful) == 0 {
		return Quality{Mode: QualityAutomatic}
	}
	at := -1
	for i, preset := range useful {
		if preset.ID == current.ID {
			at = i
		}
	}
	out := Quality{Mode: QualityAutomatic, ID: current.ID}
	switch direction {
	case "down":
		switch {
		case current.ID == "":
			out.ID = useful[0].ID
		case at >= 0 && at < len(useful)-1:
			out.ID = useful[at+1].ID
		case at < 0:
			// On a step that no longer applies to this file: the nearest
			// useful step below it, or the smallest.
			out.ID = useful[len(useful)-1].ID
			if here, ok := playback.LadderPreset(current.ID); ok {
				for _, preset := range useful {
					if preset.VideoBitrateBPS < here.VideoBitrateBPS {
						out.ID = preset.ID
						break
					}
				}
			}
		}
	case "up":
		switch {
		case current.ID == "":
		case at > 0:
			out.ID = useful[at-1].ID
		default:
			out.ID = ""
		}
	}
	return out
}

// fitAutomatic is the automatic quality for a measured connection: the
// original when it fits, else the widest ladder step that does, else the
// smallest step. A stream fits when it needs no more than four fifths of what
// the connection was measured to carry. An unknown file bitrate fits.
func fitAutomatic(throughputKbps int, source SourceFacts) Quality {
	out := Quality{Mode: QualityAutomatic}
	if throughputKbps <= 0 {
		return out
	}
	budget := throughputKbps * 4 / 5
	if source.BitrateKbps <= budget {
		return out
	}
	steps := []playback.QualityPreset{}
	for _, p := range ladderFor(source) {
		if source.Height > 0 && (p.Height <= source.Height || source.Height < playback.QualityLadder[len(playback.QualityLadder)-1].Height && p.ID == playback.QualityLadder[len(playback.QualityLadder)-1].ID) && presetKbps(p) < source.BitrateKbps {
			steps = append(steps, p)
		}
	}
	if len(steps) == 0 {
		return out
	}
	out.ID = steps[len(steps)-1].ID
	for _, preset := range steps {
		if presetKbps(preset) <= budget {
			out.ID = preset.ID
			break
		}
	}
	return out
}

// QualityOption is one entry of the quality menu for a title (playback
// options `qualities`). Video starts with Original and Automatic; music
// offers Original and its five audio limits.
type QualityOption struct {
	Mode                string  `json:"mode"`
	ID                  string  `json:"id,omitempty"`
	MaxHeight           int     `json:"maxHeight,omitempty"`
	MaxVideoBitrateKbps int     `json:"maxVideoBitrateKbps,omitempty"`
	MaxAudioBitrateKbps int     `json:"maxAudioBitrateKbps,omitempty"`
	SameAsOriginal      bool    `json:"sameAsOriginal"`
	DataGBPerHour       float64 `json:"dataGBPerHour,omitempty"`
	Applicable          bool    `json:"applicable"`
}

// qualityOptions is the menu for one source.
func qualityOptions(source SourceFacts, audioOnly bool) []QualityOption {
	out := []QualityOption{{Mode: QualityOriginal, Applicable: true}, {Mode: QualityAutomatic, Applicable: true}}
	for _, preset := range playback.QualityLadder {
		out = append(out, QualityOption{Mode: QualityFixed, ID: preset.ID, MaxHeight: preset.Height, MaxVideoBitrateKbps: preset.VideoBitrateBPS / 1000, MaxAudioBitrateKbps: preset.MaxAudioBitrateBPS / 1000, DataGBPerHour: preset.DataGBPerHour, SameAsOriginal: !audioOnly && preset.SameAsSource(source.Height, int64(source.BitrateKbps)*1000), Applicable: !audioOnly})
	}
	return out
}

// planKbps is what a plan needs the connection to carry: the file's own rate
// when the picture is not converted, the conversion's budget when it is.
func planKbps(plan playback.DeliveryPlan) int {
	if plan.VideoAction == "convert" || plan.VideoAction == "none" && plan.AudioAction == "convert" {
		return (plan.VideoBitrateBPS + plan.AudioBitrateBPS) / 1000
	}
	if plan.Trace != nil {
		return int(plan.Trace.BitRate / 1000)
	}
	return 0
}

// sourceFactsOf reads the quality-relevant facts of a plan's source.
func sourceFactsOf(plan *playback.DeliveryPlan) SourceFacts {
	var out SourceFacts
	if plan == nil || plan.Trace == nil {
		return out
	}
	out.BitrateKbps = int(plan.Trace.BitRate / 1000)
	if plan.Trace.Video != nil {
		out.Height = plan.Trace.Video.Height
	}
	return out
}

// suggestedQuality is the widest fixed quality a measured connection carries
// for a source, for the refusal that says a start cannot work.
func suggestedQuality(throughputKbps int, source SourceFacts) string {
	fitted := fitAutomatic(throughputKbps, SourceFacts{Height: source.Height, BitrateKbps: 1 << 30})
	return fitted.ID
}
