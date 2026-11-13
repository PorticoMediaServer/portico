package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

type streamFactsProber struct {
	facts mediafacts.Facts
	calls int
}

func (p *streamFactsProber) Probe(context.Context, string) (scan.Facts, error) {
	p.calls++
	return scan.FromMedia(p.facts), nil
}

func TestScannedStreamFactsReachFileInfoAndPlaybackChoices(t *testing.T) {
	s := newTestServer(t)
	engine := &fakeEngine{}
	s.playback.SetEngine(engine, engine)
	owner, ctx := onMain(t, s, setup(t, s)).AccessToken, context.Background()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Film (2020).mkv"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	lib, err := s.db.CreateLibrary(ctx, "Films", db.LibraryMovies, "en", []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	probe := &streamFactsProber{facts: mediafacts.Facts{Container: "mkv", Duration: 60, Streams: []mediafacts.Stream{
		{Type: "video", Codec: "hevc", Detail: mediafacts.StreamDetail{Width: 3840, Height: 2160, BitDepth: 10, DynamicRange: mediafacts.RangeHDR10}},
		{Type: "audio", Codec: "truehd", Title: "Director notes", Channels: 8},
		{Type: "audio", Codec: "dts", Channels: 8},
		{Type: "audio", Codec: "aac", Channels: 2, Default: true},
	}}}
	scanner := &scan.Scanner{DB: s.db, Prober: probe}
	if _, err := scanner.Scan(ctx, lib); err != nil {
		t.Fatal(err)
	}
	id := get[ItemPage](t, s, owner, "/v1/items?library="+lib.PublicID+"&kind=movie").Items[0].ID
	// Previously dropped facts cannot be recovered from the old stored rows alone.
	old := get[Item](t, s, owner, "/v1/items/"+id).Versions[0].Parts[0].Streams
	if old[0].HDR10Plus || old[1].Commentary || old[1].ObjectAudio != "" {
		t.Fatalf("unobserved features were invented: %+v", old)
	}
	probe.facts.Streams[0].Detail.HDR10Plus = true
	probe.facts.Streams[1].Commentary = true
	probe.facts.Streams[1].Detail.ObjectAudio = "atmos"
	probe.facts.Streams[2].Detail.ObjectAudio = "dtsx"
	if _, err := scanner.Scan(ctx, lib); err != nil || probe.calls != 1 {
		t.Fatalf("ordinary unchanged scan probed again: %d, %v", probe.calls, err)
	}
	scanner.AllFiles = true
	// The scan manager forgets the old probes before Scan all files.
	if err := s.db.ForgetProbes(ctx, lib.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := scanner.Scan(ctx, lib); err != nil || probe.calls != 2 {
		t.Fatalf("Scan all files did not read the facts: %d, %v", probe.calls, err)
	}
	streams := get[Item](t, s, owner, "/v1/items/"+id).Versions[0].Parts[0].Streams
	if streams[0].HDR != "hdr10" || !streams[0].HDR10Plus || streams[1].ObjectAudio != "atmos" || !streams[1].Commentary || streams[2].ObjectAudio != "dtsx" {
		t.Fatalf("File info lost probed facts: %+v", streams)
	}
	choices := get[PlaybackOptions](t, s, owner, "/v1/items/"+id+"/playback-options").Versions[0]
	if !choices.Video[0].HDR10Plus || choices.Audio[0].ObjectAudio != "atmos" || !choices.Audio[0].Commentary || choices.Audio[1].ObjectAudio != "dtsx" {
		t.Fatalf("playback choices lost probed facts: %+v", choices)
	}
	if choices.Audio[2].ObjectAudio != "" || choices.Audio[2].Commentary || streams[3].HDR10Plus {
		t.Fatalf("plain stream gained an unobserved fact: %+v", choices.Audio[2])
	}
}
