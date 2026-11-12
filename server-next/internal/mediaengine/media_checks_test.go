package mediaengine_test

import (
	"fmt"
	"math"
	"os/exec"
	"regexp"
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

// boxChange compares two frames: lift is how much brighter the subtitle's box is in a than
// in b, on average, and outside how much the rest of the picture differs. Averaging the
// box's brightness with its sign ignores the noise of two different encodes, which a white
// box does not.
func boxChange(a, b []byte) (lift, outside float64) {
	var in, out, nIn, nOut float64
	for y := 0; y < 180; y++ {
		for x := 0; x < 320; x++ {
			d := float64(a[y*320+x]) - float64(b[y*320+x])
			if x >= boxX && x < boxX+boxW && y >= boxY && y < boxY+boxH {
				in, nIn = in+d, nIn+1
			} else {
				out, nOut = out+math.Abs(d), nOut+1
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

// knownFailures are failures that come out today for a reason that is known and owned, by
// the case's name. Each mark names the failure line it expects, what a viewer loses and what
// removes it. A failed line a mark of its case expects is logged, not failed, so a new
// failure stands out; any other failed line in the same case is still a FINDING. A mark
// seen on every run whose line stops coming fails the test with FIXED?, so the mark is
// removed. A sometimes mark comes and goes; a run that does not show it says so in the log,
// and the mark is removed when what it waits for has landed and the job has stopped showing it.
var knownFailures = map[string][]mark{
	// Finding 1 on #2604. It follows the FFmpeg doing the conversion: seen on every run with
	// Ubuntu 24.04's FFmpeg 6.1.1 (GitHub's runners), never with FFmpeg 8.0.1 (mini-1) or
	// Portico's own FFmpeg 8.1.3. So these marks are in force only when the suite runs with
	// an FFmpeg 6 or older (seekFaultFFmpeg); with any other the cases must simply pass.
	"hevc-hlg for chromium after a seek":   {lateAfterSeek},
	"h264-10bit for chromium after a seek": {lateAfterSeek},
	"long-jump for chromium after a seek":  {lateAfterSeek},
	"hevc-hdr10 for chromium after a seek": {lateAfterSeek, endedAfterSeek},
}

type mark struct {
	expect       *regexp.Regexp
	loses, until string
	sometimes    bool
	// onFFmpeg6 says the mark is in force only when the FFmpeg in use has the AV1 seek fault.
	onFFmpeg6 bool
}

// seekFaultFFmpeg says the FFmpeg this run uses is one whose AV1 conversion after a seek
// starts off the segment grid. newMatrix sets it from the version FFmpeg reports.
var seekFaultFFmpeg bool

// hasSeekFault reads the first line of `ffmpeg -version`. Keyed on FFmpeg's major version,
// not on its AV1 encoder's: the three FFmpegs measured (6.1.1 with the fault, 8.0.1 and 8.1.3
// without) do not tell at which SVT-AV1 version it went. A version it cannot read (a build
// from git, say) counts as without the fault, so the fault would show as a FINDING there.
func hasSeekFault(versionLine string) bool {
	m := regexp.MustCompile(`^ffmpeg version n?([0-9]+)\.`).FindStringSubmatch(versionLine)
	if m == nil {
		return false
	}
	major, _ := strconv.Atoi(m[1])
	return major <= 6
}

var (
	lateAfterSeek = mark{
		expect: regexp.MustCompile(`^(video|audio) starts at [0-9.]+s, want about [0-9]+s$`),
		loses: "a person on a browser who seeks in a film converted to AV1 sees the picture start about 1.4 s later than the player was told; " +
			"this happens on a server using a system FFmpeg 6 (such as Ubuntu 24.04's), not with Portico's own FFmpeg",
		until:     "the streaming lane holds an AV1 conversion to the segment grid after a seek on FFmpeg 6 too, or Portico stops running on a system FFmpeg 6 (finding 1 on #2604)",
		onFFmpeg6: true,
	}
	// Seen in 5 of 9 runs on the runners (FFmpeg 6.1.1), only here: the server ends the stream it started
	// for the seek before it serves the last segment its own playlist names (the test stops
	// nothing until that segment is read).
	endedAfterSeek = mark{
		expect:    regexp.MustCompile(`^segment-[0-9]+\.m4s, named in video\.m3u8, answered 404: .*This playback has ended`),
		loses:     "a person on a browser who seeks in a film converted to AV1 can have the film stop before its end, with \"This playback has ended\"",
		until:     "the streaming lane finds why the server ends a stream it started for a seek before its last segment, and fixes it",
		sometimes: true,
	}
)

// classify sorts a case's failed lines against its marks: the lines no mark expects, and
// for each mark whether one of its lines came.
func classify(marks []mark, failed []string) (unexpected []string, seen []bool) {
	seen = make([]bool, len(marks))
	for _, line := range failed {
		matched := false
		for i, m := range marks {
			if m.expect.MatchString(line) {
				seen[i], matched = true, true
				break
			}
		}
		if !matched {
			unexpected = append(unexpected, line)
		}
	}
	return unexpected, seen
}

func (f *finding) report(where string) {
	f.t.Helper()
	var marks []mark
	for _, m := range knownFailures[where] {
		if !m.onFFmpeg6 || seekFaultFFmpeg {
			marks = append(marks, m)
		}
	}
	unexpected, seen := classify(marks, f.failed)
	if len(unexpected) > 0 {
		f.t.Errorf("FINDING %s: what came out is not what was planned:\n  %s", where, strings.Join(f.failed, "\n  "))
	}
	for i, m := range marks {
		switch {
		case seen[i] && len(unexpected) == 0:
			f.t.Logf("EXPECTED FAILURE %s: %s, until %s:\n  %s", where, m.loses, m.until, strings.Join(f.failed, "\n  "))
		case !seen[i] && m.sometimes:
			f.t.Logf("NOT SEEN THIS RUN %s: the sometimes mark for %q", where, m.loses)
		case !seen[i]:
			f.t.Errorf("FIXED? %s no longer shows %q, marked as failing until %s: if that has landed, remove its mark from knownFailures", where, m.expect, m.until)
		}
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
