package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/audiofacts"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
	"github.com/PorticoMediaServer/portico/server/internal/subtitlevideo"
)

func musicPrefetchLimit(t *testing.T, f *v1Fixture, url string, limit int64) string {
	t.Helper()
	grant := strings.Split(url, "/")[3]
	p, sid, err := f.v1.Playback.AudioPlanByGrant(context.Background(), grant)
	if err != nil {
		t.Fatal(err)
	}
	p.PrefetchBytes = limit
	raw, _ := json.Marshal(p)
	if _, err = f.db.Exec(`UPDATE playback_audio_plans SET plan_json=? WHERE session_id=?`, string(raw), sid); err != nil {
		t.Fatal(err)
	}
	return sid
}

func privateAudioHTTP(t *testing.T, f *v1Fixture, url string) (*httptest.Server, <-chan struct{}) {
	t.Helper()
	done := make(chan struct{}, 8)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == url {
			defer func() { done <- struct{}{} }()
		}
		f.handler.ServeHTTP(w, r)
	}))
	t.Cleanup(s.Close)
	return s, done
}

func openPrivateBody(t *testing.T, s *httptest.Server, url, rangeHeader string) (*http.Response, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	r, err := http.NewRequestWithContext(ctx, "GET", s.URL+url, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if rangeHeader != "" {
		r.Header.Set("Range", rangeHeader)
	}
	response, err := s.Client().Do(r)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); response.Body.Close() })
	if response.StatusCode != 200 && response.StatusCode != 206 {
		t.Fatal(response.Status)
	}
	return response, cancel
}

// Private reads are ordinary bounded responses. No request waits for commit.
func TestMusicPrivateDirectBoundedRangesAndCommitR36(t *testing.T) {
	f, _ := renderingFixture(t, 2)
	q := playingQueue(t, f, "music-private-queue-01")
	p := prepareNext(t, f, q, "music-private-prep-01")
	url := p.Presentation.AudioRender.URL
	musicPrefetchLimit(t, f, url, 4)
	if w := f.raw("HEAD", url, "", nil, nil); w.Code != 200 || w.Body.Len() != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, header := range []string{"", "bytes=0-"} {
		if w := f.raw("GET", url, "", map[string]string{"Range": header}, nil); w.Code != 403 {
			t.Fatal("unbounded private request", header, w.Code, w.Body.String())
		}
	}
	for _, header := range []string{"bytes=0-1", "bytes=-2"} {
		w := f.raw("GET", url, "", map[string]string{"Range": header}, nil)
		if w.Code != 206 || w.Body.Len() != 2 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w := f.raw("GET", url, "", map[string]string{"Range": "bytes=0-0"}, nil); w.Code != 403 {
		t.Fatal("reopen reset budget", w.Code, w.Body.String())
	}
	var ended int64
	if err := f.db.QueryRow(`SELECT ended_ms FROM playback_v1_sessions WHERE id=?`, q.Session.ID).Scan(&ended); err != nil || ended != 0 {
		t.Fatal(ended, err)
	}
	var commit playbackv1.QueueReply
	f.call("POST", "/v1/queues/"+q.Queue.ID+":commit-next", nil, map[string]any{"token": p.Token}, 200, &commit)
	if commit.Session.Presentation.AudioRender.URL != url {
		t.Fatal("commit changed media identity")
	}
	s, _ := privateAudioHTTP(t, f, url)
	r, _ := openPrivateBody(t, s, url, "")
	body, err := io.ReadAll(r.Body)
	if err != nil || string(body) != "0123456789" {
		t.Fatal(string(body), err)
	}
}

func TestMusicPrivateBudgetStopsWithoutWaitR36(t *testing.T) {
	for _, kind := range []string{"partial", "disconnect", "revoked"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "disconnect" {
				cancel()
			}
			var out bytes.Buffer
			b := budgetWriter{w: &out, ctx: ctx, check: func() error {
				if kind == "revoked" {
					return playback.ErrGrantEnded
				}
				return nil
			}, take: func(int64) int64 { return 4 }, refund: func(int64) {}}
			n, err := b.Write([]byte("0123456789"))
			if kind == "partial" {
				if n != 4 || err != errBudget || out.String() != "0123" {
					t.Fatal(n, err, out.String())
				}
			} else if n != 0 || err == nil || out.Len() != 0 {
				t.Fatal(n, err, out.String())
			}
		})
	}
}

func TestMusicPrivateReopenRefusesExpiredOrStoppedGrantR36(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, kind := range []string{"expired", "stopped"} {
		t.Run(kind, func(t *testing.T) {
			f, player := renderingFixture(t, 2)
			q := playingQueue(t, f, "music-revoke-queue-01")
			p := prepareNext(t, f, q, "music-revoke-prep-01")
			url := p.Presentation.AudioRender.URL
			sid := musicPrefetchLimit(t, f, url, 4)
			if w := f.raw("GET", url, "", map[string]string{"Range": "bytes=0-1"}, nil); w.Code != 206 || w.Body.Len() != 2 {
				t.Fatal(w.Code, w.Body.String())
			}
			if kind == "expired" {
				if _, err := f.db.Exec(`UPDATE playback_private_presentations SET expires_ms=? WHERE session_id=?`, time.Now().Add(-time.Second).UnixMilli(), sid); err != nil {
					t.Fatal(err)
				}
			} else if err := player.StopV1(context.Background(), sid); err != nil {
				t.Fatal(err)
			}
			if w := f.raw("GET", url, "", map[string]string{"Range": "bytes=0-1"}, nil); w.Code != 404 {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

// This runs the actual retained-input runtime and supervised encoder through
// HTTP. It qualifies server bytes and authority, never CAF fetch behaviour.
func TestMusicPrivateConvertedHTTPFormatGainFramesAfterCommitR36(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	probe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe unavailable")
	}
	for _, codec := range []string{"flac", "opus", "aac"} {
		t.Run(codec, func(t *testing.T) {
			f, player := renderingFixture(t, 2)
			runtime, err := subtitlevideo.New(subtitlevideo.Options{DB: f.db, Storage: f.deps.Storage, Directory: filepath.Join(f.root, "music-conversion"), FFmpeg: ff, FFprobe: probe})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { runtime.Close() })
			f.deps.AudioMedia = runtime
			f.handler = New(f.deps)
			for _, item := range f.records {
				var path string
				if err = f.db.QueryRow(`SELECT path FROM catalog_assets WHERE token=?`, item.Token).Scan(&path); err != nil {
					t.Fatal(err)
				}
				if out, e := exec.Command(ff, "-v", "error", "-y", "-f", "lavfi", "-i", "aevalsrc=0.4*sin(2*PI*997*t)|0.3*sin(2*PI*1301*t):s=44100:d=1", "-threads", "1", "-c:a", "aac", "-b:a", "192k", path).CombinedOutput(); e != nil {
					t.Fatal(e, string(out))
				}
				info, e := os.Stat(path)
				if e != nil {
					t.Fatal(e)
				}
				f.catalogTest.Write(func(ctx context.Context, tx *sql.Tx) error {
					_, _, e := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Container: "mp4", AudioCodec: "aac", Duration: 1})
					return e
				})
				f.catalogTest.Drain()
				facts, e := runtime.MeasureAudio(context.Background(), item.Public, item.Token)
				if e != nil {
					t.Fatal(e)
				}
				if e = audiofacts.Save(context.Background(), f.db, item.Token, facts); e != nil {
					t.Fatal(e)
				}
				if _, e = f.db.Exec(`INSERT INTO audio_tag_evidence(library_id,asset_id,field,source,value) VALUES(?,?,'replaygain_track_gain','embedded','-12 dB'),(?,?,'replaygain_track_peak','embedded','0.9')`, f.library, item.Token, f.library, item.Token); e != nil {
					t.Fatal(e)
				}
			}
			caps := playback.BaselineCapabilityDocument()
			caps.Client.Family = "cast"
			caps.Client.Form = "cast"
			caps.Client.Platform = "cast"
			caps.AudioDecode = nil
			caps.Engines.Native.Evidence = playback.EvidenceDeclared
			caps.Engines.Native.Audio = []playback.ClientAudioCodec{{Codec: "aac", MaxChannels: 2, MaxSampleRate: 48000, Evidence: playback.EvidenceDeclared}}
			containers := []string{"mp4", "aac"}
			audio := []string{"aac"}
			if codec != "aac" {
				caps.Engines.Native.Audio = append(caps.Engines.Native.Audio, playback.ClientAudioCodec{Codec: codec, MaxChannels: 2, MaxSampleRate: 96000, MaxBitDepth: 24, Evidence: playback.EvidenceDeclared})
				audio = append(audio, codec)
				if codec == "opus" {
					containers = append(containers, "ogg")
				} else {
					containers = append(containers, "flac")
				}
			}
			caps.Engines.Native.Transports = []playback.ClientTransport{{Transport: playback.TransportDirect, Containers: containers, Audio: audio}}
			var current playbackv1.CapabilitiesDocument
			f.call("GET", "/v1/me/devices/current/capabilities", nil, nil, 200, &current)
			f.call("PUT", "/v1/me/devices/current/capabilities", map[string]string{"If-Match": playbackv1.ETag(current.Revision)}, caps, 204, nil)
			player.ConfigureDelivery(nil, func(*sql.Tx, identity.Viewer, string) (playback.DeliveryPreferenceReader, error) {
				return operations.PreferenceValues{"music.audioNormalization": "track", "music.neverBoost": true}, nil
			}, nil, "")
			q := playingQueue(t, f, "music-converted-http-queue")
			p := prepareNext(t, f, q, "music-converted-http-prep")
			plan := p.Presentation.AudioRender
			if plan == nil || plan.Mode != "converted" || plan.Codec != codec || plan.Gain != nil || plan.LoudnessAppliedDB == nil || *plan.LoudnessAppliedDB != -12 || p.Presentation.URL != plan.URL || p.Presentation.Container != plan.Container {
				t.Fatalf("not a baked receiver plan %+v %+v", p.Presentation, plan)
			}
			musicPrefetchLimit(t, f, plan.URL, 256)
			// Ruling17.2: ordinary commit happens before the progressive GET.
			var commit playbackv1.QueueReply
			f.call("POST", "/v1/queues/"+q.Queue.ID+":commit-next", nil, map[string]any{"token": p.Token}, 200, &commit)
			if commit.Session.Presentation.URL != plan.URL {
				t.Fatal("commit changed URL")
			}
			s, done := privateAudioHTTP(t, f, plan.URL)
			response, _ := openPrivateBody(t, s, plan.URL, "")
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("encoder/handler did not retire")
			}

			wantMIME := map[string]string{"flac": "audio/flac", "opus": "audio/ogg", "aac": "audio/aac"}[codec]
			if response.Header.Get("Content-Type") != wantMIME || response.Header.Get("X-Audio-Frames") != strconv.FormatInt(plan.DurationFrames, 10) || response.Header.Get("X-Audio-Trim-Start") != strconv.FormatInt(plan.Trim.StartFrames, 10) || response.Header.Get("X-Audio-Trim-End") != strconv.FormatInt(plan.Trim.EndFrames, 10) {
				t.Fatal(response.Header, plan)
			}
			decode := exec.Command(ff, "-v", "error", "-flags2", "+skip_manual", "-i", "pipe:0", "-af", "atrim=start_sample="+strconv.FormatInt(plan.Trim.StartFrames, 10)+":end_sample="+strconv.FormatInt(plan.Trim.StartFrames+plan.DurationFrames, 10), "-c:a", "pcm_f32le", "-f", "f32le", "pipe:1")
			decode.Stdin = bytes.NewReader(body)
			pcm, e := decode.Output()
			if e != nil {
				t.Fatal(e)
			}
			if len(pcm) != int(plan.DurationFrames)*plan.Channels*4 {
				t.Fatal("wrong audible frame count", len(pcm), plan)
			}
			peak := float32(0)
			for i := 0; i < len(pcm); i += 4 {
				v := math.Float32frombits(binary.LittleEndian.Uint32(pcm[i : i+4]))
				if v < 0 {
					v = -v
				}
				if v > peak {
					peak = v
				}
			}
			if peak < 0.04 || peak > 0.14 {
				t.Fatal("server -12dB gain missing or applied twice", peak)
			}
			t.Logf("actual %s/%s %s, %dHz %dch, %d audible frames, %.5f peak; fresh progressive GET after commit", plan.Codec, plan.Container, wantMIME, plan.SampleRate, plan.Channels, plan.DurationFrames, peak)
		})
	}
}
