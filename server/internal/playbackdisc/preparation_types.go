package playbackdisc

import (
	"context"
	"database/sql"
	"time"
)

// MaxPreparationLifetime does not extend the shared client's existing wait.
const MaxPreparationLifetime = 120 * time.Second

type PreparationKey struct{ DeviceID, StartKeyDigest string }

// PreparationBinding is the original selection, not a recipe for selecting
// again on a poll. OriginalTokenExpiresMs is the admitted token's actual expiry;
// the monotonic deadline and live token proof remain in Media's original scope.
type PreparationBinding struct {
	Actor                   Actor
	StartBodyDigest, ItemID string
	AssetID                 string
	PartIndex               int
	Source                  Source
	Queue                   Operation
	ReplacesLegacySessionID string
	ReplacesGeneration      int
	OriginalTokenExpiresMs  int64
	DeadlineMs              int64
}

type PreparationClaim struct {
	OperationID, ExecutionID string
	Revision                 int64
}

type PreparationAccepted struct {
	SessionID, SnapshotID string
	Legacy                Key
}

type Preparation struct {
	ID        string
	Key       PreparationKey
	Binding   PreparationBinding
	State     string
	Claim     PreparationClaim
	CreatedMs int64
	Accepted  PreparationAccepted
}

// PreparationLimits must come from the actual composition's finite resource
// budget. There are no defaults, new settings, or permission-bearing booleans.
// Retained bounds include terminal idempotency tombstones; saturation refuses
// admission rather than forgetting an original key and renewing its deadline.
type PreparationLimits struct{ Retained, Active, PerDevice int }

type PreparationTerminal string

const (
	PreparationFailed  PreparationTerminal = "failed"
	PreparationRetired PreparationTerminal = "retired"
)

// PreparationCheck repeats the original actor/token/family/device/horizon,
// source/content/physical selection, queue occurrence/custody and operation
// proof in THIS transaction. Prepared checks additionally retain the exact
// process-private execution/borrow and sealed measurement through outer commit.
// Rows alone cannot reconstruct that process proof after restart.
type PreparationCheck func(context.Context, *sql.Tx, Preparation) error

// PreparationContinuationCheck belongs to the currently admitted original POST
// continuation, not the background worker. It repeats that request's exact
// body/key/device/token and normal actor/content/queue/idempotency/capacity
// admission. A worker's retained input proof alone cannot accept a session.
type PreparationContinuationCheck func(context.Context, *sql.Tx, Preparation) error

// AcceptedPreparationCheck is ordinary accepted-session authority, independent
// of the expired preparation wait. It must reprove the exact accepted result;
// the original preparation rows are never a renewed preparation or byte grant.
type AcceptedPreparationCheck func(context.Context, *sql.Tx, Preparation, PreparationAccepted) error
