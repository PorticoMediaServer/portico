package remotesources

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func captureOwnerSource(t *testing.T, f observationFixture, ref ObservationRef, identities *identity.Service, owner identity.Principal, purpose SourceReadPurpose, changed func()) *SourceReadLease {
	t.Helper()
	tx, err := dbwork.BeginSnapshot(context.Background(), f.s.db)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	lease, err := f.s.CaptureReadSourceTx(context.Background(), tx.Tx(), SourceSessionAdmission{Purpose: purpose, Observation: ref, OnChanged: changed, CheckTx: func(ctx context.Context, tx *sql.Tx, binding SourceReadBinding) error {
		if binding.Path != f.file || binding.Observation != ref || binding.Purpose != purpose {
			return storage.ErrRemoteChanged
		}
		return currentLocalOwnerTx(ctx, tx, identities, owner)
	}})
	if err != nil {
		t.Fatal(err)
	}
	return lease
}
func TestSourceSessionCaptureDoesNotOpenProviderOrInferPermission(t *testing.T) {
	f := newObservationFixture(t)
	identities, owner, _ := observationOwner(t, f)
	ref, err := f.record(t, scanFacts("capture"), 0)
	if err != nil {
		t.Fatal(err)
	}
	lease := captureOwnerSource(t, f, ref, identities, owner, SourceReading, func() {})
	defer lease.Close()
	binding := lease.Binding()
	if binding.Path != f.file || binding.Size != 16 || binding.LeaseID < 1 || binding.Profile.Saved || binding.Profile.ScanDepth != "file_list_only" {
		t.Fatal(binding)
	}
	if len(f.s.serveLeases) != 0 {
		t.Fatal("DB capture launched a source process")
	}
	if err = lease.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	second := captureOwnerSource(t, f, ref, identities, owner, SourceReading, func() {})
	defer second.Close()
	if second.Binding().LeaseID == binding.LeaseID {
		t.Fatal("logical lease identity reused")
	}
	tx, err := dbwork.BeginSnapshot(context.Background(), f.s.db)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = f.s.CaptureReadSourceTx(context.Background(), tx.Tx(), SourceSessionAdmission{Purpose: SourceReading, Observation: ref}); err == nil {
		t.Fatal("missing real authority defaulted to permission")
	}
	if _, err = f.s.CaptureReadSourceTx(context.Background(), tx.Tx(), SourceSessionAdmission{Purpose: "analysis", Observation: ref, CheckTx: func(context.Context, *sql.Tx, SourceReadBinding) error {
		t.Fatal("pool label dispatched authority")
		return nil
	}, OnChanged: func() {}}); err == nil {
		t.Fatal("pool label became operation permission")
	}
}
func TestSourceSessionPublicationCheckIsLocalAndABAIsTerminal(t *testing.T) {
	f := newObservationFixture(t)
	identities, owner, _ := observationOwner(t, f)
	ref, err := f.record(t, scanFacts("publication"), 0)
	if err != nil {
		t.Fatal(err)
	}
	var writerHeld atomic.Bool
	notices := 0
	lease := captureOwnerSource(t, f, ref, identities, owner, SourceReading, func() {
		if writerHeld.Load() {
			t.Error("publication transaction invoked external callback")
		}
		notices++
	})
	defer lease.Close()
	tx, err := dbwork.Begin(context.Background(), f.s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		t.Fatal(err)
	}
	writerHeld.Store(true)
	if _, err = tx.Tx().Exec(`UPDATE remote_scan_observations SET revision=revision+1`); err != nil {
		t.Fatal(err)
	}
	if err = lease.CheckTx(context.Background(), tx.Tx()); !errors.Is(err, storage.ErrRemoteChanged) {
		t.Fatal("publication missed current observation", err)
	}
	tx.Rollback()
	writerHeld.Store(false)
	if notices != 0 {
		t.Fatal("transaction delivered a notice", notices)
	}
	if err = lease.Check(context.Background()); !errors.Is(err, storage.ErrRemoteChanged) {
		t.Fatal("A-to-B-to-A revived old logical lease", err)
	}
	if err = lease.Check(context.Background()); !errors.Is(err, storage.ErrRemoteChanged) || notices != 1 {
		t.Fatal(err, notices)
	}
}
func TestSourceSessionCurrentIdentityRevocationClosesLease(t *testing.T) {
	f := newObservationFixture(t)
	identities, owner, family := observationOwner(t, f)
	ref, err := f.record(t, scanFacts("actor"), 0)
	if err != nil {
		t.Fatal(err)
	}
	lease := captureOwnerSource(t, f, ref, identities, owner, SourceReading, func() { t.Error("actor revocation reported a file replacement") })
	defer lease.Close()
	if _, err = f.s.db.Exec(`UPDATE authorization_session_families SET revoked=1 WHERE id=?`, family); err != nil {
		t.Fatal(err)
	}
	if err = lease.Check(context.Background()); err == nil {
		t.Fatal("revoked family retained source")
	}
	if _, err = f.s.db.Exec(`UPDATE authorization_session_families SET revoked=0 WHERE id=?`, family); err != nil {
		t.Fatal(err)
	}
	if err = lease.Check(context.Background()); err == nil {
		t.Fatal("old lease revived after authority ABA")
	}
}
func TestServeTransportHasNoProxyCommandOrNativeFallback(t *testing.T) {
	for _, backend := range []string{"s3", "b2", "drive", "onedrive", "dropbox", "webdav", "http", "sftp", "smb"} {
		raw := "[active]\ntype=" + backend + "\nhost=owner.example\nknown_hosts_file=/retained/owner-file\n"
		if backend == "webdav" || backend == "http" {
			raw += "url=https://owner.example/\n"
		}
		actual, host, port, err := serveTransport([]byte(raw), "active:path")
		if err != nil || actual != backend {
			t.Fatal(backend, actual, err)
		}
		if backend == "sftp" && (host != "owner.example" || port != "22") {
			t.Fatal(host, port)
		}
		if backend == "smb" && (host != "owner.example" || port != "445") {
			t.Fatal(host, port)
		}
	}
	for _, extra := range []string{"http_proxy=http://proxy.example", "ssh=external -oProxyCommand=bad", "unix_socket=/private/socket", "password_command=bad", "auth_redirect=true", "override.http_proxy=", "override.max_connections=0"} {
		if _, _, _, err := serveTransport([]byte("[active]\ntype=sftp\nhost=owner.example\n"+extra+"\n"), "active:"); err == nil {
			t.Fatal("escape hatch admitted", extra)
		}
	}
	for _, raw := range []string{"[active]\ntype=unknown\n", "[active]\ntype=crypt\nremote=active:\n", "[active]\ntype=http\ntype=sftp\n"} {
		if _, _, _, err := serveTransport([]byte(raw), "active:"); err == nil {
			t.Fatal("unknown transport became permission", strings.Split(raw, "\n")[1])
		}
	}
	actual, _, _, err := serveTransport([]byte("[active]\ntype=crypt\nremote=underlying:path\n[underlying]\ntype=drive\n"), "active:")
	if err != nil || actual != "drive" {
		t.Fatal("configured crypt chain", actual, err)
	}
}

func TestSourceSessionActivityExpiresWithoutAnotherRead(t *testing.T) {
	f := newObservationFixture(t)
	identities, owner, _ := observationOwner(t, f)
	ref, err := f.record(t, scanFacts("lifetime"), 0)
	if err != nil {
		t.Fatal(err)
	}
	life, cancel := context.WithCancel(context.Background())
	defer cancel()
	view, err := dbwork.BeginSnapshot(life, f.s.db)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := f.s.CaptureReadSourceTx(life, view.Tx(), SourceSessionAdmission{Purpose: SourceReading, Observation: ref, OnChanged: func() { t.Error("owner expiry reported replacement") }, CheckTx: func(ctx context.Context, tx *sql.Tx, binding SourceReadBinding) error {
		if binding.Observation != ref || binding.Path != f.file {
			return storage.ErrRemoteChanged
		}
		return currentLocalOwnerTx(ctx, tx, identities, owner)
	}})
	view.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if !f.s.PlaybackActive("remote") {
		t.Fatal("actual retained interactive session not active")
	}
	cancel()
	deadline := time.Now().Add(time.Second)
	for f.s.PlaybackActive("remote") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if f.s.PlaybackActive("remote") {
		t.Fatal("expired idle session retained activity")
	}
	if err = lease.Check(context.Background()); err == nil {
		t.Fatal("expired owner lifetime revived")
	}
}

func TestSourceSessionShutdownAndInvalidationRetireCurrentReaders(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(fmt.Sprint("shutdown=", shutdown), func(t *testing.T) {
			f := newObservationFixture(t)
			identities, owner, _ := observationOwner(t, f)
			ref, err := f.record(t, scanFacts("retire"), 0)
			if err != nil {
				t.Fatal(err)
			}
			lease := captureOwnerSource(t, f, ref, identities, owner, SourceReading, func() {})
			defer lease.Close()
			if shutdown {
				f.s.stopServeProcesses()
			} else {
				f.s.invalidate("remote")
			}
			if f.s.PlaybackActive("remote") {
				t.Fatal("retired source retained activity")
			}
			if err = lease.Check(context.Background()); err == nil {
				t.Fatal("old source reader survived retirement")
			}
		})
	}
}
