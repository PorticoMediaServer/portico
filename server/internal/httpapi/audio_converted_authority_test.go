package httpapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/audiofacts"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

type authorityTestConverter struct {
	write func(context.Context, io.Writer) error
}

func (c authorityTestConverter) MeasureAudio(context.Context, string, string) (audiofacts.Facts, error) {
	return audiofacts.Facts{}, nil
}
func (c authorityTestConverter) ConvertAudio(ctx context.Context, _, _ string, _ decoder.AnalysisSpec, out io.Writer) error {
	return c.write(ctx, out)
}

func TestConvertedAudioStopsAfterPublicSessionEnds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, _ := renderingFixture(t, 1)
	declareDecode(t, f, map[string]any{"codec": "opus", "containers": []string{"ogg"}})
	var session playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "converted-authority-01"}, startBody(f.items[0], nil), 201, &session)
	plan := session.Presentation.AudioRender
	if plan == nil || plan.Mode != "converted" {
		t.Fatalf("expected converted plan: %+v", plan)
	}
	grant := grantOf(t, plan)
	f.deps.AudioMedia = authorityTestConverter{write: func(ctx context.Context, out io.Writer) error {
		if n, err := out.Write([]byte("before")); n != 6 || err != nil {
			t.Fatalf("first write: %d %v", n, err)
		}
		f.call("DELETE", "/v1/playback/sessions/"+session.ID, nil, nil, 204, nil)
		if _, _, _, err := f.v1.Playback.ResolveDecodeGrantContext(ctx, grant); err == nil {
			t.Fatal("stopped grant still resolves")
		}
		time.Sleep(streamVerdictLife + 20*time.Millisecond)
		n, err := out.Write([]byte("after"))
		if n != 0 || err == nil {
			t.Fatalf("stopped write: %d %v", n, err)
		}
		return err
	}}
	f.handler = New(f.deps)
	w := f.raw("GET", plan.URL, "", nil, nil)
	if w.Code != 200 || w.Body.String() != "before" {
		t.Fatalf("response %d %q", w.Code, w.Body.String())
	}
}

func TestConvertedAuthorityWriterCancellationAndGeneration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out bytes.Buffer
	denied := errors.New("revoked")
	allowed, checks := true, 0
	a := newStreamAuthority(func() error {
		checks++
		if !allowed {
			return denied
		}
		return nil
	})
	w := &authorityWriter{w: &out, ctx: ctx, check: a.check}
	if _, err := w.Write([]byte("one")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("two")); err != nil || checks != 1 {
		t.Fatalf("cached verdict: %d %v", checks, err)
	}
	allowed = false
	dbwork.BumpAuthority()
	if n, err := w.Write([]byte("revoked")); n != 0 || !errors.Is(err, denied) {
		t.Fatalf("revoked: %d %v", n, err)
	}
	cancel()
	if n, err := w.Write([]byte("cancelled")); n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %d %v", n, err)
	}
	if strings.Contains(out.String(), "revoked") || out.String() != "onetwo" {
		t.Fatal(out.String())
	}
}

func TestPrivateConvertedAudioStopsAfterQueueSessionEnds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, _ := renderingFixture(t, 2)
	declareDecode(t, f, map[string]any{"codec": "opus", "containers": []string{"ogg"}})
	q := playingQueue(t, f, "converted-private-queue-01")
	p := prepareNext(t, f, q, "converted-private-prep-01")
	plan := p.Presentation.AudioRender
	if plan == nil || plan.Mode != "converted" {
		t.Fatalf("expected converted plan: %+v", plan)
	}
	grant := grantOf(t, plan)
	f.deps.AudioMedia = authorityTestConverter{write: func(ctx context.Context, out io.Writer) error {
		if n, err := out.Write([]byte("before")); n != 6 || err != nil {
			t.Fatalf("first write: %d %v", n, err)
		}
		f.call("DELETE", "/v1/playback/sessions/"+q.Session.ID, nil, nil, 204, nil)
		if _, _, _, err := f.v1.Playback.ResolveDecodeGrantContext(ctx, grant); err == nil {
			t.Fatal("stopped private grant still resolves")
		}
		time.Sleep(streamVerdictLife + 20*time.Millisecond)
		n, err := out.Write([]byte("after"))
		if n != 0 || err == nil {
			t.Fatalf("stopped private write: %d %v", n, err)
		}
		return err
	}}
	f.handler = New(f.deps)
	w := f.raw("GET", plan.URL, "", nil, nil)
	if w.Code != 200 || w.Body.String() != "before" {
		t.Fatalf("response %d %q", w.Code, w.Body.String())
	}
}
