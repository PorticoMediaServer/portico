package decoder

import (
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
)

func TestDecoderCommandRejectsUntrustedInput(t *testing.T) {
	token := strings.Repeat("a", 64)
	for _, input := range []string{
		"file:///private/source", "http://localhost:19502/input/" + token,
		"http://127.0.0.1:19503/input/" + token + "?next=private",
		"http://127.0.0.1:19502/input/" + token + "?",
		"http://user@127.0.0.1:19502/input/" + token,
		"http://127.0.0.1:019502/input/" + token,
		"http://127.0.0.1:19502/input/%61" + token[1:],
		"http://127.0.0.1:19502/input/short",
	} {
		if _, err := ProbeCommand("/private/tool/ffprobe", input, &EndpointReservation{endpoint: "127.0.0.1:19502"}); !errors.Is(err, ErrInvalidConfiguration) {
			t.Fatalf("accepted invalid input: %v", err)
		}
	}
	if _, err := ProbeCommand("ffprobe", "http://127.0.0.1:19502/input/"+token, nil); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatal(err)
	}
}

// realTool is an executable that exists, standing in for ffprobe: the media
// executor resolves and mounts the real file.
func realTool(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("true")
	if err != nil {
		t.Skip("no true(1) on this host")
	}
	if path, err = filepath.EvalSymlinks(path); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDecoderCommandIsConfinedAndFixed(t *testing.T) {
	tool := realTool(t)
	input := "http://127.0.0.1:19502/input/" + strings.Repeat("b", 64)
	cmd, err := ProbeCommand(tool, input, &EndpointReservation{endpoint: "127.0.0.1:19502"})
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(cmd.Args, " ")
	posture := mediaexec.Decide(tool)
	if runtime.GOOS == "linux" && bridgeSandbox() {
		// With a working bubblewrap, the job enters our trusted helper, whose
		// specification carries the tool and its arguments.
		if len(cmd.Args) != 3 || cmd.Args[1] != "--portico-decoder-linux" {
			t.Fatalf("Linux sandbox bypass: %q", cmd.Args)
		}
		executable, arguments, err := helperSpec(cmd.Args[2])
		if err != nil || executable != tool || arguments[len(arguments)-1] != input {
			t.Fatalf("helper specification: %s %q %v", executable, arguments, err)
		}
		args = strings.Join(arguments, " ")
	} else if cmd.Args[len(cmd.Args)-1] != input {
		t.Fatal("input changed")
	}
	if !strings.Contains(args, "-protocol_whitelist http,tcp") || !strings.Contains(args, "-format_whitelist mov,matroska,mp3,flac,ogg,wav,aac,mpegts,avi") {
		t.Fatal("decoder allowlist missing")
	}
	switch {
	case runtime.GOOS == "linux" && bridgeSandbox():
	case runtime.GOOS == "darwin" && posture.Sandboxed:
		if cmd.Path != "/usr/bin/sandbox-exec" || len(cmd.Args) < 5 || cmd.Args[1] != "-p" || cmd.Args[3] != tool {
			t.Fatal("sandbox bypass")
		}
		profile := cmd.Args[2]
		for _, rule := range []string{`(deny network*)`, `(remote tcp "localhost:19502")`, `(deny file-read-data)`, `(deny file-write*)`} {
			if !strings.Contains(profile, rule) {
				t.Fatalf("missing confinement rule %s", rule)
			}
		}
	case posture.Sandboxed:
		if !strings.Contains(args, "bwrap") {
			t.Fatalf("sandboxed posture, unsandboxed command: %q", cmd.Args)
		}
	default:
		// No sandbox here: the baseline runs the tool itself, and the posture
		// says why (D-MEDIA-6).
		if cmd.Args[0] != tool || posture.Reason == "" {
			t.Fatalf("baseline: %q %+v", cmd.Args, posture)
		}
	}
}

func TestProbeAcceptsResolverLibraryBudgetWithoutWideningInputAuthority(t *testing.T) {
	libraries := make([]string, maxLibraryFiles)
	for i := range libraries {
		libraries[i] = "/private/trusted/runtime.so"
	}
	input := "http://127.0.0.1:19502/input/" + strings.Repeat("c", 64)
	reservation := &EndpointReservation{endpoint: "127.0.0.1:19502"}
	tool := realTool(t)
	_, err := ProbeCommand(tool, input, reservation, libraries...)
	if errors.Is(err, ErrInvalidConfiguration) {
		t.Fatal("resolved library budget rejected", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	libraries = append(libraries, "/private/trusted/extra.so")
	if _, err = ProbeCommand(tool, input, reservation, libraries...); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatal("unbounded dependencies accepted", err)
	}
}

func TestBridgeHardwareJobRetainsDeviceRequirement(t *testing.T) {
	job := decoderJob{executable: "/usr/bin/ffmpeg", endpoint: "127.0.0.1:1234", hardware: true}.mediaJob()
	if !job.Hardware {
		t.Fatal("bridge executor dropped the graph device requirement")
	}
	job = decoderJob{executable: "/usr/bin/ffmpeg", endpoint: "127.0.0.1:1234"}.mediaJob()
	if job.Hardware {
		t.Fatal("software bridge job gained hardware access")
	}
}
