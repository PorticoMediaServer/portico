//go:build linux || darwin

package assets

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/disc"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--portico-storage-helper" {
		if err := storage.Helper(os.Stdin, os.Stdout); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestDiscTitleProbeUsesActualConfinedISOAndRechecksInvocation(t *testing.T) {
	binary, helper := os.Getenv("PORTICO_DISC_TEST_FFPROBE"), os.Getenv("PORTICO_DISC_TEST_BINARY")
	if binary == "" || helper == "" {
		t.Skip("set explicit same-bundle ffprobe and disc helper for admitted ISO probing")
	}
	root, err := filepath.Abs("../../../fixtures/discs")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "bluray-udf250.iso")
	structure, err := (disc.Tool{Binary: helper}).ProbeLocal(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	local := storage.New(exe)
	observed, err := local.InspectRoot(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	request := storage.InventoryRequest{Root: root, RootIdentity: storage.RootIdentity(observed)}
	ctx := storage.WithScanInput(context.Background(), local, "disc-inventory-source", request)
	guards := 0
	ctx = storage.WithScanReadGuard(ctx, func(context.Context, string) error { guards++; return nil })
	probe := Probe{Binary: binary}
	title := structure.OrderedTitles()[0]
	facts, err := probe.InspectDiscScan(ctx, path, structure, title)
	if err != nil || facts.Duration != float64(title.DurationMs)/1000 || facts.VideoCodec != "h264" || guards != 1 {
		t.Fatalf("confined disc facts: %+v guards=%d err=%v", facts, guards, err)
	}
	denied := errors.New("inventory source revision changed")
	probe.ReadGuard = func(context.Context, string) error { return denied }
	if _, err = probe.InspectDiscScan(ctx, path, structure, title); !errors.Is(err, denied) {
		t.Fatal("revoked before probe", err)
	}
	calls := 0
	probe.ReadGuard = func(context.Context, string) error {
		calls++
		if calls == 2 {
			return denied
		}
		return nil
	}
	if _, err = probe.InspectDiscScan(ctx, path, structure, title); !errors.Is(err, denied) || calls != 2 {
		t.Fatal("revoked after probe", calls, err)
	}
	probe.ReadGuard = nil
	request.RootIdentity = "changed-root"
	ctx = storage.WithScanInput(context.Background(), local, "disc-inventory-source", request)
	if _, err = probe.InspectDiscScan(ctx, path, structure, title); err == nil {
		t.Fatal("root replacement accepted")
	}
	outside := t.TempDir()
	alias := filepath.Join(outside, "outside.iso")
	if err = os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	observed, err = local.InspectRoot(context.Background(), outside)
	if err != nil {
		t.Fatal(err)
	}
	ctx = storage.WithScanInput(context.Background(), local, "disc-inventory-source", storage.InventoryRequest{Root: outside, RootIdentity: storage.RootIdentity(observed)})
	if _, err = probe.InspectDiscScan(ctx, alias, structure, title); err == nil {
		t.Fatal("ISO symlink escaped admitted root")
	}
	if local.Supervisor.Active() != 0 {
		t.Fatal("probe retained a process")
	}
}
