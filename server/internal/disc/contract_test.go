package disc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type fixture struct {
	ID, Path string
	Files    map[string]string
	Expected json.RawMessage
}

func discFixtures(t *testing.T) (string, []fixture) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "fixtures", "discs"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Schema   int
		Fixtures []fixture
	}
	if err = json.Unmarshal(raw, &m); err != nil || m.Schema != 1 || len(m.Fixtures) != 3 {
		t.Fatalf("fixture manifest: %v", err)
	}
	return root, m.Fixtures
}

func TestFixtureInventoryAndNavigation(t *testing.T) {
	root, fixtures := discFixtures(t)
	for _, f := range fixtures {
		t.Run(f.ID, func(t *testing.T) {
			if !filepath.IsLocal(f.Path) || len(f.Files) == 0 {
				t.Fatal("unbounded fixture path")
			}
			p, err := DecodeProbe(f.Expected)
			if err != nil {
				t.Fatal(err)
			}
			base := filepath.Join(root, f.Path)
			info, err := os.Lstat(base)
			if err != nil || info.Mode()&os.ModeSymlink != 0 {
				t.Fatal("invalid fixture root")
			}
			if info.IsDir() {
				count := 0
				err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, err error) error {
					if err != nil {
						return err
					}
					if entry.Type()&os.ModeSymlink != 0 {
						return errors.New("symlink fixture member")
					}
					if entry.IsDir() {
						return nil
					}
					relative, err := filepath.Rel(base, path)
					if err != nil {
						return err
					}
					if _, ok := f.Files[filepath.ToSlash(relative)]; !ok {
						return errors.New("unlisted fixture member")
					}
					count++
					return nil
				})
				if err != nil || count != len(f.Files) {
					t.Fatalf("fixture inventory is incomplete: %v", err)
				}
			}
			for member, want := range f.Files {
				if member != "." && !filepath.IsLocal(member) {
					t.Fatal("unbounded member")
				}
				path := base
				if member != "." {
					path = filepath.Join(base, member)
				}
				r, err := os.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				h := sha256.New()
				_, err = io.Copy(h, r)
				r.Close()
				if err != nil || hex.EncodeToString(h.Sum(nil)) != want {
					t.Fatalf("fixture bytes changed: %s", member)
				}
			}
			if p.Kind == "bluray" {
				if len(p.Titles) != 1 || len(p.Titles[0].Clips) != 2 || len(p.Titles[0].Chapters) != 2 {
					t.Fatal("fixture must exercise two-clip playlist and chapters")
				}
			} else if len(p.Titles) != 2 || p.Titles[0].DurationMs == p.Titles[1].DurationMs {
				t.Fatal("fixture must exercise two distinct DVD titles")
			}
		})
	}
}

func TestISOActualUDF250Descriptor(t *testing.T) {
	root, _ := discFixtures(t)
	f, err := os.Open(filepath.Join(root, "bluray-udf250.iso"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b := make([]byte, 2048)
	if _, err = f.ReadAt(b, 256*2048); err != nil || binary.LittleEndian.Uint16(b) != 2 {
		t.Fatal("missing UDF anchor")
	}
	length, start := binary.LittleEndian.Uint32(b[16:]), binary.LittleEndian.Uint32(b[20:])
	if length > 1<<20 || length%2048 != 0 {
		t.Fatal("unbounded descriptor sequence")
	}
	for i := uint32(0); i < length/2048; i++ {
		if _, err = f.ReadAt(b, int64(start+i)*2048); err != nil {
			t.Fatal(err)
		}
		if binary.LittleEndian.Uint16(b) != 6 {
			continue
		}
		if string(bytes.TrimRight(b[217:240], "\x00")) != "*OSTA UDF Compliant" || binary.LittleEndian.Uint16(b[240:]) != 0x0250 {
			t.Fatal("actual LVD is not UDF 2.50")
		}
		return
	}
	t.Fatal("no logical volume descriptor")
}

func TestProbeRejectsMalformedOrAmbiguousFacts(t *testing.T) {
	_, fixtures := discFixtures(t)
	var good map[string]any
	if err := json.Unmarshal(fixtures[0].Expected, &good); err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(map[string]any){
		"unknown-field":         func(m map[string]any) { m["url"] = "private" },
		"wrong-schema":          func(m map[string]any) { m["schema"] = 2 },
		"encrypted-with-titles": func(m map[string]any) { m["encryption"] = "aacs" },
		"null-titles":           func(m map[string]any) { m["titles"] = nil },
		"playlist-mismatch":     func(m map[string]any) { m["titles"].([]any)[0].(map[string]any)["playlistFile"] = "99999.mpls" },
		"clip-path-escape": func(m map[string]any) {
			m["titles"].([]any)[0].(map[string]any)["clips"].([]any)[0].(map[string]any)["file"] = "../00001.m2ts"
		},
		"unordered-chapters": func(m map[string]any) {
			m["titles"].([]any)[0].(map[string]any)["chapters"] = []any{map[string]any{"startMs": 1}, map[string]any{"startMs": 0}}
		},
		"unobserved-el": func(m map[string]any) {
			m["titles"].([]any)[0].(map[string]any)["streams"].([]any)[0].(map[string]any)["dolbyVisionEnhancementLayer"] = true
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			var m map[string]any
			_ = json.Unmarshal(fixtures[0].Expected, &m)
			mutate(m)
			raw, _ := json.Marshal(m)
			if _, err := DecodeProbe(raw); !errors.Is(err, ErrProtocol) {
				t.Fatalf("accepted invalid facts: %v", err)
			}
		})
	}
	for _, raw := range [][]byte{append(append([]byte{}, fixtures[0].Expected...), []byte("{}")...), bytes.Repeat([]byte(" "), MaxOutputBytes+1)} {
		if _, err := DecodeProbe(raw); !errors.Is(err, ErrProtocol) {
			t.Fatal("accepted trailing or oversized response")
		}
	}
	if _, err := DecodeProbe([]byte(`{"schema":1,"kind":"dvd","packaging":"iso","encryption":"css","titles":[]}`)); !errors.Is(err, ErrEncrypted) {
		t.Fatal("lost actual encryption classification")
	}
}

func TestStableTitlesChaptersPIDAndSeekAuthority(t *testing.T) {
	a, b := Title{DiscOrder: 0, Title: 1, DurationMs: 66000}, Title{DiscOrder: 1, Title: 2, DurationMs: 72000}
	p := Probe{Kind: "dvd", Titles: []Title{a, b}}
	if got := p.OrderedTitles(); got[0].Key(p.Kind) != "dvd:2" || got[1].Key(p.Kind) != "dvd:1" {
		t.Fatal("main-title order is not longest-first")
	}
	p.Titles[0].DurationMs = 72000
	if got := p.OrderedTitles(); got[0].DiscOrder != 0 {
		t.Fatal("equal duration changed disc order")
	}
	title := Title{DurationMs: 72000, Chapters: []Chapter{{0}, {36000}}, Streams: []Stream{{PID: 4352, Language: "eng"}, {PID: 4352, Language: "eng"}}}
	if title.LanguageForPID(4352) != "eng" {
		t.Fatal("shared TrueHD/AC3 PID lost language")
	}
	title.Streams[1].Language = "jpn"
	if title.LanguageForPID(4352) != "" {
		t.Fatal("contradictory clip languages were invented")
	}
	meta, err := title.ChapterMetadata()
	if err != nil || !strings.Contains(string(meta), "START=36000\nEND=72000") {
		t.Fatalf("chapters lost their timebase: %v", err)
	}
	seek := []byte(`{"schema":1,"playlist":1,"requestedMs":37500,"byteOffset":1346304,"titleTimeMs":37000}`)
	if _, err := DecodeSeek(seek, 1, 37500); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeSeek(seek, 2, 37500); err == nil {
		t.Fatal("different playlist accepted")
	}
	if _, err := DecodeSeek(seek, 1, 37000); err == nil {
		t.Fatal("different seek accepted")
	}
}

func TestHelperFixtureProbeAndCrossClipSeek(t *testing.T) {
	binaryPath := os.Getenv("PORTICO_DISC_TEST_BINARY")
	if binaryPath == "" {
		t.Skip("set PORTICO_DISC_TEST_BINARY to the compiled helper; no target acceptance inferred")
	}
	root, fixtures := discFixtures(t)
	tool := Tool{Binary: binaryPath}
	for _, f := range fixtures {
		t.Run(f.ID, func(t *testing.T) {
			got, err := tool.ProbeLocal(context.Background(), filepath.Join(root, f.Path))
			if err != nil {
				t.Fatal(err)
			}
			want, err := DecodeProbe(f.Expected)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("actual helper differs: got %+v want %+v, err %v", got, want, err)
			}
			if got.Kind == "bluray" {
				seek, err := tool.SeekLocal(context.Background(), filepath.Join(root, f.Path), 1, 37500)
				boundary := (got.Titles[0].Clips[0].OutTime90k - got.Titles[0].Clips[0].InTime90k) / 90
				if err != nil || seek.TitleTimeMs < boundary || 37500-seek.TitleTimeMs > 1000 {
					t.Fatalf("seek did not land across clip boundary within GOP: %+v %v", seek, err)
				}
				if _, err := tool.SeekLocal(context.Background(), filepath.Join(root, f.Path), 1, got.Titles[0].DurationMs+1); err == nil {
					t.Fatal("out-of-title seek silently clamped")
				}
			}
		})
	}
	cmd := exec.Command(binaryPath, "seek", filepath.Join(root, fixtures[0].Path), "../1", "0")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err == nil || out.Len() != 0 {
		t.Fatal("invalid arguments published JSON")
	}
}

func TestHelperScrambledPESRefusesPlayableTitles(t *testing.T) {
	binaryPath := os.Getenv("PORTICO_DISC_TEST_BINARY")
	if binaryPath == "" {
		t.Skip("compiled helper required")
	}
	root, _ := discFixtures(t)
	source := filepath.Join(root, "dvd-two-title-video-ts")
	dest := filepath.Join(t.TempDir(), "disc")
	if err := os.CopyFS(dest, os.DirFS(source)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dest, "VIDEO_TS", "VTS_01_1.VOB")
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	prefix := make([]byte, 1<<20)
	n, err := f.Read(prefix)
	if err != nil {
		f.Close()
		t.Fatal(err)
	}
	changed := false
	for i := 0; i+9 < n; i++ {
		if bytes.Equal(prefix[i:i+4], []byte{0, 0, 1, 0xe0}) && prefix[i+6]&0xc0 == 0x80 {
			prefix[i+6] |= 0x10
			if _, err = f.WriteAt(prefix[i+6:i+7], int64(i+6)); err != nil {
				f.Close()
				t.Fatal(err)
			}
			changed = true
			break
		}
	}
	f.Close()
	if !changed {
		t.Fatal("fixture has no video PES header")
	}
	// Synthetic scrambling flags qualify classification/refusal, not the
	// private real-CSS/decryption fixture or hardware acceptance checklist.
	p, err := (Tool{Binary: binaryPath}).ProbeLocal(context.Background(), dest)
	if !errors.Is(err, ErrEncrypted) || p.Encryption != "css" || len(p.Titles) != 0 {
		t.Fatalf("scrambled PES was playable: %+v %v", p, err)
	}
}

func TestDiscScopedFFmpegInputs(t *testing.T) {
	root, fixtures := discFixtures(t)
	for _, f := range fixtures {
		p, err := DecodeProbe(f.Expected)
		if err != nil {
			t.Fatal(err)
		}
		title := p.OrderedTitles()[0]
		var seek *Seek
		if p.Kind == "bluray" {
			seek = &Seek{Schema: 1, Playlist: 1, RequestedMs: 37500, ByteOffset: 1346304, TitleTimeMs: 37000}
		}
		in, err := p.FFmpegInput(filepath.Join(root, f.Path), title, 37500, seek)
		if err != nil {
			t.Fatal(err)
		}
		options := strings.Join(in.Options, " ")
		if strings.Contains(options, "http") || strings.Contains(options, "network") {
			t.Fatal("disc input widened network authority")
		}
		if p.Kind == "bluray" {
			if !strings.Contains(options, "-min_playlist_length 60 -playlist 1 -angle 0 -f mpegts") || !strings.Contains(options, "-skip_initial_bytes 1346304") || in.TimestampOffsetMs != 37000 {
				t.Fatalf("lost playlist/seek authority: %+v", in)
			}
		} else if !strings.Contains(options, "-protocol_whitelist file,pipe -format_whitelist dvdvideo,mpeg,ffmetadata") || !strings.Contains(options, "-f dvdvideo -title 1 -angle 1 -preindex 0 -ss 37.500") {
			t.Fatalf("lost DVD reader semantics: %+v", in)
		}
		if _, err = p.FFmpegInput("relative", title, 0, nil); err == nil {
			t.Fatal("relative input admitted")
		}
	}
}

// The main title is the longest title of at least a minute; ties keep disc
// order and shorter titles stay listed behind it (VIDEO §3 VS-4b).
func TestMainTitleIsTheLongestOfAtLeastAMinute(t *testing.T) {
	titles := []Title{
		{DiscOrder: 0, DurationMs: 900_000},
		{DiscOrder: 1, DurationMs: 7_200_000},
		{DiscOrder: 2, DurationMs: 7_200_000},
		{DiscOrder: 3, DurationMs: 60_000},
	}
	p := Probe{Schema: Schema, Kind: "bluray", Packaging: "iso", Encryption: "none", Titles: titles}
	main, ok := p.MainTitle()
	if !ok || main.DiscOrder != 1 || main.DurationMs != 7_200_000 {
		t.Fatalf("main title: %+v %v", main, ok)
	}
	if empty := (Probe{Schema: Schema, Kind: "dvd", Packaging: "iso", Encryption: "none"}); func() bool { _, ok := empty.MainTitle(); return ok }() {
		t.Fatal("an empty probe named a main title")
	}
}

// A folder disc's consumed-member manifest covers every published title, is
// rooted at BDMV/ or VIDEO_TS/, marks required members, and is bounded.
func TestConsumedMembersAreRelativeAndBounded(t *testing.T) {
	playlist := 1
	probe := Probe{Kind: "bluray", Packaging: "folder", Titles: []Title{
		{DiscOrder: 0, DurationMs: 120000, Playlist: &playlist, PlaylistFile: "00001.mpls", Clips: []Clip{{Sequence: 0, File: "00002.m2ts", InTime90k: 0, OutTime90k: 1}}},
		{DiscOrder: 1, DurationMs: 90000, Playlist: &playlist, PlaylistFile: "00001.mpls", Clips: []Clip{{Sequence: 0, File: "00002.m2ts", InTime90k: 0, OutTime90k: 1}}},
	}}
	members, err := ConsumedMembers(probe)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]DiscMember{}
	for _, member := range members {
		found[member.Path] = member
	}
	for _, dir := range []string{"BDMV", "BDMV/PLAYLIST", "BDMV/CLIPINF", "BDMV/STREAM"} {
		if member, ok := found[dir]; !ok || !member.Required || !member.Directory {
			t.Errorf("missing required directory %s in %+v", dir, members)
		}
	}
	for _, want := range []string{"BDMV/PLAYLIST/00001.mpls", "BDMV/STREAM/00002.m2ts", "BDMV/CLIPINF/00002.clpi", "BDMV/index.bdmv"} {
		member, ok := found[want]
		if !ok || !member.Required || member.Directory {
			t.Errorf("missing required file %s in %+v", want, members)
		}
	}
	if member, ok := found["BDMV/MovieObject.bdmv"]; !ok || member.Required {
		t.Errorf("MovieObject must be an optional member: %+v", members)
	}
	dvd := Probe{Kind: "dvd", Packaging: "folder", Titles: []Title{{DiscOrder: 0, DurationMs: 120000, Title: 1, VTS: 3, PGC: 1}}}
	members, err = ConsumedMembers(dvd)
	if err != nil {
		t.Fatal(err)
	}
	required := map[string]DiscMember{}
	for _, member := range members {
		required[member.Path] = member
	}
	if !required["VIDEO_TS"].Directory || !required["VIDEO_TS/VTS_03_0.IFO"].Required || !required["VIDEO_TS/VTS_03_1.VOB"].Required || required["VIDEO_TS/VTS_03_2.VOB"].Required {
		t.Fatal(members)
	}
	if _, err = ConsumedMembers(Probe{Kind: "bluray", Packaging: "iso"}); err == nil {
		t.Fatal("an image source was given folder members")
	}
	many := Probe{Kind: "bluray", Packaging: "folder"}
	for i := 0; i < MaxConsumedMembers; i++ {
		// Each title names its own playlist, stream and clip-info file. Repeating
		// one reference legitimately collapses to a single member, so only
		// distinct files can exercise the manifest's bound.
		number := i + 1
		many.Titles = append(many.Titles, Title{DiscOrder: i, DurationMs: 120000, Playlist: &number, PlaylistFile: fmt.Sprintf("%05d.mpls", number), Clips: []Clip{{Sequence: 0, File: fmt.Sprintf("%05d.m2ts", number), InTime90k: 0, OutTime90k: 1}}})
	}
	if _, err = ConsumedMembers(many); err == nil {
		t.Fatal("an unbounded disc was admitted")
	}
}
