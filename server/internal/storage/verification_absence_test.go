package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

// absenceRemote implements the full RemoteSources shape through an embedded nil
// interface, plus the narrow absence methods. Only the methods the Client calls
// are ever reached.
type absenceRemote struct {
	RemoteSources
	absent  bool
	probes  int
	lastKey string
	fenced  []string
}

func (f *absenceRemote) Handles(path string) bool {
	return path == "/remote" || strings.HasPrefix(path, "/remote/")
}

func (f *absenceRemote) InventoryAbsence(_ context.Context, key string, _ InventoryRequest) (bool, error) {
	f.probes++
	f.lastKey = key
	return f.absent, nil
}

func (f *absenceRemote) ValidateInventoryAbsenceTx(_ context.Context, _ *sql.Tx, key string, _ InventoryRequest) error {
	f.fenced = append(f.fenced, key)
	return nil
}

// absencePlainRemote is a RemoteSources adapter that cannot answer remote
// absence proofs. It must never be interpreted as "the file is not there".
type absencePlainRemote struct {
	RemoteSources
}

func (f *absencePlainRemote) Handles(path string) bool {
	return strings.HasPrefix(path, "/remote")
}

func TestClientInventoryAbsenceDispatchesRemoteProof(t *testing.T) {
	remote := &absenceRemote{absent: true}
	client := &Client{Remote: remote}
	request := InventoryRequest{Root: "/remote/library", RootIdentity: "remote-dir:source:root:generation:1", RelativePath: "film.mkv"}
	absent, err := client.InventoryAbsence(context.Background(), "source-verify:source:7", request)
	if err != nil || !absent {
		t.Fatalf("remote proof was not dispatched: absent=%v err=%v", absent, err)
	}
	if remote.probes != 1 || remote.lastKey != "source-verify:source:7" {
		t.Fatalf("remote proof lost its verification key: %+v", remote)
	}
}

func TestClientInventoryAbsenceRefusesRemoteWithoutProof(t *testing.T) {
	client := &Client{Remote: &absencePlainRemote{}}
	absent, err := client.InventoryAbsence(context.Background(), "source-verify:source:7", InventoryRequest{Root: "/remote/library", RootIdentity: "identity", RelativePath: "film.mkv"})
	if !errors.Is(err, ErrRemoteCursor) {
		t.Fatalf("remote proof refusal = %v, want ErrRemoteCursor", err)
	}
	if absent {
		t.Fatal("an adapter without remote absence proof reported absence")
	}
}

func TestClientValidateInventoryAbsenceTxFencesRemoteAndDefaultsLocal(t *testing.T) {
	ctx := context.Background()
	if err := (&Client{}).ValidateInventoryAbsenceTx(ctx, new(sql.Tx), "key", InventoryRequest{Root: "/local/library"}); err != nil {
		t.Fatalf("local absence validation = %v, want nil", err)
	}
	if err := (&Client{}).ValidateInventoryAbsenceTx(ctx, nil, "key", InventoryRequest{Root: "/local/library"}); !errors.Is(err, ErrInventoryChanged) {
		t.Fatalf("nil writer = %v, want ErrInventoryChanged", err)
	}

	remote := &absenceRemote{}
	client := &Client{Remote: remote}
	if err := client.ValidateInventoryAbsenceTx(ctx, new(sql.Tx), "source-verify:source:7", InventoryRequest{Root: "/remote/library"}); err != nil {
		t.Fatalf("remote absence validation = %v", err)
	}
	if len(remote.fenced) != 1 || remote.fenced[0] != "source-verify:source:7" {
		t.Fatalf("remote writer did not receive the captured key: %v", remote.fenced)
	}

	plain := &Client{Remote: &absencePlainRemote{}}
	if err := plain.ValidateInventoryAbsenceTx(ctx, new(sql.Tx), "key", InventoryRequest{Root: "/remote/library"}); !errors.Is(err, ErrRemoteCursor) {
		t.Fatalf("unsupported remote writer = %v, want ErrRemoteCursor", err)
	}
}
