package decoder

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestProbeQueryRejectsUntrustedArguments(t *testing.T) {
	input := "http://127.0.0.1:19502/input/" + strings.Repeat("a", 64)
	valid := []string{"-show_entries", "packet=pts_time,flags", "-of", "csv=p=0"}
	for name, query := range map[string][]string{
		"empty":                nil,
		"whole file packets":   valid,
		"missing value":        {"-show_entries"},
		"protocols":            append(append([]string{}, valid...), "-protocol_whitelist", "file,http,tcp"),
		"formats":              {"-format_whitelist", "concat"},
		"input":                {"-i", "file:///private/input"},
		"output":               append(append([]string{}, valid...), "/private/output"),
		"output option":        {"-o", "/private/output"},
		"option as value":      {"-show_entries", "-i", "-of", "csv=p=0"},
		"writer options":       {"-show_entries", "format=start_time", "-of", "csv=p=0:s=private"},
		"other streams":        {"-select_streams", "a:0"},
		"all packets interval": append(append([]string{}, valid...), "-read_intervals", "0%"),
		"unbounded packets":    append(append([]string{}, valid...), "-read_intervals", "6%+#2"),
		"invalid timestamp":    append(append([]string{}, valid...), "-read_intervals", "NaN%+#1"),
		"too many intervals":   append(append([]string{}, valid...), "-read_intervals", strings.Repeat("6%+#1,", 1000)+"6%+#1"),
		"duplicate option":     append(append([]string{}, valid...), "-of", "csv=p=0"),
	} {
		t.Run(name, func(t *testing.T) {
			res := &EndpointReservation{endpoint: "127.0.0.1:19502"}
			if _, err := ProbeQueryCommand("/private/ffprobe", input, res, query); !errors.Is(err, ErrInvalidConfiguration) {
				t.Fatalf("accepted %q: %v", query, err)
			}
			if _, err := RunProbeQuery(context.Background(), nil, "query", "/private/ffprobe", input, res, query); !errors.Is(err, ErrInvalidConfiguration) {
				t.Fatalf("runner accepted %q: %v", query, err)
			}
			if res.matches("127.0.0.1:19502") {
				t.Fatal("runner leaked refused reservation")
			}
		})
	}
}

func TestProbeQueryCommandIsConfinedAndFixed(t *testing.T) {
	tool := realTool(t)
	input := "http://127.0.0.1:19502/input/" + strings.Repeat("b", 64)
	for _, query := range [][]string{
		{"-show_entries", "format=start_time", "-of", "csv=p=0"},
		{"-select_streams", "v:0", "-show_entries", "packet=pts_time,flags", "-of", "csv=p=0", "-read_intervals", "-0.021000%+#1,6.000000%+#1,12.000000%+#1"},
	} {
		res := &EndpointReservation{endpoint: "127.0.0.1:19502"}
		cmd, err := ProbeQueryCommand(tool, input, res, query)
		if err != nil {
			t.Fatal(err)
		}
		args := cmd.Args[1:]
		if runtime.GOOS == "linux" && bridgeSandbox() {
			executable, arguments, err := helperSpec(cmd.Args[2])
			if err != nil || executable != tool {
				t.Fatalf("helper: %q %v", cmd.Args, err)
			}
			args = arguments
		} else if runtime.GOOS == "darwin" && cmd.Path == "/usr/bin/sandbox-exec" {
			args = cmd.Args[4:]
		} else {
			// Baseline bridge execution may still use the filesystem sandbox.
			// Its trusted tool follows bubblewrap's option terminator.
			for i := 0; i+1 < len(cmd.Args); i++ {
				if cmd.Args[i] == "--" && cmd.Args[i+1] == tool {
					args = cmd.Args[i+2:]
					break
				}
			}
		}
		want := []string{"-v", "error", "-threads", "1", "-protocol_whitelist", "http,tcp", "-format_whitelist", "mov,matroska,mp3,flac,ogg,wav,aac,mpegts,avi", "-probesize", "8388608", "-analyzeduration", "10000000"}
		want = append(want, query...)
		want = append(want, input)
		if !reflect.DeepEqual(args, want) {
			t.Fatalf("arguments %q, want %q", args, want)
		}
		if _, err := ProbeQueryCommand(tool, input, &EndpointReservation{endpoint: "127.0.0.1:19503"}, query); !errors.Is(err, ErrInvalidConfiguration) {
			t.Fatal("accepted wrong endpoint", err)
		}
	}
}
