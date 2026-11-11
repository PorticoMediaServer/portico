package streaming

import (
	"encoding/json"
	"errors"
	"math/rand"
	"reflect"
	"testing"
)

// planEveryFile plans the matrix's kinds of file for one document, as a start would.
func planEveryFile(t *testing.T, device *CapabilityDocument) []DeliveryPlan {
	t.Helper()
	var plans []DeliveryPlan
	for _, row := range matrixRows() {
		source := row.build()
		plan, _ := Plan(PlanRequest{Measured: &source, Device: device, Settings: DefaultDeliveryConfiguration(), Toolchain: matrixToolchain(),
			TranscodingEnabled: true, FragmentedOutput: true})
		plans = append(plans, plan)
	}
	return plans
}

// A device is never refused for what its document is. One of a version this server does
// not know, older or newer, is planned from the baseline, and so is one that names only
// itself: an app that is behind or ahead of the server, or a first attempt at a client,
// still plays. Only something that is not a document at all is turned away at the door.
func TestADocumentTheServerCannotReadPlansAsTheBaseline(t *testing.T) {
	baseline := BaselineCapabilityDocument()
	want := planEveryFile(t, &baseline)
	for _, raw := range []string{
		`{"version":1,"client":{"family":"old"},"engines":{"native":{"video":"an older shape"}}}`,
		`{"version":3,"client":{"family":"new"},"players":[{"something":"this server has never seen"}]}`,
		`{"version":2,"client":{"family":"minimal"}}`,
	} {
		d, err := ParseCapabilityDocument([]byte(raw))
		if err != nil {
			t.Fatalf("%s was refused: %v", raw, err)
		}
		got := planEveryFile(t, &d)
		for i := range got {
			if got[i].Trace == nil || !containsString(got[i].Trace.Notes, NoteBaselineProfile) || got[i].Trace.Client.Published {
				t.Fatalf("%s: plan %d does not say it was made from the baseline", raw, i)
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s was not planned as the baseline", raw)
		}
	}
	for _, raw := range []string{``, `not json`, `[]`, `{}`, `{"version":0}`, `{"version":"2"}`, `{"version":-2}`,
		`{"version":2,"client":{"family":"x"},"engines":{"mpv":{}}}`} {
		if _, err := ParseCapabilityDocument([]byte(raw)); !errors.Is(err, ErrCapabilityDocument) {
			t.Errorf("%q was accepted as a document: %v", raw, err)
		}
	}
	// How a device knows what it plays is optional, and read as its own table.
	d, err := ParseCapabilityDocument([]byte(`{"version":2,"client":{"family":"x"},"engines":{"native":{"video":[{"codec":"h264"}]}}}`))
	if err != nil || d.Engines.Native.Evidence != EvidenceDeclared {
		t.Fatalf("a document without evidence: %+v %v", d.Engines.Native, err)
	}
}

// lyingDocument claims everything: every codec at any size, every range, every container,
// passthrough of everything, tone mapping, and a second player that claims the same. Some
// claims are then dropped at random so the documents differ.
func lyingDocument(r *rand.Rand) CapabilityDocument {
	some := func(values ...string) []string {
		out := []string{}
		for _, v := range values {
			if r.Intn(5) > 0 {
				out = append(out, v)
			}
		}
		return out
	}
	videoNames := []string{"h264", "hevc", "av1", "vp9", "vp8", "mpeg2video", "mpeg4", "vc1"}
	audioNames := []string{"aac", "mp3", "mp2", "ac3", "eac3", "dts", "truehd", "flac", "alac", "opus", "vorbis", "pcm"}
	ranges := []string{"sdr", "hdr10", "hdr10plus", "hlg", "dolby_vision"}
	engine := func() *EngineCapabilities {
		e := &EngineCapabilities{ToneMapsHDR: r.Intn(4) > 0, EmbeddedAudioSwitching: r.Intn(4) > 0, SubtitleRendering: some("text", "ass", "pgs", "vobsub", "dvb")}
		for _, codec := range some(videoNames...) {
			e.Video = append(e.Video, ClientVideoCodec{Codec: codec, BitDepths: []int{8, 10, 12}, MaxWidth: r.Intn(2) * 32768, MaxHeight: r.Intn(2) * 32768,
				MaxFrameRate: float64(r.Intn(2) * 1000), MaxBitrateBPS: r.Intn(2) * 2_000_000_000, DynamicRanges: some(ranges...),
				DolbyVisionProfiles: []int{5, 7, 8, 9}, Interlaced: true})
		}
		for _, codec := range some(audioNames...) {
			e.Audio = append(e.Audio, ClientAudioCodec{Codec: codec, MaxChannels: 64, ObjectAudio: []string{"atmos", "dtsx"}})
			e.AudioPassthrough = append(e.AudioPassthrough, ClientAudioPassthrough{Codec: codec, MaxChannels: 64, ObjectAudio: []string{"atmos", "dtsx"}})
		}
		for _, transport := range some(TransportDirect, TransportHLSTS, TransportHLSFMP4) {
			e.Transports = append(e.Transports, ClientTransport{Transport: transport,
				Containers: some("mp4", "m4v", "mov", "mkv", "webm", "ts", "m2ts", "avi", "flac", "mpegts", "fmp4"), Video: some(videoNames...), Audio: some(audioNames...)})
		}
		return e
	}
	d := CapabilityDocument{Version: CapabilityDocumentVersion, Client: ClientIdentity{Family: "liar"},
		EngineSetting: []string{EngineSettingAutomatic, EngineSettingNative, EngineSettingMPV}[r.Intn(3)],
		Display:       ClientDisplay{DynamicRanges: some(ranges...)},
		AudioOutput:   ClientAudioOutput{Route: []string{"hdmi", "earc", "speaker", ""}[r.Intn(4)], MaxChannels: r.Intn(2) * 64},
		Engines:       CapabilityEngines{Native: engine()}}
	if r.Intn(2) == 0 {
		d.Engines.MPV = engine()
	}
	return d
}

// scramble replaces parts of a JSON value with things that do not belong there.
func scramble(r *rand.Rand, v any) any {
	if r.Intn(400) == 0 {
		return []any{nil, "text", -1.0, 1e300, true, []any{}, map[string]any{}, []any{map[string]any{"codec": 7.0}}}[r.Intn(8)]
	}
	switch value := v.(type) {
	case map[string]any:
		for key, inner := range value {
			value[key] = scramble(r, inner)
		}
	case []any:
		for i, inner := range value {
			value[i] = scramble(r, inner)
		}
	}
	return v
}

// What a device says about itself can only narrow what it is given. The owner's and the
// member's limits are applied after the document: a client that claims to play anything,
// or sends a damaged document, is never sent a picture above the member's limit and is
// never given a conversion the owner has turned off. Nothing it sends makes planning fail
// in any way but a plain refusal.
func TestNoDocumentWidensTheOwnersOrTheMembersLimits(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	rows := matrixRows()
	check := func(doc *CapabilityDocument) {
		for _, row := range rows {
			source := row.build()
			if source.Video == nil {
				continue
			}
			ceiling := ReplanCeilings{Remote: true, MaxVideoBitrateBPS: []int{1_000_000, 4_000_000, 20_000_000}[r.Intn(3)], MaxVideoHeight: []int{480, 720, 1080}[r.Intn(3)]}
			conversion := r.Intn(3) > 0
			plan, err := Plan(PlanRequest{Measured: &source, Device: doc, Settings: DefaultDeliveryConfiguration(), Toolchain: matrixToolchain(),
				TranscodingEnabled: conversion, FragmentedOutput: true, MemberCeiling: ceiling})
			if err != nil {
				continue // refused: nothing was sent
			}
			converts := plan.VideoAction == "convert"
			if !conversion && (converts || plan.AudioAction == "convert") {
				t.Fatalf("%s: a conversion was planned with conversion turned off", row.source)
			}
			rate, height := int(max(source.Video.BitRate, source.Video.MaxBitRate, source.BitRate, source.MaxBitRate)), source.Video.Height
			if converts {
				rate = plan.VideoBitrateBPS
				if plan.TargetHeight > 0 {
					height = min(height, plan.TargetHeight)
				}
			}
			if rate > ceiling.MaxVideoBitrateBPS || height > ceiling.MaxVideoHeight {
				raw, _ := json.Marshal(doc)
				t.Fatalf("%s reached the device at %d bps and %dp (%s), above the member's limit of %d bps and %dp, for the document %s",
					row.source, rate, height, plan.Strategy, ceiling.MaxVideoBitrateBPS, ceiling.MaxVideoHeight, raw)
			}
		}
	}
	accepted := 0
	for i := 0; i < 150; i++ {
		doc := lyingDocument(r)
		raw, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if parsed, err := ParseCapabilityDocument(raw); err != nil {
			t.Fatalf("a well-formed document was refused: %v\n%s", err, raw)
		} else {
			check(&parsed)
		}
		// The same document, damaged.
		var tree any
		if err := json.Unmarshal(raw, &tree); err != nil {
			t.Fatal(err)
		}
		damaged, err := json.Marshal(scramble(r, tree))
		if err != nil {
			t.Fatal(err)
		}
		if parsed, err := ParseCapabilityDocument(damaged); err == nil {
			accepted++
			check(&parsed)
		} else if !errors.Is(err, ErrCapabilityDocument) {
			t.Fatalf("a damaged document failed with something other than a refusal: %v", err)
		}
	}
	if accepted < 20 || accepted > 130 {
		t.Fatalf("%d of 150 damaged documents were accepted: the damage is too heavy or too light to test both sides", accepted)
	}
}
