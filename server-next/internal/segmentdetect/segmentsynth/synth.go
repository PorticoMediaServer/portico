// Package segmentsynth makes deterministic programme audio for the segment
// detection tests and accuracy harness: theme music that repeats across
// episodes and episode audio that does not. Nothing here is real media; every
// sample comes from a seed.
package segmentsynth

import (
	"encoding/binary"
	"math"
	"math/rand/v2"
)

// Signal fills samples at rate starting at second offset (so a long signal
// can be produced in pieces).
type Signal func(rate int, offset float64, out []float64)

// Theme is music: a seeded melody of harmonic notes over a bass line, a kick
// every half second and hats between, like an opening or closing theme. The
// same seed is the same music in every episode.
func Theme(seed uint64) Signal {
	r := rand.New(rand.NewPCG(seed, seed^0x5eed))
	scale := []float64{0, 2, 4, 5, 7, 9, 11, 12, 14, 16}
	notes := make([]float64, 256)
	for i := range notes {
		notes[i] = 220 * math.Pow(2, scale[r.IntN(len(scale))]/12)
	}
	bass := make([]float64, 64)
	for i := range bass {
		bass[i] = 55 * math.Pow(2, scale[r.IntN(5)]/12)
	}
	noteLength := 0.25 + 0.125*float64(r.IntN(3))
	return func(rate int, offset float64, out []float64) {
		for i := range out {
			t := offset + float64(i)/float64(rate)
			n := int(t / noteLength)
			p := t - float64(n)*noteLength
			env := math.Exp(-3*p/noteLength) * math.Min(1, p*200)
			f := notes[n%len(notes)]
			v := 0.0
			for h := 1; h <= 4; h++ {
				v += math.Sin(2*math.Pi*f*float64(h)*t) / float64(h)
			}
			v *= 0.28 * env
			b := bass[int(t/(noteLength*4))%len(bass)]
			v += 0.22 * math.Sin(2*math.Pi*b*t)
			beat := math.Mod(t, 0.5)
			v += 0.35 * math.Sin(2*math.Pi*(60+90*math.Exp(-beat*30))*beat) * math.Exp(-beat*12)
			if off := math.Mod(t+0.25, 0.5); off < 0.04 {
				v += 0.12 * noise(seed+1, t, rate) * (1 - off/0.04)
			}
			out[i] = v * 0.7
		}
	}
}

// Speech is episode audio: voiced syllables at a seeded pitch with formants,
// pauses between phrases, and room tone. Different seeds share nothing.
func Speech(seed uint64) Signal {
	r := rand.New(rand.NewPCG(seed, seed*31+1))
	type syllable struct {
		start, length, pitch float64
		gain                 [12]float64 // per harmonic: the formants' shape
	}
	var syllables []syllable
	t := 0.0
	for t < 4000 {
		phrase := 1 + r.IntN(8)
		for range phrase {
			s := syllable{start: t, length: 0.12 + r.Float64()*0.2, pitch: 90 + r.Float64()*160}
			f1, f2 := 300+r.Float64()*600, 900+r.Float64()*1600
			for h := range s.gain {
				hz := s.pitch * float64(h+1)
				s.gain[h] = 0.18 * (math.Exp(-math.Pow((hz-f1)/250, 2)) + 0.6*math.Exp(-math.Pow((hz-f2)/400, 2)) + 0.05) / math.Sqrt(float64(h+1))
			}
			syllables = append(syllables, s)
			t += s.length + r.Float64()*0.08
		}
		t += 0.2 + r.Float64()*0.9
	}
	return func(rate int, offset float64, out []float64) {
		k := 0
		for i := range out {
			t := offset + float64(i)/float64(rate)
			for k < len(syllables)-1 && syllables[k].start+syllables[k].length < t {
				k++
			}
			for k > 0 && syllables[k].start > t {
				k--
			}
			v := 0.015 * noise(seed^0xabcdef, t, rate)
			s := &syllables[k]
			if p := t - s.start; p >= 0 && p < s.length {
				// Harmonics by angle addition: one sine and cosine per sample.
				sin1, cos1 := math.Sincos(2 * math.Pi * s.pitch * t)
				sh, ch := sin1, cos1
				voiced := 0.0
				for h := range s.gain {
					voiced += s.gain[h] * sh
					sh, ch = sh*cos1+ch*sin1, ch*cos1-sh*sin1
				}
				v += voiced * math.Sin(math.Pi*p/s.length)
			}
			out[i] = v
		}
	}
}

// noise is white noise in [-1, 1) that depends only on the seed and the
// sample's time, so any stretch renders the same whatever came before it.
func noise(seed uint64, t float64, rate int) float64 {
	x := seed ^ uint64(math.Round(t*float64(rate)))*0x9e3779b97f4a7c15
	x ^= x >> 33
	x *= 0xff51afd7ed558ccd
	x ^= x >> 33
	x *= 0xc4ceb9fe1a85ec53
	x ^= x >> 33
	return float64(x>>11)/float64(1<<52) - 1
}

// Mix adds b at gain to a.
func Mix(a Signal, b Signal, gain float64) Signal {
	return func(rate int, offset float64, out []float64) {
		a(rate, offset, out)
		tmp := make([]float64, len(out))
		b(rate, offset, tmp)
		for i := range out {
			out[i] += gain * tmp[i]
		}
	}
}

// Silence is digital silence.
func Silence() Signal { return func(_ int, _ float64, out []float64) { clear(out) } }

// Part is one stretch of a programme: Length seconds of Signal, read from
// SourceAt seconds into it (a theme always starts at its own beginning).
type Part struct {
	Signal   Signal
	Length   float64
	SourceAt float64
	Gain     float64
}

// Programme is a file's audio as consecutive parts.
type Programme []Part

// Duration is the programme length in seconds.
func (p Programme) Duration() float64 {
	total := 0.0
	for _, part := range p {
		total += part.Length
	}
	return total
}

// Render writes the programme as signed 16-bit little-endian mono at rate.
func (p Programme) Render(rate int) []byte {
	out := make([]byte, 0, int(p.Duration()*float64(rate))*2+16)
	buf := make([]float64, rate)
	for _, part := range p {
		gain := part.Gain
		if gain == 0 {
			gain = 1
		}
		total := int(math.Round(part.Length * float64(rate)))
		for done := 0; done < total; {
			n := min(len(buf), total-done)
			part.Signal(rate, part.SourceAt+float64(done)/float64(rate), buf[:n])
			for _, v := range buf[:n] {
				s := int16(max(-32767, min(32767, math.Round(v*gain*32767))))
				out = binary.LittleEndian.AppendUint16(out, uint16(s))
			}
			done += n
		}
	}
	return out
}

// RenderRange writes length seconds of the programme from second from, as
// Render would have written them (a decoder asked for one region).
func (p Programme) RenderRange(rate int, from, length float64) []byte {
	out := make([]byte, 0, int(length*float64(rate))*2+16)
	first, total := int(math.Round(from*float64(rate))), int(math.Round(length*float64(rate)))
	at := 0 // sample index where the current part starts
	buf := make([]float64, rate)
	for _, part := range p {
		gain := part.Gain
		if gain == 0 {
			gain = 1
		}
		n := int(math.Round(part.Length * float64(rate)))
		lo, hi := max(first, at), min(first+total, at+n)
		for done := lo; done < hi; {
			k := min(len(buf), hi-done)
			part.Signal(rate, part.SourceAt+float64(done-at)/float64(rate), buf[:k])
			for _, v := range buf[:k] {
				s := int16(max(-32767, min(32767, math.Round(v*gain*32767))))
				out = binary.LittleEndian.AppendUint16(out, uint16(s))
			}
			done += k
		}
		at += n
	}
	return out
}
