package mediaprobe

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/sourceaccess"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--portico-storage-helper" {
		if storage.Helper(os.Stdin, os.Stdout) != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// This fixture uses the real registration/helper/preparer, while its immutable
// selected-path authority is synthetic. Actual coordinator Publish is separate.
type preparedFixture struct{ *playback.PreparedInput }

func (f preparedFixture) Metadata() (playback.PreparedInputMetadata, error) {
	return f.PreparedInput.Metadata(), nil
}

func TestRealConfinedProbeRegisteredFixture(t *testing.T) {
	configPath := os.Getenv("PORTICO_PROBE_FIXTURE_CONFIG")
	if configPath == "" {
		t.Skip("explicit allocated tiny fixture configuration required")
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Executable string                                     `json:"executable"`
		Libraries  []string                                   `json:"libraries"`
		Identity   struct{ ExecutableSHA256, Version string } `json:"identity"`
		Fixture    string                                     `json:"fixture"`
	}
	if err = json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	client := storage.New(binary)
	client.Timeout = 5 * time.Second
	client.Supervisor.Limit = 4
	registry := sourceaccess.New(client, nil)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if e := registry.Shutdown(ctx); e != nil {
			t.Error(e)
		}
		if client.Supervisor.Active() != 0 {
			t.Error("physical helper debt after shutdown")
		}
	}()
	owner, cancel := context.WithCancel(context.Background())
	defer cancel()
	input := playback.SourcePreparationInput{Fence: playback.SourcePreparationFence{PlaybackID: "fixture-playback", PresentationID: "fixture-presentation", OwnershipRevision: 1, DesiredRevision: 1, Generation: 1, PolicyRevision: 1}, Selection: playback.SourcePreparationSelection{Kind: "local_file", ItemID: "fixture-item", AssetID: "fixture-asset", LibraryID: "fixture-library", RootID: "fixture-root", Path: config.Fixture}}
	preparer := playback.NewObservedLocalInputPreparer(client, func(ctx context.Context, in playback.SourcePreparationInput) (*storage.RootLease, error) {
		if in != input {
			return nil, sourceaccess.ErrAuthority
		}
		return registry.Borrow(ctx, sourceaccess.Authority{OwnerID: "fixture-attempt", RootID: "fixture-root", RootPath: filepath.Dir(config.Fixture), RelativePath: filepath.Base(config.Fixture), Revision: "fixture-incarnation:1", Lifetime: owner, Validate: func(context.Context) error { return owner.Err() }})
	})
	ctx, done := context.WithTimeout(owner, 15*time.Second)
	defer done()
	prepared, err := preparer.PrepareInput(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	v4, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer v4.Close()
	v6, err := net.ListenTCP("tcp6", &net.TCPAddr{IP: net.IPv6loopback, Port: v4.Addr().(*net.TCPAddr).Port})
	if err != nil {
		t.Fatal(err)
	}
	defer v6.Close()
	cache, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(cache, 0700); err != nil {
		t.Fatal(err)
	}
	tool := Tool{Executable: config.Executable, Libraries: config.Libraries}
	tool.Identity.ExecutableSHA256 = config.Identity.ExecutableSHA256
	tool.Identity.Version = config.Identity.Version
	result, err := Run(ctx, client.Supervisor, "real-fixture", tool, preparedFixture{prepared}, cache, v4, v6)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Facts.Streams) < 1 || result.Facts.Format.DurationSeconds == nil || result.Facts.Format.DurationSeconds.Numerator <= 0 || result.Source.Reference.Kind != playback.ObservedSourceReference || len(result.Source.Chunks) == 0 || result.Source.Final.Observation == nil || result.Source.Final.Observation.Continuity != "intact" {
		t.Fatal("missing actual analyzed source facts")
	}
	if result.Source.Reference != prepared.Metadata().Reference {
		t.Fatal("probe evidence changed acquisition")
	}
	t.Logf("actual streams=%d duration=%d/%d retained_chunks=%d assurance=%s", len(result.Facts.Streams), result.Facts.Format.DurationSeconds.Numerator, result.Facts.Format.DurationSeconds.Denominator, len(result.Source.Chunks), result.Source.Reference.Kind)
	if out := os.Getenv("PORTICO_PROBE_FIXTURE_RESULT"); out != "" {
		encoded, e := json.MarshalIndent(result, "", "  ")
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(out, encoded, 0600); e != nil {
			t.Fatal(e)
		}
	}
}
