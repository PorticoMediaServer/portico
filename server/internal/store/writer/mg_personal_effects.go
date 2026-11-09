package writer

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Bodies only on the existing admitted commandTurn. Frozen owner/source/request
// values are NOT authority. Parent resolves ORIGINAL public user-mutation roots
// and receipts first, checks current opaque authority (including hidden/rating
// and RB/RS item policy), and saves its exact result before COMMIT. No side
// writer, raw handle, SQL callback, JSON permission or Applied result is exposed.
// Every error after the first effect requires WHOLE-frame rollback.
func mgPersonalStep(t *commandTurn, id stmt.ID) (statementStep, error) {
	if t == nil || t.transaction == nil {
		return statementStep{}, ErrInvalid
	}
	switch id {
	case mgPersonalLiveChannel, mgPersonalLibraryChannel, mgPersonalProgramme, mgPersonalOccurrence, mgPersonalPreferenceRead, mgPersonalPreferenceInsert, mgPersonalPreferenceUpdate, mgPersonalReminderRead, mgPersonalReminderInsert, mgPersonalReminderUpdate, mgPersonalReminderDelete:
	default:
		return statementStep{}, ErrInvalid
	}
	s, ok := t.steps[id]
	if !ok {
		return statementStep{}, ErrInvalid
	}
	return s.statementStep, nil
}
func mgPersonalRow(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) (commandRow, error) {
	s, err := mgPersonalStep(t, id)
	if err != nil {
		return commandRow{}, err
	}
	return t.queryRow(ctx, s, args...)
}
func mgPersonalMissing(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	return err
}
func mgPersonalNow(ctx context.Context, t *commandTurn) (time.Time, error) {
	if t == nil || t.transaction == nil || t.transaction.writer == nil || t.transaction.writer.now == nil {
		return time.Time{}, ErrInvalid
	}
	if err := t.checkLifetimes(ctx); err != nil {
		return time.Time{}, err
	}
	tx := t.transaction
	if tx.done || !tx.begun {
		return time.Time{}, ErrClosed
	}
	defer tx.guardPanic()
	now := tx.writer.now()
	if now.UnixMilli() < 0 {
		return time.Time{}, ErrInvalid
	}
	if err := tx.observeTime(now); err != nil {
		return time.Time{}, err
	}
	deadline, ok := tx.ctx.Deadline()
	if !ok {
		return time.Time{}, ErrInvalid
	}
	if !now.Before(deadline) {
		return time.Time{}, context.DeadlineExceeded
	}
	return now, nil
}
func observeMGPersonalChannel(ctx context.Context, t *commandTurn, c mgPersonalChannel) error {
	if !c.valid() {
		return ErrInvalid
	}
	id := mgPersonalLiveChannel
	args := []any{c.source, c.revision, c.generation, c.channel}
	if c.kind == mgPersonalLibrary {
		id = mgPersonalLibraryChannel
		args = []any{c.channel, c.revision, c.generation}
	}
	row, err := mgPersonalRow(ctx, t, id, args...)
	if err != nil {
		return err
	}
	var revision int64
	var generation string
	if err := row.Scan(&revision, &generation); err != nil {
		return mgPersonalMissing(err)
	}
	if revision != c.revision || generation != c.generation {
		return ErrConflict
	}
	return t.checkLifetimes(ctx)
}
func scanMGPersonalPreference(row commandRow) (mgPersonalPreferenceFacts, error) {
	var p mgPersonalPreferenceFacts
	var favorite, hidden int64
	if err := row.Scan(&favorite, &hidden, &p.revision); err != nil {
		return p, err
	}
	if (favorite != 0 && favorite != 1) || (hidden != 0 && hidden != 1) || p.revision < 1 {
		return mgPersonalPreferenceFacts{}, ErrConflict
	}
	p.favorite = favorite == 1
	p.hidden = hidden == 1
	return p, nil
}

// Fresh expected=0 requires absence. Parent composition resolves identical
// HTTP retries from their ORIGINAL public receipt, never today's row projection.
func setMGPersonalPreference(ctx context.Context, t *commandTurn, r mgPersonalPreferenceRequest) (out mgPersonalPreferenceFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = r.validate(); err != nil {
		return out, err
	}
	if err = observeMGPersonalChannel(ctx, t, r.target); err != nil {
		return out, err
	}
	row, err := mgPersonalRow(ctx, t, mgPersonalPreferenceRead, r.ownerValues()...)
	if err != nil {
		return out, err
	}
	before, err := scanMGPersonalPreference(row)
	present := !errors.Is(err, sql.ErrNoRows)
	if err != nil && present {
		return out, err
	}
	if !present && r.expected != 0 || present && before.revision != r.expected {
		return out, ErrConflict
	}
	favorite, hidden := int64(0), int64(0)
	if r.favorite {
		favorite = 1
	}
	if r.hidden {
		hidden = 1
	}
	id := mgPersonalPreferenceInsert
	args := append(r.ownerValues(), favorite, hidden, int64(1))
	if present {
		id = mgPersonalPreferenceUpdate
		args = append([]any{favorite, hidden}, r.ownerValues()...)
		args = append(args, r.expected)
	}
	row, err = mgPersonalRow(ctx, t, id, args...)
	if err != nil {
		return out, err
	}
	changed, err := scanMGPersonalPreference(row)
	if err != nil {
		return out, mgPersonalMissing(err)
	}
	if changed != (mgPersonalPreferenceFacts{r.expected + 1, r.favorite, r.hidden}) {
		return out, ErrConflict
	}
	row, err = mgPersonalRow(ctx, t, mgPersonalPreferenceRead, r.ownerValues()...)
	if err != nil {
		return out, err
	}
	out, err = scanMGPersonalPreference(row)
	if err != nil {
		return mgPersonalPreferenceFacts{}, err
	}
	if out != changed {
		return mgPersonalPreferenceFacts{}, ErrConflict
	}
	return out, nil
}
func mgPersonalReminderStart(ctx context.Context, t *commandTurn, r mgPersonalReminderRequest) (string, error) {
	if r.target.kind == mgPersonalLive {
		row, err := mgPersonalRow(ctx, t, mgPersonalProgramme, r.target.generation, r.programme, r.target.channel)
		if err != nil {
			return "", err
		}
		var start, end string
		if err := row.Scan(&start, &end); err != nil {
			return "", mgPersonalMissing(err)
		}
		if len(start) > 64 || len(end) > 64 {
			return "", ErrConflict
		}
		a, e1 := time.Parse(time.RFC3339Nano, start)
		b, e2 := time.Parse(time.RFC3339Nano, end)
		if e1 != nil || e2 != nil || !b.After(a) {
			return "", ErrConflict
		}
		return start, nil
	}
	v := r.libraryItem
	row, err := mgPersonalRow(ctx, t, mgPersonalOccurrence, r.target.generation, r.programme, r.target.channel, v.item, v.library, v.asset, v.source)
	if err != nil {
		return "", err
	}
	var start, end, offset int64
	if err := row.Scan(&start, &end, &offset); err != nil {
		return "", mgPersonalMissing(err)
	}
	if start < 0 || end <= start || offset < 0 {
		return "", ErrConflict
	}
	// Existing LC directory public starts use whole-second UTC strftime. Keep
	// that presentation identity while checking the actual native occurrence.
	return time.UnixMilli(start).UTC().Truncate(time.Second).Format(time.RFC3339), nil
}
func scanMGPersonalReminder(row commandRow) (mgPersonalReminderFacts, error) {
	var out mgPersonalReminderFacts
	var kind string
	var external, library sql.NullString
	if err := row.Scan(&out.id, &out.owner, &kind, &out.source, &out.channel, &out.generation, &out.programme, &out.revision, &out.start, &external, &library); err != nil {
		return out, err
	}
	out.kind = mgPersonalKind(kind)
	if out.revision < 1 || !mgPersonalText(out.id) || !mgPersonalText(out.owner) || !mgPersonalText(out.generation) {
		return mgPersonalReminderFacts{}, ErrConflict
	}
	switch out.kind {
	case mgPersonalLive:
		if !external.Valid || external.String != out.generation || library.Valid {
			return mgPersonalReminderFacts{}, ErrConflict
		}
	case mgPersonalLibrary:
		if !library.Valid || library.String != out.generation || external.Valid {
			return mgPersonalReminderFacts{}, ErrConflict
		}
	default:
		return mgPersonalReminderFacts{}, ErrConflict
	}
	out.externalGeneration = external.String
	out.libraryGeneration = library.String
	return out, nil
}
func setMGPersonalReminder(ctx context.Context, t *commandTurn, r mgPersonalReminderRequest) (out mgPersonalReminderFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = r.validate(); err != nil {
		return out, err
	}
	if err = observeMGPersonalChannel(ctx, t, r.target); err != nil {
		return out, err
	}
	start, err := mgPersonalReminderStart(ctx, t, r)
	if err != nil {
		return out, err
	}
	instant, err := time.Parse(time.RFC3339Nano, start)
	if err != nil {
		return out, ErrConflict
	}
	now, err := mgPersonalNow(ctx, t)
	if err != nil {
		return out, err
	}
	if !instant.After(now) {
		return out, ErrConflict
	}
	key, owner := r.id(), r.owner.key()
	row, err := mgPersonalRow(ctx, t, mgPersonalReminderRead, key, owner)
	if err != nil {
		return out, err
	}
	before, err := scanMGPersonalReminder(row)
	present := !errors.Is(err, sql.ErrNoRows)
	if err != nil && present {
		return out, err
	}
	if !present && r.expected != 0 || present && before.revision != r.expected {
		return out, ErrConflict
	}
	want := mgPersonalReminderFacts{id: key, owner: owner, kind: r.target.kind, source: r.target.source, channel: r.target.channel, generation: r.target.generation, programme: r.programme, revision: r.expected + 1, start: start}
	if want.kind == mgPersonalLive {
		want.externalGeneration = want.generation
	} else {
		want.libraryGeneration = want.generation
	}
	if present {
		old := want
		old.revision = r.expected
		if old != before {
			return out, ErrConflict
		}
	}
	id := mgPersonalReminderInsert
	args := []any{key, owner, string(want.kind), want.source, want.channel, want.generation, want.programme, int64(1), start}
	if present {
		id = mgPersonalReminderUpdate
		args = []any{key, owner, r.expected, string(want.kind), want.source, want.channel, want.generation, want.programme, start}
	}
	row, err = mgPersonalRow(ctx, t, id, args...)
	if err != nil {
		return out, err
	}
	changed, err := scanMGPersonalReminder(row)
	if err != nil {
		return out, mgPersonalMissing(err)
	}
	if changed != want {
		return out, ErrConflict
	}
	row, err = mgPersonalRow(ctx, t, mgPersonalReminderRead, key, owner)
	if err != nil {
		return out, err
	}
	out, err = scanMGPersonalReminder(row)
	if err != nil {
		return mgPersonalReminderFacts{}, err
	}
	if out != changed {
		return mgPersonalReminderFacts{}, ErrConflict
	}
	return out, nil
}

// Deletion is owner/revision CAS on reminder INTENT. It remains possible after
// source/head replacement. It never releases a playback/capture holder or any
// other physical debt; only this row's generated G generation ref is removed.
func deleteMGPersonalReminder(ctx context.Context, t *commandTurn, r mgPersonalDeleteRequest) (out mgPersonalReminderFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = r.validate(); err != nil {
		return out, err
	}
	row, err := mgPersonalRow(ctx, t, mgPersonalReminderDelete, r.id, r.owner.key(), r.expected)
	if err != nil {
		return out, err
	}
	out, err = scanMGPersonalReminder(row)
	if err != nil {
		return mgPersonalReminderFacts{}, mgPersonalMissing(err)
	}
	if out.id != r.id || out.owner != r.owner.key() || out.revision != r.expected {
		return mgPersonalReminderFacts{}, ErrConflict
	}
	row, err = mgPersonalRow(ctx, t, mgPersonalReminderRead, r.id, r.owner.key())
	if err != nil {
		return mgPersonalReminderFacts{}, err
	}
	_, err = scanMGPersonalReminder(row)
	if !errors.Is(err, sql.ErrNoRows) {
		if err != nil {
			return mgPersonalReminderFacts{}, err
		}
		return mgPersonalReminderFacts{}, ErrConflict
	}
	return out, nil
}
