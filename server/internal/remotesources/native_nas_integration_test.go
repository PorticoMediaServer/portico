//go:build rcloneintegration && linux

package remotesources

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"database/sql"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/childprocess"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/rcloneprogram"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// An actual owner command saves a real read-only NAS connection, then the
// retained shared serve producer reads fresh metadata. No content/decode grant
// or immutable-version claim follows from this connection-only acceptance.
func TestNativeSavedNASConnectionAndForbiddenEdits(t *testing.T) {
	for _, backend := range []string{"sftp", "smb"} {
		t.Run(backend, func(t *testing.T) { testNativeSavedNAS(t, backend) })
	}
}

func TestImportedSavedPrivateEndpointsAndCryptOrigin(t *testing.T) {
	for _, backend := range []string{"http", "s3", "crypt"} {
		t.Run(backend, func(t *testing.T) { testNativeSavedNAS(t, backend) })
	}
}

func testNativeSavedNAS(t *testing.T, backend string) {
	program := lockedRcloneFixture(t)
	image, job := os.Getenv("PORTICO_SAMBA_TEST_IMAGE"), os.Getenv("PORTICO_SAMBA_TEST_JOB")
	if !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(image) || !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,79}$`).MatchString(job) {
		t.Fatal("owned disposable NAS image and job label are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	f, managed, identities, owner, _, _ := nativeSaveFixture(t)
	// The listing path needs the actual service constructor's local worker
	// capacity and lifecycle hooks, not the smaller DB-only save fixture.
	sources, err := New(f.s.db, t.TempDir(), managed)
	if err != nil {
		t.Fatal(err)
	}
	f.s = sources
	if err := f.s.startReadRuntime(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.s.stopServeProcesses)
	authorize := func(tx *sql.Tx) error { return currentLocalOwnerTx(ctx, tx, identities, owner) }
	environment := []string{"HOME=" + t.TempDir(), "TMPDIR=" + os.Getenv("TMPDIR")}
	docker := func(ctx context.Context, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, "docker", args...)
		cmd.Env, cmd.Stderr = environment, io.Discard
		childprocess.Configure(cmd)
		return cmd
	}
	label, err := docker(ctx, "image", "inspect", "--format", `{{ index .Config.Labels "portico.job" }}`, image).Output()
	if err != nil || strings.TrimSpace(string(label)) != job {
		t.Fatal("NAS image belongs to another job", err)
	}
	network := job + "-nas-" + backend
	third := 1 + int(time.Now().UnixNano()%250)
	address := ""
	for attempt := 0; attempt < 8; attempt++ {
		base := fmt.Sprintf("192.168.%d.", 1+(third+attempt)%250)
		if err := docker(ctx, "network", "create", "--internal", "--label", "portico=remote-sources-r40", "--label", "portico.job="+job, "--subnet", base+"0/24", network).Run(); err == nil {
			address = base + "2"
			break
		}
	}
	if address == "" {
		t.Fatal("owned 192.168 NAS network could not be created")
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := docker(cleanup, "network", "rm", network).Run(); err != nil {
			t.Error("owned NAS network did not retire", err)
		}
	})
	root := t.TempDir()
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("controlled-nas"), 16)
	ignore := []byte("ignored-file.mkv\n")
	if err := os.WriteFile(filepath.Join(root, "one.mkv"), payload, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ignore"), ignore, 0644); err != nil {
		t.Fatal(err)
	}
	modified := time.Unix(1700000000, 0).UTC()
	if err := os.Chtimes(filepath.Join(root, "one.mkv"), modified, modified); err != nil {
		t.Fatal(err)
	}
	configuration, err := os.ReadFile("../rcloneprogram/testdata/smb/smb.conf")
	if err != nil {
		t.Fatal(err)
	}
	configuration = bytes.ReplaceAll(configuration, []byte("interfaces = 127.0.0.1"), []byte("interfaces = "+address))
	configPath := filepath.Join(t.TempDir(), "smb.conf")
	if err := os.WriteFile(configPath, configuration, 0600); err != nil {
		t.Fatal(err)
	}
	arguments := []string{"run", "--detach", "--label", "portico=remote-sources-r40", "--label", "portico.job=" + job, "--read-only", "--memory=256m", "--cpus=1", "--pids-limit=128", "--network", network, "--ip", address, "--log-driver=none", "--tmpfs", "/run", "--tmpfs", "/var/lib/samba", "--tmpfs", "/var/cache/samba", "--tmpfs", "/var/log/samba", "--mount", "type=bind,source=" + root + ",target=/media,readonly", "--mount", "type=bind,source=" + configPath + ",target=/etc/samba/smb.conf,readonly"}
	if backend == "s3" {
		if err := os.Mkdir(filepath.Join(root, "bucket"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "bucket", "one.mkv"), payload, 0644); err != nil {
			t.Fatal(err)
		}
	}
	known, password := "", ""
	if backend == "sftp" {
		private := t.TempDir()
		public, key, e := ed25519.GenerateKey(rand.Reader)
		if e != nil {
			t.Fatal(e)
		}
		encoded, e := x509.MarshalPKCS8PrivateKey(key)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(private, "key"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}), 0600); e != nil {
			t.Fatal(e)
		}
		clear(encoded)
		clear(key)
		if e = os.WriteFile(filepath.Join(private, "authorized"), nil, 0600); e != nil {
			t.Fatal(e)
		}
		serverKey, e := ssh.NewPublicKey(public)
		if e != nil {
			t.Fatal(e)
		}
		known = filepath.Join(private, "known-hosts")
		if e = os.WriteFile(known, []byte(knownhosts.Line([]string{address + ":2222"}, serverKey)+"\n"), 0600); e != nil {
			t.Fatal(e)
		}
		obscure := exec.CommandContext(ctx, program, "obscure", "fixture-only")
		obscure.Env, obscure.Stderr = environment, io.Discard
		childprocess.Configure(obscure)
		value, e := obscure.Output()
		if e != nil || len(value) > 512 {
			t.Fatal("fixture password encoding", e)
		}
		password = strings.TrimSpace(string(value))
		clear(value)
		arguments = append(arguments, "--entrypoint=/fixture/rclone", "--mount", "type=bind,source="+program+",target=/fixture/rclone,readonly", "--mount", "type=bind,source="+private+",target=/fixture/private,readonly", image, "serve", "sftp", "/media", "--addr", "0.0.0.0:2222", "--key", "/fixture/private/key", "--authorized-keys", "/fixture/private/authorized", "--user=fixture", "--pass=fixture-only", "--read-only", "--dir-cache-time=0s", "--buffer-size=0", "--log-level=ERROR")
	} else if backend == "smb" {
		arguments = append(arguments, image)
	} else {
		protocol := backend
		if backend == "crypt" {
			protocol = "webdav"
		}
		arguments = append(arguments, "--entrypoint=/fixture/rclone", "--mount", "type=bind,source="+program+",target=/fixture/rclone,readonly", image, "serve", protocol, "/media", "--addr", "0.0.0.0:8080", "--read-only", "--dir-cache-time=0s", "--buffer-size=0", "--log-level=ERROR")
		if backend == "s3" {
			arguments = append(arguments, "--auth-key=fixture,fixture-only")
		}
		if backend == "crypt" {
			obscure := exec.CommandContext(ctx, program, "obscure", "fixture-only")
			obscure.Env = environment
			childprocess.Configure(obscure)
			value, e := obscure.Output()
			if e != nil || len(value) > 512 {
				t.Fatal("fixture password encoding", e)
			}
			password = strings.TrimSpace(string(value))
			clear(value)
		}
	}
	raw, err := docker(ctx, arguments...).Output()
	if err != nil {
		t.Fatal("owned NAS start", err)
	}
	container := strings.TrimSpace(string(raw))
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(container) {
		t.Fatal("NAS start returned no exact owned container")
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := docker(cleanup, "rm", "--force", container).Run(); err != nil {
			t.Error("owned NAS container did not retire", err)
		}
	})
	config := func(host string) string {
		switch backend {
		case "http":
			return "[nas]\ntype=http\nurl=http://" + host + ":8080/\n"
		case "s3":
			return "[nas]\ntype=s3\nprovider=Other\nregion=us-east-1\naccess_key_id=fixture\nsecret_access_key=fixture-only\nforce_path_style=true\nno_check_bucket=true\nendpoint=http://" + host + ":8080/\n"
		case "crypt":
			return "[nas]\ntype=crypt\nremote=network:\npassword=" + password + "\n[network]\ntype=webdav\nurl=http://" + host + ":8080/\n"
		}
		if backend == "sftp" {
			return "[nas]\ntype=sftp\nhost=" + host + "\nport=2222\nuser=fixture\npass=" + password + "\nknown_hosts_file=" + known + "\ndisable_hashcheck=true\n"
		}
		return "[nas]\ntype=smb\nhost=" + host + "\nuser=fixture-guest\n"
	}
	remote := "nas:media"
	if backend == "sftp" {
		remote = "nas:/"
	}
	if backend == "http" || backend == "crypt" {
		remote = "nas:"
	}
	if backend == "s3" {
		remote = "nas:bucket"
	}
	command := mounts.Command{OperationID: nativeSaveOperation(101), Action: "create", Name: "NAS", Executable: program, Remote: remote, Config: config(address)}
	// The real backend connection test supplies readiness; no synthetic success
	// or pre-created approval enters the source transaction.
	var receipt mounts.Receipt
	for deadline := time.Now().Add(10 * time.Second); ; {
		receipt, err = managed.Command(ctx, "actual-owner-nas", "", command, authorize)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("actual owner NAS save", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := f.s.refreshBindings(ctx); err != nil {
		t.Fatal(err)
	}
	view, err := dbwork.BeginSnapshot(ctx, f.s.db)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := SourceReadProfileTx(ctx, view.Tx(), receipt.MountID)
	view.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	binding := SourceReadBinding{Observation: ObservationRef{SourceID: receipt.MountID, Generation: strconv.FormatInt(receipt.Mount.BackendRevision, 10)}, Root: receipt.Mount.MountPath, Profile: profile}
	process, err := f.s.ensureSourceServe(ctx, binding, "")
	if err != nil {
		t.Fatal("saved NAS shared source start", err)
	}
	t.Cleanup(func() {
		process.Stop()
		wait, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := process.Wait(wait); !errors.Is(err, rcloneprogram.ErrServeStopped) {
			t.Error("NAS source did not physically retire", err)
		}
	})
	if backend == "http" || backend == "s3" || backend == "crypt" {
		var stored []byte
		if err = f.s.db.QueryRowContext(ctx, `SELECT config FROM remote_sources WHERE id=? AND kind='rclone'`, receipt.MountID).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		approved, err := f.s.importedSourceConfig(stored)
		expected := "http://" + address + ":8080/"
		if err != nil || len(approved.Approvals) != 1 || approved.Approvals[0].Root != expected || len(approved.Approvals[0].Addresses) != 1 || approved.Approvals[0].Addresses[0] != address {
			t.Fatal("save did not persist only the tested origin", approved, err)
		}
		root, err := f.s.InspectRoot(ctx, receipt.Mount.MountPath)
		if err != nil || !root.Directory {
			t.Fatal("saved imported source did not connect", err)
		}
		if backend != "crypt" {
			file, err := f.s.Stat(ctx, filepath.Join(receipt.Mount.MountPath, "one.mkv"))
			if err != nil || file.Size != int64(len(payload)) {
				t.Fatal("imported source metadata did not reach actual origin", file.Size, err)
			}
		}
		if backend == "http" || backend == "s3" {
			bad := config(address)
			if backend == "s3" {
				bad = strings.Replace(bad, "secret_access_key=fixture-only", "secret_access_key=wrong-fixture-secret", 1)
			} else {
				bad = strings.Replace(bad, ":8080/", ":8080/missing-root/", 1)
			}
			if _, err := managed.Command(ctx, "actual-owner-nas", "", mounts.Command{OperationID: nativeSaveOperation(140), Action: "create", Name: "Refused fixture", Executable: program, Remote: remote, Config: bad}, authorize); err == nil {
				t.Fatal("serve readiness published an unconnected backend")
			}
			var count int
			if err := f.s.db.QueryRowContext(ctx, `SELECT count(*) FROM remote_sources WHERE kind='rclone'`).Scan(&count); err != nil || count != 1 {
				t.Fatal("failed connection published approval/source", count, err)
			}
			if err := process.Check(ctx); err != nil {
				t.Fatal("failed test retired saved source", err)
			}
		}
		for i, host := range []string{"127.0.0.1", "169.254.169.254", "169.254.1.2"} {
			if _, err := managed.Command(ctx, "actual-owner-nas", receipt.MountID, mounts.Command{OperationID: nativeSaveOperation(130 + i), Action: "configure", ExpectedRevision: receipt.ControlRevision, Config: config(host)}, authorize); err == nil {
				t.Fatal("forbidden imported edit admitted", host)
			}
		}
		t.Logf("imported_backend=%s saved_actual_private_origin=true blocked_edits_refused=true", backend)
		return
	}
	metadata, err := process.Metadata(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := metadata.Stat(ctx, "one.mkv", process.Check)
	if err != nil || fresh.Size != int64(len(payload)) {
		t.Fatal("saved native NAS did not connect through shared rclone", fresh.Size, err)
	}
	file, err := f.s.Stat(ctx, filepath.Join(receipt.Mount.MountPath, "one.mkv"))
	if err != nil || file.Size != int64(len(payload)) || file.Directory || file.ChangeToken == "" || file.ObjectIdentity == "" {
		t.Fatal("storage Stat did not use admitted source metadata", file, err)
	}
	directory, err := f.s.InspectRoot(ctx, receipt.Mount.MountPath)
	if err != nil || !directory.Directory || directory.ObjectIdentity == "" {
		t.Fatal("storage root inspection did not use the shared source", directory, err)
	}
	if counts := process.OperationCounts(); counts.Active != 0 || counts.Peak > counts.Capacity || counts.Capacity != profile.Limits.Connections {
		t.Fatal("storage metadata escaped the source budget", counts)
	}
	// This checks actual native transport metadata and terminal range behavior,
	// not a catalogue/playback/Reading grant. The owner family and saved source
	// are real; a content consumer must additionally bind its own exact target.
	check := func(ctx context.Context) error {
		view, err := dbwork.BeginSnapshot(ctx, f.s.db)
		if err != nil {
			return err
		}
		err = currentLocalOwnerTx(ctx, view.Tx(), identities, owner)
		view.Rollback()
		if err != nil {
			return err
		}
		return process.Check(ctx)
	}
	observe := func(ctx context.Context) (remotemedia.FileObservation, error) {
		return observeProcessFile(ctx, process, "one.mkv", check)
	}
	scan, err := observe(ctx)
	if err != nil || !scan.ModifiedKnown || scan.ModifiedNS != modified.UnixNano() || scan.Size != int64(len(payload)) {
		t.Fatal("native upstream observation", scan, err)
	}
	endpoint, err := process.Endpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	notices := 0
	session, err := OpenRangeSession(ctx, endpoint, RangeAdmission{Scan: scan, Check: check, Observe: observe, OnChanged: func() { notices++ }})
	if err != nil {
		t.Fatal("native range capture", err)
	}
	t.Cleanup(func() { session.Close() })
	read := make([]byte, 16)
	if n, err := session.ReadAt(ctx, read, 7); n != len(read) || err != nil || !bytes.Equal(read, payload[7:7+len(read)]) {
		t.Fatal("actual native range", n, err)
	}
	if err := os.WriteFile(filepath.Join(root, "one.mkv"), bytes.Repeat([]byte("B"), len(payload)), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(root, "one.mkv"), modified.Add(2*time.Second), modified.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	clear(read)
	if n, err := session.ReadAt(ctx, read, 7); n != 0 || !errors.Is(err, storage.ErrRemoteChanged) || !bytes.Equal(read, make([]byte, len(read))) || notices != 1 {
		t.Fatal("native replacement exposed bytes or missed terminal change", n, err, notices)
	}
	// Returning the original cheap metadata cannot revive the failed reader.
	// It also cannot establish an immutable version for a newly captured reader.
	if err := os.WriteFile(filepath.Join(root, "one.mkv"), payload, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(root, "one.mkv"), modified, modified); err != nil {
		t.Fatal(err)
	}
	if n, err := session.ReadAt(ctx, read, 7); n != 0 || !errors.Is(err, storage.ErrRemoteChanged) || notices != 1 {
		t.Fatal("native observation ABA revived the failed reader", n, err, notices)
	}
	t.Logf("native_backend=%s observed_replacement_terminal=true change_notices=%d", backend, notices)
	// The actual library creation inspects its source before publishing the
	// library-policy contributor. Its first scan/root check must be pending
	// during old-process retirement, not permanently source_unavailable.
	client := storage.New("")
	client.Remote = f.s
	libraries := catalog.New(f.s.db)
	libraries.SetStorage(client)
	library, err := libraries.CreateAuthorized(ctx, "NAS books", "book", receipt.Mount.MountPath, authorize)
	if err != nil {
		t.Fatal("actual remote library creation", err)
	}
	if value, err := f.s.InspectRoot(ctx, receipt.Mount.MountPath); !errors.Is(err, storage.ErrBusy) || value != (storage.Snapshot{}) {
		t.Fatal("first post-create root check did not preserve a pending scan", value, err)
	}
	if err = process.Wait(ctx); !errors.Is(err, rcloneprogram.ErrServeStopped) {
		t.Fatal("obsolete library-policy process was not physically retired", err)
	}
	reopened, err := f.s.InspectRoot(ctx, receipt.Mount.MountPath)
	if err != nil || reopened.ObjectIdentity != directory.ObjectIdentity {
		t.Fatal("fresh admission changed root identity or failed after physical retirement", reopened, directory, err)
	}
	replacement, err := f.s.ensureSourceServe(ctx, binding, "")
	if err != nil || replacement.Generation() == process.Generation() {
		t.Fatal("new admission reused the retired epoch", err)
	}
	t.Cleanup(func() {
		replacement.Stop()
		wait, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := replacement.Wait(wait); !errors.Is(err, rcloneprogram.ErrServeStopped) {
			t.Error("replacement source did not physically retire", err)
		}
	})
	exerciseNativeListedIgnore(t, ctx, f.s, libraries, library.ID, receipt.MountID, ignore)
	for i, host := range []string{"127.0.0.1", "169.254.169.254", "169.254.1.2"} {
		bad := mounts.Command{OperationID: nativeSaveOperation(110 + i), Action: "configure", ExpectedRevision: receipt.ControlRevision, Config: config(host)}
		if _, err := managed.Command(ctx, "actual-owner-nas", receipt.MountID, bad, authorize); err == nil {
			t.Fatal("forbidden NAS edit was accepted", host)
		}
	}
}

// This controlled running inventory uses the real saved source and closed
// rclone listing. No scan observation is preseeded for the configuration file.
func exerciseNativeListedIgnore(t *testing.T, ctx context.Context, sources *Service, libraries *catalog.Service, library, remote string, want []byte) {
	t.Helper()
	folders, err := libraries.LibrarySources(ctx, library)
	if err != nil || len(folders) != 1 {
		t.Fatal("actual NAS library source", folders, err)
	}
	folder := folders[0]
	policy, err := libraries.ScanPolicy(ctx, library)
	if err != nil {
		t.Fatal(err)
	}
	job := "nas-ignore-" + remote
	for _, command := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO jobs(id,library_id,status,created_at) VALUES(?,?,'running','2026-10-23T00:00:00Z')`, []any{job, library}},
		{`INSERT INTO inventory_runs(job_id,source_id,source_generation,root_incarnation,root_identity,policy_revision) VALUES(?,?,?,?,?,?)`, []any{job, folder.ID, folder.Generation, folder.Incarnation, folder.RootIdentity, policy.Revision}},
		{`INSERT INTO remote_scan_generations(job_id,source_id,generation) SELECT ?,id,CAST(generation AS TEXT) FROM remote_sources WHERE id=?`, []any{job, remote}},
	} {
		if _, err = sources.db.ExecContext(ctx, command.query, command.args...); err != nil {
			t.Fatal(err)
		}
	}
	var page storage.RemotePage
	for {
		page, err = sources.ListPage(ctx, job, folder.ResolvedPath, "")
		if err != nil {
			t.Fatal("actual native inventory listing", err)
		}
		if !page.Pending {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	var member storage.Snapshot
	for _, entry := range page.Entries {
		if entry.Name == ".ignore" {
			member = entry
		}
	}
	if member.Path == "" || !page.Complete {
		t.Fatal("closed native listing omitted configuration", page)
	}
	var normalized int
	if err = sources.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM remote_scan_observations WHERE source_id=? AND relative_path='.ignore'`, remote).Scan(&normalized); err != nil || normalized != 0 {
		t.Fatal("fixture fabricated a published ignore observation", normalized, err)
	}
	admission := storage.InventoryBlobAdmission{Purpose: storage.InventoryBlobIgnore, JobID: job, LibraryID: library, LibrarySourceID: folder.ID, RootIncarnation: folder.Incarnation, RootIdentity: folder.RootIdentity, SourceGeneration: folder.Generation, PolicyRevision: policy.Revision, ListingID: page.ID, Member: member}
	request := storage.InventoryRequest{Root: folder.ResolvedPath, RootIdentity: folder.RootIdentity, RelativePath: ".ignore", FollowSymlinks: folder.FollowSymlinks}
	client := storage.New("")
	client.Remote = sources
	readContext := storage.WithScanInput(ctx, client, folder.ID, request, admission)
	got, err := client.ReadSmall(readContext, folder.ID, member.Path, 65536)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatal("admitted native listing configuration", len(got), err)
	}
	if err = sources.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM remote_scan_observations WHERE source_id=? AND relative_path='.ignore'`, remote).Scan(&normalized); err != nil || normalized != 0 {
		t.Fatal("configuration read published an uncommitted scan observation", normalized, err)
	}
	forged := admission
	forged.Member.ChangeToken += "-replacement"
	forgedContext := storage.WithScanInput(ctx, client, folder.ID, request, forged)
	if got, err := client.ReadSmall(forgedContext, folder.ID, member.Path, 65536); err == nil || len(got) != 0 {
		t.Fatal("caller replacement of exact listed member admitted", err)
	}
}
