// Package sonic contains server-local musicnn inference and numeric embeddings.
// A stored vector always identifies its original model/version/digest. Catalogue
// readers must apply the current viewer's visibility before forming a mean or
// ranking a candidate; a global album/artist mean would leak restricted tracks.
package sonic

import (
	"encoding/binary"
	"errors"
	"math"
)

const Dimensions = 200

var ErrEmbedding = errors.New("sonic: invalid or empty embedding")

type Embedding [Dimensions]float32

// Normalize rejects silent/invalid inference instead of publishing a fabricated
// zero vector. Float64 accumulation prevents overflow from finite float32 input.
func Normalize(values []float32) (Embedding, error) {
	var out Embedding
	if len(values) != Dimensions {
		return out, ErrEmbedding
	}
	var squared float64
	for _, value := range values {
		v := float64(value)
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return out, ErrEmbedding
		}
		squared += v * v
	}
	if squared <= 0 {
		return out, ErrEmbedding
	}
	norm := math.Sqrt(squared)
	for i, value := range values {
		out[i] = float32(float64(value) / norm)
	}
	return out, nil
}

// Mean is the normalized arithmetic mean of the caller's permitted tracks.
// Each track has equal weight, independent of its duration or play count.
func Mean(tracks []Embedding) (Embedding, error) {
	var sum [Dimensions]float64
	if len(tracks) == 0 {
		return Embedding{}, ErrEmbedding
	}
	for _, track := range tracks {
		if !track.valid() {
			return Embedding{}, ErrEmbedding
		}
		for i, v := range track {
			sum[i] += float64(v)
		}
	}
	var values [Dimensions]float32
	for i := range values {
		values[i] = float32(sum[i] / float64(len(tracks)))
	}
	return Normalize(values[:])
}

func (e Embedding) valid() bool {
	var squared float64
	for _, value := range e {
		v := float64(value)
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
		squared += v * v
	}
	return math.Abs(squared-1) <= 1e-5
}

// Bytes is a fixed 800-byte float32LE numeric vector, never a serialized object
// graph. Invalid internal values cannot become a stored inference result.
func (e Embedding) Bytes() ([]byte, error) {
	if !e.valid() {
		return nil, ErrEmbedding
	}
	out := make([]byte, Dimensions*4)
	for i, v := range e {
		binary.LittleEndian.PutUint32(out[i*4:], math.Float32bits(v))
	}
	return out, nil
}

func FromBytes(data []byte) (Embedding, error) {
	var out Embedding
	if len(data) != Dimensions*4 {
		return out, ErrEmbedding
	}
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
	}
	if !out.valid() {
		return Embedding{}, ErrEmbedding
	}
	return out, nil
}

// Cosine compares checked unit vectors. Callers cannot turn corrupt storage
// into a high-scoring candidate; they must fall back to the approved non-sonic
// selector when either vector is unavailable.
func Cosine(a, b Embedding) (float64, error) {
	if !a.valid() || !b.valid() {
		return 0, ErrEmbedding
	}
	var dot float64
	for i, v := range a {
		dot += float64(v) * float64(b[i])
	}
	return min(1, max(-1, dot)), nil
}
