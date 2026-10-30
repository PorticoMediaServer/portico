// Package segmentdetect finds intros, credits and the scenes around them from
// measurements of the media: audio fingerprints compared across the episodes
// of a show, and pictures read near the end of a file. It is pure computation
// over decoded samples and frames; reading media, storage and publication
// belong to mediaanalysis.
package segmentdetect

import (
	"encoding/binary"
	"errors"
	"math"
	"math/bits"
)

// SampleRate is the rate of the mono signed 16-bit audio the fingerprint
// reads. Eight kilohertz keeps every band the fingerprint uses (below 3.4 kHz)
// and makes one fingerprint point exactly 1000 samples.
const SampleRate = 8000

// StepUS is the distance between fingerprint points.
const StepUS int64 = 125000

const (
	frameSize = 2048 // 256 ms analysis window
	hop       = 250  // 31.25 ms between analysis frames
	perPoint  = 4    // analysis frames averaged into one point
	bandCount = 33
)

// ErrInput reports samples or evidence this package cannot read.
var ErrInput = errors.New("segmentdetect: unreadable input")

// Fingerprint is the audio of one stretch of a file: one 32-bit point every
// StepUS from StartUS, and the loudness of each point (0 is digital silence,
// 255 full scale) so silence never counts as a match.
type Fingerprint struct {
	StartUS int64
	Points  []uint32
	Energy  []uint8
}

// EndUS is the file time just after the last point.
func (f Fingerprint) EndUS() int64 { return f.StartUS + int64(len(f.Points))*StepUS }

// At is the file time of point i.
func (f Fingerprint) At(i int) int64 { return f.StartUS + int64(i)*StepUS }

// Index is the point covering a file time, clamped to the fingerprint.
func (f Fingerprint) Index(us int64) int {
	i := int((us - f.StartUS) / StepUS)
	return max(0, min(len(f.Points), i))
}

// Each point's bits compare smoothed log band energies, so they survive
// re-encoding, volume changes and the up-to-half-a-point misalignment between
// two copies of the same audio in different files:
//
//   - bits 0..15 compare band m with the mean of its neighbours (the shape of
//     the spectrum, which changes slowly and so tolerates misalignment);
//   - bits 16..31 compare the change of a pair of bands over a quarter second
//     with the change of the next pair (how the sound moves).
//
// Silence and steady tones make constant points; Energy lets a matcher ignore
// them rather than call two quiet passages the same.
type Fingerprinter struct {
	startUS int64
	window  []float64
	ring    []float64 // the last frameSize samples, circular from next
	next    int
	filled  int
	since   int // samples since the last analysis frame
	fft     *fft
	edges   [bandCount + 1]int
	accum   [bandCount]float64
	power   float64
	powerN  int
	count   int
	history [][bandCount]float64
	out     Fingerprint
	limit   int
}

// NewFingerprinter starts a fingerprint whose first point is at startUS. At
// most maxPoints points are kept; later samples are ignored.
func NewFingerprinter(startUS int64, maxPoints int) *Fingerprinter {
	f := &Fingerprinter{startUS: startUS, ring: make([]float64, frameSize), fft: newFFT(frameSize), limit: maxPoints}
	f.window = make([]float64, frameSize)
	for i := range f.window {
		f.window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(frameSize-1))
	}
	for i := range f.edges {
		hz := 300 * math.Pow(3400.0/300, float64(i)/bandCount)
		f.edges[i] = int(math.Round(hz * frameSize / SampleRate))
	}
	f.out.StartUS = startUS
	return f
}

// WritePCM consumes little-endian signed 16-bit mono samples at SampleRate.
// An odd trailing byte is an error: callers hand over whole samples.
func (f *Fingerprinter) WritePCM(data []byte) error {
	if len(data)%2 != 0 {
		return ErrInput
	}
	for i := 0; i < len(data); i += 2 {
		f.push(float64(int16(binary.LittleEndian.Uint16(data[i:]))) / 32768)
	}
	return nil
}

// Write consumes samples already converted to [-1, 1).
func (f *Fingerprinter) Write(samples []float64) {
	for _, v := range samples {
		f.push(v)
	}
}

func (f *Fingerprinter) push(v float64) {
	if f.limit > 0 && len(f.out.Points) >= f.limit {
		return
	}
	f.ring[f.next] = v
	f.next = (f.next + 1) % frameSize
	f.filled++
	f.since++
	f.power += v * v
	f.powerN++
	// The first frame is centred on the first point; frames then follow
	// every hop samples. Points come from perPoint consecutive frames.
	if f.filled < frameSize/2 || f.since < hop && f.filled != frameSize/2 {
		return
	}
	f.since = 0
	f.analyse()
}

func (f *Fingerprinter) analyse() {
	re, im := f.fft.re, f.fft.im
	for i := range re {
		re[i] = f.ring[(f.next+i)%frameSize] * f.window[i]
		im[i] = 0
	}
	f.fft.transform()
	for b := 0; b < bandCount; b++ {
		e := 0.0
		for k := f.edges[b]; k < max(f.edges[b]+1, f.edges[b+1]); k++ {
			e += re[k]*re[k] + im[k]*im[k]
		}
		f.accum[b] += math.Log(e + 1e-9)
	}
	f.count++
	if f.count < perPoint {
		return
	}
	var point [bandCount]float64
	for b := range point {
		point[b] = f.accum[b] / perPoint
	}
	f.accum = [bandCount]float64{}
	f.count = 0
	rms := math.Sqrt(f.power / float64(max(1, f.powerN)))
	f.power, f.powerN = 0, 0
	f.history = append(f.history, point)
	if len(f.history) > 3 {
		f.history = f.history[1:]
	}
	f.out.Points = append(f.out.Points, pointBits(f.history))
	f.out.Energy = append(f.out.Energy, energyByte(rms))
}

// pointBits derives one point from the newest smoothed spectrum and the one
// two points (a quarter second) before it.
func pointBits(history [][bandCount]float64) uint32 {
	now := history[len(history)-1]
	before := history[0]
	var v uint32
	for m := 0; m < 16; m++ {
		b := 1 + m*2
		if now[b] > (now[b-1]+now[b+1])/2 {
			v |= 1 << m
		}
	}
	for m := 0; m < 16; m++ {
		b := m * 2
		d0 := (now[b] + now[b+1]) - (before[b] + before[b+1])
		d1 := (now[b+1] + now[b+2]) - (before[b+1] + before[b+2])
		if d0 > d1 {
			v |= 1 << (16 + m)
		}
	}
	return v
}

// energyByte maps RMS to 0..255 on a decibel scale: 0 at -72 dBFS and below.
func energyByte(rms float64) uint8 {
	if rms <= 0 {
		return 0
	}
	db := 20 * math.Log10(rms)
	return uint8(max(0, min(255, math.Round((db+72)*255/72))))
}

// QuietEnergy is the loudness byte at and below which a point is treated as
// silence (about -50 dBFS).
const QuietEnergy = 78

// Finish returns the fingerprint of everything written.
func (f *Fingerprinter) Finish() Fingerprint { return f.out }

// Distance is the number of differing bits between two points.
func Distance(a, b uint32) int { return bits.OnesCount32(a ^ b) }

// fft is an in-place radix-2 complex transform of a fixed power-of-two size.
type fft struct {
	n        int
	re, im   []float64
	cos, sin []float64
	reverse  []int
}

func newFFT(n int) *fft {
	f := &fft{n: n, re: make([]float64, n), im: make([]float64, n), cos: make([]float64, n/2), sin: make([]float64, n/2), reverse: make([]int, n)}
	for i := 0; i < n/2; i++ {
		f.cos[i] = math.Cos(2 * math.Pi * float64(i) / float64(n))
		f.sin[i] = -math.Sin(2 * math.Pi * float64(i) / float64(n))
	}
	shift := bits.LeadingZeros(uint(n)) + 1
	for i := 0; i < n; i++ {
		f.reverse[i] = int(bits.Reverse(uint(i)) >> shift)
	}
	return f
}

func (f *fft) transform() {
	re, im := f.re, f.im
	for i, j := range f.reverse {
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
	for size := 2; size <= f.n; size <<= 1 {
		half, step := size/2, f.n/size
		for start := 0; start < f.n; start += size {
			for k := 0; k < half; k++ {
				c, s := f.cos[k*step], f.sin[k*step]
				i, j := start+k, start+k+half
				tr := re[j]*c - im[j]*s
				ti := re[j]*s + im[j]*c
				re[j], im[j] = re[i]-tr, im[i]-ti
				re[i], im[i] = re[i]+tr, im[i]+ti
			}
		}
	}
}
