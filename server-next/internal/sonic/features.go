package sonic

import (
	"bufio"
	"encoding/binary"
	"io"
	"math"
)

const (
	SampleRate  = 16000
	PatchFrames = 187
	MelBands    = 96
	fftSize     = 512
	hop         = 256
	patchHop    = PatchFrames * hop
)

// LogMel streams the original musicnn input representation with bounded memory:
// periodic Hann, centered 512-point STFT, Slaney-normalized 96-band mel power,
// float16 power quantization, then log10(10000*x+1). It never normalizes loudness.
// Input is already decoded 16 kHz mono float32LE from the closed decoder recipe.
// Full patches follow the original 187-frame non-overlap policy. Only a track
// shorter than one patch is repeat-padded; an incomplete final patch is dropped.
func LogMel(input io.Reader, maxSamples int64, yield func([]float32) error) (int, error) {
	if maxSamples < 1 || yield == nil {
		return 0, ErrEmbedding
	}
	r := bufio.NewReaderSize(input, 64<<10)
	var window [fftSize]float64
	for i := range window {
		window[i] = .5 - .5*math.Cos(2*math.Pi*float64(i)/fftSize)
	}
	filters := melFilters()
	var audio [patchHop + hop]float32
	var raw [4]byte
	var samples int64
	patches := 0
	for {
		n := 0
		for n < patchHop {
			_, err := io.ReadFull(r, raw[:])
			if err == io.EOF {
				break
			}
			if err != nil {
				return patches, err
			}
			samples++
			if samples > maxSamples {
				return patches, ErrEmbedding
			}
			value := math.Float32frombits(binary.LittleEndian.Uint32(raw[:]))
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
				return patches, ErrEmbedding
			}
			audio[hop+n] = value
			n++
		}
		if n == 0 {
			if patches == 0 {
				return 0, ErrEmbedding
			}
			return patches, nil
		}
		if n < patchHop-hop && patches > 0 {
			return patches, nil
		}
		if n < patchHop-hop {
			// A genuine short track has no full input patch. Repeat its measured
			// samples rather than publishing a zero/silent replacement embedding.
			for i := n; i < patchHop; i++ {
				audio[hop+i] = audio[hop+i%n]
			}
		} else if n < patchHop {
			// The final complete STFT patch may end at the last sample's center.
			// Reflect only the missing right lookahead, as the original STFT does.
			for i := n; i < patchHop; i++ {
				audio[hop+i] = audio[hop+2*n-2-i]
			}
		}
		if patches == 0 {
			for i := 0; i < hop; i++ {
				audio[i] = audio[2*hop-i]
			}
		}
		features := make([]float32, PatchFrames*MelBands)
		for frame := 0; frame < PatchFrames; frame++ {
			var spectrum [fftSize]complex128
			for i, v := range audio[frame*hop : frame*hop+fftSize] {
				spectrum[i] = complex(float64(v)*window[i], 0)
			}
			fft(&spectrum)
			var power [fftSize/2 + 1]float32
			for i := range power {
				re, im := float32(real(spectrum[i])), float32(imag(spectrum[i]))
				magnitude := float32(math.Hypot(float64(re), float64(im)))
				power[i] = magnitude * magnitude
			}
			for band := 0; band < MelBands; band++ {
				var sum float32
				for bin, value := range power {
					sum = float32(sum + float32(value*filters[band][bin]))
				}
				value := float32(math.Log10(1 + 10000*float64(halfPower(sum))))
				if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) || value > 16 {
					return patches, ErrEmbedding
				}
				features[frame*MelBands+band] = value
			}
		}
		if err := yield(features); err != nil {
			return patches, err
		}
		patches++
		if n < patchHop {
			return patches, nil
		}
		copy(audio[:hop], audio[patchHop:])
	}
}

func halfPower(value float32) float32 {
	if value <= 0 {
		return 0
	}
	exponent := math.Ilogb(float64(value))
	if exponent < -14 {
		return float32(math.Ldexp(math.RoundToEven(math.Ldexp(float64(value), 24)), -24))
	}
	if exponent > 15 {
		return float32(math.Inf(1))
	}
	rounded := math.Ldexp(math.RoundToEven(math.Ldexp(float64(value), 10-exponent)), exponent-10)
	if rounded > 65504 {
		return float32(math.Inf(1))
	}
	return float32(rounded)
}

func melFilters() [MelBands][fftSize/2 + 1]float32 {
	toMel := func(hz float64) float64 {
		if hz < 1000 {
			return hz / (200. / 3)
		}
		return 15 + math.Log(hz/1000)/(math.Log(6.4)/27)
	}
	toHz := func(mel float64) float64 {
		if mel < 15 {
			return mel * (200. / 3)
		}
		return 1000 * math.Exp((mel-15)*(math.Log(6.4)/27))
	}
	var edges [MelBands + 2]float64
	for i := range edges {
		edges[i] = toHz(toMel(SampleRate/2) * float64(i) / (MelBands + 1))
	}
	var out [MelBands][fftSize/2 + 1]float32
	for band := range out {
		for bin := range out[band] {
			hz := float64(bin) * SampleRate / fftSize
			triangle := max(0, min((hz-edges[band])/(edges[band+1]-edges[band]), (edges[band+2]-hz)/(edges[band+2]-edges[band+1])))
			// librosa first stores the triangular coefficient as float32, then
			// applies Slaney's area normalization in the same output dtype.
			out[band][bin] = float32(float64(float32(triangle)) * (2 / (edges[band+2] - edges[band])))
		}
	}
	return out
}

func fft(values *[fftSize]complex128) {
	for i, j := 1, 0; i < fftSize; i++ {
		bit := fftSize >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			values[i], values[j] = values[j], values[i]
		}
	}
	for size := 2; size <= fftSize; size <<= 1 {
		angle := -2 * math.Pi / float64(size)
		root := complex(math.Cos(angle), math.Sin(angle))
		for start := 0; start < fftSize; start += size {
			w := complex(1, 0)
			for j := 0; j < size/2; j++ {
				a, b := values[start+j], w*values[start+j+size/2]
				values[start+j], values[start+j+size/2] = a+b, a-b
				w *= root
			}
		}
	}
}
