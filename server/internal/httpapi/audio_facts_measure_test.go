package httpapi

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/subtitlevideo"
	"github.com/PorticoMediaServer/portico/server/internal/testtier"
)

// NEW-29 (demo and runner, 24 Sep): every audio render measurement recorded
// "unreadable", so gapless and crossfade were never planned. This measures a
// real tagged FLAC, AAC and MP3 song of a music library through the server's
// own path: subtitlevideo.MeasureAudio (selection, observed read, private
// bridge, confined ffprobe, audiofacts.Parse, validation).
func TestAudioFactsMeasureRealSongsThroughTheServerPath(t *testing.T) {
	ffmpeg, ffprobe := requireAudioMeasureTools(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	music := filepath.Join(root, "music")
	if err = os.MkdirAll(filepath.Join(music, "Artist", "Album"), 0o700); err != nil {
		t.Fatal(err)
	}
	fixtures := catalogtest.New(t, db)
	library := fixtures.Library("music", "Music", "music", music)
	artist := fixtures.Artist(library, "Artist")
	album := fixtures.Album(artist, "Album", 2026)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	rt, err := subtitlevideo.New(subtitlevideo.Options{DB: db, Storage: storage.New(binary), Directory: filepath.Join(root, "subtitle-video"), FFmpeg: ffmpeg, FFprobe: ffprobe})
	if err != nil {
		t.Fatal(err)
	}
	for n, format := range []struct{ name, codec, container string }{{"01 Flac.flac", "flac", "flac"}, {"02 Aac.m4a", "aac", "mov"}, {"03 Mp3.mp3", "libmp3lame", "mp3"}} {
		t.Run(format.name, func(t *testing.T) {
			path := filepath.Join(music, "Artist", "Album", format.name)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=441:sample_rate=44100:duration=5", "-metadata", "artist=Artist", "-metadata", "album=Album", "-c:a", format.codec, path).CombinedOutput(); err != nil {
				t.Skipf("encode: %v %s", err, out)
			}
			item := fixtures.Song(album, n+1, path, format.name)
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			fixtures.Write(func(ctx context.Context, tx *sql.Tx) error {
				asset, token, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Container: format.container, AudioCodec: format.codec, Duration: 5})
				if err != nil {
					return err
				}
				if err = compactcatalog.LinkAssetTx(ctx, tx, item.ID, asset, compactcatalog.Link{}); err != nil {
					return err
				}
				item.Asset, item.Token = asset, token
				return nil
			})
			fixtures.Drain()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			facts, err := rt.MeasureAudio(ctx, item.Public, item.Token)
			if err != nil {
				t.Fatalf("MeasureAudio(%s): %v", format.name, err)
			}
			if facts.SampleRate != 44100 || facts.Channels != 1 {
				t.Fatalf("%s facts: %+v", format.name, facts)
			}
		})
	}
}

func requireAudioMeasureTools(t *testing.T) (string, string) {
	t.Helper()
	testtier.Media(t, "real FFmpeg audio measurement")
	ffmpeg, ffmpegErr := exec.LookPath("ffmpeg")
	ffprobe, ffprobeErr := exec.LookPath("ffprobe")
	if ffmpegErr != nil || ffprobeErr != nil {
		t.Skip("audio measurement needs FFmpeg")
	}
	return ffmpeg, ffprobe
}
