package recordingroot

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels/dvr"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
)

// StartupBinding remains unpublished and has no workers until Publish. Verify
// checks the constructed driver's own pinned identity. Publish only installs
// already-constructed bindings; it must not perform filesystem or database work.
type StartupBinding struct {
	Rollback func()
	Verify func() error
	Publish func()
}

func activationChoice(request operations.RecordingRootRequest, operator string) (string, bool, error) {
	chosen := operator
	if request.Path != "" {
		if operator != "" && operator != request.Path { return "", false, errors.New("recording folder is operator controlled") }
		chosen = request.Path
	}
	// An absent owner request never authorizes dropping or replacing a root
	// accepted on an earlier startup, even when the new choice is empty.
	if request.Path == "" && request.AppliedPath != "" && chosen != request.AppliedPath {
		return "", false, errors.New("accepted recording folder cannot be dropped or replaced")
	}
	pending := request.Path != "" && (chosen != request.AppliedPath || request.FolderIdentity != request.AppliedIdentity)
	return chosen, pending, nil
}

func authorizePending(ctx context.Context, tx *sql.Tx, ident *identity.Service, request operations.RecordingRootRequest) error {
	var actor []string
	if json.Unmarshal([]byte(request.RequestedBy), &actor) != nil || len(actor) != 3 { return identity.ErrForbidden }
	if err := ident.AuthorizeRecordingRootOwnerTx(ctx, tx, actor[0], actor[1], actor[2], request.RequestedEpoch); err != nil { return err }
	return dvr.RetainedRootReferences(ctx, tx)
}

// Only one directory entry is requested, never a recursive tree walk.
func pendingFolders(admin *administration.Service, request operations.RecordingRootRequest, chosen string) (string, error) {
	if request.AppliedPath != "" {
		old, err := storagepaths.EmptyRecordingRootIdentity(request.AppliedPath)
		if err != nil || old != request.AppliedIdentity { return "", errors.New("previous recording folder contains content or changed") }
	}
	identity, err := admin.RecordingDestination(chosen)
	if err != nil { return "", err }
	if identity != request.FolderIdentity { return "", errors.New("requested recording folder changed") }
	return identity, nil
}

// Activate captures private pins, constructs/measures an unpublished driver
// outside SQLite writer occupancy, then rechecks everything in a short accepting
// transaction. It runs once before recording workers are started. No move,
// deletion or live rebind is performed.
func Activate(ctx context.Context, db *sql.DB, admin *administration.Service, ident *identity.Service, data, operator string, bind func(string) (StartupBinding, error)) (err error) {
	if db == nil || admin == nil || ident == nil || bind == nil || !validRootPath(operator) { return errors.New("recording root activation unavailable") }
	read, err := dbwork.BeginSnapshot(ctx, db)
	if err != nil { return err }
	request, err := operations.RecordingRootRequestTx(read.Tx())
	if err != nil { read.Rollback(); return err }
	chosen, pending, err := activationChoice(request, operator)
	if err == nil && pending { err = authorizePending(ctx, read.Tx(), ident, request) }
	read.Rollback()
	if err != nil { return err }

	expectedIdentity := request.AppliedIdentity
	if chosen != "" {
		if err = storagepaths.At(data).ValidateRecordings(chosen); err != nil { return err }
	}
	if pending {
		expectedIdentity, err = pendingFolders(admin, request, chosen)
		if err != nil { return err }
	} else if chosen != "" {
		current, e := storagepaths.RecordingRootIdentity(chosen)
		if e != nil {
			if request.AppliedPath != "" || !errors.Is(e, os.ErrNotExist) { return e }
		} else {
			if request.AppliedPath != "" && (chosen != request.AppliedPath || current != request.AppliedIdentity) { return errors.New("effective recording folder changed") }
			expectedIdentity = current
		}
	}
	// Existing accepted roots may contain arbitrary retained content. New's
	// measurement/tree walk and decoder resolution must not hold a DB writer.
	binding, err := bind(chosen)
	if err != nil { if binding.Rollback != nil { binding.Rollback() }; return err }
	if binding.Rollback == nil || binding.Verify == nil || binding.Publish == nil {
		if binding.Rollback != nil { binding.Rollback() }
		return errors.New("recording root binding contract unavailable")
	}
	defer func() { if err != nil { binding.Rollback() } }()
	if err = ctx.Err(); err != nil { return err }

	gated, err := dbwork.Begin(ctx, db, dbwork.ClassInteractive)
	if err != nil { return err }
	defer gated.Rollback()
	tx := gated.Tx()
	current, err := operations.RecordingRootRequestTx(tx)
	if err != nil { return err }
	if current != request { return operations.ErrConflict }
	finalChoice, finalPending, err := activationChoice(current, operator)
	if err != nil { return err }
	if finalChoice != chosen || finalPending != pending { return operations.ErrConflict }
	if pending {
		if err = authorizePending(ctx, tx, ident, current); err != nil { return err }
		if _, err = pendingFolders(admin, current, chosen); err != nil { return err }
	}
	actualIdentity := ""
	if chosen != "" {
		if err = storagepaths.At(data).ValidateRecordings(chosen); err != nil { return err }
		if pending { actualIdentity, err = admin.RecordingRootIdentity(chosen) } else { actualIdentity, err = storagepaths.RecordingRootIdentity(chosen) }
		if err != nil { return err }
		if expectedIdentity != "" && actualIdentity != expectedIdentity { return errors.New("recording folder changed while binding") }
	}
	if err = binding.Verify(); err != nil { return err }
	result, err := tx.ExecContext(ctx, `UPDATE recording_root_request SET applied_path=?,applied_identity=? WHERE singleton=1 AND revision=? AND path=? AND folder_identity=? AND requested_by=? AND requested_epoch=? AND applied_path=? AND applied_identity=?`, chosen, actualIdentity, request.Revision, request.Path, request.FolderIdentity, request.RequestedBy, request.RequestedEpoch, request.AppliedPath, request.AppliedIdentity)
	if err != nil { return err }
	changed, err := result.RowsAffected()
	if err != nil { return err }
	if changed != 1 { return operations.ErrConflict }
	if err = gated.Commit(); err != nil { return err }
	binding.Publish()
	return nil
}
