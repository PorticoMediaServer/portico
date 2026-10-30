package mediaanalysis

import (
	"context"
	"io"

	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/segmentdetect"
)

// Segment detection modes (operations.SegmentDetectionModes): standard listens
// to a show's episodes; thorough also reads the picture near the end of every
// episode and film.
const (
	SegmentModeOff      = "off"
	SegmentModeStandard = "standard"
	SegmentModeThorough = "thorough"
)

// region is one stretch of a file the detector reads, in file microseconds.
type region struct{ StartUS, LengthUS int64 }

func (r region) empty() bool { return r.LengthUS <= 0 }

// segmentPlan is what to read from one file. An empty plan measures nothing
// (a film in standard mode has only its chapter titles).
type segmentPlan struct {
	Opening, Closing, Frames region
}

// planSegments chooses the regions for a file. An intro is looked for in the
// first quarter of an episode (at least five minutes, at most ten, never past
// its middle); credits in its last third, at most eight minutes. A film's
// credits are looked for in its last quarter, at most twenty minutes.
func planSegments(kind, mode string, durationUS int64, audio, video bool) segmentPlan {
	return planSegmentsWithPolicy(kind, mode, durationUS, audio, video, defaultSegmentPlanning())
}

func planSegmentsWithPolicy(kind, mode string, durationUS int64, audio, video bool, policy segmentPlanning) segmentPlan {
	var p segmentPlan
	introCap, creditsCap, filmCap := int64(600_000_000), int64(480_000_000), int64(1_200_000_000)
	if policy.IntroWindow == "long" {
		introCap = 900_000_000
	}
	if policy.CreditsWindow == "long" {
		creditsCap = 900_000_000
	}
	if policy.FilmCreditsWindow == "long" {
		filmCap = 1_800_000_000
	}
	switch kind {
	case "episode":
		if durationUS < policy.EpisodeMinutes*60_000_000 || !audio && !(video && mode == SegmentModeThorough) {
			return p
		}
		if audio {
			opening := min(durationUS/2, max(durationUS/4, 300_000_000), introCap)
			if policy.IntroWindow == "long" {
				opening = min(durationUS/2, introCap)
			}
			closing := min(durationUS/3, creditsCap)
			if policy.CreditsWindow == "long" {
				closing = min(durationUS, creditsCap)
			}
			p.Opening = region{0, opening}
			p.Closing = region{durationUS - closing, closing}
		}
		if video && mode == SegmentModeThorough {
			closing := min(durationUS/3, creditsCap)
			if policy.CreditsWindow == "long" {
				closing = min(durationUS, creditsCap)
			}
			p.Frames = region{durationUS - closing, closing}
		}
	case "movie":
		if video && mode == SegmentModeThorough && durationUS >= policy.FilmMinutes*60_000_000 {
			closing := min(durationUS/4, filmCap)
			if policy.FilmCreditsWindow == "long" {
				closing = min(durationUS, filmCap)
			}
			p.Frames = region{durationUS - closing, closing}
		}
	}
	return p
}

func (p segmentPlan) empty() bool { return p.Opening.empty() && p.Closing.empty() && p.Frames.empty() }

// extractSegmentEvidence decodes the plan's regions and measures them. Each
// region is one bounded decoder run; the output is read as it arrives and
// never held whole.
func extractSegmentEvidence(ctx context.Context, decode Decode, key string, p segmentPlan, durationUS int64, video decoder.PreviewVideoFacts) (segmentdetect.Evidence, error) {
	e := segmentdetect.Evidence{DurationUS: durationUS}
	audio := func(name string, r region) (segmentdetect.Fingerprint, error) {
		if r.empty() {
			return segmentdetect.Fingerprint{}, nil
		}
		points := int(r.LengthUS/segmentdetect.StepUS) + 16
		if points > segmentdetect.MaxPoints {
			return segmentdetect.Fingerprint{}, ErrBudget
		}
		f := segmentdetect.NewFingerprinter(r.StartUS, points)
		limit := (r.LengthUS/1_000_000 + 4) * segmentdetect.SampleRate * 2
		spec := decoder.AnalysisSpec{Kind: "segment_audio", StartUS: r.StartUS, RegionUS: r.LengthUS, MaxDurationUS: durationUS}
		err := decode(ctx, key+":"+name, spec, func(reader io.Reader) error {
			buf := make([]byte, 32*1024)
			// A read can end between the two bytes of a sample.
			var odd []byte
			read := int64(0)
			for {
				n, err := reader.Read(buf)
				read += int64(n)
				if read > limit {
					return ErrBudget
				}
				data := buf[:n]
				if len(odd) == 1 && len(data) > 0 {
					if e := f.WritePCM([]byte{odd[0], data[0]}); e != nil {
						return e
					}
					odd, data = odd[:0], data[1:]
				}
				whole := len(data) &^ 1
				if e := f.WritePCM(data[:whole]); e != nil {
					return e
				}
				if whole < len(data) {
					odd = append(odd[:0], data[whole])
				}
				if err == io.EOF {
					return nil
				}
				if err != nil {
					return err
				}
			}
		})
		return f.Finish(), err
	}
	var err error
	if e.Opening, err = audio("opening", p.Opening); err != nil {
		return e, err
	}
	if e.Closing, err = audio("closing", p.Closing); err != nil {
		return e, err
	}
	if !p.Frames.empty() {
		count := int(p.Frames.LengthUS/1_000_000) + 2
		if count > segmentdetect.MaxFrames {
			return e, ErrBudget
		}
		spec := decoder.AnalysisSpec{Kind: "segment_frames", StartUS: p.Frames.StartUS, RegionUS: p.Frames.LengthUS, IntervalUS: 1_000_000, MaxFrames: count, MaxDurationUS: durationUS, SourceVideo: video}
		e.FramesStartUS, e.FrameStepUS = p.Frames.StartUS, 1_000_000
		reader := segmentdetect.NewFrameReader()
		err = decode(ctx, key+":frames", spec, func(r io.Reader) error {
			frame := make([]byte, segmentdetect.FrameWidth*segmentdetect.FrameHeight)
			for {
				if _, err := io.ReadFull(r, frame); err == io.EOF {
					return nil
				} else if err != nil {
					return err
				}
				if len(e.Frames) >= count {
					return ErrBudget
				}
				feature, err := reader.Read(frame)
				if err != nil {
					return err
				}
				e.Frames = append(e.Frames, feature)
			}
		})
		if err != nil {
			return e, err
		}
	}
	return e, nil
}

// fileSegments are the detections one file's evidence and chapters give on
// their own, without another episode: chapter titles, and the credits the
// picture shows. They are suggestions for the owner, never automatic.
func fileSegments(e segmentdetect.Evidence, chapters []chapter) []segmentdetect.Detection {
	var out []segmentdetect.Detection
	for _, c := range chapters {
		if c.Start < 0 || c.End > e.DurationUS || c.End <= c.Start {
			continue
		}
		if kind := segmentdetect.ChapterKind(c.Title); kind != "" {
			out = append(out, segmentdetect.Detection{Kind: kind, StartUS: c.Start, EndUS: c.End, Confidence: 0.85, Provenance: segmentdetect.ChapterProvenance})
		}
	}
	if len(e.Frames) > 0 {
		out = append(out, segmentdetect.VisualCredits(e.Frames, e.FramesStartUS, e.FrameStepUS, e.DurationUS)...)
	}
	return out
}
