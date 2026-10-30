package subtitlevideo

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/decoder"
)

func TestMediaBridgeComposesWithConfinedDecoder(t *testing.T) {
	tool, lookErr := exec.LookPath("true")
	if lookErr != nil {
		t.Skip("no true(1) here")
	}
	source := &fixtureCachedSource{}
	bridge := NewMediaBridge(source)
	var previous string
	for range 2 {
		err := bridge.With(context.Background(), func(url string, reservation *decoder.EndpointReservation) error {
			if url == previous {
				t.Fatal("fresh physical attempt reused its private endpoint")
			}
			previous = url
			cmd, err := decoder.ProbeCommand(tool, url, reservation)
			// Every platform builds the decoder: sandboxed where it can, the
			// baseline elsewhere (mediaexec).
			{
				if err != nil {
					t.Fatalf("bridge rejected by decoder boundary: %v", err)
				}
				if cmd == nil {
					t.Fatal("missing confined command")
				}
			}
			// The admitted URL reaches only its retained source; altered tokens and
			// provider-style query strings cannot redirect or fetch other data.
			request, _ := http.NewRequest(http.MethodGet, url, nil)
			request.Header.Set("Range", "bytes=0-15")
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				return err
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil {
				return err
			}
			if response.StatusCode != 206 || len(body) != 16 {
				t.Fatalf("range response %d / %d bytes", response.StatusCode, len(body))
			}
			for _, invalid := range []string{url + "?next=private", url[:strings.LastIndex(url, "/")+1] + strings.Repeat("z", 64)} {
				if _, err := decoder.ProbeCommand("/private/test/ffprobe", invalid, reservation); !errors.Is(err, decoder.ErrInvalidConfiguration) {
					t.Fatalf("accepted altered URL: %v", err)
				}
				response, err := http.Get(invalid)
				if err != nil {
					return err
				}
				response.Body.Close()
				if response.StatusCode != 404 {
					t.Fatalf("altered URL reached source: %d", response.StatusCode)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// fragileInput behaves like the observed source reader: a read cancelled while
// it is being served loses the input for good.
type fragileInput struct {
	data []byte
	lost bool
}

func (f *fragileInput) Evidence() string { return "observed:fixture" }
func (f *fragileInput) Size() int64      { return int64(len(f.data)) }
func (f *fragileInput) Validate(ctx context.Context) error {
	if f.lost {
		return errors.New("lost")
	}
	return nil
}
func (f *fragileInput) Close() error { return nil }
func (f *fragileInput) ReadExtent(ctx context.Context, offset, length int64) ([]byte, error) {
	if f.lost {
		return nil, errors.New("lost")
	}
	select {
	case <-ctx.Done():
		f.lost = true
		return nil, ctx.Err()
	case <-time.After(20 * time.Millisecond):
	}
	return f.data[offset : offset+length], nil
}

func TestAnalysisBridgeSurvivesADecoderHangingUpMidResponse(t *testing.T) {
	input := &fragileInput{data: make([]byte, 8<<20)}
	b, err := openBridge(context.Background(), input, &extentLedger{})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	// A seeking decoder reads the start of a long response and drops it.
	response, err := http.Get(b.url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.ReadFull(response.Body, make([]byte, 64<<10)); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	time.Sleep(100 * time.Millisecond)
	// Its next request, at the new position, still reaches the source.
	request, _ := http.NewRequest(http.MethodGet, b.url, nil)
	request.Header.Set("Range", "bytes=6291456-6291471")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if input.lost || response.StatusCode != 206 || len(body) != 16 {
		t.Fatalf("the source was lost when the decoder hung up: lost=%v status=%d bytes=%d", input.lost, response.StatusCode, len(body))
	}
}
