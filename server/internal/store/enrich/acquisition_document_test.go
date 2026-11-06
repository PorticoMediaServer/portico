package enrich

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
)

func documentKey() RequestKey {
	return RequestKey{"tmdb", "movie", "42", "en-CA", TMDBMovieRequestShape, "credential-generation:1"}
}
func documentMetadata() AcquisitionMetadata {
	return AcquisitionMetadata{"2026-11-06T10:23:19.123456789+00:00", `"etag:157"`, "Thu, 05 Nov 2026 10:00:00 GMT", "https://api.themoviedb.org/3/movie/42?api_key=test-secret&language=en-CA"}
}
func documentConfig() TransportConfig {
	c := config()
	c.MaxMetadataBytes = 512
	c.CacheTTL = time.Hour
	return c
} // HARNESS allocation limit; no production default

func TestAcquisitionDocumentPreservesOriginalMetadataOnReuse(t *testing.T) {
	cfg := documentConfig()
	input := documentMetadata()
	expected := input
	expected.SourceURL = "https://api.themoviedb.org/3/movie/42"
	var calls atomic.Int32
	var observedHint atomic.Int64
	tr := broker(t, cfg, admissionFunc(func(_ context.Context, hint int64) (func(), error) { observedHint.Store(hint); return func() {}, nil }), func(context.Context, RequestKey, int64) (Response, error) {
		calls.Add(1)
		return Response{Status: 200, Body: []byte("hello"), Metadata: input}, nil
	})
	clock := newFakeClock()
	tr.clock = clock
	first, err := tr.GetDocument(t.Context(), documentKey())
	if err != nil || first.Metadata() != expected || first.Key() != documentKey() || string(first.Body()) != "hello" {
		t.Fatal("initial typed acquisition", first.Metadata(), err)
	}
	if observedHint.Load() != cfg.MaxResponseBytes+cfg.MaxMetadataBytes {
		t.Fatal("metadata omitted from acquisition reservation", observedHint.Load())
	}
	body := first.Body()
	body[0] = 'X'
	copyMeta := first.Metadata()
	copyMeta.ObservedAt = "invented"
	input.ObservedAt = "later fetch"
	input.ETag = "later validator"
	clock.advance(5 * time.Second)
	second, err := tr.GetDocument(t.Context(), documentKey())
	if err != nil || second.Metadata() != expected || first.Metadata() != expected || string(second.Body()) != "hello" || calls.Load() != 1 {
		t.Fatal("cache fabricated later metadata", second.Metadata(), err, calls.Load())
	}
	tr.mu.Lock()
	cached := tr.cache[documentKey()].Value.(cachedResponse)
	tr.mu.Unlock()
	for i, field := range []string{cached.metadata.ObservedAt, cached.metadata.ETag, cached.metadata.LastModified, cached.metadata.SourceURL} {
		got := []string{second.metadata.ObservedAt, second.metadata.ETag, second.metadata.LastModified, second.metadata.SourceURL}[i]
		if got != field || unsafe.StringData(got) == unsafe.StringData(field) {
			t.Fatal("consumer retained cache metadata allocation", i)
		}
	}
	// Independent literal rounded string costs:64 timestamp+16ETag+32LastModified+64URL.
	stats := tr.Stats()
	if stats.CacheMetadataBytes != 176 || stats.CacheBytes != 192 || stats.Reused != 1 {
		t.Fatal("metadata is uncharged/additional to cache cap", stats)
	}
}

func TestAcquisitionDocumentDedupeKeepsOneOriginalObservation(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	tr := broker(t, documentConfig(), admissionFunc(allowed), func(ctx context.Context, _ RequestKey, _ int64) (Response, error) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return Response{}, ctx.Err()
		}
		return Response{Status: 200, Body: []byte("shared"), Metadata: documentMetadata()}, nil
	})
	var wg sync.WaitGroup
	docs := make([]AcquisitionDocument, 2)
	errs := make([]error, 2)
	wg.Go(func() { docs[0], errs[0] = tr.GetDocument(t.Context(), documentKey()) })
	<-entered
	wg.Go(func() { docs[1], errs[1] = tr.GetDocument(t.Context(), documentKey()) })
	waitFor(t, func() bool { return tr.Stats().Waiters == 2 })
	once.Do(func() { close(release) })
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || docs[0].Metadata() != docs[1].Metadata() || string(docs[0].Body()) != "shared" || tr.Stats().Deduplicated != 1 {
		t.Fatal("dedupe metadata mismatch", errs)
	}
	if unsafe.StringData(docs[0].metadata.ObservedAt) == unsafe.StringData(docs[1].metadata.ObservedAt) || unsafe.SliceData(docs[0].body) == unsafe.SliceData(docs[1].body) {
		t.Fatal("waiters share retained allocations")
	}
}

func TestAcquisitionDocumentCacheChargesMetadataOnEvictionAndClose(t *testing.T) {
	cfg := documentConfig()
	cfg.CacheBytes = 192
	tr := broker(t, cfg, admissionFunc(allowed), func(_ context.Context, k RequestKey, _ int64) (Response, error) {
		m := documentMetadata()
		m.SourceURL = "https://api.themoviedb.org/3/movie/" + k.ExternalID
		return Response{Status: 200, Body: []byte("hello"), Metadata: m}, nil
	})
	clock := newFakeClock()
	tr.clock = clock
	for _, id := range []string{"42", "43", "44"} {
		clock.advance(time.Second)
		k := documentKey()
		k.ExternalID = id
		if _, err := tr.GetDocument(t.Context(), k); err != nil {
			t.Fatal(err)
		}
		tr.mu.Lock()
		count := len(tr.cache)
		tr.mu.Unlock()
		if stats := tr.Stats(); stats.CacheBytes != 192 || stats.CacheMetadataBytes != 176 || count != 1 {
			t.Fatal("metadata admission/eviction undercharged", stats, count)
		}
	}
	tr.Close()
	if s := tr.Stats(); s.CacheBytes != 0 || s.CacheMetadataBytes != 0 {
		t.Fatal("metadata debt survived close", s)
	}
}

func TestAcquisitionDocumentMissingMalformedAndOverBudgetRefuse(t *testing.T) {
	for name, mutate := range map[string]func(*AcquisitionMetadata){
		"missing":     func(m *AcquisitionMetadata) { m.ObservedAt = "" },
		"malformed":   func(m *AcquisitionMetadata) { m.ObservedAt = "not an observation time" },
		"nul":         func(m *AcquisitionMetadata) { m.ETag = "x\x00y" },
		"utf8":        func(m *AcquisitionMetadata) { m.LastModified = string([]byte{255}) },
		"wrong_route": func(m *AcquisitionMetadata) { m.SourceURL = "https://api.themoviedb.org/3/movie/43?api_key=never-echo" },
		"oversize":    func(m *AcquisitionMetadata) { m.ETag = strings.Repeat("x", 513) },
	} {
		t.Run(name, func(t *testing.T) {
			m := documentMetadata()
			mutate(&m)
			tr := broker(t, documentConfig(), admissionFunc(allowed), func(context.Context, RequestKey, int64) (Response, error) {
				return Response{Status: 200, Body: []byte("hello"), Metadata: m}, nil
			})
			d, err := tr.GetDocument(t.Context(), documentKey())
			wantError := ErrAcquisitionMetadata
			if name == "oversize" {
				wantError = ErrBusy
			}
			if !errors.Is(err, wantError) || !reflect.DeepEqual(d, AcquisitionDocument{}) || strings.Contains(err.Error(), "never-echo") {
				t.Fatal("missing metadata fabricated/fell back", err)
			}
			if name == "oversize" {
				var size *AcquisitionMetadataSizeError
				if !errors.As(err, &size) || size.LimitAllocationBytes != 512 || size.ObservedAllocationBytes <= 512 {
					t.Fatal("missing metadata allocation diagnostic", err)
				}
			}
			if s := tr.Stats(); s.Bytes != 0 || s.CacheBytes != 0 || s.CacheMetadataBytes != 0 || s.Failures != 1 {
				t.Fatal("invalid original metadata cached/counted", s)
			}
		})
	}
	tr := broker(t, config(), admissionFunc(allowed), func(context.Context, RequestKey, int64) (Response, error) {
		t.Fatal("missing metadata budget dispatched")
		return Response{}, nil
	})
	if d, err := tr.GetDocument(t.Context(), documentKey()); !errors.Is(err, ErrTMDBPolicy) || !reflect.DeepEqual(d, AcquisitionDocument{}) {
		t.Fatal("missing metadata policy", err)
	}
	for _, change := range []func(*RequestKey){
		func(k *RequestKey) { k.ExternalID = "0042" }, func(k *RequestKey) { k.ExternalID = "42?api_key=never-send" },
		func(k *RequestKey) { k.Kind = "season" }, func(k *RequestKey) { k.Shape = "unreviewed" }, func(k *RequestKey) { k.Locale = "" },
	} {
		key := documentKey()
		change(&key)
		strict := broker(t, documentConfig(), admissionFunc(allowed), func(context.Context, RequestKey, int64) (Response, error) {
			t.Fatal("unqualified key dispatched")
			return Response{}, nil
		})
		if d, err := strict.GetDocument(t.Context(), key); !errors.Is(err, ErrAcquisitionMetadata) || !reflect.DeepEqual(d, AcquisitionDocument{}) {
			t.Fatal("wrong typed document namespace", err)
		}
	}
}

func TestAcquisitionDocumentEarlyRefusalKeepsProviderFloor(t *testing.T) {
	tr := broker(t, config(), admissionFunc(allowed), func(context.Context, RequestKey, int64) (Response, error) {
		return Response{Status: 429, RetryAfter: 48 * time.Hour}, nil
	})
	if _, err := tr.Get(t.Context(), key("limited")); err == nil {
		t.Fatal("provider floor absent")
	}
	_, err := tr.GetDocument(t.Context(), documentKey())
	var provider *ProviderError
	if !errors.Is(err, ErrTMDBPolicy) || !errors.As(err, &provider) || provider.RetryAfter < 47*time.Hour || !provider.Suspended {
		t.Fatal("early metadata refusal hid floor", err)
	}
}

type acquisitionCancelAtReturn struct {
	context.Context
	reads atomic.Int32
}

func (c *acquisitionCancelAtReturn) Err() error {
	if c.reads.Add(1) >= 3 {
		return context.Canceled
	}
	return nil
}

func TestAcquisitionDocumentReturnCancellationPreservesLongFloor(t *testing.T) {
	for _, failed := range []bool{false, true} {
		tr := broker(t, documentConfig(), admissionFunc(allowed), func(context.Context, RequestKey, int64) (Response, error) {
			if failed {
				return Response{Status: 503, RetryAfter: 2 * time.Second}, &ProviderError{Status: 503, RetryAfter: 48 * time.Hour, Suspended: true}
			}
			return Response{Status: 200, Body: []byte("complete"), Metadata: documentMetadata()}, nil
		})
		ctx := &acquisitionCancelAtReturn{Context: context.Background()}
		doc, err := tr.GetDocument(ctx, documentKey())
		if !reflect.DeepEqual(doc, AcquisitionDocument{}) || !errors.Is(err, context.Canceled) {
			t.Fatal("return cancellation leaked acquisition data", err)
		}
		if failed {
			var provider *ProviderError
			if !errors.As(err, &provider) || provider.RetryAfter < 47*time.Hour || !provider.Suspended {
				t.Fatal("return cancellation hid original provider guidance", err)
			}
		}
	}
}
