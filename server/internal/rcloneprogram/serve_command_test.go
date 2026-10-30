package rcloneprogram

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func TestPrepareServeRequiresOutboundEnforcementAndRemovesTransientConfig(t *testing.T) {
	spec := ServeCommandSpec{SourceID: "fixture", SourceGeneration: "1", PrivateDirectory: t.TempDir(), Configuration: []byte("[media]\ntype = local\n"), Remote: "media:", Port: 12345, Username: "private", Password: "secret", Connections: 2, RequestsPerSecond: 2}
	if _, err := PrepareServe(context.Background(), Program{Path: "/unused/rclone", Included: true}, spec); err == nil {
		t.Fatal("missing enforcing boundary accepted")
	}
	enforced := false
	spec.Enforce = func(cmd *exec.Cmd) error {
		enforced = true
		if runtime.GOOS != "windows" && cmd.SysProcAttr == nil {
			t.Fatal("child lifecycle not configured")
		}
		if strings.Contains(strings.Join(cmd.Args, " "), "secret") {
			t.Fatal("credential in argv")
		}
		return nil
	}
	p, err := PrepareServe(context.Background(), Program{Path: "/unused/rclone", Included: true}, spec)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Loaded()
	if !strings.Contains(strings.Join(p.Command.Args, " "), "--poll-interval=0") {
		t.Fatal("autonomous polling can bypass source operation admission")
	}
	if !enforced {
		t.Fatal("egress boundary not installed")
	}
	private := p.private
	info, err := os.Stat(private + "/rclone.conf")
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	if err = p.Loaded(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(private); !os.IsNotExist(err) {
		t.Fatalf("transient config survived: %v", err)
	}
	if _, err = os.Stat(spec.PrivateDirectory); err != nil {
		t.Fatalf("removed retained parent: %v", err)
	}
	if p.Command.Process != nil {
		t.Fatal("preparation launched a process")
	}
}
