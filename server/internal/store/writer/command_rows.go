package writer

import (
	"database/sql"
	"errors"
)

// None of database/sql's live rows or scanner callbacks leaves this adapter.
// Recovery inside Scanner.Scan lets database/sql unlock before frame cleanup.
type commandRows struct {
	rows *sql.Rows
	turn *commandTurn
}
type commandRow struct{ rows *commandRows }
type guardedScanner struct {
	sql.Scanner
	log *driverPanicLog
}

func (s guardedScanner) Scan(value any) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = s.log.add("scan callback", p)
		}
	}()
	return s.Scanner.Scan(value)
}
func (r *commandRows) Next() bool {
	defer r.turn.transaction.guardPanic()
	ok := r.rows.Next()
	r.turn.panicAfter(r.rows.Err())
	return ok
}
func (r *commandRows) Scan(dest ...any) error {
	defer r.turn.transaction.guardPanic()
	protected := make([]any, len(dest))
	for i, d := range dest {
		if _, ok := d.(*sql.RawBytes); ok {
			return invalid("raw_scan_destination")
		}
		if scanner, ok := d.(sql.Scanner); ok {
			protected[i] = guardedScanner{scanner, r.turn.transaction.writer.driverPanics}
		} else {
			protected[i] = d
		}
	}
	err := r.rows.Scan(protected...)
	r.turn.panicAfter(err)
	return err
}
func (r *commandRows) Err() error {
	defer r.turn.transaction.guardPanic()
	err := r.rows.Err()
	r.turn.panicAfter(err)
	return err
}
func (r *commandRows) Columns() ([]string, error) {
	defer r.turn.transaction.guardPanic()
	columns, err := r.rows.Columns()
	r.turn.panicAfter(err)
	return columns, err
}
func (r *commandRows) Close() error {
	defer r.turn.transaction.guardPanic()
	err := r.rows.Close()
	r.turn.panicAfter(err)
	return err
}
func (r commandRow) Scan(dest ...any) error {
	if r.rows == nil {
		return ErrInvalid
	}
	defer r.rows.turn.transaction.guardPanic()
	if !r.rows.Next() {
		err := r.rows.Err()
		closeErr := r.rows.Close()
		if err != nil {
			return errors.Join(err, closeErr)
		}
		if closeErr != nil {
			return closeErr
		}
		return sql.ErrNoRows
	}
	err := r.rows.Scan(dest...)
	return errors.Join(err, r.rows.Close())
}

func (t *commandTurn) closeRowsRaw() error {
	var err error
	for _, rows := range t.rows {
		err = errors.Join(err, rows.rows.Close())
	}
	t.rows = nil
	return err
}
func (t *commandTurn) panicAfter(err error) {
	events := t.transaction.writer.driverPanics.take()
	primary := panicFrom(err)
	if primary == nil && len(events) > 0 {
		primary = events[0]
	}
	if primary == nil {
		return
	}
	for _, fault := range events {
		if fault == primary {
			continue
		}
		t.transaction.cleanupPanics = append(t.transaction.cleanupPanics, terminalPanic{fault.phase, fault.value})
	}
	// This never rethrows through a database/sql callback. Its driver methods
	// contain their own panics, so secondary cleanup cannot replace primary.
	if closeErr := t.closeRowsRaw(); closeErr != nil {
		t.transaction.cleanupPanics = append(t.transaction.cleanupPanics, terminalPanic{"close rows", closeErr})
	}
	for _, fault := range t.transaction.writer.driverPanics.take() {
		t.transaction.cleanupPanics = append(t.transaction.cleanupPanics, terminalPanic{fault.phase, fault.value})
	}
	panic(primary.value)
}

type commandResult struct {
	sql.Result
	turn *commandTurn
}

func (r commandResult) RowsAffected() (int64, error) {
	defer r.turn.transaction.guardPanic()
	n, err := r.Result.RowsAffected()
	r.turn.panicAfter(err)
	return n, err
}
func (r commandResult) LastInsertId() (int64, error) {
	defer r.turn.transaction.guardPanic()
	n, err := r.Result.LastInsertId()
	r.turn.panicAfter(err)
	return n, err
}
