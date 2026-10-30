package mounts

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

var ErrSourceInUse = errors.New("stop current source playback and wait for its process before removal")

type RcloneSourceRemoveInput struct {
	OperationID        string `json:"operationId"`
	ExpectedGeneration int64  `json:"expectedGeneration"`
}

// RemoveRcloneSource publishes logical source removal and its actor receipt
// together. Private file cleanup remains the existing guarded removal worker;
// this result makes no claim that those bytes have already been erased.
func (s *Service) RemoveRcloneSource(ctx context.Context, actor, id string, input RcloneSourceRemoveInput, authorize func(*sql.Tx) error) (RcloneSourceReceipt, error) {
	if actor == "" {
		return RcloneSourceReceipt{}, identity.ErrUnauthorized
	}
	if err := s.rcloneSourceOwner(ctx, authorize); err != nil {
		return RcloneSourceReceipt{}, err
	}
	if id == "" || input.ExpectedGeneration < 1 {
		return RcloneSourceReceipt{}, ErrInvalid
	}
	receipt, err := s.Command(ctx, actor, id, Command{OperationID: input.OperationID, Action: "delete", sourceGeneration: input.ExpectedGeneration}, authorize)
	if err != nil {
		return RcloneSourceReceipt{}, err
	}
	return sourceReceipt(receipt)
}
