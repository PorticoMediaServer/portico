package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

func storeMusicDepthFixture(t *testing.T, c *catalogtest.Catalog, s *Service, root, path string, facts assets.Facts) (string, string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	var token string
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var err error
		_, token, err = compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Container: facts.Container, AudioCodec: facts.AudioCodec, Duration: facts.Duration})
		if err != nil {
			return err
		}
		return s.commitAudio(ctx, tx, "music", "music", root, token, path, facts)
	})
	c.Drain()
	var item string
	if err = c.DB.QueryRow(`SELECT pid(e.public_id) FROM catalog_entities e JOIN catalog_asset_links l ON l.entity_id=e.id JOIN catalog_assets a ON a.id=l.asset_id WHERE a.token=? AND e.kind=7 AND e.retired=0`, token).Scan(&item); err != nil {
		t.Fatal(err)
	}
	return item, token
}

func assertMusicDepthReaders(t *testing.T, s *Service, item string, depth int) {
	t.Helper()
	single, err := s.musicFacts(item)
	if err != nil || single == nil || single.BitDepth != depth {
		t.Fatal("single music facts", single, err)
	}
	items := map[string]*Item{item: {ID: item, Kind: "song"}}
	if err := s.musicFactsPage(items); err != nil {
		t.Fatal(err)
	}
	page := items[item].AudioFacts
	if page == nil || !reflect.DeepEqual(page, single) {
		t.Fatal("page music facts differ", page, single)
	}
	raw, err := json.Marshal(single)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if depth == 0 {
		if _, exists := fields["bitDepth"]; exists {
			t.Fatal("unknown audio depth was invented", string(raw))
		}
	} else if fields["bitDepth"] != float64(depth) {
		t.Fatal("known audio depth missing", string(raw))
	}
}

func TestMusicTechnicalFactsUseAudioDepthAndOmitUnknown(t *testing.T) {
	for _, tc := range []struct {
		name         string
		audio, video int
	}{{"audio24", 24, 0}, {"audio32", 32, 8}, {"unknown", 0, 16}} {
		t.Run(tc.name, func(t *testing.T) {
			c := catalogtest.Open(t)
			s := New(c.DB)
			root := t.TempDir()
			c.Library("music", "Music", "music", root)
			path := filepath.Join(root, "source.flac")
			if err := os.WriteFile(path, []byte("structural catalogue fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			facts := assets.Facts{AudioCodec: "flac", Container: "flac", Duration: 1, Streams: []assets.Stream{{Type: "audio", Codec: "flac", Channels: 2, Detail: assets.StreamDetail{SampleRate: 96000, BitsPerSample: tc.audio, BitDepth: tc.video}}}}
			item, _ := storeMusicDepthFixture(t, c, s, root, path, facts)
			assertMusicDepthReaders(t, s, item, tc.audio)
		})
	}
}

func TestMusicProbedBitDepthReachesBrowseFacts(t *testing.T) {
	if tier := os.Getenv("PORTICO_TEST_TIER"); tier != "media" && tier != "all" {
		t.Skip("real media tier")
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal(err)
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Fatal(err)
	}
	c := catalogtest.Open(t)
	s := New(c.DB)
	root := t.TempDir()
	c.Library("music", "Music", "music", root)
	for _, depth := range []int{16, 24, 32} {
		t.Run(strconv.Itoa(depth), func(t *testing.T) {
			path := filepath.Join(root, "measured-"+strconv.Itoa(depth)+".flac")
			format := "s32"
			if depth == 16 {
				format = "s16"
			}
			command := exec.Command(ffmpeg, "-v", "error", "-nostdin", "-f", "lavfi", "-i", "anullsrc=r=96000:cl=stereo", "-t", "0.1", "-c:a", "flac", "-sample_fmt", format, "-bits_per_raw_sample", strconv.Itoa(depth), "-strict", "-2", path)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatal(err, string(output))
			}
			facts, err := (assets.Probe{Binary: ffprobe}).InspectScan(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			if len(facts.Streams) != 1 || facts.Streams[0].Detail.BitsPerSample != depth || facts.Streams[0].Detail.BitDepth != 0 {
				t.Fatal("actual audio probe boundary", facts.Streams)
			}
			item, token := storeMusicDepthFixture(t, c, s, root, path, facts)
			assertMusicDepthReaders(t, s, item, depth)
			c.Exec(`UPDATE catalog_assets SET modified_ns=modified_ns+1 WHERE token=?`, token)
			if stale, err := s.musicFacts(item); err != nil || stale != nil {
				t.Fatal("changed source retained stale facts", stale, err)
			}
			page := map[string]*Item{item: {ID: item, Kind: "song"}}
			if err := s.musicFactsPage(page); err != nil || page[item].AudioFacts != nil {
				t.Fatal("changed source retained stale page facts", page[item].AudioFacts, err)
			}
		})
	}
}
