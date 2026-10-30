package subtitlevideo

import (
	"context"
	"errors"
	"testing"
	"time"
)

type progressFixture struct{ fail bool }

func (progressFixture) Evidence() string               { return "fixture" }
func (progressFixture) Size() int64                    { return 100 }
func (progressFixture) Validate(context.Context) error { return nil }
func (progressFixture) Close() error                   { return nil }
func (p progressFixture) ReadExtent(context.Context, int64, int64) ([]byte, error) {
	if p.fail {
		return nil, errors.New("stalled read")
	}
	return []byte{1}, nil
}

func TestExtractionProgressResetsOnlyForSuccessfulInput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	ctx, input, closeInput := extractionProgressLimits(context.Background(), progressFixture{}, 300*time.Millisecond, 3*time.Second)
	defer closeInput()
	for i := 0; i < 3; i++ {
		time.Sleep(120 * time.Millisecond)
		if ctx.Err() != nil {
			t.Fatal("progressing track expired")
		}
		if _, err := input.ReadExtent(ctx, 0, 1); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("idle track did not expire")
	}

	ctx, input, closeFailed := extractionProgressLimits(context.Background(), progressFixture{fail: true}, 300*time.Millisecond, 3*time.Second)
	defer closeFailed()
	for i := 0; i < 3; i++ {
		time.Sleep(120 * time.Millisecond)
		_, _ = input.ReadExtent(ctx, 0, 1)
	}
	if ctx.Err() == nil {
		t.Fatal("failed reads reset the idle budget")
	}
}

func TestExtractionProgressHasAnIndependentOverallBound(t *testing.T) {
	ctx, input, closeInput := extractionProgressLimits(context.Background(), progressFixture{}, time.Second, 300*time.Millisecond)
	defer closeInput()
	for i := 0; i < 4; i++ {
		time.Sleep(100 * time.Millisecond)
		_, _ = input.ReadExtent(ctx, 0, 1)
	}
	if ctx.Err() == nil {
		t.Fatal("input progress extended the overall limit")
	}
}
