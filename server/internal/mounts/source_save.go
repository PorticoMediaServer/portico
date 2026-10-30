package mounts

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/rcloneprogram"
)

// RcloneSourceInput accepts connection material, never a process, mount action,
// executable path or a prior unsaved Test result as authority to save.
type RcloneSourceInput struct {
	OperationID string `json:"operationId"`
	Name        string `json:"name"`
	Remote      string `json:"remote"`
	Config      string `json:"config"`
}

// RcloneSourceReceipt projects only the durable source identity. The private
// allocation and stopped legacy mount record are not a public lifecycle.
type RcloneSourceReceipt struct {
	OperationID string `json:"operationId"`
	Accepted    bool   `json:"accepted"`
	SourceID    string `json:"sourceId"`
	Generation  int64  `json:"generation"`
	Removed     bool   `json:"removed,omitempty"`
}

func sourceReceipt(receipt Receipt) (RcloneSourceReceipt, error) {
	if !receipt.Accepted || receipt.Mount == nil || receipt.MountID == "" || receipt.Mount.ID != receipt.MountID || receipt.Mount.BackendRevision < 1 || receipt.Deleted != receipt.Mount.RemovalPending {
		return RcloneSourceReceipt{}, ErrCommandConflict
	}
	return RcloneSourceReceipt{OperationID: receipt.OperationID, Accepted: true, SourceID: receipt.MountID, Generation: receipt.Mount.BackendRevision, Removed: receipt.Deleted}, nil
}

func (s *Service) rcloneSourceOwner(ctx context.Context, authorize func(*sql.Tx) error) error {
	if s == nil || authorize == nil {
		return identity.ErrUnauthorized
	}
	snapshot, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return err
	}
	defer snapshot.Rollback()
	return authorize(snapshot.Tx())
}

// RcloneSourceOperation recovers a durable receipt without rerunning the save,
// resolving a new executable or interpreting a test result as approval.
func (s *Service) RcloneSourceOperation(ctx context.Context, actor, operation string, authorize func(*sql.Tx) error) (RcloneSourceReceipt, error) {
	if actor == "" {
		return RcloneSourceReceipt{}, identity.ErrUnauthorized
	}
	if err := s.rcloneSourceOwner(ctx, authorize); err != nil {
		return RcloneSourceReceipt{}, err
	}
	receipt, err := s.Receipt(actor, operation)
	if err != nil {
		return RcloneSourceReceipt{}, err
	}
	if err = s.rcloneSourceOwner(ctx, authorize); err != nil {
		return RcloneSourceReceipt{}, err
	}
	return sourceReceipt(receipt)
}

// SaveRcloneSource uses the existing owner-save transaction and candidate test:
// configuration, endpoint approvals, observed range facts and receipt publish
// together. No result from TestRcloneConnection skips that independent test.
func (s *Service) SaveRcloneSource(ctx context.Context, actor string, input RcloneSourceInput, authorize func(*sql.Tx) error) (RcloneSourceReceipt, error) {
	if s == nil || authorize == nil || actor == "" {
		return RcloneSourceReceipt{}, identity.ErrUnauthorized
	}
	if err := s.rcloneSourceOwner(ctx, authorize); err != nil {
		return RcloneSourceReceipt{}, err
	}
	if err := validateConfig(input.Config, input.Remote); err != nil {
		return RcloneSourceReceipt{}, ErrConfigInvalid
	}
	program, err := rcloneprogram.Resolve()
	if err != nil {
		return RcloneSourceReceipt{}, ErrExecutableInvalid
	}
	receipt, err := s.Command(ctx, actor, "", Command{OperationID: input.OperationID, Action: "create", Name: input.Name,
		Executable: program.Path, Remote: input.Remote, Config: input.Config, UseIncluded: boolPointer(true)}, authorize)
	if err != nil {
		return RcloneSourceReceipt{}, err
	}
	return sourceReceipt(receipt)
}

// RcloneConfigSourceInput saves one remote of the server account's own rclone
// configuration. The configuration itself never arrives from a client.
type RcloneConfigSourceInput struct {
	OperationID string
	Name        string
	Remote      string
}

// accountConfigReceiptKey marks a receipt whose identity is the posted remote
// name rather than configuration content, so a retry can be answered from the
// durable receipt before the mutable configuration is read again.
const accountConfigReceiptKey = "account-config"

// rcloneConfigSourceReceipt looks up the durable outcome of this exact request
// without reading the configuration. It reports found=false when the operation
// has no receipt yet.
func (s *Service) rcloneConfigSourceReceipt(actor string, input RcloneConfigSourceInput, program rcloneprogram.Program) (RcloneSourceReceipt, bool, error) {
	if s == nil || actor == "" {
		return RcloneSourceReceipt{}, false, identity.ErrUnauthorized
	}
	if input.OperationID == "" || strings.TrimSpace(input.Name) == "" || !configSectionName(input.Remote) {
		return RcloneSourceReceipt{}, false, ErrConfigInvalid
	}
	identityCommand := Command{OperationID: input.OperationID, Action: "create", Name: input.Name, Executable: program.Path, Remote: input.Remote + ":", receiptKey: accountConfigReceiptKey}
	hash := s.fingerprint(actor, "", identityCommand)
	s.commands.Lock()
	existing, err := s.receipt(s.db, actor, input.OperationID, hash)
	s.commands.Unlock()
	switch {
	case err == nil:
		receipt, err := sourceReceipt(existing)
		return receipt, true, err
	case errors.Is(err, sql.ErrNoRows):
		if freshOperation(input.OperationID) != nil {
			return RcloneSourceReceipt{}, false, ErrCommandExpired
		}
		return RcloneSourceReceipt{}, false, nil
	default:
		return RcloneSourceReceipt{}, false, err
	}
}

// SaveRcloneConfigSource saves one remote of the account configuration. The
// durable receipt is consulted before the mutable configuration is read, so a
// retry is answered when the file has changed or disappeared since; a different
// body for the same operation still conflicts.
func (s *Service) SaveRcloneConfigSource(ctx context.Context, actor string, input RcloneConfigSourceInput, authorize func(*sql.Tx) error) (RcloneSourceReceipt, error) {
	if s == nil || authorize == nil {
		return RcloneSourceReceipt{}, identity.ErrUnauthorized
	}
	if err := s.rcloneSourceOwner(ctx, authorize); err != nil {
		return RcloneSourceReceipt{}, err
	}
	program, err := rcloneprogram.Resolve()
	if err != nil {
		return RcloneSourceReceipt{}, ErrExecutableInvalid
	}
	if receipt, found, err := s.rcloneConfigSourceReceipt(actor, input, program); err != nil || found {
		return receipt, err
	}
	section, err := ReadAccountRcloneSection(input.Remote)
	if err != nil {
		return RcloneSourceReceipt{}, err
	}
	if err = validateConfig(section, input.Remote+":"); err != nil {
		return RcloneSourceReceipt{}, ErrConfigInvalid
	}
	receipt, err := s.Command(ctx, actor, "", Command{OperationID: input.OperationID, Action: "create", Name: input.Name, Executable: program.Path,
		Remote: input.Remote + ":", Config: section, receiptKey: accountConfigReceiptKey, UseIncluded: boolPointer(true)}, authorize)
	if err != nil {
		return RcloneSourceReceipt{}, err
	}
	return sourceReceipt(receipt)
}
