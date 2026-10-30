package remotesources

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
)

func TestSourceProgramMutationActualAcceptanceAndReauthorization(t *testing.T) {
	f, managed, identities, owner, family, binary := nativeSaveFixture(t)
	proof := func(tx *sql.Tx) error { return currentLocalOwnerTx(context.Background(), tx, identities, owner) }
	created, err := managed.Command(context.Background(), owner.Hash, "", mounts.Command{OperationID: nativeSaveOperation(550), Action: "create", Name: "NAS", Executable: binary, Remote: "native:/media", Config: "[native]\ntype=sftp\nhost=192.168.42.7\nport=2222\n"}, proof)
	if err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprintf("p251_%013d_%032x", time.Now().UnixMilli(), 550)
	binding := usermutation.Binding{Authority: owner.Authority, AccountID: owner.AccountID, ProfileID: owner.ProfileID, FamilyID: family}
	envelope := usermutation.Envelope{Key: key, Operation: "put_storage_source_program", Digest: strings.Repeat("a", 64)}
	ctx := usermutation.WithRequest(dbwork.WithDurableUser(context.Background()), binding, envelope)
	ctx = usermutation.WithAuthority(ctx, func(ctx context.Context, tx *sql.Tx) error {
		current, err := identities.SessionFamilyTx(ctx, tx, owner)
		if err != nil {
			return err
		}
		if current.ID != family {
			return identity.ErrUnauthorized
		}
		return currentLocalOwnerTx(ctx, tx, identities, owner)
	})
	revision := created.ControlRevision
	// A receipt write failure rolls back the mount config AND actual source
	// publication, not just a test-only command counter.
	if _, err = f.s.db.Exec(`CREATE TRIGGER fail_program_receipt BEFORE UPDATE OF status ON user_mutation_receipts WHEN NEW.status='applied' BEGIN SELECT RAISE(ABORT,'receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.SetSourceProgram(ctx, identities, owner, created.MountID, key, binary, false, revision); err == nil {
		t.Fatal("receipt failure succeeded")
	}
	var generation int64
	if err = f.s.db.QueryRow(`SELECT generation FROM remote_sources WHERE id=?`, created.MountID).Scan(&generation); err != nil || generation != 1 {
		t.Fatal("source publication escaped rollback", generation, err)
	}
	if _, err = f.s.db.Exec(`DROP TRIGGER fail_program_receipt`); err != nil {
		t.Fatal(err)
	}
	first, err := f.s.SetSourceProgram(ctx, identities, owner, created.MountID, key, binary, false, revision)
	if err != nil || first.Revision != revision+1 {
		t.Fatal("actual acceptance failed", first, err)
	}
	if err = f.s.db.QueryRow(`SELECT generation FROM remote_sources WHERE id=?`, created.MountID).Scan(&generation); err != nil || generation != 2 {
		t.Fatal("source publication absent", generation, err)
	}
	// Receipt recovery uses the original request; mutable generation/revision
	// checks and even approval of the original executable must not run again.
	prepare := managed.PrepareSourceCandidate
	managed.PrepareSourceCandidate = func(context.Context, mounts.SourceCandidateInput, func(*sql.Tx) error) (*mounts.SourceCandidatePublication, error) {
		t.Error("replay repeated external approval")
		return nil, errors.New("unexpected preparation")
	}
	replay, err := f.s.SetSourceProgram(ctx, identities, owner, created.MountID, key, binary, false, revision)
	if err != nil || replay != first {
		t.Fatal("actual source replay failed", replay, err)
	}
	managed.PrepareSourceCandidate = prepare
	envelope.Digest = strings.Repeat("b", 64)
	changed := usermutation.WithRequest(ctx, binding, envelope)
	if _, err = f.s.SetSourceProgram(changed, identities, owner, created.MountID, key, binary, false, revision); !errors.Is(err, usermutation.ErrConflict) {
		t.Fatal("changed original request accepted", err)
	}
	var full int
	if err = f.s.db.QueryRow(`SELECT (SELECT count(*) FROM mount_operations WHERE operation_id=?)+(SELECT count(*) FROM remote_source_operations WHERE operation_id=?)`, key, key).Scan(&full); err != nil || full != 0 {
		t.Fatal("full response stored", full, err)
	}
	if _, err = f.s.db.Exec(`UPDATE authorization_session_families SET revoked=1 WHERE id=?`, family); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.SetSourceProgram(ctx, identities, owner, created.MountID, key, binary, false, revision); err == nil {
		t.Fatal("revoked owner disclosed receipt/readback")
	}
}
