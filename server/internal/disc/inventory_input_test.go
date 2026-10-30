//go:build linux || darwin

package disc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

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

// This is the physical scan-input component, not an inventory-job or playback
// activation grant. Production must retain its actual job/policy read guard and
// recheck the selected observation before committing the helper's structure.
func TestDiscISOProbeUsesActualConfinedInventoryInput(t *testing.T) {
	binary := os.Getenv("PORTICO_DISC_TEST_BINARY")
	if binary == "" {
		t.Skip("set the explicit same-bundle disc helper for inventory-input qualification")
	}
	root, fixtures := discFixtures(t)
	var path string
	var expected Probe
	for _, fixture := range fixtures {
		if fixture.ID == "bluray-udf250-iso" {
			path = filepath.Join(root, fixture.Path)
			var err error
			expected, err = DecodeProbe(fixture.Expected)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if path == "" {
		t.Fatal("checked ISO fixture missing")
	}
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	local := storage.New(helper)
	observedRoot, err := local.InspectRoot(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	request := storage.InventoryRequest{Root: root, RootIdentity: storage.RootIdentity(observedRoot)}
	ctx := storage.WithScanInput(context.Background(), local, "disc-source", request)
	reads := 0
	ctx = storage.WithScanReadGuard(ctx, func(ctx context.Context, selected string) error {
		if selected != path {
			t.Fatal("source path changed", selected)
		}
		reads++
		return ctx.Err()
	})
	got, err := (Tool{Binary: binary}).ProbeLocal(ctx, path)
	if err != nil || !reflect.DeepEqual(got, expected) || reads != 1 {
		t.Fatal("actual confined ISO probe", got, reads, err)
	}
	selected := expected.OrderedTitles()[0]
	if selected.Playlist == nil {
		t.Fatal("checked Blu-ray title has no playlist")
	}
	direct, err := (Tool{Binary: binary}).SeekLocal(context.Background(), path, *selected.Playlist, 37500)
	if err != nil || direct.TitleTimeMs != 37000 || direct.ByteOffset <= 0 {
		t.Fatal("checked fixture entry point", direct, err)
	}
	seek, err := (Tool{Binary: binary}).SeekLocal(ctx, path, *selected.Playlist, 37500)
	if err != nil || !reflect.DeepEqual(seek, direct) || reads != 2 {
		t.Fatal("confined cross-clip entry point", seek, reads, err)
	}
	revoked := errors.New("inventory invocation revoked")
	ctx = storage.WithScanReadGuard(ctx, func(context.Context, string) error { return revoked })
	if _, err = (Tool{Binary: binary}).ProbeLocal(ctx, path); !errors.Is(err, revoked) {
		t.Fatal("revoked invocation reached helper", err)
	}
	request.RootIdentity = "different-root"
	ctx = storage.WithScanInput(context.Background(), local, "disc-source", request)
	if _, err = (Tool{Binary: binary}).ProbeLocal(ctx, path); err == nil {
		t.Fatal("changed inventory root admitted")
	}
	// The real root helper must reject an ISO-shaped link outside its root.
	confined := t.TempDir()
	alias := filepath.Join(confined, "outside.iso")
	if err = os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	observedRoot, err = local.InspectRoot(context.Background(), confined)
	if err != nil {
		t.Fatal(err)
	}
	ctx = storage.WithScanInput(context.Background(), local, "disc-source", storage.InventoryRequest{Root: confined, RootIdentity: storage.RootIdentity(observedRoot)})
	if _, err = (Tool{Binary: binary}).ProbeLocal(ctx, alias); err == nil {
		t.Fatal("ISO alias escaped the admitted root")
	}
	if local.Supervisor.Active() != 0 {
		t.Fatal("scan helper retained a physical process")
	}
}
