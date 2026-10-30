package mounts

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/rcloneprogram"
)

type RcloneSourceEditInput struct {
	OperationID        string `json:"operationId"`
	ExpectedGeneration int64  `json:"expectedGeneration"`
	Name               string `json:"name"`
	Remote             string `json:"remote"`
	Config             string `json:"config"`
}

// importedSourceControlTx binds the public source generation to its actual
// backend and root, and captures the internal control CAS in that same read.
// The accepting configuration writer repeats the complete association check.
func importedSourceControlTx(ctx context.Context, tx *sql.Tx, id string, expected int64) (int64, error) {
	var generation, control int64
	err := tx.QueryRowContext(ctx, `SELECT r.generation,c.revision FROM remote_sources r
 JOIN managed_mounts m ON m.id=r.mount_id AND m.id=r.id AND m.mount_path=r.root
 JOIN mount_backend_configs b ON b.mount_id=m.id AND b.generation=r.generation
 JOIN mount_controls c ON c.mount_id=m.id AND c.removal_pending=0
 WHERE r.id=? AND r.removed=0 AND r.kind IN('rclone','sftp','smb')`, id).Scan(&generation, &control)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (generation != expected || control < 1) {
		return 0, ErrCommandConflict
	}
	return control, err
}

// EditRcloneSource requires complete replacement connection material. It never
// returns stored secrets or treats an earlier unsaved Test as save authority.
func (s *Service) EditRcloneSource(ctx context.Context, actor, id string, input RcloneSourceEditInput, authorize func(*sql.Tx) error) (RcloneSourceReceipt, error) {
	if actor == "" {
		return RcloneSourceReceipt{}, identity.ErrUnauthorized
	}
	if err := s.rcloneSourceOwner(ctx, authorize); err != nil {
		return RcloneSourceReceipt{}, err
	}
	if id == "" || input.ExpectedGeneration < 1 {
		return RcloneSourceReceipt{}, ErrInvalid
	}
	if strings.TrimSpace(input.Name) == "" || len(input.Name) > 100 || validateConfig(input.Config, input.Remote) != nil {
		return RcloneSourceReceipt{}, ErrConfigInvalid
	}
	program, err := rcloneprogram.Resolve()
	if err != nil {
		return RcloneSourceReceipt{}, ErrExecutableInvalid
	}
	receipt, err := s.Command(ctx, actor, id, Command{OperationID: input.OperationID, Action: "configure", sourceGeneration: input.ExpectedGeneration,
		Name: input.Name, Executable: program.Path, Remote: input.Remote, Config: input.Config, UseIncluded: boolPointer(true)}, authorize)
	if err != nil {
		return RcloneSourceReceipt{}, err
	}
	return sourceReceipt(receipt)
}
