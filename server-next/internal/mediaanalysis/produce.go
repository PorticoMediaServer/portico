package mediaanalysis

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

type Marker struct {
	ID         string  `json:"id"`
	Kind       string  `json:"kind"`
	Title      string  `json:"title"`
	StartUS    string  `json:"startUS"`
	EndUS      string  `json:"endUS"`
	Confidence float64 `json:"confidence"`
	Provenance string  `json:"provenance"`
	Approved   bool    `json:"approved"`
	Edited     bool    `json:"edited"`
}

func (m Marker) start() int64 { n, _ := strconv.ParseInt(m.StartUS, 10, 64); return n }
func (m Marker) end() int64   { n, _ := strconv.ParseInt(m.EndUS, 10, 64); return n }
func candidate(kind string, start, end int64, confidence float64, provenance string) Marker {
	return Marker{Kind: kind, Title: strings.ToUpper(kind[:1]) + kind[1:] + " candidate", StartUS: fmt.Sprint(start), EndUS: fmt.Sprint(end), Confidence: confidence, Provenance: provenance}
}

func assurance(evidence string) string {
	if strings.HasPrefix(evidence, "strong:") || strings.HasPrefix(evidence, "remote:") {
		return "strong_version"
	}
	return "observed_acquisition"
}

type chapter struct {
	Title      string
	Start, End int64
}

// Audio work consumes the entire successful decode with constant-sized windows.
// Fingerprints are perceptual spectral signatures, never cryptographic identity.
func analyzePCM(reader io.Reader, duration int64, stage string) (any, error) {
	const rate int64 = 11025
	if duration <= 0 || duration > math.MaxInt64/rate {
		return nil, ErrBudget
	}
	bucket := max(int64(1), (duration*rate/1000000+1023)/1024)
	stride := max(int64(1), (duration/1000000+4095)/4096)
	type point struct {
		StartUS string  `json:"startUS"`
		EndUS   string  `json:"endUS"`
		Peak    float64 `json:"peak"`
		RMS     float64 `json:"rms"`
	}
	type signature struct {
		PositionUS string `json:"positionUS"`
		Value      string `json:"value"`
	}
	points := []point{}
	signatures := []signature{}
	window := make([]float64, 0, 2048)
	var prior [16]float64
	sampleIndex, count := int64(0), int64(0)
	sum, peak := float64(0), float64(0)
	flush := func() {
		if count > 0 {
			points = append(points, point{fmt.Sprint((sampleIndex - count) * 1000000 / rate), fmt.Sprint(sampleIndex * 1000000 / rate), peak, math.Sqrt(sum / float64(count))})
			count = 0
			sum = 0
			peak = 0
		}
	}
	buf := make([]byte, 16384)
	carry := []byte{}
	for {
		n, err := reader.Read(buf)
		data := append(carry, buf[:n]...)
		carry = nil
		if len(data)%2 != 0 {
			carry = []byte{data[len(data)-1]}
			data = data[:len(data)-1]
		}
		for i := 0; i < len(data); i += 2 {
			value := float64(int16(binary.LittleEndian.Uint16(data[i:i+2]))) / 32768
			if stage == "waveform" {
				sum += value * value
				peak = max(peak, math.Abs(value))
				count++
			}
			if stage == "fingerprint" && sampleIndex%(rate*stride) < 2048 {
				window = append(window, value)
				if len(window) == 2048 {
					bands := spectralBands(window)
					var bits uint32
					for k := 0; k < 16; k++ {
						if bands[k] > prior[k] {
							bits |= 1 << k
						}
						if k < 15 && bands[k] > bands[k+1] {
							bits |= 1 << (k + 16)
						}
					}
					signatures = append(signatures, signature{fmt.Sprint((sampleIndex - 2047) * 1000000 / rate), fmt.Sprintf("%08x", bits)})
					prior = bands
					window = window[:0]
				}
			}
			sampleIndex++
			if stage == "waveform" && count == bucket {
				flush()
			}
			if sampleIndex > (duration/1000000+60)*rate || len(points) > 2048 || len(signatures) > 4097 {
				return nil, ErrBudget
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	if len(carry) > 0 || sampleIndex == 0 {
		return nil, io.ErrUnexpectedEOF
	}
	if stage == "waveform" {
		flush()
		return map[string]any{"algorithm": "mono-peak-rms-11025-v1", "decodedDurationUS": fmt.Sprint(sampleIndex * 1000000 / rate), "points": points}, nil
	}
	if len(signatures) == 0 {
		return nil, ErrUnsupported
	}
	return map[string]any{"algorithm": "spectral-sign-16x2-11025-2048-v1", "decodedDurationUS": fmt.Sprint(sampleIndex * 1000000 / rate), "strideSeconds": stride, "signatures": signatures}, nil
}
func spectralBands(samples []float64) (out [16]float64) {
	for band := range out {
		hz := 80 * math.Pow(5000.0/80, float64(band)/15)
		coefficient := 2 * math.Cos(2*math.Pi*hz/11025)
		q1, q2 := 0.0, 0.0
		for i, x := range samples {
			window := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(len(samples)-1))
			q := x*window + coefficient*q1 - q2
			q2 = q1
			q1 = q
		}
		out[band] = math.Log1p(max(0, q1*q1+q2*q2-coefficient*q1*q2))
	}
	return
}
