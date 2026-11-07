package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Inert private scalar effects on ONE existing commandTurn, no writer opening,
// raw SQL handle/callback or public facade. A desired policy is data, not admin
// authority, storage readiness or retirement capability. The parent owns opaque
// authority, public receipts/lifetimes, events, full admission and COMMIT truth.
// Same-frame defaults integration and actual availability before enabling any
// nonzero retention/storage enforcement remain held at parent composition.
// No recording/layout/physical-debt row or filesystem is touched by these bodies.
func mgDVRPolicyStep(t *commandTurn, id stmt.ID) (statementStep, error) {
	if t == nil || t.transaction == nil {
		return statementStep{}, ErrInvalid
	}
	switch id {
	case mgDVRPolicyDefaultsRead, mgDVRPolicyDefaultsUpdate, mgDVRPolicyStorageRead, mgDVRPolicyStorageUpdate:
	default:
		return statementStep{}, ErrInvalid
	}
	step, ok := t.steps[id]
	if !ok {
		return statementStep{}, ErrInvalid
	}
	return step.statementStep, nil
}
func mgDVRPolicyRow(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) (commandRow, error) {
	step, err := mgDVRPolicyStep(t, id)
	if err != nil {
		return commandRow{}, err
	}
	return t.queryRow(ctx, step, args...)
}
func mgDVRPolicyMissing(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	return err
}
func scanMGDVRDefaults(row commandRow) (mgDVRDefaultsSnapshot, error) {
	var out mgDVRDefaultsSnapshot
	err := row.Scan(&out.revision, &out.beforeSeconds, &out.afterSeconds, &out.retentionDays, &out.episodeLimit, &out.conflict, &out.showTemplate, &out.movieTemplate)
	if err != nil {
		return mgDVRDefaultsSnapshot{}, mgDVRPolicyMissing(err)
	}
	if !out.valid() {
		return mgDVRDefaultsSnapshot{}, ErrInvalid
	}
	return out, nil
}
func scanMGDVRStorage(row commandRow) (mgDVRStorageSnapshot, error) {
	var out mgDVRStorageSnapshot
	err := row.Scan(&out.revision, &out.policy.retentionDays, &out.policy.episodeLimit, &out.policy.floorBytes, &out.policy.capBytes)
	if err != nil {
		return mgDVRStorageSnapshot{}, mgDVRPolicyMissing(err)
	}
	if !out.valid() {
		return mgDVRStorageSnapshot{}, ErrInvalid
	}
	return out, nil
}

// Missing singleton rows are errors. Never synthesize defaults, a revision or a
// fallback layout when the actual policy was not observed on this same turn.
func readMGDVRDefaults(ctx context.Context, t *commandTurn) (out mgDVRDefaultsSnapshot, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	row, err := mgDVRPolicyRow(ctx, t, mgDVRPolicyDefaultsRead, int64(1))
	if err != nil {
		return out, err
	}
	return scanMGDVRDefaults(row)
}
func readMGDVRStorage(ctx context.Context, t *commandTurn) (out mgDVRStorageSnapshot, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	row, err := mgDVRPolicyRow(ctx, t, mgDVRPolicyStorageRead, int64(1))
	if err != nil {
		return out, err
	}
	return scanMGDVRStorage(row)
}

// Exact observed state CAS, actual RETURNING and independent point readback.
// Every error after an UPDATE requires whole-frame rollback by the parent.
// Templates are preserved: current public settings do not enable template edits.
func setMGDVRDefaults(ctx context.Context, t *commandTurn, before mgDVRDefaultsSnapshot, desired mgDVRDefaultsDesired) (out mgDVRDefaultsSnapshot, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	// Revalidate inside the boundary; calling the freezer is not a capability.
	if !before.valid() || before.revision == math.MaxInt64 || !desired.valid() {
		return out, ErrInvalid
	}
	current, err := readMGDVRDefaults(ctx, t)
	if err != nil {
		return out, err
	}
	if current != before {
		return out, ErrConflict
	}
	args := append([]any{desired.beforeSeconds, desired.afterSeconds, desired.retentionDays, desired.episodeLimit, desired.conflict, int64(1)}, before.values()...)
	row, err := mgDVRPolicyRow(ctx, t, mgDVRPolicyDefaultsUpdate, args...)
	if err != nil {
		return out, err
	}
	changed, err := scanMGDVRDefaults(row)
	if err != nil {
		return out, err
	}
	want := before
	want.revision++
	want.beforeSeconds = desired.beforeSeconds
	want.afterSeconds = desired.afterSeconds
	want.retentionDays = desired.retentionDays
	want.episodeLimit = desired.episodeLimit
	want.conflict = desired.conflict
	if changed != want {
		return out, ErrConflict
	}
	out, err = readMGDVRDefaults(ctx, t)
	if err != nil {
		return mgDVRDefaultsSnapshot{}, err
	}
	if out != changed {
		return mgDVRDefaultsSnapshot{}, ErrConflict
	}
	return out, nil
}
func setMGDVRStorage(ctx context.Context, t *commandTurn, before mgDVRStorageSnapshot, desired mgDVRStorageDesired) (out mgDVRStorageSnapshot, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if !before.valid() || before.revision == math.MaxInt64 || !desired.valid() {
		return out, ErrInvalid
	}
	current, err := readMGDVRStorage(ctx, t)
	if err != nil {
		return out, err
	}
	if current != before {
		return out, ErrConflict
	}
	args := append(desired.values(), int64(1))
	args = append(args, before.values()...)
	row, err := mgDVRPolicyRow(ctx, t, mgDVRPolicyStorageUpdate, args...)
	if err != nil {
		return out, err
	}
	changed, err := scanMGDVRStorage(row)
	if err != nil {
		return out, err
	}
	if changed != (mgDVRStorageSnapshot{before.revision + 1, desired}) {
		return out, ErrConflict
	}
	out, err = readMGDVRStorage(ctx, t)
	if err != nil {
		return mgDVRStorageSnapshot{}, err
	}
	if out != changed {
		return mgDVRStorageSnapshot{}, ErrConflict
	}
	return out, nil
}
