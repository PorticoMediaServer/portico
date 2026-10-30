// Package recordingroot composes owner folder admission with the recording
// lifecycle. It does not restart or rebind a running recording driver.
package recordingroot

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels/dvr"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
)

// Runtime captures the process's actual effective binding once. A requested
// destination remains inert until separately reviewed startup activation.
func Runtime(admin *administration.Service, recordings *dvr.Store, effective string, operatorControlled bool) (*operations.RecordingRootRuntime, error) {
	if admin == nil || recordings == nil || !validRootPath(effective) { return nil, errors.New("recording root admission unavailable") }
	identity := ""
	var err error
	if effective != "" {
		identity, err = storagepaths.RecordingRootIdentity(effective)
		if err != nil { return nil, err }
	}
	return &operations.RecordingRootRuntime{
		Effective: effective, Identity: identity, OperatorControlled: operatorControlled,
		Admit: func(ctx context.Context, tx *sql.Tx, old, destination string) (string, func(), error) {
			if old != effective { return "", nil, errors.New("recording root binding changed") }
			release, err := recordings.AdmitRootChange(ctx, tx)
			if err != nil {
				if errors.Is(err, dvr.ErrConflict) { return "", nil, operations.ErrConflict }
				return "", nil, err
			}
			accepted := false
			defer func() { if !accepted { release() } }()
			if old != "" {
				current, err := storagepaths.EmptyRecordingRootIdentity(old)
				if err != nil {
					if errors.Is(err, storagepaths.ErrRecordingFolderNotEmpty) { return "", nil, operations.ErrConflict }
					return "", nil, err
				}
				if current != identity { return "", nil, operations.ErrConflict }
			}
			chosen, err := admin.RecordingDestination(destination)
			if err != nil {
				if errors.Is(err, storagepaths.ErrRecordingFolderNotEmpty) { return "", nil, operations.ErrConflict }
				if errors.Is(err, administration.ErrDenied) || errors.Is(err, administration.ErrInput) { return "", nil, operations.ErrInvalid }
				return "", nil, err
			}
			if err = ctx.Err(); err != nil { return "", nil, err }
			accepted = true
			return chosen, release, nil
		},
	}, nil
}

func validRootPath(path string) bool {
	return path == "" || operations.SafeText(path, 1024) && !strings.ContainsAny(path, "\n\t") && filepath.IsAbs(path) && filepath.Clean(path) == path
}
