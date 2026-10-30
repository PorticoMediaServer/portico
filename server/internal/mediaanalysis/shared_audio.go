package mediaanalysis

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"sort"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

// errSharedStagePanicked is a shared audio stage's error when its analysis
// panicked (supervise.Go recovered and logged it).
var errSharedStagePanicked = errors.New("shared audio analysis stopped unexpectedly")

// One decode of a file's audio serves every audio analysis a scan wants: the
// decoder hands over the first audio track once, as 48 kHz stereo, and the
// waveform, the fingerprint and the loudness measurement all read that one
// stream. Before, each read and decoded the whole file again (for a film,
// that is the whole multi-gigabyte file per analysis).

// sharedAudioStages are the stages one audio decode serves, in the order a
// scan runs them.
var sharedAudioStages = []string{"waveform", "fingerprint"}

func isSharedAudioStage(stage string) bool {
	for _, s := range sharedAudioStages {
		if s == stage {
			return true
		}
	}
	return false
}

const sharedRate = 48000

// r128 measures programme loudness as EBU R 128 / ITU-R BS.1770-4 describes:
// K-weighted mean square in 400 ms blocks every 100 ms, gated absolutely at
// -70 LUFS and relatively at -10 LU for the integrated value; the loudness
// range from 3 s short-term values gated at -70 LUFS and -20 LU, between the
// 10th and 95th percentiles; the true peak from a 4x oversampled signal.
type r128 struct {
	shelf, highpass [2]biquad
	sub             [2]float64 // this 100 ms sub-block's sum of squares, per channel
	subCount        int
	history         []float64 // the last 30 sub-block powers (both channels summed)
	blocks          []float64 // 400 ms block powers
	shortTerm       []float64 // 3 s window powers
	frames          int64
	peak            float64
	tail            [2][4]float64 // the last samples per channel, for the oversampler
}

type biquad struct{ b0, b1, b2, a1, a2, z1, z2 float64 }

func (q *biquad) step(x float64) float64 {
	y := q.b0*x + q.z1
	q.z1 = q.b1*x - q.a1*y + q.z2
	q.z2 = q.b2*x - q.a2*y
	return y
}

func newR128() *r128 {
	m := &r128{}
	for c := range 2 {
		// BS.1770-4 K-weighting at 48 kHz: the head's high shelf, then the RLB high-pass.
		m.shelf[c] = biquad{b0: 1.53512485958697, b1: -2.69169618940638, b2: 1.19839281085285, a1: -1.69065929318241, a2: 0.73248077421585}
		m.highpass[c] = biquad{b0: 1, b1: -2, b2: 1, a1: -1.99004745483398, a2: 0.99007225036621}
	}
	return m
}

// truePeakTaps interpolate the three in-between phases of a 4x oversampling
// (a short windowed-sinc kernel, four taps per phase).
var truePeakTaps = func() [3][4]float64 {
	var out [3][4]float64
	for phase := 1; phase <= 3; phase++ {
		frac := float64(phase) / 4
		for k := range 4 {
			x := float64(k-1) - frac // taps at -1, 0, 1, 2 around the interpolated point
			sinc := 1.0
			if x != 0 {
				sinc = math.Sin(math.Pi*x) / (math.Pi * x)
			}
			window := 0.5 + 0.5*math.Cos(math.Pi*x/2.5)
			out[phase-1][k] = sinc * window
		}
	}
	return out
}()

func (m *r128) push(left, right float64) {
	for c, x := range [2]float64{left, right} {
		y := m.highpass[c].step(m.shelf[c].step(x))
		m.sub[c] += y * y
		t := &m.tail[c]
		t[0], t[1], t[2], t[3] = t[1], t[2], t[3], x
		m.peak = max(m.peak, math.Abs(x))
		for _, taps := range truePeakTaps {
			v := taps[0]*t[0] + taps[1]*t[1] + taps[2]*t[2] + taps[3]*t[3]
			m.peak = max(m.peak, math.Abs(v))
		}
	}
	m.subCount++
	if m.subCount == sharedRate/10 {
		m.flushSub()
	}
}

func (m *r128) flushSub() {
	power := (m.sub[0] + m.sub[1]) / float64(sharedRate/10)
	m.sub, m.subCount = [2]float64{}, 0
	m.frames++
	m.history = append(m.history, power)
	if len(m.history) > 30 {
		m.history = m.history[1:]
	}
	if n := len(m.history); n >= 4 {
		m.blocks = append(m.blocks, (m.history[n-1]+m.history[n-2]+m.history[n-3]+m.history[n-4])/4)
	}
	if len(m.history) == 30 {
		sum := 0.0
		for _, p := range m.history {
			sum += p
		}
		m.shortTerm = append(m.shortTerm, sum/30)
	}
}

func lufs(power float64) float64 { return -0.691 + 10*math.Log10(power) }

// gatedMean is the mean power of the values above the absolute gate and then
// above the relative gate (relative LU below the first mean); 0 when none pass.
func gatedMean(values []float64, relative float64) (float64, []float64) {
	absolute := math.Pow(10, (-70+0.691)/10)
	var kept []float64
	sum := 0.0
	for _, v := range values {
		if v > absolute {
			kept = append(kept, v)
			sum += v
		}
	}
	if len(kept) == 0 {
		return 0, nil
	}
	gate := sum / float64(len(kept)) * math.Pow(10, relative/10)
	var final []float64
	sum = 0
	for _, v := range kept {
		if v > gate {
			final = append(final, v)
			sum += v
		}
	}
	if len(final) == 0 {
		return 0, nil
	}
	return sum / float64(len(final)), final
}

// summary is the loudness stage's result, in the shape the decoder's ebur128
// measurement had. ok is false when the audio is all silence.
func (m *r128) summary() (map[string]any, bool) {
	integrated, _ := gatedMean(m.blocks, -10)
	if integrated <= 0 || m.frames == 0 {
		return nil, false
	}
	out := map[string]any{"algorithm": "EBU-R128", "integratedLUFS": math.Round(lufs(integrated)*10) / 10, "frames": m.frames, "normalizationApplied": false, "truePeakLinear": m.peak, "measuredOn": "stereo-48000"}
	if _, gated := gatedMean(m.shortTerm, -20); len(gated) > 1 {
		sort.Float64s(gated)
		at := func(p float64) float64 { return lufs(gated[int(math.Round(p*float64(len(gated)-1)))]) }
		out["loudnessRangeLU"] = math.Round((at(0.95)-at(0.10))*10) / 10
	}
	return out, true
}

// downmix turns the 48 kHz stereo stream into the 11025 Hz mono stream the
// waveform and fingerprint read: a fourth-order low-pass below the new
// Nyquist frequency, then linear interpolation at the new rate.
type downmix struct {
	filter   [2]biquad
	previous float64
	position float64 // where the next output sample falls, in input samples from the previous one
	out      []byte
}

func newDownmix() *downmix {
	d := &downmix{position: 0}
	// Butterworth low-pass at 4.5 kHz (48 kHz), as two second-order sections.
	for i, q := range []float64{0.54119610, 1.30656296} {
		w := 2 * math.Pi * 4500 / sharedRate
		alpha := math.Sin(w) / (2 * q)
		c := math.Cos(w)
		a0 := 1 + alpha
		d.filter[i] = biquad{b0: (1 - c) / 2 / a0, b1: (1 - c) / a0, b2: (1 - c) / 2 / a0, a1: -2 * c / a0, a2: (1 - alpha) / a0}
	}
	return d
}

const downmixStep = float64(sharedRate) / 11025

func (d *downmix) push(left, right float64) {
	x := d.filter[1].step(d.filter[0].step((left + right) / 2))
	// Output samples fall between the previous input sample and this one.
	for d.position <= 1 {
		v := d.previous + (x-d.previous)*d.position
		s := int16(max(-32768, min(32767, math.Round(v*32768))))
		d.out = binary.LittleEndian.AppendUint16(d.out, uint16(s))
		d.position += downmixStep
	}
	d.position--
	d.previous = x
}

// analyzeSharedAudio reads one decode of 48 kHz stereo signed 16-bit audio and
// produces the summary of every stage asked for. The waveform and fingerprint
// are computed by analyzePCM over the downmixed stream, unchanged.
func analyzeSharedAudio(reader io.Reader, duration int64, stages []string) (map[string]any, error) {
	if duration <= 0 || duration > math.MaxInt64/sharedRate {
		return nil, ErrBudget
	}
	wants := map[string]bool{}
	for _, s := range stages {
		wants[s] = true
	}
	var meter *r128
	if wants["loudness"] {
		meter = newR128()
	}
	type pcmStage struct {
		name    string
		writer  *io.PipeWriter
		stopped bool // its reader has gone (only this loop reads it)
		summary any
		err     error // set by its goroutine, read after Wait
	}
	var pcm []*pcmStage
	var wg sync.WaitGroup
	for _, name := range []string{"waveform", "fingerprint"} {
		if !wants[name] {
			continue
		}
		r, w := io.Pipe()
		p := &pcmStage{name: name, writer: w}
		pcm = append(pcm, p)
		wg.Add(1)
		supervise.Go("mediaanalysis.sharedAudio."+name, func() {
			defer wg.Done()
			// Unblock the writer if the analysis stopped early (a budget) or
			// panicked; a panic leaves errSharedStagePanicked as the stage's error.
			p.err = errSharedStagePanicked
			defer func() { _ = r.CloseWithError(errors.Join(p.err, io.ErrClosedPipe)) }()
			p.summary, p.err = analyzePCM(r, duration, p.name)
		})
	}
	var mix *downmix
	if len(pcm) > 0 {
		mix = newDownmix()
	}
	limit := (duration/1000000 + 60) * sharedRate
	samples := int64(0)
	buf := make([]byte, 64*1024)
	carry := []byte{}
	var readErr error
	for readErr == nil {
		n, err := reader.Read(buf)
		data := append(carry, buf[:n]...)
		whole := len(data) / 4 * 4
		for i := 0; i < whole; i += 4 {
			left := float64(int16(binary.LittleEndian.Uint16(data[i:]))) / 32768
			right := float64(int16(binary.LittleEndian.Uint16(data[i+2:]))) / 32768
			if meter != nil {
				meter.push(left, right)
			}
			if mix != nil {
				mix.push(left, right)
			}
			samples++
		}
		carry = append([]byte{}, data[whole:]...)
		if samples > limit {
			readErr = ErrBudget
			break
		}
		if mix != nil && len(mix.out) > 0 {
			for _, p := range pcm {
				if !p.stopped {
					if _, e := p.writer.Write(mix.out); e != nil {
						p.stopped = true
					}
				}
			}
			mix.out = mix.out[:0]
		}
		switch {
		case err == io.EOF:
			readErr = io.EOF
		case err != nil:
			readErr = err
		}
	}
	for _, p := range pcm {
		if readErr == io.EOF && len(carry) == 0 && samples > 0 {
			_ = p.writer.Close()
		} else {
			_ = p.writer.CloseWithError(errors.Join(readErr, io.ErrUnexpectedEOF))
		}
	}
	wg.Wait()
	if readErr != io.EOF {
		return nil, readErr
	}
	if len(carry) > 0 || samples == 0 {
		return nil, io.ErrUnexpectedEOF
	}
	out := map[string]any{}
	for _, p := range pcm {
		if p.err != nil {
			return nil, p.err
		}
		out[p.name] = p.summary
	}
	if meter != nil {
		if meter.subCount > 0 {
			meter.flushSub()
		}
		summary, ok := meter.summary()
		if !ok {
			return nil, ErrUnsupported
		}
		out["loudness"] = summary
	}
	return out, nil
}

// produceSharedAudio runs one decode of the audio for every stage in stages
// (all shared audio stages) and returns each stage's result.
func (s *Service) produceSharedAudio(ctx context.Context, b binding, stages []string, decode Decode, publications map[string]string) (map[string]produced, error) {
	results := map[string]produced{}
	for _, stage := range stages {
		results[stage] = produced{Artifacts: []artifactOutput{}, Markers: []Marker{}}
	}
	if b.AudioCodec == "" {
		for _, stage := range stages {
			results[stage] = produced{Artifacts: []artifactOutput{}, Markers: []Marker{}, Summary: map[string]any{"status": "not_applicable", "reason": "no_audio_stream"}}
		}
		return results, nil
	}
	if b.DurationUS <= 0 {
		return results, ErrUnsupported
	}
	spec := decoder.AnalysisSpec{Kind: "pcm_shared", MaxDurationUS: b.DurationUS}
	var summaries map[string]any
	key := publications[stages[0]]
	if err := decode(ctx, key, spec, func(r io.Reader) error {
		var e error
		summaries, e = analyzeSharedAudio(r, b.DurationUS, stages)
		return e
	}); err != nil {
		return results, err
	}
	for _, stage := range stages {
		out := results[stage]
		out.Summary = summaries[stage]
		if stage == "waveform" || stage == "fingerprint" {
			if err := s.adder(ctx, publications[stage], &out)([]byte(jsonString(out.Summary)), stage, "application/json", 0, 0, b.DurationUS, 0, 0); err != nil {
				results[stage] = out
				return results, err
			}
		}
		results[stage] = out
	}
	return results, nil
}
