package mounts

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestRcloneSourceSaveRefusesOwnerBeforeProgramOrCandidate(t *testing.T) {
	s, input := fixture(t)
	prepared := false
	s.PrepareSourceCandidate = func(context.Context, SourceCandidateInput, func(*sql.Tx) error) (*SourceCandidatePublication, error) {
		prepared = true
		return nil, ErrConfigInvalid
	}
	_, err := s.SaveRcloneSource(context.Background(), "owner", RcloneSourceInput{Name: "Cloud", Remote: input.Remote, Config: input.Config}, func(*sql.Tx) error { return identity.ErrUnauthorized })
	if !errors.Is(err, identity.ErrUnauthorized) || prepared {
		t.Fatal("owner refusal reached program or candidate", err)
	}
	for _, table := range []string{"managed_mounts", "mount_backend_configs", "mount_operations"} {
		var count int
		if err := s.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatal("refused source save published durable state", table, count, err)
		}
	}
}

func TestRcloneSourceReceiptContainsOnlyAcceptedSourceIdentity(t *testing.T) {
	for _, receipt := range []Receipt{{}, {Accepted: true, MountID: "source"}, {Accepted: true, Deleted: true, MountID: "source", Mount: &Mount{ID: "source", BackendRevision: 1}}, {Accepted: true, MountID: "source", Mount: &Mount{ID: "other", BackendRevision: 1}}} {
		if _, err := sourceReceipt(receipt); !errors.Is(err, ErrCommandConflict) {
			t.Fatal("invalid source receipt admitted")
		}
	}
	result, err := sourceReceipt(Receipt{OperationID: "op", Accepted: true, MountID: "source", Mount: &Mount{ID: "source", BackendRevision: 2}})
	if err != nil || result.SourceID != "source" || result.Generation != 2 || result.OperationID != "op" || !result.Accepted {
		t.Fatal("source receipt lost durable identity", err)
	}
	removed, err := sourceReceipt(Receipt{OperationID: "remove", Accepted: true, Deleted: true, MountID: "source", Mount: &Mount{ID: "source", BackendRevision: 2, RemovalPending: true}})
	if err != nil || !removed.Removed || removed.SourceID != "source" {
		t.Fatal("logical removal receipt lost its explicit state", err)
	}
}
