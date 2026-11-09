//go:build linux

package decoder

import (
	"strings"
	"testing"
)

func TestBridgeSandboxUsesHardwareGrantsOnlyWhenRequested(t *testing.T) {
	for _, hardware := range []bool{false, true} {
		spec := linuxSpec{Executable: "/bin/true", Endpoint: "127.0.0.1:1234", Hardware: hardware}
		encoded := encodeLinux(spec)
		decoded, err := decodeLinux(encoded)
		if err != nil || decoded.Hardware != hardware {
			t.Fatal(decoded, err)
		}
		args, err := sandboxArgs(decoded, "", 0)
		if err != nil {
			t.Fatal(err)
		}
		text := strings.Join(args, " ")
		if strings.Contains(text, "--dev-bind-try /dev/dri /dev/dri") != hardware {
			t.Fatal(text)
		}
		if !strings.Contains(text, "--unshare-net") || !strings.Contains(text, "--remount-ro /") {
			t.Fatal("confinement lost", text)
		}
	}
}

func TestBridgeHardwareEnvironmentRejectsUnrelatedVariables(t *testing.T) {
	spec := linuxSpec{Executable: "/bin/true", Endpoint: "127.0.0.1:1234", Hardware: true, HardwareEnvironment: [][2]string{{"LIBVA_DRIVER_NAME", "iHD"}}}
	if _, err := decodeLinux(encodeLinux(spec)); err != nil {
		t.Fatal(err)
	}
	spec.HardwareEnvironment = [][2]string{{"SERVER_SECRET", "private"}}
	if _, err := decodeLinux(encodeLinux(spec)); err == nil {
		t.Fatal("unrelated environment variable admitted")
	}
	spec.Hardware = false
	spec.HardwareEnvironment = [][2]string{{"LIBVA_DRIVER_NAME", "iHD"}}
	if _, err := decodeLinux(encodeLinux(spec)); err == nil {
		t.Fatal("software job admitted hardware environment")
	}
}
