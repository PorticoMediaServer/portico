package decoder

import (
	"context"
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server-next/internal/processpool"
)

func TestAnalysisProbeClosedRecipes(t *testing.T) {
	input := "http://127.0.0.1:19502/input/" + strings.Repeat("b", 64)
	for _, tc := range []struct {
		kind     AnalysisProbeKind
		query    []string
		limit    int64
		deadline time.Duration
	}{
		{ProbeFrameCadence, []string{"-read_intervals", "%+#240", "-show_frames", "-show_entries", "frame=best_effort_timestamp_time,interlaced_frame,repeat_pict", "-of", "json"}, 4 << 20,
			12 * time.Second},
		{ProbeFrameHDR, []string{"-read_intervals", "%+#1", "-show_frames", "-show_entries", "frame=media_type,width,height,side_data_list", "-of", "json"}, 1 << 20, 8 * time.Second},
		{ProbePacketPeak, []string{"-show_packets", "-show_entries", "packet=dts_time,size", "-of", "csv=p=0"}, 32 << 20, 90 * time.Second},
	} {
		t.Run(string(tc.kind), func(t *testing.T) {
			recipe, err := analysisProbeRecipe(tc.kind)
			if err != nil || recipe.limit != tc.limit || recipe.deadline != tc.deadline {
				t.Fatalf("recipe %+v: %v", recipe, err)
			}
			want := []string{
				"-v",
				"error",
				"-threads",
				"1",
				"-protocol_whitelist",
				"http,tcp",
				"-format_whitelist",
				mediaexec.LocalInputFormats,
				"-probesize",
				"8388608",
				"-analyzeduration",
				"5000000",
				"-select_streams",
				"v:0",
			}
			want = append(want, tc.query...)
			want = append(want, input)
			if got := recipe.args(input); !reflect.DeepEqual(got, want) {
				t.Fatalf("args %q, want %q", got, want)
			}
			// Analysis recipes must never become an escape hatch in timeline queries.
			if _, err := ProbeQueryCommand(realTool(t), input, &EndpointReservation{endpoint: "127.0.0.1:19502"}, append([]string{"-select_streams", "v:0"}, tc.query...)); !errors.Is(
				err,
				ErrInvalidConfiguration,
			) {
				t.Fatalf("timeline accepted analysis recipe: %v", err)
			}
		})
	}
}

func TestAnalysisProbeKeepsAllOrdinaryHeaderDemuxers(t *testing.T) {
	recipe, err := analysisProbeRecipe(ProbeFrameCadence)
	if err != nil {
		t.Fatal(err)
	}
	args := recipe.args("http://127.0.0.1:19502/input/" + strings.Repeat("c", 64))
	for i, arg := range args {
		if arg == "-format_whitelist" {
			if args[i+1] != mediaexec.LocalInputFormats {
				t.Fatalf("frame pass rejects ordinary scanner formats: %s", args[i+1])
			}
			return
		}
	}
	t.Fatal("missing closed format whitelist")
}

func TestAnalysisProbeRetiresOnCancellation(t *testing.T) {
	v4, v6, res := runFixture(t)
	defer v4.Close()
	defer v6.Close()
	defer res.Close()
	endpoint := v4.Addr().String()
	cmd, err := confinedCommand(testExecutable(t), []string{"-test.run=^TestDecoderProcessHelper$", "--", "decoder-process-helper", "hold", endpoint}, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sup := &processpool.Supervisor{Limit: 1}
	done := make(chan error, 1)
	go func() {
		data, err := runAnalysisProbeOwned(ctx, sup, "fixture-retirement", cmd, endpoint, res, 4<<20)
		if data != nil {
			t.Error("returned incomplete evidence")
		}
		done <- err
	}()
	v4.SetDeadline(time.Now().Add(3 * time.Second))
	conn, err := v4.Accept()
	if err != nil {
		t.Fatal("child did not start", err)
	}
	conn.Close()
	v4.Close()
	v6.Close()
	if err := res.Close(); !errors.Is(err, ErrReservationInUse) {
		t.Fatal("released live endpoints", err)
	}
	if _, err := runAnalysisProbeOwned(ctx, sup, "competing", cmd, endpoint, res, 4<<20); !errors.Is(err, ErrReservationInUse) {
		t.Fatal("accepted overlapping owner", err)
	}
	for _, address := range []string{endpoint, v6.Addr().String()} {
		if l, err := net.Listen("tcp", address); err == nil {
			l.Close()
			t.Fatal("live endpoint rebound")
		}
	}
	cancel()
	select {
	case err = <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("child failed to retire")
	}
	if sup.Active() != 0 || res.matches(endpoint) {
		t.Fatal("returned before retirement")
	}
}

func TestAnalysisProbeDiscardsExcessOutputAndNoStart(t *testing.T) {
	for _, mode := range []string{"oversize", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			v4, v6, res := runFixture(t)
			defer v4.Close()
			defer v6.Close()
			defer res.Close()
			endpoint := v4.Addr().String()
			cmd, err := confinedCommand(testExecutable(t), []string{"-test.run=^TestDecoderProcessHelper$", "--", "decoder-process-helper", "oversize", endpoint}, endpoint)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if mode == "canceled" {
				cancel()
			}
			sup := &processpool.Supervisor{Limit: 1}
			data, err := runAnalysisProbeOwned(ctx, sup, "fixture-output", cmd, endpoint, res, 16)
			want := ErrProbeOutput
			if mode == "canceled" {
				want = context.Canceled
			}
			if !errors.Is(err, want) || data != nil {
				t.Fatalf("data=%q err=%v want=%v", data, err, want)
			}
			if sup.Active() != 0 || res.matches(endpoint) {
				t.Fatal("failed invocation retained authority")
			}
		})
	}
}

func TestAnalysisProbeRejectsInvalidRequests(t *testing.T) {
	input := "http://127.0.0.1:19502/input/" + strings.Repeat("a", 64)
	for _, kind := range []AnalysisProbeKind{"", "frames", "-show_frames", "packet=dts_time,size", "frame_hdr -i file:///private"} {
		res := &EndpointReservation{endpoint: "127.0.0.1:19502"}
		if _, err := RunAnalysisProbe(context.Background(), &processpool.Supervisor{Limit: 1}, "recipe", realTool(t), input, res, AnalysisProbeSpec{Kind: kind}); !errors.Is(err, ErrInvalidConfiguration) {
			t.Fatalf("accepted %q: %v", kind, err)
		}
		if res.matches("127.0.0.1:19502") {
			t.Fatal("refused recipe retained endpoints")
		}
	}
	for _, altered := range []string{input + "?next=private", "file:///private/input", strings.Replace(input, "19502", "19503", 1)} {
		res := &EndpointReservation{endpoint: "127.0.0.1:19502"}
		if _, err := RunAnalysisProbe(context.Background(), &processpool.Supervisor{Limit: 1}, "recipe", realTool(t), altered, res, AnalysisProbeSpec{Kind: ProbeFrameCadence}); !errors.Is(
			err,
			ErrInvalidConfiguration,
		) {
			t.Fatalf("accepted input %q: %v", altered, err)
		}
		if res.matches("127.0.0.1:19502") {
			t.Fatal("refused input retained endpoints")
		}
	}
}
