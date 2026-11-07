package writer

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MGSetDVRDefaults      CommandID = 845
	MGSetDVRStoragePolicy CommandID = 846
)

func mgDVRPolicyDescriptors() []Descriptor {
	return []Descriptor{{MGSetDVRDefaults, "mg.dvr.defaults", Normal}, {MGSetDVRStoragePolicy, "mg.dvr.storage-policy", Normal}}
}

const mgDVRPolicyMaxBytes int64 = 1<<53 - 1

// Owned value snapshots, never admin/source authority or physical retirement
// capabilities. The parent supplies current opaque authority, ORIGINAL public
// receipts/lifetimes, admission/events, same-frame scheduler/default selection,
// final disclosure and COMMIT truth. No bool/certificate here authorizes cleanup.
// Before accepting any nonzero retention/storage enforcement as an enabled
// command, the parent must bind actual storage/retirement availability. These
// unwired scalar effects never run retention, release debt or touch recordings.
type mgDVRDefaultsSnapshot struct {
	revision, beforeSeconds, afterSeconds, retentionDays, episodeLimit int64
	conflict, showTemplate, movieTemplate                              string
}
type mgDVRDefaultsInput struct {
	prePaddingMS, postPaddingMS, keepCount, keepDays int64
	keepMode, conflict, showTemplate, movieTemplate  string
}
type mgDVRDefaultsDesired struct {
	beforeSeconds, afterSeconds, retentionDays, episodeLimit int64
	conflict                                                 string
}
type mgDVRStorageDesired struct{ retentionDays, episodeLimit, floorBytes, capBytes int64 }
type mgDVRStorageSnapshot struct {
	revision int64
	policy   mgDVRStorageDesired
}

func mgDVRClamp(v, maximum int64) int64 {
	if v < 0 {
		return 0
	}
	if v > maximum {
		return maximum
	}
	return v
}

// Public administration behavior: clamp before converting milliseconds to whole
// seconds; reject unsupported template EDITS; retain the actually observed
// templates in the effect. Recording-layout validation does not enable its UI.
func freezeMGDVRDefaults(in mgDVRDefaultsInput) (mgDVRDefaultsDesired, error) {
	if in.showTemplate != "" || in.movieTemplate != "" {
		return mgDVRDefaultsDesired{}, ErrInvalid
	}
	if in.conflict == "" {
		in.conflict = "keep-watching"
	}
	out := mgDVRDefaultsDesired{beforeSeconds: mgDVRClamp(in.prePaddingMS, 1800000) / 1000, afterSeconds: mgDVRClamp(in.postPaddingMS, 7200000) / 1000, conflict: in.conflict}
	switch in.keepMode {
	case "keep-all":
	case "keep-days":
		if in.keepDays < 1 {
			return mgDVRDefaultsDesired{}, ErrInvalid
		}
		out.retentionDays = mgDVRClamp(in.keepDays, 3650)
	case "keep-count":
		if in.keepCount < 1 {
			return mgDVRDefaultsDesired{}, ErrInvalid
		}
		out.episodeLimit = mgDVRClamp(in.keepCount, 1000)
	default:
		return mgDVRDefaultsDesired{}, ErrInvalid
	}
	if !out.valid() {
		return mgDVRDefaultsDesired{}, ErrInvalid
	}
	return out, nil
}
func (d mgDVRDefaultsDesired) valid() bool {
	return d.beforeSeconds >= 0 && d.beforeSeconds <= 1800 && d.afterSeconds >= 0 && d.afterSeconds <= 7200 && d.retentionDays >= 0 && d.retentionDays <= 3650 && d.episodeLimit >= 0 && d.episodeLimit <= 1000 && (d.retentionDays == 0 || d.episodeLimit == 0) && mgDVRConflict(d.conflict)
}
func freezeMGDVRStorage(in mgDVRStorageDesired) (mgDVRStorageDesired, error) {
	if !in.valid() {
		return mgDVRStorageDesired{}, ErrInvalid
	}
	return in, nil
}
func (d mgDVRStorageDesired) valid() bool {
	// Independent public floor/cap limits: do not invent a floor<=cap relation.
	return d.retentionDays >= 0 && d.retentionDays <= 3650 && d.episodeLimit >= 0 && d.episodeLimit <= 100000 && d.floorBytes >= 0 && d.floorBytes <= mgDVRPolicyMaxBytes && d.capBytes >= 0 && d.capBytes <= mgDVRPolicyMaxBytes
}
func mgDVRConflict(s string) bool { return s == "keep-watching" || s == "stop-watching" }
func (s mgDVRDefaultsSnapshot) valid() bool {
	// Observations also serve the existing DefaultOptions/layout readers. Their
	// legal retained option range is broader than the public admin edit range.
	return s.revision > 0 && s.beforeSeconds >= 0 && s.beforeSeconds <= 21600 && s.afterSeconds >= 0 && s.afterSeconds <= 21600 && s.retentionDays >= 0 && s.retentionDays <= 3650 && s.episodeLimit >= 0 && s.episodeLimit <= 100000 && mgDVRConflict(s.conflict) && mgDVRRecordingTemplate(s.showTemplate) && mgDVRRecordingTemplate(s.movieTemplate)
}
func (s mgDVRStorageSnapshot) valid() bool { return s.revision > 0 && s.policy.valid() }

// The bounded existing recording-layout grammar, independent of old runtime
// writers and filesystem code. Empty selects the occurrence-private layout;
// literal collisions/fallback and rendering remain with the actual layout owner.
func mgDVRRecordingTemplate(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > 400 || !utf8.ValidString(value) || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." {
			return false
		}
	}
	for len(value) > 0 {
		before, tail, found := strings.Cut(value, "{")
		if strings.ContainsAny(before, "}\\:*?\"<>|") {
			return false
		}
		if !found {
			break
		}
		name, after, closed := strings.Cut(tail, "}")
		if !closed {
			return false
		}
		token, pad, padded := strings.Cut(name, ":")
		supports := false
		switch token {
		case "season", "episode", "channelNumber":
			supports = true
		case "series", "title", "year", "channel", "date", "time", "source", "group":
		default:
			return false
		}
		if padded && (pad != "02" || !supports) {
			return false
		}
		value = after
	}
	return true
}
