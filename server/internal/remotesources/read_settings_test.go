package remotesources

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestSourceReadSettingsOwnerCommandAndExactReceipt(t *testing.T) {
	ctx := context.Background()
	f, _, identities, owner, family, _ := nativeSaveFixture(t)
	before, err := f.s.ReadSourceReadSettings(ctx, identities, owner, "remote")
	if err != nil || before.Saved || before.Generation != "1" || before.Revision != 0 || before.Values.ScanDepth != "file_list_only" {
		t.Fatal("missing saved profile did not stay list-only", before, err)
	}
	complete, _ := SourceReadPreset("everything")
	input := SourceReadSettingsChange{OperationID: nativeSaveOperation(600), ExpectedGeneration: before.Generation, ExpectedRevision: before.Revision, Values: sourceReadValues(complete)}
	first, err := f.s.SaveSourceReadSettings(ctx, identities, owner, "remote", input)
	if err != nil || first.ReadSettings == nil || first.ReadSettings.Revision != 1 || !first.ReadSettings.Saved || !reflect.DeepEqual(first.ReadSettings.Values, input.Values) || first.Source.Generation != 1 {
		t.Fatal("owner settings publication", first, err)
	}
	policy, err := catalog.New(f.s.db).ScanPolicy(ctx, f.library)
	if err != nil || policy.Revision != 2 {
		t.Fatal("affected library authority was not advanced", policy, err)
	}
	replay, err := f.s.SaveSourceReadSettings(ctx, identities, owner, "remote", input)
	if err != nil || replay.ReadSettings == nil || !reflect.DeepEqual(*replay.ReadSettings, *first.ReadSettings) {
		t.Fatal("exact command receipt was not recovered", replay, err)
	}
	policy, err = catalog.New(f.s.db).ScanPolicy(ctx, f.library)
	if err != nil || policy.Revision != 2 {
		t.Fatal("receipt replay changed library authority again", policy, err)
	}
	changed := input
	changed.Values.Connections--
	if _, err = f.s.SaveSourceReadSettings(ctx, identities, owner, "remote", changed); !errors.Is(err, mounts.ErrCommandConflict) {
		t.Fatal("same operation accepted different values", err)
	}
	changed.OperationID = nativeSaveOperation(601)
	if _, err = f.s.SaveSourceReadSettings(ctx, identities, owner, "remote", changed); !errors.Is(err, storage.ErrRemoteChanged) {
		t.Fatal("new stale CAS command accepted", err)
	}
	changed.ExpectedRevision = 1
	changed.ExpectedGeneration = "2"
	if _, err = f.s.SaveSourceReadSettings(ctx, identities, owner, "remote", changed); !errors.Is(err, storage.ErrRemoteChanged) {
		t.Fatal("wrong source generation accepted", err)
	}
	if _, err = f.s.db.Exec(`UPDATE authorization_session_families SET revoked=1 WHERE id=?`, family); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.SaveSourceReadSettings(ctx, identities, owner, "remote", input); err == nil {
		t.Fatal("revoked owner recovered a command through save")
	}
	if _, err = f.s.ReadSourceReadSettings(ctx, identities, owner, "remote"); err == nil {
		t.Fatal("revoked owner read source settings")
	}
}

func TestSourceReadSettingsRejectMissingAuthorityAndPartialValues(t *testing.T) {
	ctx := context.Background()
	f, _, identities, owner, _, _ := nativeSaveFixture(t)
	values, _ := SourceReadPreset("list")
	input := SourceReadSettingsChange{OperationID: nativeSaveOperation(610), ExpectedGeneration: "1", Values: sourceReadValues(values)}
	for _, actor := range []identity.Principal{{}, func() identity.Principal { forged := owner; forged.Epoch++; return forged }()} {
		if _, err := f.s.ReadSourceReadSettings(ctx, identities, actor, "remote"); err == nil {
			t.Fatal("unknown/forged authority read settings")
		}
		if _, err := f.s.SaveSourceReadSettings(ctx, identities, actor, "remote", input); err == nil {
			t.Fatal("unknown/forged authority saved settings")
		}
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err = json.Unmarshal(encoded, &raw); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"operationId", "expectedGeneration", "expectedRevision", "values"} {
		for _, missing := range []bool{true, false} {
			var body map[string]json.RawMessage
			_ = json.Unmarshal(encoded, &body)
			if missing {
				delete(body, field)
			} else {
				body[field] = json.RawMessage(`null`)
			}
			bytes, _ := json.Marshal(body)
			var got SourceReadSettingsChange
			if json.Unmarshal(bytes, &got) == nil {
				t.Fatal("partial/null command accepted", field, missing)
			}
		}
	}
	for _, field := range []string{"scanDepth", "wholeFileReads", "connections", "requestsPerSecond", "bytesPerSecond"} {
		var body map[string]json.RawMessage
		_ = json.Unmarshal(raw["values"], &body)
		delete(body, field)
		bytes, _ := json.Marshal(body)
		var got SourceReadValues
		if json.Unmarshal(bytes, &got) == nil {
			t.Fatal("partial settings value accepted", field)
		}
	}
}

func TestSourceReadSettingsReceiptSurvivesActualSourceRemoval(t *testing.T) {
	ctx := context.Background()
	f, _, identities, owner, _, _ := nativeSaveFixture(t)
	profile, _ := SourceReadPreset("once")
	input := SourceReadSettingsChange{OperationID: nativeSaveOperation(630), ExpectedGeneration: "1", Values: sourceReadValues(profile)}
	accepted, err := f.s.SaveSourceReadSettings(ctx, identities, owner, "remote", input)
	if err != nil {
		t.Fatal(err)
	}
	actor, _ := json.Marshal([]string{owner.Authority, owner.AccountID, owner.ProfileID})
	_, err = f.s.RemoveDAV(ctx, string(actor), "remote", nativeSaveOperation(631), 1, func(tx *sql.Tx) error { return currentLocalOwnerTx(ctx, tx, identities, owner) })
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := f.s.Receipt(ctx, string(actor), input.OperationID)
	if err != nil || recovered.ReadSettings == nil || !reflect.DeepEqual(*recovered.ReadSettings, *accepted.ReadSettings) {
		t.Fatal("source removal hid the accepted profile receipt", recovered, err)
	}
	if _, err = f.s.ReadSourceReadSettings(ctx, identities, owner, "remote"); err == nil {
		t.Fatal("receipt cleanup revived the removed source")
	}
}

func TestSourceReadSettingsRetirementSurvivesRequestCancelAndReceiptReplay(t *testing.T) {
	ctx := context.Background()
	f, _, identities, owner, _, _ := nativeSaveFixture(t)
	ref, err := f.record(t, scanFacts("settings-retirement"), 0)
	if err != nil {
		t.Fatal(err)
	}
	complete, _ := SourceReadPreset("everything")
	input := SourceReadSettingsChange{OperationID: nativeSaveOperation(620), ExpectedGeneration: "1", Values: sourceReadValues(complete)}
	accepted, err := f.s.SaveSourceReadSettings(ctx, identities, owner, "remote", input)
	if err != nil {
		t.Fatal(err)
	}
	// A real committed sole-writer update with its notification missed models
	// the post-commit failure boundary. This test opens no provider connection.
	commitWithoutNotification := func(revision int64, preset string) int64 {
		t.Helper()
		profile, _ := SourceReadPreset(preset)
		writer, err := dbwork.Begin(ctx, f.s.db, dbwork.ClassInteractive)
		if err != nil {
			t.Fatal(err)
		}
		defer writer.Rollback()
		out, err := f.s.ApplySourceReadProfileTx(ctx, writer.Tx(), identities, owner, SourceReadProfileChange{SourceID: "remote", Generation: "1", ExpectedRevision: revision, Profile: profile})
		if err != nil {
			t.Fatal(err)
		}
		if err = writer.Commit(); err != nil {
			t.Fatal(err)
		}
		return out.Profile.Revision
	}
	old, err := f.s.reserveSourceProcess(ctx, "remote", "1", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Release()
	reader := captureOwnerSource(t, f, ref, identities, owner, SourceReading, func() {})
	defer reader.Close()
	var revision int64
	if err = dbwork.WithReadSnapshot(ctx, f.s.db, func(stale context.Context) error {
		before, err := SourceReadProfileTx(stale, dbwork.Snapshot(stale), "remote")
		if err != nil || before.Revision != 1 {
			t.Fatal("failed to pin the request's old profile", before, err)
		}
		revision = commitWithoutNotification(1, "list")
		canceled, cancel := context.WithCancel(stale)
		cancel()
		return f.s.finishReadSettings(canceled, "remote", revision)
	}); err != nil {
		t.Fatal("request cancellation/stale snapshot prevented committed cleanup", err)
	}
	if old.CheckLocal() == nil || reader.life.Err() == nil {
		t.Fatal("committed cleanup left old process/reader alive")
	}
	if _, err = f.s.reserveSourceProcess(ctx, "remote", "1", revision); !errors.Is(err, storage.ErrBusy) {
		t.Fatal("cancellation released physical custody before Wait", err)
	}
	old.Release()
	second, err := f.s.reserveSourceProcess(ctx, "remote", "1", revision)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Release()
	secondReader := captureOwnerSource(t, f, ref, identities, owner, SourceReading, func() {})
	defer secondReader.Close()
	revision = commitWithoutNotification(revision, "everything")
	actor, _ := json.Marshal([]string{owner.Authority, owner.AccountID, owner.ProfileID})
	replayed, err := f.s.Receipt(ctx, string(actor), input.OperationID)
	if err != nil || replayed.ReadSettings == nil || !reflect.DeepEqual(*replayed.ReadSettings, *accepted.ReadSettings) {
		t.Fatal("receipt replay failed after missed notification", replayed, err)
	}
	if second.CheckLocal() == nil || secondReader.life.Err() == nil {
		t.Fatal("receipt replay did not retire obsolete process/reader")
	}
	second.Release()
	current, err := f.s.reserveSourceProcess(ctx, "remote", "1", revision)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Release()
	if _, err = f.s.SaveSourceReadSettings(ctx, identities, owner, "remote", input); err != nil {
		t.Fatal(err)
	}
	if err = current.CheckLocal(); err != nil {
		t.Fatal("old receipt retired current replacement", err)
	}
	actual, err := f.s.ReadSourceReadSettings(ctx, identities, owner, "remote")
	if err != nil || actual.Revision != revision || actual.Generation != "1" {
		t.Fatal("cleanup/recovery wrote authority or source identity", actual, err)
	}
}
