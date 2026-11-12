package scan_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
)

// An audiobook in three MP3 files, the second a variable bitrate with no header that says its
// length, and a book in one such file. FFmpeg estimates that file's length from its first
// frames, and is seconds out; the scan counts its frames. The book's page places each part
// after the time the parts before it take in packets, which is where a copied stream starts it.
// A library scanned before this is put right by its next scan, not by removing it.
func TestBookPartsAreMeasuredAndAnEstimatedMP3IsCounted(t *testing.T) {
	ffmpeg := decodertest.QualifiedFFmpeg(t)
	ffprobe := decodertest.QualifiedFFprobe(t)
	root := filepath.Join(t.TempDir(), "library")
	book := filepath.Join(root, "An Author", "A Book")
	if err := os.MkdirAll(book, 0o755); err != nil {
		t.Fatal(err)
	}
	encode := func(path string, args ...string) {
		t.Helper()
		if out, err := exec.Command(ffmpeg, append([]string{"-v", "error", "-y"}, append(args, path)...)...).CombinedOutput(); err != nil {
			decodertest.Unavailable(t, fmt.Sprintf("fixture encode unavailable: %v %s", err, out))
		}
	}
	// Quiet for its first seconds, then loud: the first frames' bitrate says little of the rest.
	speech := []string{"-f", "lavfi", "-i", "anoisesrc=d=60:c=pink:r=44100:a=0.3,volume='if(lt(t,3),0.01,1)':eval=frame",
		"-c:a", "libmp3lame", "-q:a", "4", "-write_xing", "0"}
	parts := []string{filepath.Join(book, "01.mp3"), filepath.Join(book, "02.mp3"), filepath.Join(book, "03.mp3")}
	encode(parts[0], "-f", "lavfi", "-i", "sine=frequency=300:sample_rate=44100:duration=20", "-c:a", "libmp3lame", "-b:a", "64k")
	encode(parts[1], speech...)
	encode(parts[2], "-f", "lavfi", "-i", "sine=frequency=600:sample_rate=44100:duration=20", "-c:a", "libmp3lame", "-b:a", "64k")
	loose := filepath.Join(root, "A Loose Book.mp3")
	encode(loose, speech...)

	counted := packetSeconds(t, ffprobe, parts[1])
	if estimate := formatSeconds(t, ffprobe, parts[1]); math.Abs(estimate-counted) < 1 {
		t.Fatalf("FFmpeg's estimate %.3f s is close to the counted %.3f s, so this would show nothing", estimate, counted)
	}
	lengths := []float64{packetSeconds(t, ffprobe, parts[0]), counted, packetSeconds(t, ffprobe, parts[2])}

	path := filepath.Join(t.TempDir(), "main.db")
	d, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := d.CreateLibrary(ctx, "Books", db.LibraryAudiobooks, "en", []string{root}); err != nil {
		t.Fatal(err)
	}
	viewers, err := seed.Profiles(ctx, d, 1)
	if err != nil {
		t.Fatal(err)
	}
	scanner := &scan.Scanner{DB: d, Prober: scan.FileProber{}}
	libraries, err := d.AllLibraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	run := func() {
		t.Helper()
		if _, err := scanner.Scan(ctx, libraries[0]); err != nil {
			t.Fatal(err)
		}
	}
	check := func(when string) {
		t.Helper()
		page, err := d.Items(ctx, viewers[0], db.ItemQuery{Library: libraries[0].PublicID, Kinds: []db.ItemKind{db.KindAudiobook}, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		found := 0
		for _, item := range page.Items {
			detail, err := d.Item(ctx, viewers[0], item.PublicID)
			if err != nil {
				t.Fatal(err)
			}
			v := detail.Versions[0]
			switch item.Title {
			case "A Loose Book":
				found++
				if ms := float64(v.DurationMS); math.Abs(ms-counted*1000) > 1 {
					t.Errorf("%s: the book in one file lasts %.0f ms, its frames %.0f ms", when, ms, counted*1000)
				}
			case "A Book":
				found++
				if len(v.Parts) != 3 {
					t.Fatalf("%s: %d parts", when, len(v.Parts))
				}
				if ms := float64(*v.Parts[1].DurationMS); math.Abs(ms-counted*1000) > 1 {
					t.Errorf("%s: part 2 lasts %.0f ms, its frames %.0f ms", when, ms, counted*1000)
				}
				at := 0.0
				for n, part := range v.Parts {
					if part.OffsetMS == nil {
						t.Errorf("%s: part %d has no place in the book", when, n+1)
					} else if math.Abs(float64(*part.OffsetMS)-at*1000) > 1 {
						t.Errorf("%s: part %d starts at %d ms, after the parts before it in packets at %.0f ms", when, n+1, *part.OffsetMS, at*1000)
					}
					at += lengths[n]
				}
			}
		}
		if found != 2 {
			t.Fatalf("%s: %d of the two books found", when, found)
		}
	}
	run()
	check("scanned")

	// As a server before this left them: FFmpeg's estimate stored, nothing measured, nothing
	// to say how they were read. The next scan puts them right.
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	estimate := formatSeconds(t, ffprobe, parts[1])
	if _, err := raw.ExecContext(ctx, `UPDATE files SET extra = json_remove(extra, '$.packets'),
		duration_ms = CASE WHEN name IN ('02.mp3', 'A Loose Book.mp3') THEN ? ELSE duration_ms END`, int64(math.Round(estimate*1000))); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `UPDATE media SET duration_ms = ? WHERE id IN (SELECT p.media_id FROM media_parts p JOIN files f ON f.id = p.file_id
		WHERE f.name = 'A Loose Book.mp3')`, int64(math.Round(estimate*1000))); err != nil {
		t.Fatal(err)
	}
	run()
	check("scanned again after a server that estimated")

	// Music files use the same packet measurements; an unchanged scan does not probe again.
	music, err := d.CreateLibrary(ctx, "Music", db.LibraryMusic, "en", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scanner.Scan(ctx, music); err != nil {
		t.Fatal(err)
	}
	var measured int
	if err := raw.QueryRowContext(ctx, `SELECT count(*) FROM files f JOIN directories dr ON dr.id = f.directory_id
  JOIN library_paths lp ON lp.id = dr.library_path_id WHERE lp.library_id = ? AND json_extract(f.extra, '$.packets.lengthUs') > 0`, music.ID).Scan(&measured); err != nil || measured != 4 {
		t.Fatalf("music packet measurements: %d, %v", measured, err)
	}
	if stats, err := scanner.Scan(ctx, music); err != nil || stats.Probed != 0 {
		t.Fatalf("unchanged music scan probed %d files: %v", stats.Probed, err)
	}
	// The remote prober runs over the isolated range bridge, including its whole-file
	// count when an MP3 has no length header.
	file, err := os.Open(loose)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	remote, err := (scan.RemoteProber{}).ProbeReader(ctx, file, info.Size())
	if err != nil || remote.Packets == nil {
		t.Fatalf("remote packet facts: %+v, %v", remote, err)
	}
	if math.Abs(float64(remote.DurationMS)-counted*1000) > 1 || math.Abs(float64(remote.Packets.LengthUS)-counted*1e6) > 1 {
		t.Fatalf("remote length %d ms, packets %d us, want %.6f seconds", remote.DurationMS, remote.Packets.LengthUS, counted)
	}
}

// packetSeconds is how long every packet of the file's sound takes, read in full.
func packetSeconds(t *testing.T, ffprobe, path string) float64 {
	t.Helper()
	out, err := exec.Command(ffprobe, "-v", "error", "-select_streams", "a:0", "-show_entries", "stream=time_base:packet=pts,duration", "-of", "json", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Streams []struct {
			TimeBase string `json:"time_base"`
		} `json:"streams"`
		Packets []struct{ PTS, Duration int64 } `json:"packets"`
	}
	if err := json.Unmarshal(out, &doc); err != nil || len(doc.Packets) == 0 {
		t.Fatalf("%s: %v", path, err)
	}
	var end int64
	for _, p := range doc.Packets {
		end = max(end, p.PTS+p.Duration)
	}
	base, _ := new(big.Rat).SetString(doc.Streams[0].TimeBase)
	seconds, _ := new(big.Rat).Mul(big.NewRat(end-doc.Packets[0].PTS, 1), base).Float64()
	return seconds
}

// formatSeconds is the length FFmpeg gives the file.
func formatSeconds(t *testing.T, ffprobe, path string) float64 {
	t.Helper()
	out, err := exec.Command(ffprobe, "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	seconds, err := strconv.ParseFloat(string(out[:len(out)-1]), 64)
	if err != nil {
		t.Fatal(err)
	}
	return seconds
}
