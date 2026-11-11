package mediaengine_test

import (
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/api"
	"github.com/PorticoMediaServer/portico/server-next/internal/streaming"
)

// What the media matrix holds a stream's output to.

// frame decodes the picture at a time into 320×180 grey levels.
func (m *matrix) frame(t *testing.T, at float64, input ...string) []byte {
	t.Helper()
	args := append([]string{"-v", "error", "-nostdin"}, input...)
	args = append(args, "-ss", strconv.FormatFloat(at, 'f', 2, 64), "-frames:v", "1", "-vf", "scale=320:180,format=gray", "-f", "rawvideo", "pipe:1")
	out, err := exec.Command(m.ffmpeg, args...).Output()
	if err != nil || len(out) != 320*180 {
		t.Fatalf("decoding a frame at %.1fs of %v: %v (%d bytes)", at, input, err, len(out))
	}
	return out
}

// brightness is a frame's mean grey level and its shares of near-black and clipped pixels.
func brightness(frame []byte) (mean, black, clipped float64) {
	for _, v := range frame {
		mean += float64(v)
		if v <= 20 {
			black++
		}
		if v >= 250 {
			clipped++
		}
	}
	n := float64(len(frame))
	return mean / n, black / n, clipped / n
}

// boxChange is how much the subtitle's box differs between two frames, inside and outside it.
func boxChange(a, b []byte) (inside, outside float64) {
	var in, out, nIn, nOut float64
	for y := 0; y < 180; y++ {
		for x := 0; x < 320; x++ {
			d := math.Abs(float64(a[y*320+x]) - float64(b[y*320+x]))
			if x >= boxX && x < boxX+boxW && y >= boxY && y < boxY+boxH {
				in, nIn = in+d, nIn+1
			} else {
				out, nOut = out+d, nOut+1
			}
		}
	}
	return in / nIn, out / nOut
}

// finding is one place where what came out is not what was planned.
type finding struct {
	t      *testing.T
	failed []string
}

func (f *finding) check(ok bool, format string, args ...any) {
	if !ok {
		f.failed = append(f.failed, fmt.Sprintf(format, args...))
	}
}

func (f *finding) report(where string) {
	f.t.Helper()
	if len(f.failed) > 0 {
		f.t.Errorf("FINDING %s: what came out is not what was planned:\n  %s", where, strings.Join(f.failed, "\n  "))
	}
}

// checkVideo holds the stream's picture against the plan and the source.
func checkVideo(f *finding, plan streaming.DeliveryPlan, source, out *probed, vfr bool) {
	if out == nil {
		f.check(false, "the stream has no picture")
		return
	}
	sw, sh := source.display()
	ow, oh := out.display()
	if plan.VideoAction != "convert" {
		f.check(out.Codec == source.Codec, "picture copied, so %s, but it is %s", source.Codec, out.Codec)
		f.check(out.depth() == source.depth(), "picture copied, so %d-bit, but it is %d-bit (%s)", source.depth(), out.depth(), out.PixFmt)
		f.check(out.Transfer == source.Transfer, "picture copied, so transfer %q, but it is %q", source.Transfer, out.Transfer)
		f.check(ow == sw && oh == sh, "picture copied, so %dx%d upright, but it shows %dx%d (rotation lost or added)", sw, sh, ow, oh)
		return
	}
	f.check(out.Codec == plan.OutputVideoCodec, "picture converted to %s, but it is %s", plan.OutputVideoCodec, out.Codec)
	depth := max(plan.OutputBitDepth, 8)
	f.check(out.depth() == depth, "planned %d-bit, but it is %d-bit (%s)", depth, out.depth(), out.PixFmt)
	switch {
	case plan.OutputRange == "hdr10":
		f.check(out.Transfer == "smpte2084" && out.Primaries == "bt2020", "planned HDR10, but tagged %s/%s", out.Transfer, out.Primaries)
	case plan.OutputRange == "hlg":
		f.check(out.Transfer == "arib-std-b67" && out.Primaries == "bt2020", "planned HLG, but tagged %s/%s", out.Transfer, out.Primaries)
	case plan.ToneMap:
		f.check(out.Transfer == "bt709" && out.Primaries == "bt709", "planned tone-mapped SDR, but tagged %s/%s", out.Transfer, out.Primaries)
	default:
		f.check(!hdr(out.Transfer), "planned SDR, but tagged %s", out.Transfer)
		f.check(!hdr(source.Transfer) || plan.ToneMapOff, "an HDR picture planned to SDR without tone mapping")
	}
	f.check(out.Range == "tv" || out.Range == "unknown" || out.Range == "", "colour range %q, want limited (tv)", out.Range)
	if plan.TargetHeight > 0 {
		f.check(oh == plan.TargetHeight || out.Height == plan.TargetHeight, "planned %d high, but it is %dx%d", plan.TargetHeight, ow, oh)
	} else {
		f.check(oh <= sh && ow <= sw, "the picture grew from %dx%d to %dx%d", sw, sh, ow, oh)
	}
	if sw > 0 && sh > 0 && ow > 0 && oh > 0 {
		f.check(math.Abs(float64(ow)/float64(oh)-float64(sw)/float64(sh)) < 0.05, "the shape changed from %dx%d to %dx%d (sideways or stretched)", sw, sh, ow, oh)
	}
	if source.FieldOrder != "" && source.FieldOrder != "progressive" && source.FieldOrder != "unknown" {
		f.check(plan.Deinterlace, "an interlaced picture (%s) converted without deinterlacing", source.FieldOrder)
		f.check(out.FieldOrder == "progressive" || out.FieldOrder == "" || out.FieldOrder == "unknown", "still interlaced after conversion (%s)", out.FieldOrder)
	}
	if !vfr && source.fps() > 0 && out.fps() > 0 {
		want := source.fps()
		if plan.MaxFrameRate > 0 && want > plan.MaxFrameRate {
			want = plan.MaxFrameRate
		}
		doubled := plan.Deinterlace && math.Abs(out.fps()-2*want) < 0.5
		f.check(math.Abs(out.fps()-want) < 0.5 || doubled, "frame rate %.3f, want %.3f", out.fps(), want)
	}
}

// checkAudio holds the stream's sound against the plan and the source.
func checkAudio(f *finding, plan streaming.DeliveryPlan, source, out *probed) {
	if source == nil {
		return
	}
	if out == nil {
		f.check(false, "the stream has no sound")
		return
	}
	if plan.AudioAction != "convert" {
		f.check(out.Codec == source.Codec, "sound copied, so %s, but it is %s", source.Codec, out.Codec)
		f.check(out.Channels == source.Channels, "sound copied, so %d channels, but it has %d", source.Channels, out.Channels)
		return
	}
	f.check(out.Codec == plan.OutputAudioCodec, "sound converted to %s, but it is %s", plan.OutputAudioCodec, out.Codec)
	if plan.AudioChannels > 0 {
		f.check(out.Channels == plan.AudioChannels, "planned %d channels, but it has %d", plan.AudioChannels, out.Channels)
	}
}

// checkTimeline holds that each track starts where it should, runs to where it should
// without a hole, and that picture and sound start together.
func checkTimeline(f *finding, video, audio *probed, start, end float64) {
	for _, track := range []*probed{video, audio} {
		if track == nil {
			continue
		}
		if len(track.times) < 10 {
			f.check(false, "%s has only %d packets", track.Type, len(track.times))
			continue
		}
		first, last := track.times[0], track.times[len(track.times)-1]
		f.check(math.Abs(first-start) <= 0.5, "%s starts at %.3fs, want about %.0fs", track.Type, first, start)
		f.check(last >= end-1, "%s ends at %.3fs, want about %.0fs", track.Type, last, end)
		for i := 1; i < len(track.times); i++ {
			if gap := track.times[i] - track.times[i-1]; gap > 0.25 {
				f.check(false, "%s has a %.3fs hole at %.3fs", track.Type, gap, track.times[i-1])
				break
			}
		}
	}
	if video != nil && audio != nil && len(video.times) > 0 && len(audio.times) > 0 {
		f.check(math.Abs(video.times[0]-audio.times[0]) <= 0.3, "picture starts at %.3fs and sound at %.3fs", video.times[0], audio.times[0])
	}
}

// checkText holds that every text subtitle delivered beside the stream has its words at
// their times.
func (m *matrix) checkText(f *finding, session *api.PlaybackSession, tracks map[string]string) {
	served := map[string]string{}
	for _, s := range session.Presentation.Subtitles {
		code, _, body := m.on(f.t).media(s.URL)
		f.check(code == 200 && strings.HasPrefix(string(body), "WEBVTT"), "subtitle %s answered %d: %.80q", s.TrackID, code, body)
		served[s.TrackID] = string(body)
	}
	for kind, wanted := range textCues {
		vtt, ok := served[tracks[kind]]
		if !ok {
			f.check(false, "the %s subtitle is not delivered beside the stream", kind)
			continue
		}
		got := cues(vtt)
		for _, want := range wanted {
			found := false
			for _, c := range got {
				found = found || c.words == want.words && math.Abs(c.from-want.from) <= 0.1 && math.Abs(c.to-want.to) <= 0.1
			}
			f.check(found, "the %s subtitle lacks %q from %.1fs to %.1fs in:\n%s", kind, want.words, want.from, want.to, vtt)
		}
		f.check(!strings.Contains(vtt, `{\`), "the %s subtitle still carries styling codes:\n%s", kind, vtt)
	}
}
