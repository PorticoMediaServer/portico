package dlna

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server/internal/testtier"
)

// Procedural audio goes through the actual probe, catalogue resource lookup,
// DIDL serialization and original HTTP serving. No player/DAC claim follows.
func TestMusicOriginalDLNAUsesRealCAFAndWave64ContainerFacts(t *testing.T) {
	testtier.Media(t, "generates and probes bounded CAF/Wave64 originals")
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	for _, row := range []struct{ format, mime string }{{"caf", "audio/x-caf"}, {"w64", "application/octet-stream"}} {
		t.Run(row.format, func(t *testing.T) {
			f := newFixture(t)
			song := f.items["song1"]
			path := filepath.Join(t.TempDir(), "tone."+row.format)
			command := exec.Command(ffmpeg, "-nostdin", "-v", "error", "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo", "-t", "0.02", "-c:a", "pcm_s24le", "-f", row.format, path)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("fixture encode: %v %s", err, output)
			}
			facts, err := (assets.Probe{Binary: ffprobe, Supervisor: f.server.Storage.Supervisor}).InspectScan(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			if facts.Container != row.format || len(facts.Streams) != 1 || facts.Streams[0].Detail.SampleRate != 48000 || facts.Streams[0].Detail.BitsPerSample != 24 || facts.Streams[0].Channels != 2 {
				t.Fatalf("unexpected real fixture: container=%s streams=%d", facts.Container, len(facts.Streams))
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			f.c.Exec(`UPDATE catalog_assets SET path=?,size=?,modified_ns=?,container=?,audio_codec=?,video_codec='',duration=? WHERE token=?`, path, info.Size(), info.ModTime().UnixNano(), facts.Container, facts.AudioCodec, facts.Duration, song.Token)
			stream := facts.Streams[0]
			f.c.Exec(`INSERT INTO music_technical_facts(asset_id,size,modified_ns,codec,sample_rate,bit_depth,channels,lossless) VALUES(?,?,?,?,?,?,?,1)`, song.Token, info.Size(), info.ModTime().UnixNano(), stream.Codec, stream.Detail.SampleRate, stream.Detail.BitsPerSample, stream.Channels)
			got := f.browse("e"+song.Public, "BrowseMetadata", 0, 1)
			if len(got.objects) != 1 || len(got.objects[0].Res) != 1 {
				t.Fatal(got)
			}
			resource := got.objects[0].Res[0]
			if resource.Protocol != protocolInfo(row.mime) || resource.SampleRate != "48000" || resource.BitDepth != "24" || resource.Channels != "2" || resource.Duration != duration(facts.Duration) {
				t.Fatalf("DIDL disagrees with actual source: %+v", resource)
			}
			locator, err := url.Parse(resource.URL)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(locator.Path, "."+row.format) {
				t.Fatal("wrong original suffix", locator.Path)
			}
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			response := f.request("GET", locator.RequestURI(), "192.168.1.20", "")
			if response.Code != 200 || response.Header().Get("Content-Type") != row.mime || !bytes.Equal(response.Body.Bytes(), original) {
				t.Fatal("original serving changed the format/bytes", response.Code, response.Header())
			}
			head := f.request("HEAD", locator.RequestURI(), "192.168.1.20", "")
			if head.Code != 200 || head.Header().Get("Content-Type") != row.mime || head.Header().Get("Content-Length") != strconv.Itoa(len(original)) || head.Body.Len() != 0 {
				t.Fatal("HEAD disagrees with original", head.Code, head.Header())
			}
			protocols := f.soap(connectionManagerType, "GetProtocolInfo", "")
			if protocols.Code != 200 || !strings.Contains(protocols.Body.String(), protocolInfo(row.mime)) {
				t.Fatal("advertised protocols omit actual type", protocols.Code)
			}
			// A new asset revision must not inherit the previous measured res facts.
			f.c.Exec(`UPDATE catalog_assets SET modified_ns=modified_ns+1 WHERE token=?`, song.Token)
			changed := f.browse("e"+song.Public, "BrowseMetadata", 0, 1).objects[0].Res[0]
			if changed.SampleRate != "" || changed.BitDepth != "" || changed.Channels != "" {
				t.Fatal("stale source claims survived", changed)
			}
			if response := f.request("GET", locator.RequestURI(), "192.168.1.20", ""); response.Code != http.StatusConflict {
				t.Fatal("changed source was served", response.Code)
			}
		})
	}
}
