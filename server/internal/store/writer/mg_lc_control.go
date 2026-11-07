package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// 869 (request regeneration) and 871 (reorder) remain reserved, unregistered.
const MGSaveLCConfiguration CommandID = 868
const MGRetireLCChannel CommandID = 870

func mgLCControlDescriptors() []Descriptor {
	return []Descriptor{{MGSaveLCConfiguration, "mg.lc.configuration", Full}, {MGRetireLCChannel, "mg.lc.retire", Full}}
}

// Descriptive normalized canonical text (including library scope and enabled
// state) comes from the parent's read/validation port. It is never authority.
// Parent MUST compose immutable generation birth/enqueue on this SAME transaction
// after an enabled save, then public receipt/events and COMMIT. This effect alone
// is not an executable public save command. Any error rolls back the whole frame.
type mgLCControlConfig struct {
	canonical, name   string
	enabled, position int64
}

// This leaf selects the stock canonical non-null active_generation contract.
// Nullable G draft heads require a separate selected-overlay integration.
type mgLCControlChannel struct {
	id                string
	revision          int64
	config            string
	enabled, position int64
	name              string
	head              sql.NullString
	state, health     string
	through, removed  int64
}
type mgLCControlResult struct {
	channel   mgLCControlChannel
	appliedMS int64
}

func mgLCControlPurpose(ctx context.Context, t *commandTurn, p CommandID) (err error) {
	if t == nil || t.transaction == nil {
		return ErrInvalid
	}
	tx := t.transaction
	if tx.descriptor.ID != p || !tx.policyVerified || tx.descriptor.Durability != Full || tx.settings.Synchronous != int(sqliteconn.Full) || tx.conn == nil || tx.writer == nil {
		return ErrInvalid
	}
	// Check the actual native connection, independently of cached frame settings.
	cfg := tx.writer.config
	cfg.Synchronous = sqliteconn.Full
	native, err := sqliteconn.Assert(ctx, tx.conn, cfg)
	if err != nil {
		return err
	}
	if native.Synchronous != int(sqliteconn.Full) {
		return ErrInvalid
	}
	return t.checkLifetimes(ctx)
}
func mgLCControlTime(ctx context.Context, t *commandTurn) (out int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if t == nil || t.transaction == nil {
		return 0, ErrInvalid
	}
	tx := t.transaction
	if tx.writer == nil || tx.writer.now == nil || tx.ctx == nil || tx.conn == nil || tx.quantum == nil || tx.custody == nil {
		return 0, ErrInvalid
	}
	if tx.done || !tx.begun {
		return 0, ErrClosed
	}
	if err = t.checkLifetimes(ctx); err != nil {
		return 0, err
	}
	deadline, ok := tx.ctx.Deadline()
	if !ok {
		return 0, ErrInvalid
	}
	defer tx.guardPanic()
	now := tx.writer.now()
	if now.UnixMilli() < 0 {
		return 0, ErrInvalid
	}
	if err = tx.observeTime(now); err != nil {
		return 0, err
	}
	if !now.Before(deadline) {
		return 0, context.DeadlineExceeded
	}
	return now.UnixMilli(), nil
}
func mgLCControlStep(t *commandTurn, id stmt.ID) (statementStep, error) {
	if t == nil {
		return statementStep{}, ErrInvalid
	}
	switch id {
	case mgLCControlRead, mgLCControlKeys, mgLCControlInsert, mgLCControlUpdate, mgLCControlPending, mgLCControlInvalidate, mgLCControlGeneration, mgLCControlRefs:
	default:
		return statementStep{}, ErrInvalid
	}
	s, ok := t.steps[id]
	if !ok {
		return statementStep{}, ErrInvalid
	}
	return s.statementStep, nil
}
func mgLCControlRow(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) (commandRow, error) {
	s, e := mgLCControlStep(t, id)
	if e != nil {
		return commandRow{}, e
	}
	return t.queryRow(ctx, s, args...)
}
func mgLCControlReadChannel(ctx context.Context, t *commandTurn, id string) (out mgLCControlChannel, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	row, err := mgLCControlRow(ctx, t, mgLCControlRead, id)
	if err != nil {
		return out, err
	}
	err = row.Scan(&out.id, &out.revision, &out.config, &out.enabled, &out.position, &out.name, &out.head, &out.state, &out.health, &out.through, &out.removed)
	return
}
func mgLCControlChange(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) error {
	s, e := mgLCControlStep(t, id)
	if e != nil {
		return e
	}
	r, e := t.exec(ctx, s, args...)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
func mgLCControlApply(ctx context.Context, t *commandTurn, before mgLCControlChannel, c mgLCControlConfig, retire bool) (out mgLCControlResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	purpose := MGSaveLCConfiguration
	if retire {
		purpose = MGRetireLCChannel
	}
	if err = mgLCControlPurpose(ctx, t, purpose); err != nil {
		return
	}
	if _, err = mgLCControlTime(ctx, t); err != nil {
		return
	}
	if !mgSourceText(before.id, true, 128) || before.revision < 0 || before.revision == math.MaxInt64 || before.removed != 0 || (before.revision > 0 && !before.head.Valid) {
		err = ErrConflict
		return
	}
	actual, e := mgLCControlReadChannel(ctx, t, before.id)
	fresh := before.revision == 0
	if fresh {
		if !errors.Is(e, sql.ErrNoRows) || retire {
			err = ErrConflict
			return
		}
	} else if e != nil {
		err = e
		return
	} else if actual != before {
		err = ErrConflict
		return
	}
	if retire {
		row, e := mgLCControlRow(ctx, t, mgLCControlRefs, before.id)
		if e != nil {
			err = e
			return
		}
		var ref string
		e = row.Scan(&ref)
		if e == nil {
			err = ErrConflict
			return
		}
		if !errors.Is(e, sql.ErrNoRows) {
			err = e
			return
		}
	} else if !mgSourceText(c.canonical, true, 1<<20) || !mgSourceText(c.name, true, 120) || c.position < 0 || c.position > 100000 || (c.enabled != 0 && c.enabled != 1) {
		err = ErrInvalid
		return
	}
	if fresh {
		total := 0
		for enabled := int64(0); enabled < 2; enabled++ {
			s, e := mgLCControlStep(t, mgLCControlKeys)
			if e != nil {
				err = e
				return
			}
			rows, e := t.query(ctx, s, enabled)
			if e != nil {
				err = e
				return
			}
			for rows.Next() {
				var id string
				if e = rows.Scan(&id); e != nil {
					break
				}
				total++
			}
			err = errors.Join(e, rows.Err(), rows.Close())
			if err != nil {
				return
			}
		}
		if total >= 64 {
			err = ErrInvalid
			return
		}
	}
	// Unique partial key gives at most one current pending generation. Capture
	// every stock scalar before changing only status/error; lease/debt stay intact.
	var pending [24]any
	row, e := mgLCControlRow(ctx, t, mgLCControlPending, before.id)
	if e != nil {
		err = e
		return
	}
	targets := make([]any, len(pending))
	for i := range pending {
		targets[i] = &pending[i]
	}
	e = row.Scan(targets...)
	if e == nil {
		if pending[2] != before.revision {
			err = ErrConflict
			return
		}
		if err = mgLCControlChange(ctx, t, mgLCControlInvalidate, pending[0], before.id, before.revision); err != nil {
			return
		}
		pending[6] = "superseded"
		pending[22] = "configuration-changed"
		row, e = mgLCControlRow(ctx, t, mgLCControlGeneration, pending[0])
		if e != nil {
			err = e
			return
		}
		var post [24]any
		for i := range post {
			targets[i] = &post[i]
		}
		if err = row.Scan(targets...); err != nil {
			return
		}
		if post != pending {
			err = ErrConflict
			return
		}
	} else if !errors.Is(e, sql.ErrNoRows) {
		err = e
		return
	}
	after := before
	after.revision++
	if retire {
		after.enabled = 0
		after.removed = 1
		after.state = "removed"
	} else {
		after.config = c.canonical
		after.name = c.name
		after.position = c.position
		after.enabled = c.enabled
		after.health = ""
		after.state = "disabled"
		if c.enabled == 1 {
			after.state = "generating"
		}
	}
	if fresh {
		after.head = sql.NullString{String: "", Valid: true}
		err = mgLCControlChange(ctx, t, mgLCControlInsert, after.id, after.revision, after.config, after.enabled, after.position, after.name, after.state)
	} else {
		err = mgLCControlChange(ctx, t, mgLCControlUpdate, after.revision, after.config, after.enabled, after.position, after.name, after.state, after.health, after.removed, before.id, before.revision, mgLCControlHeadValue(before.head))
	}
	if err != nil {
		return
	}
	post, e := mgLCControlReadChannel(ctx, t, before.id)
	if e != nil {
		err = e
		return
	}
	if post != after {
		err = ErrConflict
		return
	}
	now, e := mgLCControlTime(ctx, t)
	if e != nil {
		err = e
		return
	}
	return mgLCControlResult{post, now}, nil
}
func mgSaveLCConfiguration(ctx context.Context, t *commandTurn, b mgLCControlChannel, c mgLCControlConfig) (mgLCControlResult, error) {
	return mgLCControlApply(ctx, t, b, c, false)
}
func mgRetireLCChannel(ctx context.Context, t *commandTurn, b mgLCControlChannel) (mgLCControlResult, error) {
	return mgLCControlApply(ctx, t, b, mgLCControlConfig{}, true)
}

func mgLCControlHeadValue(h sql.NullString) any {
	if !h.Valid {
		return nil
	}
	return h.String
}
