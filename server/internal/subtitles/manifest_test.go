package subtitles

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"strings"
	"testing"
)

func TestManifestWindowsRetainCrossingCuesOffsetsAndEmptyWindows(t *testing.T) {
	raw, _ := json.Marshal(Document{Version: 1, TimeDomain: "source", Cues: []TextCue{{StartUS: "59000000", EndUS: "61000000", Text: "<word>", Italic: true, Top: true}}})
	for _, window := range []int{0, 1} {
		v, e := ManifestWebVTT(bytes.NewReader(raw), 500000, window)
		if e != nil {
			t.Fatal(e)
		}
		if !strings.Contains(string(v), "00:00:59.500 --> 00:01:01.500 line:10%") || !strings.Contains(string(v), "<i>&lt;word&gt;</i>") {
			t.Fatal(string(v))
		}
	}
	v, e := ManifestWebVTT(bytes.NewReader(raw), 0, 2)
	if e != nil || strings.Contains(string(v), "-->") || !strings.Contains(string(v), "X-TIMESTAMP-MAP") {
		t.Fatal(string(v), e)
	}
}
func TestManifestPinsAreViewerScopedAndRevocable(t *testing.T) {
	f := newSubtitleFixture(t)
	ctx := context.Background()
	first, e := f.s.Mutate(ctx, f.p, f.item.Public, uploadMutation("manifest", plainSubtitle, f.item.Token))
	if e != nil {
		t.Fatal(e)
	}
	gate, e := dbwork.Begin(ctx, f.db, dbwork.ClassEstablishedPlayback)
	if e != nil {
		t.Fatal(e)
	}
	if e = PinManifestTextTx(ctx, gate.Tx(), "session", f.item.Public, f.item.Token, ViewerKey("local", "account", "profile", "server")); e != nil {
		t.Fatal(e)
	}
	if e = gate.Commit(); e != nil {
		t.Fatal(e)
	}
	reader, _, e := f.s.OpenManifestDocument(ctx, f.p, f.item.Public, "session", first.ResourceID, 1)
	if e != nil {
		t.Fatal(e)
	}
	reader.Close()
	f.allowed = false
	if e = f.s.CheckManifestDocument(ctx, f.p, f.item.Public, "session", first.ResourceID, 1); e != identity.ErrUnauthorized {
		t.Fatal(e)
	}
}
