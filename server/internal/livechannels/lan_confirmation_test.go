package livechannels

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
)

// CD-06: an HDHomeRun (or any source on the owner's own network) must be
// addable. The server keeps the explicit-confirmation rule but now says exactly
// what to confirm, asks once per preview for everything the source will use,
// and treats the confirmed device's other ports (an HDHomeRun streams on :5004)
// as the same device, so playback works after the save.
func TestLANSourceConfirmationFlow(t *testing.T) {
	streams := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auto/v5.1" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("mpeg-ts"))
	}))
	defer streams.Close()
	device := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/discover.json":
			_, _ = w.Write([]byte(`{"TunerCount":2}`))
		case "/lineup.json":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"GuideNumber": "5.1", "GuideName": "Five", "URL": streams.URL + "/auto/v5.1"},
				// Another device on the LAN: never contacted until confirmed.
				{"GuideNumber": "9.1", "GuideName": "Nine", "URL": "http://10.0.0.9:5004/auto/v9.1"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer device.Close()

	s := testStore(t)
	ctx := context.Background()
	a := testAuthority("fence", true, true)
	draft := RemoteDraft{ID: strings.Repeat("ef", 24), Name: "Antenna", Kind: "hdhomerun", Locator: device.URL + "/", ConfirmedLANRoots: []string{}, Mappings: []ChannelMapping{}, RefreshMs: 3600000, ViewerAccess: "owner-only"}
	deviceRoot := "http://" + strings.TrimPrefix(device.URL, "http://") + "/"
	otherRoot := "http://10.0.0.9:5004/"

	// 1. Nothing confirmed: the device itself is the one root to confirm.
	_, e := s.PreviewRemote(ctx, a, SourceFetcher{}, draft)
	var lan *LANConfirmationRequired
	if !errors.As(e, &lan) || fmt.Sprint(lan.Roots) != fmt.Sprint([]string{deviceRoot}) {
		t.Fatalf("unconfirmed device: %v", e)
	}
	if !errors.Is(e, ErrNetworkPolicy) {
		t.Fatal("a LAN confirmation must still be a network-policy refusal")
	}

	// 2. Device confirmed: its :5004-style stream port is the same device and
	// is approved with it; the other LAN device is the one left to confirm.
	draft.ConfirmedLANRoots = lan.Roots
	_, e = s.PreviewRemote(ctx, a, SourceFetcher{}, draft)
	if !errors.As(e, &lan) || fmt.Sprint(lan.Roots) != fmt.Sprint([]string{otherRoot}) {
		t.Fatalf("second device: %v", e)
	}

	// 3. Both confirmed: the preview succeeds without contacting 10.0.0.9.
	draft.ConfirmedLANRoots = []string{deviceRoot, otherRoot}
	preview, e := s.PreviewRemote(ctx, a, SourceFetcher{}, draft)
	if e != nil {
		t.Fatal(e)
	}
	if preview.Preview.Channels != 2 || preview.DiscoveredCapacity != 2 {
		t.Fatalf("preview: %+v", preview)
	}
	if _, e = s.SaveRemote(ctx, a, preview.ID, strings.Repeat("12", 24)); e != nil {
		t.Fatal(e)
	}

	// The saved policy admits the stream on the device's other port.
	var sealed []byte
	if e = s.db.QueryRow(`SELECT sealed FROM live_remote_configs WHERE source_id=?`, draft.ID).Scan(&sealed); e != nil {
		t.Fatal(e)
	}
	raw, e := s.unseal(sealed, "remote:"+draft.ID)
	if e != nil {
		t.Fatal(e)
	}
	var c remoteConfig
	if e = json.Unmarshal([]byte(raw), &c); e != nil {
		t.Fatal(e)
	}
	client, e := remotemedia.New(ctx, streams.URL+"/auto/v5.1", remotemedia.Policy{Approvals: c.Approvals})
	if e != nil {
		t.Fatalf("stream on the confirmed device was not admitted: %v (approvals %+v)", e, c.Approvals)
	}
	defer client.Close()
	resp, e := client.Open(ctx, "GET", "")
	if e != nil || resp.StatusCode != 200 {
		t.Fatalf("stream: %v", e)
	}
	resp.Body.Close()

	// A metadata service is never confirmable.
	draft.ID, draft.Locator, draft.ConfirmedLANRoots = strings.Repeat("0a", 24), "http://169.254.169.254/", []string{}
	_, e = s.PreviewRemote(ctx, a, SourceFetcher{}, draft)
	if !errors.Is(e, ErrNetworkPolicy) || errors.As(e, &lan) {
		t.Fatalf("metadata address: %v", e)
	}
}
