package mounts

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/crypto/nacl/secretbox"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/rcloneprogram"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--portico-rclone-native" {
		if err := NativeHelper(os.Stdin, os.Stdout); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	if len(os.Args) > 2 && os.Args[1] == "lsjson" {
		if !validPolicyProxy(os.Getenv("RCLONE_HTTP_PROXY")) {
			os.Exit(2)
		}
		for _, arg := range os.Args {
			if arg == "--stat" {
				fmt.Print(`{"Path":"","Name":"root","Size":0,"IsDir":true,"ID":"fixture-root","ModTime":"2026-01-01T00:00:00Z"}`)
				os.Exit(0)
			}
		}
		fmt.Print("[]")
		os.Exit(0)
	}
	if len(os.Args) == 2 && os.Args[1] == "--portico-storage-helper" {
		if storage.Helper(os.Stdin, os.Stdout) != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	if len(os.Args) == 2 && os.Args[1] == "--portico-rclone-guardian" {
		if Guardian(os.Stdin) != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	if len(os.Args) == 2 && os.Args[1] == "version" {
		fmt.Println("rclone v" + rcloneprogram.BundledVersion)
		os.Exit(0)
	}
	if len(os.Args) > 3 && os.Args[1] == "mount" {
		if !validPolicyProxy(os.Getenv("RCLONE_HTTP_PROXY")) {
			os.Exit(2)
		}
		_ = os.WriteFile(os.Args[3]+".fixture-pid", []byte(fmt.Sprint(os.Getpid())), 0600)
		fmt.Fprintln(os.Stderr, "token=leaky-secret https://private.example/secret")
		done := make(chan os.Signal, 1)
		signal.Notify(done, syscall.SIGTERM, os.Interrupt)
		<-done
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func fixture(t *testing.T) (*Service, CreateInput) {
	t.Helper()
	root := canonicalFixtureDir(t)
	db, err := persistence.Open(filepath.Join(root, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	binary := fixtureExecutable(t)
	s, err := New(db, root, "", binary, storage.New(binary))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.shutdown)
	// The executable above simulates mounting; command tests need no host FUSE.
	s.mountSupported = func() bool { return true }
	s.PrepareSourceEgress = func(ctx context.Context, _ ServeSource) (*SourceEgress, error) {
		return fixtureEgress(ctx)
	}
	s.PrepareSourceCandidate = func(ctx context.Context, input SourceCandidateInput, _ func(*sql.Tx) error) (*SourceCandidatePublication, error) {
		return &SourceCandidatePublication{Test: func(ctx context.Context, program rcloneprogram.Program, digest string) error {
			candidate, err := s.candidateFile(input.Configuration)
			if err != nil {
				return err
			}
			defer os.Remove(candidate)
			return s.fixtureValidateCandidate(ctx, program.Path, digest, input.Remote, candidate)
		}, CheckAfterTest: func(context.Context) error { return nil }, CommitTx: func(context.Context, *sql.Tx, Mount) error { return nil }}, nil
	}
	return s, CreateInput{Name: "Cloud movies", Executable: binary, Remote: "cloud:movies", Config: "[cloud]\ntype = s3\nprovider = AWS\naccess_key_id = fixture\nsecret_access_key = leaky-secret\n"}
}
func TestPlainConfigLifecycleAndDurableIntent(t *testing.T) {
	s, input := fixture(t)
	ctx := context.Background()
	row, err := s.Create(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(s.Guard(filepath.Join(row.MountPath, "movie.mp4")), ErrUnavailable) {
		t.Fatal("stopped mount admitted")
	}
	stored, err := os.ReadFile(filepath.Join(s.private, row.ID+".conf"))
	if err != nil {
		t.Fatal(err)
	}
	// Plain rclone config format: the file holds the configuration as given.
	if string(stored) != input.Config {
		t.Fatal("stored config is not the plain input")
	}
	// Simulate mount observation only; no FUSE is available on this test host.
	s.observe = func(context.Context, string) (bool, error) { return len(s.active) > 0, nil }
	s.observeIdentity = func(context.Context, string) (string, error) { return "fixture-filesystem", nil }
	if _, err = s.Action(row.ID, "start"); err != nil {
		t.Fatal(err)
	}
	s.reconcile(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err = os.Stat(row.MountPath + ".fixture-pid"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture child did not launch")
		}
		time.Sleep(20 * time.Millisecond)
	}
	s.reconcile(ctx)
	if err = s.Guard(filepath.Join(row.MountPath, "movie.mp4")); err != nil {
		t.Fatal(err)
	}
	lease, leaseErr := s.LeaseFor(filepath.Join(row.MountPath, "movie.mp4"))
	if leaseErr != nil || lease.Identity != "fixture-filesystem" {
		t.Fatal("ready fixture lacks runtime lease", leaseErr)
	}
	rows, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	projection, _ := json.Marshal(rows)
	if strings.Contains(string(projection), "leaky-secret") || strings.Contains(string(projection), "private.example") {
		t.Fatal("secret leaked in projection")
	}
	if err = s.Delete(ctx, row.ID); err == nil {
		t.Fatal("deleted running mount")
	}
	if _, err = s.Action(row.ID, "stop"); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(s.Guard(row.MountPath), ErrUnavailable) {
		t.Fatal("stop did not fence storage immediately")
	}
	if lease.Lifetime.Err() == nil {
		t.Fatal("stop did not revoke existing runtime lease")
	}
	for len(s.active) > 0 {
		s.reconcile(ctx)
		if time.Now().After(deadline) {
			t.Fatal("owned child not reaped")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err = s.Action(row.ID, "start"); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(s.db, filepath.Dir(filepath.Dir(s.private)), s.root, s.helper, s.storage)
	if err != nil {
		t.Fatal(err)
	}
	durable, err := restarted.get(row.ID)
	if err != nil || durable.DesiredState != "running" || durable.ObservedState != "stopped" {
		t.Fatal("desired state did not survive", durable, err)
	}
	if !errors.Is(restarted.Guard(row.MountPath), ErrUnavailable) {
		t.Fatal("restart trusted stale observed mount")
	}
	restarted.observe = func(context.Context, string) (bool, error) { return true, nil }
	restarted.reconcile(ctx)
	durable, _ = restarted.get(row.ID)
	if durable.ObservedState != "unavailable" || len(restarted.active) != 0 {
		t.Fatal("adopted external mount", durable)
	}
}
func TestConfigurationAndOwnershipFences(t *testing.T) {
	s, input := fixture(t)
	for _, raw := range []string{"[cloud]\ntype=sftp\nserver_command=bad", "[cloud]\ntype=local", "[cloud]\ntype=s3\nenv_auth=true", "[cloud]\nkey=value"} {
		if validateConfig(raw, input.Remote) == nil {
			t.Fatal("accepted unsafe config")
		}
	}
	row, err := s.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := lockMount(filepath.Join(s.private, row.ID+".conf.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if release, e := lockMount(filepath.Join(s.private, row.ID+".conf.lock")); e == nil {
		release()
		t.Fatal("duplicate ownership acquired")
	}
	if _, err = s.Action(row.ID, "stop"); err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(context.Background(), row.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Guard(filepath.Join(t.TempDir(), "external.mp4")); err != nil {
		t.Fatal("external path blocked")
	}
}

func TestUnavailableMountCannotYieldEmptyInventory(t *testing.T) {
	s, input := fixture(t)
	row, err := s.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	// Even if an old observation says ready, the isolated helper must verify the
	// mount still exists before treating its empty underlying directory as data.
	s.mu.Lock()
	s.available[row.ID] = true
	s.publish()
	s.mu.Unlock()
	store := storage.New(s.helper)
	store.Guard = s.Guard
	store.MountedRoot = s.RootFor
	err = store.Inventory(context.Background(), "fixture", row.MountPath, func(storage.Snapshot) error { return nil })
	if err == nil {
		t.Fatal("unmounted directory accepted as empty inventory")
	}
}

// Configs sealed under the old managed-storage key migrate to rclone's plain
// config format on open, and the key goes away. Mount configs are durable:
// without this pass every remote would need re-entering at upgrade.
func TestSealedConfigsMigrateToPlain(t *testing.T) {
	root := canonicalFixtureDir(t)
	db, err := persistence.Open(filepath.Join(root, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	binary := fixtureExecutable(t)
	private := storagepaths.Path(root, "managed-storage")
	if err = os.MkdirAll(private, 0700); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		t.Fatal(err)
	}
	password := hex.EncodeToString(key)
	config := "[cloud]\ntype = s3\nprovider = AWS\naccess_key_id = fixture\nsecret_access_key = leaky-secret\n"
	sealed, err := func() ([]byte, error) {
		sum := sha256.Sum256([]byte("[" + password + "][rclone-config]"))
		var nonce [24]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return nil, err
		}
		box := secretbox.Seal(nonce[:], []byte(config), &nonce, &sum)
		return []byte("RCLONE_ENCRYPT_V0:\n" + base64.StdEncoding.EncodeToString(box) + "\n"), nil
	}()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(private, "sealed.conf"), sealed, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(private, "key"), key, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := New(db, root, "", binary, storage.New(binary))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.shutdown)
	stored, err := os.ReadFile(filepath.Join(private, "sealed.conf"))
	if err != nil || string(stored) != config {
		t.Fatal("sealed config not migrated to plaintext")
	}
	if _, err = os.Stat(filepath.Join(private, "key")); !os.IsNotExist(err) {
		t.Fatal("key file left behind")
	}
}

// InspectPrivateDirectory answers what New would say about a mount
// configuration folder that is already in a state folder, creating nothing.
func TestInspectPrivateDirectoryRefusesWhatNewWould(t *testing.T) {
	state := canonicalFixtureDir(t)
	if err := InspectPrivateDirectory(state); err != nil {
		t.Fatalf("a state folder with no mount configuration yet was refused: %v", err)
	}
	if _, err := os.Lstat(storagepaths.Path(state, "managed-storage")); !os.IsNotExist(err) {
		t.Fatalf("inspecting created the folder: %v", err)
	}
	if err := os.MkdirAll(storagepaths.Path(state, "managed-storage"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := InspectPrivateDirectory(state); err != nil {
		t.Fatalf("an ordinary folder was refused: %v", err)
	}
	asFile := canonicalFixtureDir(t)
	if err := writeMountFixture(storagepaths.Path(asFile, "managed-storage"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := InspectPrivateDirectory(asFile); err == nil {
		t.Fatal("a file in place of the folder was accepted")
	}
	asLink := canonicalFixtureDir(t)
	if err := os.MkdirAll(filepath.Join(asLink, "Keys"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(state, storagepaths.Path(asLink, "managed-storage")); err != nil {
		t.Fatal(err)
	}
	if err := InspectPrivateDirectory(asLink); err == nil {
		t.Fatal("a link in place of the folder was accepted")
	}
}

func writeMountFixture(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, data, mode)
}

// ValidateConfiguration tests an already private candidate. No database write
// transaction or mount/service lock is held while the provider is contacted.
func (s *Service) fixtureValidateCandidate(ctx context.Context, executable, digest, remote, file string) error {
	egress, err := fixtureEgress(ctx)
	if err != nil {
		return err
	}
	defer egress.Close()
	data, _ := json.Marshal(nativeRequest{Executable: executable, Digest: digest, Remote: remote, ConfigPath: file, PrivateHome: s.work, Password: "", Operation: "stat", HTTPProxy: egress.HTTPProxy})
	cmd := exec.Command(s.helper, "--portico-rclone-native")
	cmd.Stdin = bytes.NewReader(data)
	check, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	err = s.validationSupervisor.Run(check, "rclone-configuration", cmd, func(r io.Reader) error {
		raw, e := io.ReadAll(io.LimitReader(r, 65537))
		if e != nil || len(raw) > 65536 {
			return ErrConfigInvalid
		}
		var entry NativeEntry
		if json.Unmarshal(raw, &entry) != nil || !entry.IsDir {
			return ErrConfigInvalid
		}
		return nil
	})
	if err != nil {
		return storage.ErrRemoteCredentials
	}
	return nil
}

func fixtureEgress(ctx context.Context) (*SourceEgress, error) {
	proxy, err := remotemedia.StartPolicyProxy(ctx, remotemedia.Policy{}, "fixture", "fixture-proxy", func(ctx context.Context) error { return ctx.Err() })
	if err != nil {
		return nil, err
	}
	return &SourceEgress{HTTPProxy: proxy.Locator(), Close: func() { proxy.Close(); _ = proxy.Wait(context.Background()) }}, nil
}
