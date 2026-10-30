package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/downloads"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

// Slice 1: a device publishes its capabilities (whole-document replace with a
// revision) and reads playback options with a side-effect-free plan preview.
func TestPlaybackV1CapabilitiesAndOptions(t *testing.T) {
	f := newV1Fixture(t, 2)
	path := "/v1/me/devices/current/capabilities"
	w := f.call("PUT", path, nil, webCapabilities(), 204, nil)
	if w.Header().Get("ETag") != `"1"` {
		t.Fatalf("first revision etag %q", w.Header().Get("ETag"))
	}
	// Unchanged document: same revision. Stale If-Match: 412 with the current document.
	if w = f.call("PUT", path, nil, webCapabilities(), 204, nil); w.Header().Get("ETag") != `"1"` {
		t.Fatalf("unchanged document bumped the revision: %q", w.Header().Get("ETag"))
	}
	changed := webCapabilities()
	changed["client"] = map[string]any{"family": "web", "form": "desktop"}
	w = f.call("PUT", path, map[string]string{"If-Match": `"7"`}, changed, 412, nil)
	var stale struct {
		Error struct {
			Code    string                          `json:"code"`
			Current playbackv1.CapabilitiesDocument `json:"current"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &stale)
	if stale.Error.Code != "revision_mismatch" || stale.Error.Current.Revision != 1 || stale.Error.Current.Document.Client.Form != "browser" {
		t.Fatalf("412 body %s", w.Body.String())
	}
	if w = f.call("PUT", path, map[string]string{"If-Match": "1"}, changed, 204, nil); w.Header().Get("ETag") != `"2"` {
		t.Fatalf("bare If-Match not accepted: %q", w.Header().Get("ETag"))
	}
	var doc playbackv1.CapabilitiesDocument
	f.call("GET", path, nil, nil, 200, &doc)
	if doc.Revision != 2 || doc.Document.Client.Form != "desktop" || doc.Document.EngineSetting != "automatic" {
		t.Fatalf("document %+v", doc)
	}
	// A client holding this revision is answered 304; an older one gets the document.
	if w = f.call("GET", path, map[string]string{"If-None-Match": `"2"`}, nil, 304, nil); w.Body.Len() != 0 || w.Header().Get("ETag") != `"2"` {
		t.Fatalf("held revision: %d %q %s", w.Code, w.Header().Get("ETag"), w.Body)
	}
	f.call("GET", path, map[string]string{"If-None-Match": `"1"`}, nil, 200, nil)
	// Strict requests: an unknown field is refused, an unknown value is kept.
	bad := webCapabilities()
	bad["bogus"] = true
	if w = f.raw("PUT", path, f.owner.AccessToken, nil, bad); w.Code != 400 {
		t.Fatalf("unknown field accepted: %d", w.Code)
	}
	future := webCapabilities()
	nativeEngine(future)["video"] = []map[string]any{{"codec": "h267", "bitDepths": []int{12}}}
	f.call("PUT", path, nil, future, 204, nil)
	f.call("PUT", path, nil, webCapabilities(), 204, nil)

	var options playbackv1.Options
	f.call("GET", "/v1/items/"+f.items[0]+"/playback-options", nil, nil, 200, &options)
	if options.ItemID != f.items[0] || options.Kind != "movie" || len(options.Versions) != 1 || options.Plan == nil {
		t.Fatalf("options %+v", options)
	}
	v := options.Versions[0]
	if v.DurationMs != 600_000 || len(v.Parts) != 1 || v.Label == "" || options.Preferred == nil || options.Preferred.VersionID != v.ID {
		t.Fatalf("version %+v preferred %+v", v, options.Preferred)
	}
	if options.Plan.Mode != "direct" || options.Plan.VersionID != v.ID || options.Plan.Quality.Mode != "original" {
		t.Fatalf("plan %+v", options.Plan)
	}
	// Preview: a low limit converts (the reason names the request, not load), and
	// the same query twice gives the same plan (invariant 6).
	query := "/v1/items/" + f.items[0] + "/playback-options?quality=limit&maxVideoBitrateKbps=500&maxHeight=480"
	var limited, again playbackv1.Options
	f.call("GET", query, nil, nil, 200, &limited)
	f.call("GET", query, nil, nil, 200, &again)
	// This test server has no converter, so a limit below the source is refused
	// and the preview has no plan; with one, the plan converts.
	if limited.Plan != nil && (limited.Plan.Quality.Mode != "limit" || limited.Plan.Mode != "stream") {
		t.Fatalf("limited plan %+v", limited.Plan)
	}
	a, _ := json.Marshal(limited.Plan)
	b, _ := json.Marshal(again.Plan)
	if string(a) != string(b) {
		t.Fatalf("same request, different plans:\n%s\n%s", a, b)
	}
	// Bad previews and unknown query keys are refused; unknown items are 404.
	for _, q := range []string{"?quality=turbo", "?maxHeight=480", "?versionId=nope", "?nope=1", "?quality=limit&maxHeight=-1"} {
		if w = f.raw("GET", "/v1/items/"+f.items[0]+"/playback-options"+q, f.owner.AccessToken, nil, nil); w.Code != 400 {
			t.Fatalf("%s: %d %s", q, w.Code, w.Body.String())
		}
	}
	if w = f.raw("GET", "/v1/items/missing/playback-options", f.owner.AccessToken, nil, nil); w.Code != 404 {
		t.Fatalf("missing item: %d", w.Code)
	}
	if w = f.raw("GET", "/v1/items/"+f.items[0]+"/playback-options", "", nil, nil); w.Code != 401 {
		t.Fatalf("anonymous: %d", w.Code)
	}
}

// Review P5: a two-part title is one version of two parts; its chapters run
// on one timeline (part two's offset by part one); a preview names a part or
// a subtitle it has, or is refused; a session started on part two plays the
// second file.
func TestPlaybackV1MultiPartVersions(t *testing.T) {
	f := newV1Fixture(t, 1)
	item := f.items[0]
	record := f.records[0]
	var firstPath string
	if err := f.db.QueryRow(`SELECT a.path FROM catalog_assets a JOIN catalog_asset_links l ON l.asset_id=a.id WHERE l.entity_id=?`, record.ID).Scan(&firstPath); err != nil {
		t.Fatal(err)
	}
	partTwoPath := firstPath + ".2"
	if err := os.WriteFile(partTwoPath, []byte("part two"), 0600); err != nil {
		t.Fatal(err)
	}
	partTwoAsset := int64(0)
	partTwoToken := ""
	f.catalogTest.Write(func(ctx context.Context, tx *sql.Tx) error {
		if err := compactcatalog.LinkAssetTx(ctx, tx, record.ID, record.Asset, compactcatalog.Link{Part: 1}); err != nil {
			return err
		}
		info, err := os.Stat(partTwoPath)
		if err != nil {
			return err
		}
		partTwoAsset, partTwoToken, err = compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: partTwoPath, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 600})
		if err != nil {
			return err
		}
		return compactcatalog.LinkAssetTx(ctx, tx, record.ID, partTwoAsset, compactcatalog.Link{Part: 2})
	})
	for _, token := range []string{record.Token, partTwoToken} {
		if _, err := f.db.Exec(`INSERT INTO asset_chapter_facts(asset_id,revision,size,modified_ns,status,fingerprint) VALUES(?,1,1,1,'ready',?)`, token, token); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.db.Exec(`INSERT INTO asset_chapters(asset_id,chapter_index,title,start_seconds,end_seconds) VALUES(?,0,'Opening',0,300),(?,0,'Second half',60,600)`, record.Token, partTwoToken); err != nil {
		t.Fatal(err)
	}
	f.catalogTest.Drain()
	var o playbackv1.Options
	f.call("GET", "/v1/items/"+item+"/playback-options", nil, nil, 200, &o)
	if len(o.Versions) != 1 || len(o.Versions[0].Parts) != 2 || o.Versions[0].Parts[1].ID != partTwoToken {
		t.Fatalf("versions %+v", o.Versions)
	}
	if len(o.Chapters) != 2 || o.Chapters[1].Title != "Second half" || o.Chapters[1].StartMs != 660_000 {
		t.Fatalf("chapters %+v", o.Chapters)
	}
	for _, q := range []string{"partId=nope", "subtitleId=nope"} {
		if w := f.raw("GET", "/v1/items/"+item+"/playback-options?"+q, f.owner.AccessToken, nil, nil); w.Code != 400 {
			t.Fatalf("%s: %d", q, w.Code)
		}
	}
	f.call("GET", "/v1/items/"+item+"/playback-options?partId="+partTwoToken+"&subtitleId=none", nil, nil, 200, &o)
	var s playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "multi-part-0000001"}, startBody(item, map[string]any{"partIndex": 2}), 201, &s)
	var asset string
	if err := f.db.QueryRow(`SELECT p.asset_id FROM playback_v1_sessions v JOIN playback_sessions p ON p.id=v.media_session_id WHERE v.id=?`, s.ID).Scan(&asset); err != nil || asset != partTwoToken {
		t.Fatalf("part two plays %q: %v", asset, err)
	}
	if w := f.raw("POST", "/v1/playback/sessions", f.owner.AccessToken, map[string]string{"Idempotency-Key": "multi-part-0000002"}, startBody(item, map[string]any{"partIndex": 7})); w.Code != 400 {
		t.Fatalf("an unknown part: %d %s", w.Code, w.Body.String())
	}
}

func TestPlaybackV1AlternativesAreActuallyPlayableAndRespectPolicy(t *testing.T) {
	f := newV1Fixture(t, 1)
	var out playbackv1.Options
	path := "/v1/items/" + f.items[0] + "/playback-options"
	f.call("GET", path, nil, nil, 200, &out)
	if len(out.Alternatives) != 1 || out.Alternatives[0].VersionID != f.records[0].Token || out.Alternatives[0].Label == "" {
		t.Fatalf("playable alternatives %+v", out.Alternatives)
	}
	caps := webCapabilities()
	nativeEngine(caps)["video"] = []map[string]any{{"codec": "av1", "bitDepths": []int{8}}}
	nativeEngine(caps)["transports"] = []map[string]any{{"transport": "direct", "containers": []string{"mp4"}, "video": []string{"av1"}, "audio": []string{"aac"}}}
	f.call("PUT", "/v1/me/devices/current/capabilities", nil, caps, 204, nil)
	f.call("GET", path, nil, nil, 200, &out)
	if len(out.Alternatives) != 0 {
		t.Fatalf("unplayable version offered: %+v", out.Alternatives)
	}
}

func TestPlaybackV1CollectionSuccessorUsesCollectionOrderAndAvailability(t *testing.T) {
	f := newV1Fixture(t, 3)
	collection := f.catalogTest.Collection(f.libraryHandle, "Collection", f.records[2], f.records[0], f.records[1])
	f.catalogTest.Drain()
	f.catalogTest.Write(func(ctx context.Context, tx *sql.Tx) error {
		for i, n := range []int{2, 0, 1} {
			if err := compactcatalog.SetCollectionMemberTx(ctx, tx, collection.ID, f.records[n].ID, fmt.Sprintf("%04d", i), true); err != nil {
				return err
			}
		}
		return nil
	})
	var out playbackv1.Options
	path := "/v1/items/" + f.items[2] + "/playback-options"
	f.call("GET", path, nil, nil, 200, &out)
	if out.Collection == nil || out.Collection.ID != collection.Public || out.Collection.Next.ID != f.items[0] || out.Collection.Next.Title == "" {
		t.Fatalf("collection successor %+v", out.Collection)
	}
	if _, err := f.db.Exec(`UPDATE catalog_assets SET available=0 WHERE id=?`, f.records[0].Asset); err != nil {
		t.Fatal(err)
	}
	f.call("GET", path, nil, nil, 200, &out)
	if out.Collection == nil || out.Collection.Next.ID != f.items[1] {
		t.Fatalf("unavailable successor offered %+v", out.Collection)
	}
}

func TestPlaybackV1AutomaticVersionUsesMeasuredConnectionWithoutDefaultVersion(t *testing.T) {
	f := newV1Fixture(t, 1)
	item := f.records[0]
	var original string
	if err := f.db.QueryRow(`SELECT path FROM catalog_assets WHERE id=?`, item.Asset).Scan(&original); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(original, 80*1024*1024); err != nil {
		t.Fatal(err)
	}
	small := original + ".small.mp4"
	if err := os.WriteFile(small, []byte("small file"), 0600); err != nil {
		t.Fatal(err)
	}
	var smallToken string
	f.catalogTest.Write(func(ctx context.Context, tx *sql.Tx) error {
		info, err := os.Stat(original)
		if err != nil {
			return err
		}
		if _, _, err = compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: original, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 600}); err != nil {
			return err
		}
		info, err = os.Stat(small)
		if err != nil {
			return err
		}
		asset, token, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: small, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 640, Height: 360, Duration: 600})
		smallToken = token
		if err != nil {
			return err
		}
		return compactcatalog.LinkAssetTx(ctx, tx, item.ID, asset, compactcatalog.Link{})
	})
	f.catalogTest.Drain()
	var normal, slow playbackv1.Options
	path := "/v1/items/" + item.Public + "/playback-options"
	f.call("GET", path, nil, nil, 200, &normal)
	f.call("GET", path+"?throughputKbps=100", nil, nil, 200, &slow)
	if normal.Preferred == nil || slow.Preferred == nil || normal.Preferred.VersionID != item.Token || slow.Preferred.VersionID != smallToken {
		t.Fatalf("automatic versions normal=%+v slow=%+v", normal.Preferred, slow.Preferred)
	}
	var session playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "automatic-network-00001"}, startBody(item.Public, map[string]any{"throughputKbps": 100}), 201, &session)
	if session.VersionID != smallToken {
		t.Fatalf("start differs from preview: %+v", session)
	}
}

func TestPlaybackV1ContainerDownloadOptionsHTTPUsesTypedRouteAndDeviceAuthority(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, member, ids := queueAccessFixture(t)
	offline, err := downloads.New(downloads.Options{DB: f.db})
	if err != nil {
		t.Fatal(err)
	}
	defer offline.Close()
	f.deps.Downloads = offline
	f.handler = New(f.deps)
	principal, err := f.id.Authenticate(member)
	if err != nil {
		t.Fatal(err)
	}
	in := map[string]any{"target": map[string]string{"kind": "album", "id": ids["kids-album"]}, "deviceId": principal.DeviceID, "policy": map[string]string{"episodes": "all"}}
	var out downloads.ContainerOptionsView
	f.callAs(member, "POST", "/v1/downloads/options", nil, in, 200, &out)
	if out.Total != 2 || len(out.Options) == 0 || out.Options[0].Quality != "original" {
		t.Fatalf("container sheet %+v", out)
	}
	in["deviceId"] = "foreign-device"
	f.callAs(member, "POST", "/v1/downloads/options", nil, in, 401, nil)
	in["deviceId"] = principal.DeviceID
	in["target"] = map[string]string{"kind": "album", "id": ids["owner-album"]}
	f.callAs(member, "POST", "/v1/downloads/options", nil, in, 404, nil)
}
