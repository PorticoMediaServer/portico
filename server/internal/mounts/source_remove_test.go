package mounts

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestSourceRemovalPublishesLogicalRemovalAndReceiptTogether(t *testing.T) {
	s, input := fixture(t)
	ctx := context.Background()
	created, err := s.Command(ctx, "owner", "", Command{OperationID: operation(1400), Action: "create", Name: input.Name, Executable: input.Executable, Remote: input.Remote, Config: input.Config}, allowCommand)
	if err != nil {
		t.Fatal(err)
	}
	m := created.Mount
	if _, err = s.db.Exec(`INSERT INTO remote_sources(id,kind,name,root,generation,config,mount_id) VALUES(?,'rclone','Cloud',?,?,X'',?)`, m.ID, m.MountPath, m.BackendRevision, m.ID); err != nil {
		t.Fatal(err)
	}
	held, busy, guards, invalidated := false, true, 0, 0
	removeOperation := operation(1401)
	s.GuardSourceRemoval = func(context.Context, string) (func(), error) {
		guards++
		held = true
		return func() { held = false }, nil
	}
	s.NativeBusy = func(string) bool {
		if !held {
			t.Error("busy check preceded removal barrier")
		}
		return busy
	}
	s.NativeInvalidated = func(string) {
		if !held {
			t.Error("source invalidation escaped barrier")
		}
		invalidated++
		var removed, receipts int
		if err := s.db.QueryRow(`SELECT removed FROM remote_sources WHERE id=?`, m.ID).Scan(&removed); err != nil || removed != 1 {
			t.Error("invalidation preceded committed logical removal", err)
		}
		if err := s.db.QueryRow(`SELECT count(*) FROM mount_operations WHERE operation_id=?`, removeOperation).Scan(&receipts); err != nil || receipts != 1 {
			t.Error("logical removal lacked atomic receipt", err)
		}
	}
	inputRemove := RcloneSourceRemoveInput{OperationID: removeOperation, ExpectedGeneration: m.BackendRevision}
	if _, err = s.RemoveRcloneSource(ctx, "owner", m.ID, inputRemove, allowCommand); !errors.Is(err, ErrSourceInUse) || held || invalidated != 0 {
		t.Fatal("busy source was removed or barrier leaked", err)
	}
	var removed int
	if err = s.db.QueryRow(`SELECT removed FROM remote_sources WHERE id=?`, m.ID).Scan(&removed); err != nil || removed != 0 {
		t.Fatal("busy refusal changed source", err)
	}
	busy = false
	first, err := s.RemoveRcloneSource(ctx, "owner", m.ID, inputRemove, allowCommand)
	if err != nil || !first.Removed || held || invalidated != 1 {
		t.Fatal("logical removal failed", err)
	}
	priorGuards := guards
	replayed, err := s.RemoveRcloneSource(ctx, "owner", m.ID, inputRemove, allowCommand)
	if err != nil || !reflect.DeepEqual(first, replayed) || guards != priorGuards || invalidated != 1 {
		t.Fatal("removal retry repeated effect", err)
	}
	inputRemove.ExpectedGeneration++
	if _, err = s.RemoveRcloneSource(ctx, "owner", m.ID, inputRemove, allowCommand); !errors.Is(err, ErrCommandConflict) {
		t.Fatal("changed intent reused removal receipt", err)
	}
}
