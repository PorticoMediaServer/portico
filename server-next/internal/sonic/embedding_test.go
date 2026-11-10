package sonic

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestEmbeddingRejectsInvalidInferenceAndStorage(t *testing.T) {
	for _, value := range []float32{0, float32(math.NaN()), float32(math.Inf(1))} {
		var input [Dimensions]float32
		input[0] = value
		if _, err := Normalize(input[:]); err == nil {
			t.Fatalf("accepted invalid inference %v", value)
		}
	}
	if _, err := Normalize([]float32{1}); err == nil {
		t.Fatal("accepted a different model's dimensions")
	}
	for _, size := range []int{0, 799, 801} {
		if _, err := FromBytes(make([]byte, size)); err == nil {
			t.Fatalf("accepted %d stored bytes", size)
		}
	}
	data := make([]byte, Dimensions*4)
	for _, value := range []float32{0, 2, float32(math.NaN()), float32(math.Inf(-1))} {
		binary.LittleEndian.PutUint32(data, math.Float32bits(value))
		if _, err := FromBytes(data); err == nil {
			t.Fatalf("accepted corrupt unit vector %v", value)
		}
	}
}

func TestEmbeddingMeanAndCosineRetainTrackDirection(t *testing.T) {
	var x, y [Dimensions]float32
	x[0], y[1] = math.MaxFloat32, math.MaxFloat32
	a, err := Normalize(x[:])
	if err != nil {
		t.Fatal(err)
	}
	b, err := Normalize(y[:])
	if err != nil {
		t.Fatal(err)
	}
	mean, err := Mean([]Embedding{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if score, err := Cosine(a, b); err != nil || score != 0 {
		t.Fatalf("orthogonal tracks: %v %v", score, err)
	}
	if score, err := Cosine(a, mean); err != nil || math.Abs(score-math.Sqrt(.5)) > 1e-6 {
		t.Fatalf("mean direction: %v %v", score, err)
	}
	data, err := mean.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	stored, err := FromBytes(data)
	if err != nil || stored != mean || len(data) != 800 {
		t.Fatalf("storage roundtrip: %v", err)
	}
	a[0] = -1
	if _, err := Mean([]Embedding{a, {1}}); err == nil {
		t.Fatal("opposing tracks produced a fake mean")
	}
	if _, err := Cosine(Embedding{}, b); err == nil {
		t.Fatal("corrupt seed produced a score")
	}
}
