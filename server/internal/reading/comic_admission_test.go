package reading

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"
)

// observedAdmissionContext signals when a caller evaluates its cancellable wait.
// This makes the ordering assertion independent of goroutine scheduling.
type observedAdmissionContext struct {
	context.Context
	once    sync.Once
	waiting chan struct{}
}

func (c *observedAdmissionContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestComicPageAdmissionPrecedesMemberRead(t *testing.T) {
	for _, width := range []int{0, 32, 128} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			imageWork <- struct{}{}
			defer func() { <-imageWork }()
			raw := []byte(fixturePNG(t, 64, 64))
			reads := make(chan struct{}, 8)
			comic := &Comic{Names: []string{"page.png"}, pageReader: func(context.Context, string, int64) ([]byte, error) {
				reads <- struct{}{}
				return raw, nil
			}}
			ctx, cancel := context.WithCancel(context.Background())
			results := make(chan error, 8)
			var workers sync.WaitGroup
			defer func() {
				cancel()
				workers.Wait()
			}()
			waiting := make([]chan struct{}, 8)
			for i := range waiting {
				waiting[i] = make(chan struct{})
				callCtx := &observedAdmissionContext{Context: ctx, waiting: waiting[i]}
				workers.Add(1)
				go func() { defer workers.Done(); _, err := comic.Page(callCtx, 0, width); results <- err }()
			}
			for _, entered := range waiting {
				select {
				case <-entered:
				case <-reads:
					t.Fatal("page bytes read before admission")
				case <-time.After(5 * time.Second):
					t.Fatal("page did not reach admission")
				}
			}
			select {
			case <-reads:
				t.Fatal("waiting page read bytes")
			default:
			}
			cancel()
			for range 8 {
				if err := <-results; !errors.Is(err, context.Canceled) {
					t.Errorf("canceled waiter: %v", err)
				}

			}
		})
	}
}

func TestComicPageAdmissionReleasedOnReadAndImageErrors(t *testing.T) {
	sourceErr := errors.New("source read failed")
	for _, tc := range []struct {
		name string
		raw  []byte
		err  error
	}{
		{name: "source", err: sourceErr}, {name: "image", raw: []byte("invalid image")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			comic := &Comic{Names: []string{"page.png"}, pageReader: func(context.Context, string, int64) ([]byte, error) { return tc.raw, tc.err }}
			if _, err := comic.Page(context.Background(), 0, 32); err == nil {
				t.Fatal("expected page failure")
			}
			select {
			case imageWork <- struct{}{}:
				<-imageWork
			default:
				t.Fatal("page failure leaked admission")
			}
		})
	}
}
