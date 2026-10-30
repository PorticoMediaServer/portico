package remotesources

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// The controlled serve executable exercises actual owner-save/receipt/approval
// ordering. Actual provider connectivity is qualified by protocol fixtures.
func TestImportedSavePublishesOnlyTestedLeafOriginsWithConfigReceipt(t *testing.T) {
	for _, kind := range []string{"http", "s3", "crypt"} {
		t.Run(kind, func(t *testing.T) {
			f, managed, identities, owner, _, binary := nativeSaveFixture(t)
			authorize := func(tx *sql.Tx) error { return currentLocalOwnerTx(context.Background(), tx, identities, owner) }
			remote := "network:root"
			config := "[network]\ntype=http\nurl=http://192.168.42.7:8080/\n"
			expected := "http://192.168.42.7:8080/"
			if kind == "s3" {
				config = "[network]\ntype=s3\nendpoint=https://192.168.42.7:9443/\n"
				expected = "https://192.168.42.7:9443/"
			}
			if kind == "crypt" {
				remote = "encrypted:"
				// This fixture qualifies owner-save publication, not an actual DAV
				// request. Its saved option is intentionally unreproducible by the
				// diagnostic, so it must publish unknown without origin byte I/O.
				config = "[encrypted]\ntype=crypt\nremote=network:private\n[network]\ntype=webdav\nurl=https://192.168.42.7:9443/dav\npacer_min_sleep=20ms\n"
				expected = "https://192.168.42.7:9443/"
			}
			receipt, err := managed.Command(context.Background(), "actual-owner-fixture", "", mounts.Command{OperationID: nativeSaveOperation(40), Action: "create", Name: "Imported", Executable: binary, Remote: remote, Config: config}, authorize)
			if err != nil {
				t.Fatal(err)
			}
			var stored []byte
			var actualKind string
			if err = f.s.db.QueryRow(`SELECT kind,config FROM remote_sources WHERE id=?`, receipt.MountID).Scan(&actualKind, &stored); err != nil {
				t.Fatal(err)
			}
			saved, err := f.s.importedSourceConfig(stored)
			if err != nil || actualKind != "rclone" || len(saved.Approvals) != 1 || saved.Approvals[0].Root != expected || len(saved.Approvals[0].Addresses) != 1 || saved.Approvals[0].Addresses[0] != "192.168.42.7" {
				t.Fatal(saved, err)
			}
			if err = f.s.checkImportedMaterial(context.Background(), saved, []byte(config), remote); err != nil {
				t.Fatal(err)
			}
			if kind == "crypt" {
				var support, object string
				if err = f.s.db.QueryRow(`SELECT s.range_support,t.object_path FROM remote_sources s JOIN remote_source_webdav_tests t ON t.source_id=s.id WHERE s.id=?`, receipt.MountID).Scan(&support, &object); err != nil || support != "unknown" || object != "" {
					t.Fatal("synthetic setup fabricated range qualification", support, object, err)
				}
			}
			if err = f.s.checkImportedMaterial(context.Background(), saved, []byte(config+"#edited\n"), remote); err != storage.ErrRemoteChanged {
				t.Fatal("edited raw config reused approval", err)
			}
			for _, host := range []string{"127.0.0.1", "169.254.169.254", "169.254.1.2"} {
				bad := strings.ReplaceAll(config, "192.168.42.7", host)
				if _, err = managed.Command(context.Background(), "actual-owner-fixture", receipt.MountID, mounts.Command{OperationID: nativeSaveOperation(41), Action: "configure", ExpectedRevision: receipt.ControlRevision, Config: bad}, authorize); err == nil {
					t.Fatal("forbidden edit admitted", host)
				}
			}
		})
	}
}

func TestCandidatePublicationRefusesProfileChangeAfterSuccessfulTest(t *testing.T) {
	ctx := context.Background()
	f, managed, identities, owner, _, binary := nativeSaveFixture(t)
	authorize := func(tx *sql.Tx) error { return currentLocalOwnerTx(ctx, tx, identities, owner) }
	config := "[network]\ntype=http\nurl=http://192.168.42.7:8080/\n"
	first, err := managed.Command(ctx, "actual-owner-fixture", "", mounts.Command{OperationID: nativeSaveOperation(60), Action: "create", Name: "Imported", Executable: binary, Remote: "network:", Config: config}, authorize)
	if err != nil {
		t.Fatal(err)
	}
	actual := managed.PrepareSourceCandidate
	managed.PrepareSourceCandidate = func(ctx context.Context, input mounts.SourceCandidateInput, authorize func(*sql.Tx) error) (*mounts.SourceCandidatePublication, error) {
		publication, err := actual(ctx, input, authorize)
		if err != nil {
			return nil, err
		}
		after := publication.CheckAfterTest
		publication.CheckAfterTest = func(ctx context.Context) error {
			if err := after(ctx); err != nil {
				return err
			}
			// The genuine saved profile CAS wins after the network test and before
			// the config/receipt writer; publication must not reuse old tested limits.
			tx, err := dbwork.Begin(ctx, f.s.db, dbwork.ClassInteractive)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			profile, _ := SourceReadPreset("once")
			if _, err = f.s.ApplySourceReadProfileTx(ctx, tx.Tx(), identities, owner, SourceReadProfileChange{SourceID: first.MountID, Generation: "1", Profile: profile}); err != nil {
				return err
			}
			return tx.Commit()
		}
		return publication, nil
	}
	if _, err = managed.Command(ctx, "actual-owner-fixture", first.MountID, mounts.Command{OperationID: nativeSaveOperation(61), Action: "configure", ExpectedRevision: first.ControlRevision, Config: config}, authorize); !errors.Is(err, storage.ErrRemoteChanged) {
		t.Fatal("old limits authorized publication", err)
	}
	var generation int64
	if err = f.s.db.QueryRow(`SELECT generation FROM remote_sources WHERE id=?`, first.MountID).Scan(&generation); err != nil || generation != 1 {
		t.Fatal("failed publication changed source", generation, err)
	}
}
