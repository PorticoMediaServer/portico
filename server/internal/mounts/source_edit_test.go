package mounts

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestSourceEditFingerprintBindsGenerationWithoutChangingLegacyReceipts(t *testing.T) {
	s, _ := fixture(t)
	command := Command{OperationID: operation(1300), Action: "configure", ExpectedRevision: 1, Config: "private-configuration"}
	raw, err := json.Marshal(struct {
		Actor, ID string
		Command   Command
	}{"owner", "source", command})
	if err != nil {
		t.Fatal(err)
	}
	defer clear(raw)
	mac := hmac.New(sha256.New, s.fpKey[:])
	mac.Write(raw)
	if s.fingerprint("owner", "source", command) != hex.EncodeToString(mac.Sum(nil)) {
		t.Fatal("legacy receipt fingerprint changed")
	}
	command.ExpectedRevision, command.sourceGeneration = 0, 1
	first := s.fingerprint("owner", "source", command)
	command.sourceGeneration = 2
	if first == s.fingerprint("owner", "source", command) {
		t.Fatal("source generation omitted from receipt identity")
	}
	var wire Command
	if err = json.Unmarshal([]byte(`{"action":"configure","sourceGeneration":2}`), &wire); err != nil || wire.sourceGeneration != 0 {
		t.Fatal("wire input acquired the private source editor seam", err)
	}
}

func TestSourceEditRequiresOwnerBeforeProgramOrCandidate(t *testing.T) {
	s, input := fixture(t)
	prepared := false
	s.PrepareSourceCandidate = func(context.Context, SourceCandidateInput, func(*sql.Tx) error) (*SourceCandidatePublication, error) {
		prepared = true
		return nil, ErrConfigInvalid
	}
	_, err := s.EditRcloneSource(context.Background(), "owner", "source", RcloneSourceEditInput{ExpectedGeneration: 1, Name: "Cloud", Remote: input.Remote, Config: input.Config}, func(*sql.Tx) error { return identity.ErrUnauthorized })
	if !errors.Is(err, identity.ErrUnauthorized) || prepared {
		t.Fatal("source editor owner refusal reached provider work", err)
	}
}

func TestSourceEditControlRequiresExactCurrentSourceAssociation(t *testing.T) {
	s, input := fixture(t)
	ctx := context.Background()
	r, err := s.Command(ctx, "owner", "", Command{OperationID: operation(1301), Action: "create", Name: input.Name, Executable: input.Executable, Remote: input.Remote, Config: input.Config}, allowCommand)
	if err != nil {
		t.Fatal(err)
	}
	m := r.Mount
	if _, err = s.db.Exec(`INSERT INTO remote_sources(id,kind,name,root,generation,config,mount_id) VALUES(?,'rclone','Cloud',?,?,X'',?)`, m.ID, m.MountPath, m.BackendRevision, m.ID); err != nil {
		t.Fatal(err)
	}
	check := func(wantConflict bool) {
		t.Helper()
		tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		control, err := importedSourceControlTx(ctx, tx, m.ID, m.BackendRevision)
		tx.Rollback()
		if wantConflict && !errors.Is(err, ErrCommandConflict) || !wantConflict && (err != nil || control != m.ControlRevision) {
			t.Fatal("source association mismatch", err)
		}
	}
	check(false)
	for _, statement := range []string{`UPDATE remote_sources SET root=root||'/other'`, `UPDATE remote_sources SET generation=generation+1`, `UPDATE remote_sources SET mount_id='other'`, `UPDATE remote_sources SET removed=1`} {
		if _, err = s.db.Exec(statement+` WHERE id=?`, m.ID); err != nil {
			t.Fatal(err)
		}
		check(true)
		if _, err = s.db.Exec(`UPDATE remote_sources SET root=?,generation=?,mount_id=?,removed=0 WHERE id=?`, m.MountPath, m.BackendRevision, m.ID, m.ID); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSourceEditReplaysOnlyOriginalIntentBeforeRecheckingChangedSource(t *testing.T) {
	s, input := fixture(t)
	ctx := context.Background()
	created, err := s.Command(ctx, "owner", "", Command{OperationID: operation(1302), Action: "create", Name: input.Name, Executable: input.Executable, Remote: input.Remote, Config: input.Config}, allowCommand)
	if err != nil {
		t.Fatal(err)
	}
	m := created.Mount
	if _, err = s.db.Exec(`INSERT INTO remote_sources(id,kind,name,root,generation,config,mount_id) VALUES(?,'rclone','Cloud',?,?,X'',?)`, m.ID, m.MountPath, m.BackendRevision, m.ID); err != nil {
		t.Fatal(err)
	}
	prepare := s.PrepareSourceCandidate
	prepared := 0
	s.PrepareSourceCandidate = func(ctx context.Context, input SourceCandidateInput, authorize func(*sql.Tx) error) (*SourceCandidatePublication, error) {
		prepared++
		return prepare(ctx, input, authorize)
	}
	edit := Command{OperationID: operation(1303), Action: "configure", sourceGeneration: m.BackendRevision, Name: "Renamed", Executable: input.Executable, Remote: input.Remote, Config: input.Config}
	first, err := s.Command(ctx, "owner", m.ID, edit, allowCommand)
	if err != nil || prepared != 1 {
		t.Fatal("source edit failed", err, prepared)
	}
	if _, err = s.db.Exec(`UPDATE remote_sources SET removed=1 WHERE id=?`, m.ID); err != nil {
		t.Fatal(err)
	}
	replayed, err := s.Command(ctx, "owner", m.ID, edit, allowCommand)
	if err != nil || !reflect.DeepEqual(first, replayed) || prepared != 1 {
		t.Fatal("receipt replay repeated provider work or lost original result", err, prepared)
	}
	edit.sourceGeneration++
	if _, err = s.Command(ctx, "owner", m.ID, edit, allowCommand); !errors.Is(err, ErrCommandConflict) || prepared != 1 {
		t.Fatal("changed source intent reused a receipt", err)
	}
	edit.sourceGeneration--
	if _, err = s.Command(ctx, "owner", m.ID, edit, func(*sql.Tx) error { return identity.ErrUnauthorized }); !errors.Is(err, identity.ErrUnauthorized) || prepared != 1 {
		t.Fatal("revoked actor replayed source receipt", err)
	}
}
