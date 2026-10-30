package subtitlevideo

import (
	"context"
	"errors"
	"testing"
)

type fixtureCachedSource struct {
	reads, validations, closes int
	failure                    error
}

func (f *fixtureCachedSource) Evidence() string               { return "retained-source-version" }
func (f *fixtureCachedSource) Size() int64                    { return 64 << 20 }
func (f *fixtureCachedSource) Validate(context.Context) error { f.validations++; return f.failure }
func (f *fixtureCachedSource) Close() error                   { f.closes++; return nil }
func (f *fixtureCachedSource) ReadExtent(_ context.Context, offset, length int64) ([]byte, error) {
	f.reads++
	if f.failure != nil {
		return nil, f.failure
	}
	out := make([]byte, length)
	for i := range out {
		out[i] = byte(offset + int64(i%251))
	}
	return out, nil
}
func TestRetainedExtentCacheReusesImmutableBytesWithoutSharingMutableBuffers(t *testing.T) {
	source := &fixtureCachedSource{}
	cached := cacheInput(source)
	ctx := context.Background()
	first, e := cached.ReadExtent(ctx, 7, 4096)
	if e != nil {
		t.Fatal(e)
	}
	first[0] = 0
	second, e := cached.ReadExtent(ctx, 7, 4096)
	if e != nil {
		t.Fatal(e)
	}
	if source.reads != 1 || second[0] != 7 {
		t.Fatalf("retained extent not safely reused: reads=%d byte=%d", source.reads, second[0])
	}
	second[0] = 1
	third, _ := cached.ReadExtent(ctx, 7, 4096)
	if third[0] != 7 {
		t.Fatal("cached bytes were mutated by a later consumer")
	}
	source.failure = errors.New("authority revoked")
	if cached.Validate(ctx) == nil || source.validations != 1 {
		t.Fatal("cache bypassed producer authority validation")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, e = cached.ReadExtent(cancelled, 7, 4096); !errors.Is(e, context.Canceled) {
		t.Fatalf("cache bypassed cancellation: %v", e)
	}
	if e = cached.Close(); e != nil || source.closes != 1 {
		t.Fatalf("retained source was not closed: %v", e)
	}
}
func TestRetainedExtentCacheIsBoundedAcrossSegmentsAndDoesNotCacheFailures(t *testing.T) {
	source := &fixtureCachedSource{}
	cached := cacheInput(source).(*cachedInput)
	ctx := context.Background()
	for offset := int64(0); offset < 9; offset++ {
		if _, e := cached.ReadExtent(ctx, offset<<20, 1<<20); e != nil {
			t.Fatal(e)
		}
	}
	if cached.bytes > 8<<20 || len(cached.cache) != 8 {
		t.Fatalf("unbounded cache: bytes=%d entries=%d", cached.bytes, len(cached.cache))
	}
	if _, e := cached.ReadExtent(ctx, 8<<20, 1<<20); e != nil {
		t.Fatal(e)
	}
	if source.reads != 9 {
		t.Fatal("latest segment was needlessly read again")
	}
	if _, e := cached.ReadExtent(ctx, 0, 1<<20); e != nil {
		t.Fatal(e)
	}
	if source.reads != 10 {
		t.Fatal("evicted segment remained hidden in cache")
	}
	if _, e := cached.ReadExtent(ctx, 20<<20, 9<<20); e != nil {
		t.Fatal(e)
	}
	if cached.bytes > 8<<20 {
		t.Fatal("oversized extent exceeded cache bound")
	}
	if _, e := cached.ReadExtent(ctx, 20<<20, 9<<20); e != nil {
		t.Fatal(e)
	}
	if source.reads != 12 {
		t.Fatal("oversized extent was retained")
	}
	source.failure = errors.New("transient source error")
	if _, e := cached.ReadExtent(ctx, 40<<20, 100); e == nil {
		t.Fatal("source failure hidden")
	}
	source.failure = nil
	if _, e := cached.ReadExtent(ctx, 40<<20, 100); e != nil {
		t.Fatal(e)
	}
	if source.reads != 14 {
		t.Fatal("failure was cached")
	}
	cached.Close()
	if cached.bytes != 0 || len(cached.cache) != 0 {
		t.Fatal("closed cache retained private bytes")
	}
}
