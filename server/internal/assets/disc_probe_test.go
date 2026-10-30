package assets

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/disc"
)

func TestDiscTitleMediaKeepsOrderedChaptersAndPIDLanguages(t *testing.T) {
	title := disc.Title{DurationMs: 72000, Chapters: []disc.Chapter{{StartMs: 0}, {StartMs: 36000}}, Streams: []disc.Stream{
		{PID: 4352, Language: "jpn"}, {PID: 4353, Language: "eng"}, {PID: 4354, Language: "jpn"}, {PID: 4354, Language: "fra"},
	}}
	data := []byte(`{"format":{"format_name":"mpegts","duration":"36.0"},"streams":[
 {"index":0,"id":"0x1011","codec_type":"video","codec_name":"h264","width":320,"height":180},
 {"index":4,"id":"0x1100","codec_type":"audio","codec_name":"truehd","channels":8,"tags":{"language":"wrong"}},
 {"index":7,"id":"0x1100","codec_type":"audio","codec_name":"ac3","channels":6},
 {"index":8,"id":"4353","codec_type":"subtitle","codec_name":"hdmv_pgs_subtitle"},
 {"index":9,"id":"0x1102","codec_type":"audio","codec_name":"aac","channels":2},
 {"index":10,"id":"garbage","codec_type":"audio","codec_name":"aac","tags":{"language":"eng"}}
 ],"chapters":[{"start_time":"10","end_time":"20","tags":{"title":"clip chapter"}}]}`)
	facts, err := decodeDiscMedia(data, title)
	if err != nil {
		t.Fatal(err)
	}
	if facts.Container != "mpegts" || facts.Duration != 72 || len(facts.Streams) != 6 {
		t.Fatalf("selected title facts %+v", facts)
	}
	if want := []Chapter{{"Chapter 1", 0, 36}, {"Chapter 2", 36, 72}}; !reflect.DeepEqual(facts.Chapters, want) {
		t.Fatal(facts.Chapters)
	}
	for i, want := range []string{"", "jpn", "jpn", "eng", "", ""} {
		if facts.Streams[i].Language != want {
			t.Fatalf("stream %d language %q want %q", i, facts.Streams[i].Language, want)
		}
	}
	if facts.Streams[1].Codec != "truehd" || facts.Streams[2].Codec != "ac3" || facts.Streams[1].Index != 4 || facts.Streams[2].Index != 7 {
		t.Fatal("streams collapsed", facts.Streams)
	}
	if _, err = decodeDiscMedia([]byte(`{"streams":[{"codec_type":"video","codec_name":"h264"}]}`), title); err == nil {
		t.Fatal("unindexed streams accepted")
	}
}

func TestSelectedDiscTitleActualMediaProbe(t *testing.T) {
	binary, helper := os.Getenv("PORTICO_DISC_TEST_FFPROBE"), os.Getenv("PORTICO_DISC_TEST_BINARY")
	if binary == "" || helper == "" {
		t.Skip("set explicit same-bundle ffprobe and disc helper for actual selected-title probing")
	}
	if !filepath.IsAbs(binary) || filepath.Dir(binary) != filepath.Dir(helper) {
		t.Fatal("explicit same-bundle tools required")
	}
	root, err := filepath.Abs("../../../fixtures/discs")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var inventory struct {
		Fixtures []struct {
			ID, Path string
			Expected json.RawMessage
		}
	}
	if err = json.Unmarshal(raw, &inventory); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range inventory.Fixtures {
		t.Run(fixture.ID, func(t *testing.T) {
			path := filepath.Join(root, fixture.Path)
			structure, err := (disc.Tool{Binary: helper}).ProbeLocal(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			for _, title := range structure.OrderedTitles() {
				t.Run(title.Key(structure.Kind), func(t *testing.T) {
					checks := 0
					probe := Probe{Binary: binary, ReadGuard: func(context.Context, string) error { checks++; return nil }}
					facts, err := probe.InspectDiscScan(context.Background(), path, structure, title)
					if err != nil {
						t.Fatal(err)
					}
					if checks != 2 || facts.Duration != float64(title.DurationMs)/1000 || len(facts.Chapters) != len(title.Chapters) || facts.VideoCodec == "" || len(facts.Streams) == 0 || facts.EmbeddedAudio != nil {
						t.Fatalf("selected title %+v checks=%d", facts, checks)
					}
					for i, c := range facts.Chapters {
						if c.Start != float64(title.Chapters[i].StartMs)/1000 {
							t.Fatal("chapter order", facts.Chapters)
						}
					}
					denied := errors.New("source revision changed")
					probe.ReadGuard = func(context.Context, string) error { return denied }
					if _, err = probe.InspectDiscScan(context.Background(), path, structure, title); !errors.Is(err, denied) {
						t.Fatal("revoked source", err)
					}
					forged := title
					forged.DurationMs++
					if _, err = probe.InspectDiscScan(context.Background(), path, structure, forged); !errors.Is(err, disc.ErrProtocol) {
						t.Fatal("forged title", err)
					}
				})
			}
		})
	}
}
