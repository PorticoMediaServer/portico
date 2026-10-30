package remotesources

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func nativeSaveFixture(t *testing.T) (observationFixture, *mounts.Service, *identity.Service, identity.Principal, string, string) {
	t.Helper()
	f := newObservationFixture(t)
	identities, owner, family := observationOwner(t, f)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(binary)
	if err != nil {
		t.Fatal(err)
	}
	binary = filepath.Join(t.TempDir(), "native-helper-fixture")
	target, err := os.OpenFile(binary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		source.Close()
		t.Fatal(err)
	}
	_, copyErr := io.Copy(target, source)
	source.Close()
	closeErr := target.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatal(copyErr, closeErr)
	}
	managed, err := mounts.New(f.s.db, t.TempDir(), "", binary, storage.New(binary))
	if err != nil {
		t.Fatal(err)
	}
	f.s.mounts = managed
	f.s.private = t.TempDir()
	managed.PrepareSourceCandidate = f.s.prepareSourceCandidate
	return f, managed, identities, owner, family, binary
}

func nativeSaveOperation(n int) string {
	return fmt.Sprintf("%013d-00000000-0000-4000-8000-%012d", time.Now().UnixMilli(), n)
}

// The native-helper subprocess here supplies synthetic directory metadata.
// These tests prove real owner/family/CAS/transaction/receipt behavior, not NAS
// connectivity; the separate controlled protocol fixtures qualify actual I/O.
func TestNativeSavePublishesOnlyWithCurrentOwnerAndConfigReceipt(t *testing.T) {
	for _, backend := range []string{"sftp", "smb"} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			f, managed, identities, owner, family, binary := nativeSaveFixture(t)
			authorize := func(tx *sql.Tx) error { return currentLocalOwnerTx(ctx, tx, identities, owner) }
			config := func(host string) string { return "[native]\ntype=" + backend + "\nhost=" + host + "\nport=2222\n" }
			command := mounts.Command{OperationID: nativeSaveOperation(1), Action: "create", Name: "NAS", Executable: binary, Remote: "native:/media", Config: config("192.168.42.7")}
			receipt, err := managed.Command(ctx, "actual-owner-fixture", "", command, authorize)
			if err != nil {
				t.Fatal(err)
			}
			read := func() (string, int64, *nativeSourceAuthority) {
				t.Helper()
				var kind string
				var generation int64
				var stored []byte
				if err := f.s.db.QueryRow(`SELECT kind,generation,config FROM remote_sources WHERE id=?`, receipt.MountID).Scan(&kind, &generation, &stored); err != nil {
					t.Fatal(err)
				}
				authority, err := f.s.readNativeSourceAuthority(kind, stored)
				if err != nil {
					t.Fatal(err)
				}
				return kind, generation, authority
			}
			kind, generation, authority := read()
			if kind != backend || generation != 1 || authority.config.Host != "192.168.42.7" || len(authority.config.Approval.Addresses) != 1 || authority.config.Approval.Addresses[0] != "192.168.42.7" {
				t.Fatal("source approval/config were not one publication", kind, generation, authority.config)
			}
			for _, host := range []string{"127.0.0.1", "169.254.169.254", "169.254.1.2"} {
				bad := mounts.Command{OperationID: nativeSaveOperation(2), Action: "configure", ExpectedRevision: receipt.ControlRevision, Config: config(host)}
				if _, err := managed.Command(ctx, "actual-owner-fixture", receipt.MountID, bad, authorize); err == nil {
					t.Fatal("owner save admitted forbidden native endpoint", host)
				}
				_, current, _ := read()
				if current != generation {
					t.Fatal("failed connection policy changed saved generation")
				}
			}
			changed := mounts.Command{OperationID: nativeSaveOperation(3), Action: "configure", ExpectedRevision: receipt.ControlRevision, Config: config("192.168.42.8")}
			receipt, err = managed.Command(ctx, "actual-owner-fixture", receipt.MountID, changed, authorize)
			if err != nil {
				t.Fatal(err)
			}
			_, generation, authority = read()
			if generation != 2 || authority.config.Host != "192.168.42.8" || len(authority.config.Approval.Addresses) != 1 || authority.config.Approval.Addresses[0] != "192.168.42.8" {
				t.Fatal("changed config reused old approval", generation, authority.config)
			}
			if _, err := f.s.db.Exec(`UPDATE authorization_session_families SET revoked=1 WHERE id=?`, family); err != nil {
				t.Fatal(err)
			}
			changed.OperationID, changed.ExpectedRevision = nativeSaveOperation(4), receipt.ControlRevision
			if _, err := managed.Command(ctx, "actual-owner-fixture", receipt.MountID, changed, authorize); err == nil {
				t.Fatal("retired owner family saved native authority")
			}
		})
	}
}

func TestNativeSaveSourceAndReceiptRollBackTogether(t *testing.T) {
	ctx := context.Background()
	f, managed, identities, owner, _, binary := nativeSaveFixture(t)
	authorize := func(tx *sql.Tx) error { return currentLocalOwnerTx(ctx, tx, identities, owner) }
	denied := errors.New("publication aborted after source write")
	actual := managed.PrepareSourceCandidate
	managed.PrepareSourceCandidate = func(ctx context.Context, input mounts.SourceCandidateInput, authorize func(*sql.Tx) error) (*mounts.SourceCandidatePublication, error) {
		publication, err := actual(ctx, input, authorize)
		if err != nil {
			return nil, err
		}
		commit := publication.CommitTx
		publication.CommitTx = func(ctx context.Context, tx *sql.Tx, mount mounts.Mount) error {
			if err := commit(ctx, tx, mount); err != nil {
				return err
			}
			return denied
		}
		return publication, nil
	}
	command := mounts.Command{OperationID: nativeSaveOperation(10), Action: "create", Name: "NAS", Executable: binary, Remote: "native:/", Config: "[native]\ntype=smb\nhost=192.168.42.7\n"}
	if _, err := managed.Command(ctx, "actual-owner-fixture", "", command, authorize); !errors.Is(err, denied) {
		t.Fatal("source publication error was hidden", err)
	}
	for _, query := range []string{`SELECT count(*) FROM managed_mounts`, `SELECT count(*) FROM mount_operations`, `SELECT count(*) FROM remote_sources WHERE kind IN('sftp','smb')`} {
		var count int
		if err := f.s.db.QueryRow(query).Scan(&count); err != nil || count != 0 {
			t.Fatal("source/config/receipt escaped failed transaction", query, count, err)
		}
	}
}
