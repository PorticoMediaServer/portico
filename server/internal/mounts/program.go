package mounts

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
)

type sourceProgramMutation struct {
	source string
	fresh  func(*sql.Tx) error
}

// ConfigureSourceProgram is the only storage command accepting P2-51 keys.
// authorize proves current owner and source/mount authority; checkFresh checks
// the captured mutable association only after receipt lookup. The middleware's
// original wire digest, key and expected revision are never reconstructed.
func (s *Service) ConfigureSourceProgram(ctx context.Context, actor, source, id, operation, executable string, included bool, revision int64, authorize, checkFresh func(*sql.Tx) error) (Receipt, error) {
	c := Command{OperationID: operation, Action: "configure", ExpectedRevision: revision, Executable: executable, UseIncluded: &included}
	if _, envelope, ok := usermutation.RequestFrom(ctx); ok {
		if envelope.Operation != "put_storage_source_program" || envelope.Key != operation || envelope.Stop || source == "" || checkFresh == nil {
			return Receipt{}, usermutation.ErrConflict
		}
		c.sourceProgram = &sourceProgramMutation{source: source, fresh: checkFresh}
	} else {
		// Preserve the legacy timestamp/UUID protocol and its authority ordering.
		proof := authorize
		authorize = func(tx *sql.Tx) error {
			if proof == nil || checkFresh == nil {
				return ErrInvalid
			}
			if err := proof(tx); err != nil {
				return err
			}
			return checkFresh(tx)
		}
	}
	return s.Command(ctx, actor, id, c, authorize)
}

func (s *Service) sourceProgramCommand(ctx context.Context, actor, id string, c Command, authorize func(*sql.Tx) error) (Receipt, error) {
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	if err != nil {
		return Receipt{}, err
	}
	defer gated.Rollback()
	prior, err := s.checkSourceProgram(ctx, gated.Tx(), id, c, authorize)
	if err != nil {
		return Receipt{}, err
	}
	if prior != nil {
		gated.Rollback()
		s.mu.Lock()
		defer s.mu.Unlock()
		defer s.publish()
		return *prior, s.reloadSourceProgramLocked(id)
	}
	if err = c.sourceProgram.fresh(gated.Tx()); err != nil {
		return Receipt{}, err
	}
	// Provider/version approval and immutable private-file preparation happen
	// outside SQL. This provisional reservation rolls back; configure repeats
	// it in the actual acceptance transaction, before every domain write.
	gated.Rollback()
	return s.configure(ctx, actor, id, "", c, authorize, nil)
}

func (s *Service) checkSourceProgram(ctx context.Context, tx *sql.Tx, id string, c Command, authorize func(*sql.Tx) error) (*Receipt, error) {
	if err := authorize(tx); err != nil {
		return nil, err
	}
	prior, err := usermutation.CheckContext(ctx, tx, time.Now().UnixMilli())
	if err != nil || prior == nil {
		return nil, err
	}
	if prior.Status != "applied" || prior.TargetID != c.sourceProgram.source {
		return nil, usermutation.ErrConflict
	}
	// A tiny receipt proves the historical acceptance. The typed response is
	// current state, which can legitimately have advanced since that acceptance.
	var m Mount
	err = tx.QueryRowContext(ctx, `SELECT m.id,m.executable,m.program_included,c.revision,b.generation,b.version FROM managed_mounts m JOIN mount_controls c ON c.mount_id=m.id JOIN mount_backend_configs b ON b.mount_id=m.id WHERE m.id=?`, id).Scan(&m.ID, &m.Executable, &m.ProgramIncluded, &m.ControlRevision, &m.BackendRevision, &m.ExecutableVersion)
	if err != nil {
		return nil, err
	}
	return &Receipt{OperationID: c.OperationID, MountID: id, ControlRevision: prior.Revision, Accepted: true, Mount: &m}, nil
}

// The DB commit accepts the setting, not a claim that a remote process is ready.
// The synced immutable file and durable restart_generation precede acceptance.
// The worker also observes that generation after a crash. Replay only retires
// an old child, never a successor already running the accepted/current program.
func (s *Service) reloadSourceProgramLocked(id string) error {
	var wanted int64
	if err := s.db.QueryRow(`SELECT restart_generation FROM mount_controls WHERE mount_id=?`, id).Scan(&wanted); err != nil {
		return err
	}
	if child := s.active[id]; child != nil && child.restartGeneration != wanted && !child.stopping {
		child.invalidateRuntime()
		s.available[id] = false
		if err := child.control.Close(); err != nil {
			// Leave retirement retryable; the accepted receipt survives this error.
			return fmt.Errorf("source program saved; runtime retirement pending: %w", err)
		}
		child.stopping = true
		child.stopAt = time.Now()
	}
	delete(s.retry, id)
	return nil
}
