package catalog

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// The real Basic probe's header timeline must reach stored catalogue facts;
// a decoder-only frame-count test cannot establish this path.
func TestMusicMusepackBasicTimelineReachesStoredFacts(t *testing.T) {
	probe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("actual ffprobe unavailable")
	}
	c := catalogtest.Open(t)
	s := New(c.DB)
	root := t.TempDir()
	c.Library("music", "Music", "music", root)
	for _, tc := range []struct{ name, codec string }{{"tone-sv7.mpc", "mpc7"}, {"tone-sv8.mpc", "mpc8"}} {
		t.Run(tc.codec, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("../decoder/testdata/music-formats-r29", tc.name))
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, tc.name)
			if err = os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			facts, err := (assets.Probe{Binary: probe}).InspectScan(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			var token string
			c.Write(func(ctx context.Context, tx *sql.Tx) error {
				_, token, err = compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Container: facts.Container, AudioCodec: facts.AudioCodec, Duration: facts.Duration})
				if err != nil {
					return err
				}
				return s.commitAudio(ctx, tx, "music", "music", root, token, path, facts)
			})
			c.Drain()
			var duration float64
			var codec string
			var rate, channels int
			err = c.DB.QueryRow(`SELECT a.duration,a.audio_codec,f.sample_rate,f.channels FROM catalog_assets a JOIN music_technical_facts f ON f.asset_id=a.token AND f.size=a.size AND f.modified_ns=a.modified_ns WHERE a.token=?`, token).Scan(&duration, &codec, &rate, &channels)
			if err != nil || duration != .5 || codec != tc.codec || rate != 44100 || channels != 2 {
				t.Fatal(duration, codec, rate, channels, err)
			}
		})
	}
}
