package main

import (
	"context"
	"fmt"
	"os"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/backup"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// signOutAfterRestore runs once on the first start after a restore was
// applied. Everyone is signed out of the restored database, and the copies of
// the previous database that restores set aside ("pre-restore" backups) have
// their sessions ended as well: those copies are the live database of a moment
// ago moved into the backups folder, so they never went through the scrub a
// written backup gets.
//
// A sign-out that cannot be done stops the start. Serving from a restored
// database with its old sessions intact is the thing this exists to prevent.
func signOutAfterRestore(ctx context.Context, ident *identity.Service, state string) error {
	result, err := backup.ReadRestoreResult(state)
	if err != nil {
		return fmt.Errorf("read restore result: %w", err)
	}
	if result == nil || result.Outcome == "rolled_back" {
		return nil
	}
	if result.Outcome != "restored" {
		return fmt.Errorf("unrecognized restore outcome: %s", result.Outcome)
	}
	if _, err = time.Parse(time.RFC3339Nano, result.At); err != nil {
		return fmt.Errorf("invalid restore time: %w", err)
	}
	_, err = ident.SignEveryoneOutAfterRestore(ctx, result.At)
	if err != nil {
		return err
	}
	// A previous start may have signed the live database out before a copy
	// scrub failed. Retry those copies without ending any newer sign-ins.
	entries, err := os.ReadDir(storagepaths.Path(state, backup.BackupsDir))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasSuffix(entry.Name(), "-pre-restore") {
			continue
		}
		folder := storagepaths.Path(state, backup.BackupsDir, entry.Name())
		if err = backup.ScrubPreRestoreCopy(ctx, folder, identity.ScrubBackupCopy); err != nil {
			return fmt.Errorf("remove sign-ins from pre-restore backup %s: %w", entry.Name(), err)
		}
	}
	return nil
}
