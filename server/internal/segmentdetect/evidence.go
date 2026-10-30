package segmentdetect

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"strconv"
)

// EvidenceAlgorithm identifies how evidence was measured. Evidence of another
// algorithm is never compared with this one; a change here re-measures files.
const EvidenceAlgorithm = "portico-segments-1"

// Evidence is everything one file's measurement keeps for later comparison
// with other episodes: fingerprints of where an intro and the credits can be,
// and, when the picture was read, one feature per sampled frame near the end.
type Evidence struct {
	DurationUS int64
	Opening    Fingerprint
	Closing    Fingerprint
	// Frames start at FramesStartUS, one every FrameStepUS.
	FramesStartUS, FrameStepUS int64
	Frames                     []FrameFeature
}

type wireFingerprint struct {
	StartUS string `json:"startUS"`
	StepUS  string `json:"stepUS"`
	Points  string `json:"points"`
	Energy  string `json:"energy"`
}

type wireEvidence struct {
	Algorithm     string           `json:"algorithm"`
	DurationUS    string           `json:"durationUS"`
	Opening       *wireFingerprint `json:"opening,omitempty"`
	Closing       *wireFingerprint `json:"closing,omitempty"`
	FramesStartUS string           `json:"framesStartUS,omitempty"`
	FrameStepUS   string           `json:"frameStepUS,omitempty"`
	Frames        string           `json:"frames,omitempty"`
}

func encodeFingerprint(f Fingerprint) *wireFingerprint {
	if len(f.Points) == 0 {
		return nil
	}
	points := make([]byte, 0, len(f.Points)*4)
	for _, p := range f.Points {
		points = binary.LittleEndian.AppendUint32(points, p)
	}
	return &wireFingerprint{StartUS: strconv.FormatInt(f.StartUS, 10), StepUS: strconv.FormatInt(StepUS, 10), Points: base64.StdEncoding.EncodeToString(points), Energy: base64.StdEncoding.EncodeToString(f.Energy)}
}

func decodeFingerprint(w *wireFingerprint) (Fingerprint, error) {
	if w == nil {
		return Fingerprint{}, nil
	}
	start, e1 := strconv.ParseInt(w.StartUS, 10, 64)
	step, e2 := strconv.ParseInt(w.StepUS, 10, 64)
	points, e3 := base64.StdEncoding.DecodeString(w.Points)
	energy, e4 := base64.StdEncoding.DecodeString(w.Energy)
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || step != StepUS || start < 0 || len(points)%4 != 0 || len(points)/4 != len(energy) || len(energy) > MaxPoints {
		return Fingerprint{}, ErrInput
	}
	f := Fingerprint{StartUS: start, Points: make([]uint32, len(energy)), Energy: energy}
	for i := range f.Points {
		f.Points[i] = binary.LittleEndian.Uint32(points[i*4:])
	}
	return f, nil
}

// MaxPoints bounds one stored fingerprint (40 minutes of points).
const MaxPoints = 19200

// MaxFrames bounds the stored frame features (an hour at one a second).
const MaxFrames = 3600

// Marshal encodes evidence as a JSON document.
func (e Evidence) Marshal() []byte {
	w := wireEvidence{Algorithm: EvidenceAlgorithm, DurationUS: strconv.FormatInt(e.DurationUS, 10), Opening: encodeFingerprint(e.Opening), Closing: encodeFingerprint(e.Closing)}
	if len(e.Frames) > 0 {
		frames := make([]byte, 0, len(e.Frames)*2)
		for _, f := range e.Frames {
			frames = append(frames, f.Class, byte(f.Scroll))
		}
		w.FramesStartUS, w.FrameStepUS = strconv.FormatInt(e.FramesStartUS, 10), strconv.FormatInt(e.FrameStepUS, 10)
		w.Frames = base64.StdEncoding.EncodeToString(frames)
	}
	out, _ := json.Marshal(w)
	return out
}

// UnmarshalEvidence reads evidence written by Marshal with this algorithm.
func UnmarshalEvidence(data []byte) (Evidence, error) {
	var w wireEvidence
	if json.Unmarshal(data, &w) != nil || w.Algorithm != EvidenceAlgorithm {
		return Evidence{}, ErrInput
	}
	var e Evidence
	var err error
	if e.DurationUS, err = strconv.ParseInt(w.DurationUS, 10, 64); err != nil || e.DurationUS <= 0 {
		return Evidence{}, ErrInput
	}
	if e.Opening, err = decodeFingerprint(w.Opening); err != nil {
		return Evidence{}, err
	}
	if e.Closing, err = decodeFingerprint(w.Closing); err != nil {
		return Evidence{}, err
	}
	if w.Frames != "" {
		frames, err := base64.StdEncoding.DecodeString(w.Frames)
		start, e1 := strconv.ParseInt(w.FramesStartUS, 10, 64)
		step, e2 := strconv.ParseInt(w.FrameStepUS, 10, 64)
		if err != nil || e1 != nil || e2 != nil || len(frames)%2 != 0 || len(frames)/2 > MaxFrames || step <= 0 || start < 0 {
			return Evidence{}, ErrInput
		}
		e.FramesStartUS, e.FrameStepUS = start, step
		for i := 0; i < len(frames); i += 2 {
			if frames[i] > FrameText {
				return Evidence{}, ErrInput
			}
			e.Frames = append(e.Frames, FrameFeature{Class: frames[i], Scroll: int8(frames[i+1])})
		}
	}
	return e, nil
}
