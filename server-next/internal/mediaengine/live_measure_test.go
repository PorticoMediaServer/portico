package mediaengine

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/live"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
)

func TestLiveMeasurementKeepsCollectingWhenEarlyVideoSizeIsMissing(t *testing.T) {
	measureLiveSample(t, "size")
}

func TestLiveMeasurementKeepsTheFullCadenceWhenTheEarlyRateAndFieldOrderChange(t *testing.T) {
	facts := measureLiveSample(t, "cadence")
	video := facts.Streams[0].Detail
	if video.FrameRate != 59.94 || !video.Interlaced {
		t.Fatalf("cached the early progressive 29.97 fps sample instead of the full interlaced 59.94 fps sample: %+v", video)
	}
}

func measureLiveSample(t *testing.T, sample string) mediafacts.Facts {
	t.Helper()
	dir := t.TempDir()
	probe := filepath.Join(dir, "probe")
	// Early codecs alone do not establish the size or cadence. The full sample
	// supplies those facts; record what was actually probed.
	script := `#!/bin/sh
for file do :; done
size=$(wc -c < "$file")
printf '%s\n' "$size" >> "$(dirname "$0")/sizes"
height=0
average=25/1
declared=25/1
field=progressive
if [ MODE = cadence ]; then height=360; average=30000/1001; declared=60000/1001; fi
if [ "$size" -ge 3145728 ]; then
 height=360
 if [ MODE = cadence ]; then average=60000/1001; field=tt; fi
fi
printf '{"format":{"format_name":"mpegts","duration":"8"},"streams":[
{"index":0,"codec_type":"video","codec_name":"h264","profile":"High",
"level":30,"width":640,"height":%s,"pix_fmt":"yuv420p",
"avg_frame_rate":"%s","r_frame_rate":"%s","field_order":"%s"},
{"index":1,"codec_type":"audio","codec_name":"aac","profile":"LC","channels":2,"channel_layout":"stereo","sample_rate":"48000"}]}' "$height" "$average" "$declared" "$field"
`
	if err := os.WriteFile(probe, []byte(strings.ReplaceAll(script, "MODE", sample)), 0o700); err != nil {
		t.Fatal(err)
	}
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		packet := make([]byte, 188*256)
		for i := 0; i < len(packet); i += 188 {
			packet[i] = 0x47
		}
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-tick.C:
				if _, err := w.Write(packet); err != nil {
					return
				}
				w.(http.Flusher).Flush()
			}
		}
	}))
	defer source.Close()
	tuners := live.NewTuners(filepath.Join(dir, "tuned"), source.Client(), slog.New(slog.NewTextHandler(io.Discard, nil)), time.Now, func() live.Tuning { return live.DefaultTuning })
	defer tuners.Close()
	input, err := tuners.Tune(1, 1, 1, source.URL, &live.Use{Name: "Viewer"})
	if err != nil {
		t.Fatal(err)
	}
	engine := &Engine{folder: dir, probe: mediafacts.Probe{Binary: probe}}
	facts, err := engine.measureLive(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if facts.Width != 640 || facts.Height != 360 {
		t.Fatalf("the incomplete early measurement was used: %+v", facts)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "sizes"))
	if err != nil {
		t.Fatal(err)
	}
	sizes := strings.Fields(string(raw))
	if len(sizes) != 2 {
		t.Fatalf("wanted an early and full probe, got %q", raw)
	}
	early, err := strconv.Atoi(sizes[0])
	if err != nil || early <= 0 || early > 256<<10 || sizes[1] != fmt.Sprint(liveMeasure) {
		t.Fatalf("did not keep the existing full measurement: %q", raw)
	}
	return facts
}
