package catalog

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
)

// A protocol batch accepts all successful fields and its tiny receipt in one
// durable transaction. Savepoints preserve the existing per-field partial
// outcome semantics without leaving a committed prefix without a receipt.
func (s *Service) setPersonalBatchProtocol(account, profile string, m PersonalBatchMutation, authorize func(*sql.Tx, string) error) (PersonalBatchReceipt, error) {
	out := PersonalBatchReceipt{OperationID: m.OperationID, Results: []PersonalBatchResult{}, ReceiptLifetimeMs: usermutation.RetentionMs}
	gated, err := dbwork.Begin(s.Context(), s.db, dbwork.ClassFrom(s.Context(), dbwork.ClassInteractive))
	if err != nil {
		return out, err
	}
	defer gated.Rollback()
	ctx, tx := gated.Context(), gated.Tx()
	scoped := s.WithContext(ctx)
	prior, err := usermutation.CheckContext(ctx, tx, time.Now().UnixMilli())
	if err != nil {
		return out, err
	}
	if prior != nil {
		// Historical per-item success/failure arrays are deliberately not retained.
		// A caller refreshes current state; an empty result is not a failed intent.
		return out, nil
	}
	for _, item := range m.Items {
		var state PersonalState
		var failure error
		expected := int64(-1)
		if item.ExpectedRevision != nil {
			expected = *item.ExpectedRevision
		}
		if authorize != nil {
			failure = authorize(tx, item.ItemID)
			if failure != nil && !personalBatchDomainFailure(failure) {
				return out, failure
			}
		}
		authorized := failure == nil
		for _, field := range personalBatchFields(item) {
			if failure != nil {
				break
			}
			if expected < 0 {
				state, failure = readPersonal(tx, profile, item.ItemID)
				if failure != nil {
					break
				}
				expected = state.Revision
			}
			mutation := field.mutation
			mutation.OperationID = m.OperationID + "-" + operationHash([]string{item.ItemID, field.name})[:16]
			mutation.ExpectedRevision = expected
			if _, err = tx.ExecContext(ctx, `SAVEPOINT personal_protocol_field`); err != nil {
				return out, err
			}
			state, failure = scoped.applyPersonal(account, profile, item.ItemID, mutation, nil, false, tx)
			if failure != nil {
				if _, err = tx.ExecContext(ctx, `ROLLBACK TO personal_protocol_field`); err != nil {
					return out, err
				}
			}
			if _, err = tx.ExecContext(ctx, `RELEASE personal_protocol_field`); err != nil {
				return out, err
			}
			if failure != nil {
				if !personalBatchDomainFailure(failure) {
					return out, failure
				}
				break
			}
			expected = state.Revision
		}
		if failure != nil {
			if !personalBatchDomainFailure(failure) {
				return out, failure
			}
			row := PersonalBatchResult{ItemID: item.ItemID, Code: personalFailureCode(failure), Message: failure.Error()}
			if authorized && !errors.Is(failure, sql.ErrNoRows) {
				if current, e := readPersonal(tx, profile, item.ItemID); e == nil {
					row.Personal = &current
				} else {
					return out, e
				}
			}
			out.Results = append(out.Results, row)
			out.Failed++
		} else {
			current := state
			out.Results = append(out.Results, PersonalBatchResult{ItemID: item.ItemID, OK: true, Personal: &current})
			out.Updated++
		}
	}
	status := "applied"
	if out.Failed > 0 {
		status = "partial"
	}
	if err = usermutation.SaveContext(ctx, tx, usermutation.Result{Status: status}, time.Now().UnixMilli()); err != nil {
		return out, err
	}
	return out, gated.Commit()
}

func personalBatchDomainFailure(err error) bool {
	return errors.Is(err, identity.ErrUnauthorized) || errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrNotVisible) || errors.Is(err, identity.ErrContentRestricted) || requestinput.Is(err) || errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrPersonalConflict) || errors.Is(err, ErrPersonalResolution) || errors.Is(err, ErrPersonalReview) || errors.Is(err, ErrOperationConflict) || errors.Is(err, ErrOperationExpired) || errors.Is(err, ErrPersonalCapacity)
}

// Replays return present state, including after a later mark or undo. The
// committed revision is sufficient to acknowledge an applied, removed target.
func readContainerMutation(ctx context.Context, tx *sql.Tx, profile, kind, id string, revision int64) (ContainerPersonal, error) {
	out := ContainerPersonal{Revision: revision}
	entity, err := personalID(tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.Revision = 0
	err = tx.QueryRowContext(ctx, `SELECT revision,watched,watermark FROM container_personal_state WHERE profile_id=? AND kind=? AND container_id=?`, profile, kind, entity).Scan(&out.Revision, &out.Watched, &out.Watermark)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	out.Watchlisted, out.Favorite, err = readContainerSaved(ctx, tx, profile, entity)
	return out, err
}

func checkContainerReplayAccess(ctx context.Context, tx *sql.Tx, id, members, visible string, bound []any) error {
	container, err := personalID(tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var present int
	return tx.QueryRowContext(ctx, `SELECT 1 FROM (`+members+`) member JOIN catalog_entities i ON i.id=member.entity_id WHERE `+visible+` LIMIT 1`, append([]any{container}, bound...)...).Scan(&present)
}

// p251_ is a reserved protocol namespace. Omitting the header must never turn a
// retired key into a legacy operation or store a full legacy response under it.
func requireCatalogMutationContext(ctx context.Context, operation string) error {
	if strings.HasPrefix(operation, "p251_") {
		if _, _, ok := usermutation.RequestFrom(ctx); !ok {
			return usermutation.ErrInvalidKey
		}
	}
	return nil
}
