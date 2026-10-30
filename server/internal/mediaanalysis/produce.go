package mediaanalysis

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
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

// adder returns the function a stage stores one generated artifact with,
// counting the stage's output against its budget.
func (s *Service) adder(ctx context.Context, key string, out *produced) func(data []byte, kind, mime string, index int, start, end int64, w, h int) error {
	generated := int64(0)
	return func(data []byte, kind, mime string, index int, start, end int64, w, h int) error {
		generated += int64(len(data))
		if generated > s.stageLimit(ctx) {
			return ErrBudget
		}
		// Close each unsealed capture immediately. Thousands of previews must
		// not retain thousands of descriptors while the decoder is still alive.
		// Resume + seal occurs only after physical retirement and source validation.
		retained, e := mediaartifact.RetainedCaptureKey(token(key, "artifact", kind, fmt.Sprint(index)), 1)
		if e != nil {
			return e
		}
		writer, e := s.artifacts.BeginRetained(retained, int64(len(data)))
		if e != nil {
			return e
		}
		if _, e = writer.Write(data); e != nil {
			writer.Abort()
			return e
		}
		if e = writer.Retain(); e != nil {
			s.artifacts.RemoveRetained(retained)
			return e
		}
		digest := sha256.Sum256(data)
		object := mediaartifact.Object{Digest: hex.EncodeToString(digest[:]), Size: int64(len(data))}
		out.Artifacts = append(out.Artifacts, artifactOutput{Object: object, RetainedKey: retained, Kind: kind, MIME: mime, Ordinal: index, StartUS: start, EndUS: end, Width: w, Height: h})
		return nil
	}
}
func (s *Service) produce(ctx context.Context, b binding, stage string, input Input, decode Decode, key string) (out produced, err error) {
	out.Artifacts = []artifactOutput{}
	out.Markers = []Marker{}
	add := s.adder(ctx, key, &out)
	if stage == "checksum" {
		h := sha256.New()
		for pos := int64(0); pos < input.Size(); {
			if e := ctx.Err(); e != nil {
				return out, e
			}
			n := min(int64(1<<20), input.Size()-pos)
			data, e := input.ReadExtent(ctx, pos, n)
			if e != nil {
				return out, e
			}
			if int64(len(data)) != n {
				return out, io.ErrUnexpectedEOF
			}
			h.Write(data)
			pos += n
		}
		// Captured byte digest is not an upgrade from observed to immutable origin.
		out.Summary = map[string]any{"algorithm": "sha256", "digest": hex.EncodeToString(h.Sum(nil)), "bytes": fmt.Sprint(input.Size()), "originAssurance": assurance(input.Evidence())}
		return out, nil
	}
	if b.DurationUS <= 0 {
		return out, ErrUnsupported
	}
	spec := decoder.AnalysisSpec{MaxDurationUS: b.DurationUS, SourceVideo: decoder.PreviewVideoFacts{DynamicRange: b.Video.DynamicRange, DolbyVisionProfile: b.Video.DolbyVisionProfile, DolbyVisionCompatibility: b.Video.DolbyVisionCompatibility}}
	switch stage {
	case "loudness":
		if b.AudioCodec == "" {
			out.Summary = map[string]any{"status": "not_applicable", "reason": "no_audio_stream"}
			return out, nil
		}
		spec.Kind = "loudness_full"
		err = decode(ctx, key, spec, func(r io.Reader) error { var e error; out.Summary, e = readFullLoudness(r); return e })
		return out, err
	case "chapter_images":
		if b.VideoCodec == "" {
			out.Summary = map[string]any{"status": "not_applicable", "reason": "no_video_stream"}
			return out, nil
		}
		chapters, e := s.chapters(ctx, b.AssetID)
		if e != nil {
			return out, e
		}
		if len(chapters) > s.options.MaxPreviewFrames {
			return out, ErrBudget
		}
		for i, c := range chapters {
			if c.Start < 0 || c.End > b.DurationUS {
				return out, ErrConflict
			}
			spec.Kind, spec.StartUS = "image", c.Start
			var data []byte
			if err = decode(ctx, key+":"+fmt.Sprint(i), spec, func(r io.Reader) error {
				var e error
				data, e = io.ReadAll(io.LimitReader(r, (1<<20)+1))
				if len(data) > 1<<20 {
					return ErrBudget
				}
				return e
			}); err != nil {
				return out, err
			}
			cfg, e := jpeg.DecodeConfig(bytes.NewReader(data))
			if e != nil || cfg.Width != 320 || cfg.Height != 180 {
				return out, ErrUnsupported
			}
			if err = add(data, stage, "image/jpeg", i, c.Start, c.End, 320, 180); err != nil {
				return out, err
			}
		}
		out.Summary = map[string]any{"images": len(chapters), "status": "complete", "decoding": "recoverable_errors_tolerated"}
	case "trickplay":
		if b.VideoCodec == "" {
			out.Summary = map[string]any{"status": "not_applicable", "reason": "no_video_stream"}
			return out, nil
		}
		geometry := trickplayGeometry(b.Trickplay, s.options.MaxPreviewFrames)
		spec.Kind = "trickplay"
		spec.MaxFrames = geometry.MaxFrames
		spec.Width, spec.Height = geometry.TileWidth, geometry.TileHeight
		spec.IntervalUS = max(geometry.IntervalUS, (b.DurationUS+int64(spec.MaxFrames)-1)/int64(spec.MaxFrames))
		count := 0
		// A sheet is filled in decode order and sealed the moment it is complete,
		// so a long source never holds more than one sprite sheet in memory.
		sheet := newTrickplaySheet(geometry)
		flush := func() error {
			if sheet.frames == 0 {
				return nil
			}
			data, e := sheet.encode()
			if e != nil {
				return e
			}
			index := (count - 1) / geometry.PerSheet
			start := int64(index*geometry.PerSheet) * spec.IntervalUS
			end := min(b.DurationUS, int64(count)*spec.IntervalUS)
			if e = add(data, trickplaySheetKind, "image/jpeg", index, start, end, sheet.width(), sheet.height()); e != nil {
				return e
			}
			sheet.reset()
			return nil
		}
		err = decode(ctx, key, spec, func(r io.Reader) error {
			frame := make([]byte, geometry.TileWidth*geometry.TileHeight*3)
			for {
				_, e := io.ReadFull(r, frame)
				if e == io.EOF {
					return nil
				}
				if e != nil {
					return e
				}
				if count >= spec.MaxFrames {
					return ErrBudget
				}
				img := image.NewRGBA(image.Rect(0, 0, geometry.TileWidth, geometry.TileHeight))
				for p := 0; p < geometry.TileWidth*geometry.TileHeight; p++ {
					img.SetRGBA(p%geometry.TileWidth, p/geometry.TileWidth, color.RGBA{frame[p*3], frame[p*3+1], frame[p*3+2], 255})
				}
				if int64(count)*spec.IntervalUS >= b.DurationUS {
					return ErrConflict
				}
				sheet.place(img)
				count++
				if count%geometry.PerSheet == 0 {
					if e = flush(); e != nil {
						return e
					}
				}
			}
		})
		if err == nil {
			err = flush()
		}
		if err == nil && count == 0 {
			return out, ErrUnsupported
		}
		if err == nil {
			descriptor := geometry.descriptor(count, spec.IntervalUS, b.DurationUS)
			if !descriptor.valid() {
				return out, ErrUnsupported
			}
			// The same descriptor is both an immutable artifact and the stage
			// summary, so listing a set never has to open stored bytes.
			out.Summary = trickplaySummary{TrickplayDescriptor: descriptor, Sampled: true}
			err = add([]byte(jsonString(descriptor)), trickplayDescriptorKind, "application/json", 0, 0, min(b.DurationUS, max(1, int64(count)*spec.IntervalUS)), descriptor.Width, descriptor.Height)
			break
		}
		out.Summary = trickplaySummary{TrickplayDescriptor: geometry.descriptor(count, spec.IntervalUS, b.DurationUS), Sampled: true}
	default:
		return out, ErrUnsupported
	}
	return out, err
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

func (s *Service) chapters(ctx context.Context, asset string) ([]chapter, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT title,start_seconds,end_seconds FROM asset_chapters WHERE asset_id=? ORDER BY chapter_index LIMIT 4097`, asset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []chapter{}
	for rows.Next() {
		var c chapter
		var start, end float64
		if e = rows.Scan(&c.Title, &start, &end); e != nil {
			return nil, e
		}
		c.Start, e = exactUS(start)
		if e != nil {
			return nil, e
		}
		c.End, e = exactUS(end)
		if e != nil || c.End <= c.Start {
			return nil, ErrInput
		}
		out = append(out, c)
	}
	if len(out) > 4096 {
		return nil, ErrBudget
	}
	return out, rows.Err()
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
