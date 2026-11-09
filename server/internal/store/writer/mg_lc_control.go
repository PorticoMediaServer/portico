package writer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"sort"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Channel control commands 868-871. Their private effects live on one admitted
// command turn; mg_lc_control_dispatch.go owns the closed accepting frame. A
// channel configuration write never deletes a generation, entry, playback
// reference, receipt or retained debt, and it never releases physical custody.
// G reclamation remains off.
const (
	MGSaveLCConfiguration   CommandID = 868
	MGRequestLCRegeneration CommandID = 869
	MGRetireLCChannel       CommandID = 870
	MGReorderLCChannels     CommandID = 871
)

func mgLCControlDescriptors() []Descriptor {
	return []Descriptor{
		{MGSaveLCConfiguration, "mg.lc.configuration", Full},
		{MGRequestLCRegeneration, "mg.lc.regenerate", Normal},
		{MGRetireLCChannel, "mg.lc.retire", Full},
		{MGReorderLCChannels, "mg.lc.reorder", Normal},
	}
}

type mgLCControlKind string

const (
	mgLCControlSaveKind       mgLCControlKind = "save"
	mgLCControlRegenerateKind mgLCControlKind = "regenerate"
	mgLCControlRetireKind     mgLCControlKind = "retire"
	mgLCControlReorderKind    mgLCControlKind = "reorder"
)

func (k mgLCControlKind) valid() bool {
	switch k {
	case mgLCControlSaveKind, mgLCControlRegenerateKind, mgLCControlRetireKind, mgLCControlReorderKind:
		return true
	}
	return false
}

func mgLCControlKindFor(purpose CommandID) (mgLCControlKind, bool) {
	switch purpose {
	case MGSaveLCConfiguration:
		return mgLCControlSaveKind, true
	case MGRequestLCRegeneration:
		return mgLCControlRegenerateKind, true
	case MGRetireLCChannel:
		return mgLCControlRetireKind, true
	case MGReorderLCChannels:
		return mgLCControlReorderKind, true
	}
	return "", false
}

// One channel's native row. head is the stock non-null active_generation text;
// the generated nullable reference is derived by SQLite, never supplied here.
type mgLCControlChannel struct {
	id, config, name, state, health, head string
	revision, enabled, position           int64
	through, removed                      int64
}

// Descriptive normalized configuration from the parent's read/validation port.
// It is never authority. The parent must compose the immutable generation
// birth/enqueue on this same transaction after an enabled save.
type mgLCControlConfig struct {
	canonical, name   string
	enabled, position int64
}

// A reorder entry names the exact expected channel witness and the normalized
// configuration that carries its new position. It cannot change identity,
// enabled state or head.
type mgLCControlOrder struct {
	channel mgLCControlChannel
	config  mgLCControlConfig
}

// mgLCControlWitness is a comparison witness, not a permission. issuer is the
// parent-authorized command lifetime incarnation and must match the command
// identity; a forged/borrowed frame identity is refused before any effect.
type mgLCControlWitness struct {
	issuer    [16]byte
	channel   mgLCControlChannel
	directory []mgLCControlChannel
}

type mgLCControlInput struct {
	config *mgLCControlConfig
	order  []mgLCControlOrder
}

type mgLCControlCommand struct {
	identity CommandIdentity
	request  RequestDigest
}

func (c mgLCControlCommand) valid() bool {
	return c.identity.Incarnation != ([16]byte{}) && c.identity.Sequence > 0 &&
		c.identity.OperationID != ([16]byte{}) && c.request != (RequestDigest{})
}

type mgLCControlResult struct {
	channel   mgLCControlChannel
	directory []mgLCControlChannel
	applied   int64
}

type mgLCControlSaved struct {
	command mgLCControlCommand
	kind    mgLCControlKind
	digest  ResultDigest
	result  mgLCControlResult
}

func (ch mgLCControlChannel) valid() bool {
	return ch.id != "" && ch.revision > 0 && ch.revision < math.MaxInt64 && ch.enabled >= 0 && ch.enabled <= 1 &&
		ch.position >= 0 && ch.position <= 100000 && ch.removed >= 0 && ch.removed <= 1 && ch.through >= 0 &&
		mgSourceText(ch.id, true, 128) && mgSourceText(ch.config, true, mgLCControlConfigBytes) &&
		mgSourceText(ch.name, true, 120) && mgSourceText(ch.state, true, 64) && mgSourceText(ch.health, false, 64) &&
		mgSourceText(ch.head, false, 128)
}

func (w mgLCControlWitness) valid(kind mgLCControlKind) bool {
	if w.issuer == ([16]byte{}) {
		return false
	}
	switch kind {
	case mgLCControlSaveKind:
		if w.channel.revision == 0 {
			// Explicit no-head birth witness: only the identity is significant.
			return w.channel.id != "" && mgSourceText(w.channel.id, true, 128) && len(w.directory) == 0
		}
		return w.channel.valid() && w.channel.removed == 0 && len(w.directory) == 0
	case mgLCControlRegenerateKind, mgLCControlRetireKind:
		return w.channel.valid() && w.channel.removed == 0 && len(w.directory) == 0
	case mgLCControlReorderKind:
		if len(w.directory) == 0 || len(w.directory) > int(mgLCControlMaxChannels) {
			return false
		}
		seen := map[string]bool{}
		for _, ch := range w.directory {
			if !ch.valid() || ch.removed != 0 || seen[ch.id] {
				return false
			}
			seen[ch.id] = true
		}
		return true
	}
	return false
}

func (in mgLCControlInput) valid(kind mgLCControlKind) error {
	switch kind {
	case mgLCControlSaveKind:
		if in.config == nil || len(in.order) != 0 {
			return ErrInvalid
		}
		c := *in.config
		if !mgSourceText(c.canonical, true, mgLCControlConfigBytes) || !mgSourceText(c.name, true, 120) ||
			c.position < 0 || c.position > 100000 || (c.enabled != 0 && c.enabled != 1) {
			return ErrInvalid
		}
		return nil
	case mgLCControlRegenerateKind, mgLCControlRetireKind:
		if in.config != nil || len(in.order) != 0 {
			return ErrInvalid
		}
		return nil
	case mgLCControlReorderKind:
		if in.config != nil || len(in.order) == 0 || len(in.order) > int(mgLCControlMaxChannels) {
			return ErrInvalid
		}
		seen := map[string]bool{}
		total := 0
		for n, o := range in.order {
			if !o.channel.valid() || o.channel.removed != 0 || seen[o.channel.id] {
				return ErrInvalid
			}
			seen[o.channel.id] = true
			c := o.config
			// Reorder moves position/config only; the channel's display name is
			// not a reorder field and must match the existing witness exactly.
			if !mgSourceText(c.canonical, true, mgLCControlConfigBytes) || !mgSourceText(c.name, true, 120) ||
				c.position != int64(n) || c.enabled != o.channel.enabled || c.name != o.channel.name {
				return ErrInvalid
			}
			total += len(c.canonical) + len(c.name)
			if total > mgLCBytes {
				return ErrCap
			}
		}
		return nil
	}
	return ErrInvalid
}

// mgLCControlPlannedResult projects the exact result a fresh effect will return
// from the validated witness and input alone. It lets the accepting frame refuse
// an over-cap encoded receipt BEFORE any native write.
func mgLCControlPlannedResult(kind mgLCControlKind, w mgLCControlWitness, in mgLCControlInput) (mgLCControlResult, bool) {
	switch kind {
	case mgLCControlSaveKind:
		if in.config == nil {
			return mgLCControlResult{}, false
		}
		c := *in.config
		if w.channel.revision == 0 {
			if !mgSourceText(w.channel.id, true, 128) {
				return mgLCControlResult{}, false
			}
			return mgLCControlResult{channel: mgLCControlChannel{id: w.channel.id, revision: 1, config: c.canonical,
				enabled: c.enabled, position: c.position, name: c.name, state: mgLCControlChannelState(c.enabled)}}, true
		}
		next := w.channel
		next.revision++
		next.config, next.name, next.position, next.enabled = c.canonical, c.name, c.position, c.enabled
		next.state, next.health = mgLCControlChannelState(c.enabled), ""
		return mgLCControlResult{channel: next}, true
	case mgLCControlRegenerateKind:
		next := w.channel
		next.state, next.health = "generating", ""
		return mgLCControlResult{channel: next}, true
	case mgLCControlRetireKind:
		next := w.channel
		next.revision++
		next.enabled, next.removed, next.state, next.health = 0, 1, "removed", ""
		return mgLCControlResult{channel: next}, true
	case mgLCControlReorderKind:
		if len(in.order) == 0 {
			return mgLCControlResult{}, false
		}
		out := make([]mgLCControlChannel, 0, len(in.order))
		for n, o := range in.order {
			next := o.channel
			next.revision++
			next.position = int64(n)
			next.config = o.config.canonical
			out = append(out, next)
		}
		return mgLCControlResult{directory: out}, true
	}
	return mgLCControlResult{}, false
}

// The private effects below are bounded by the supplied witness and every
// mutable native scalar. Any RowsAffected != 1 or RETURNING mismatch is a
// conflict that leaves the WHOLE frame to the caller's rollback.

func mgLCControlStep(turn *commandTurn, id stmt.ID) (statementStep, error) {
	if turn == nil {
		return statementStep{}, ErrInvalid
	}
	switch id {
	case mgLCControlRead, mgLCControlDirectory, mgLCControlInsert, mgLCControlUpdate,
		mgLCControlPending, mgLCControlSupersede, mgLCControlGeneration, mgLCControlRefs,
		mgLCControlPendingAlign, mgLCControlReceiptRead, mgLCControlReceiptInsert:
	default:
		return statementStep{}, ErrInvalid
	}
	step, ok := turn.steps[id]
	if !ok {
		return statementStep{}, ErrInvalid
	}
	return step.statementStep, nil
}

func mgLCControlRow(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) (commandRow, error) {
	step, err := mgLCControlStep(turn, id)
	if err != nil {
		return commandRow{}, err
	}
	return turn.queryRow(ctx, step, args...)
}

func mgLCControlQuery(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) (*commandRows, error) {
	step, err := mgLCControlStep(turn, id)
	if err != nil {
		return nil, err
	}
	return turn.query(ctx, step, args...)
}

func mgLCControlOne(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) error {
	step, err := mgLCControlStep(turn, id)
	if err != nil {
		return err
	}
	result, err := turn.exec(ctx, step, args...)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

// Closed native time for a control frame. Fixes no real-time result; tests use
// the same executor clock and never assert wall-clock latency.
func mgLCControlTime(ctx context.Context, turn *commandTurn) (out int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil {
		return 0, ErrInvalid
	}
	tx := turn.transaction
	if _, ok := mgLCControlKindFor(tx.descriptor.ID); !ok || !tx.policyVerified || !tx.begun || tx.done ||
		tx.writer == nil || tx.writer.now == nil || tx.ctx == nil || tx.conn == nil {
		return 0, ErrInvalid
	}
	if err = turn.checkLifetimes(ctx); err != nil {
		return 0, err
	}
	if _, ok := tx.ctx.Deadline(); !ok {
		return 0, ErrInvalid
	}
	now := tx.writer.now()
	if now.UnixMilli() < 0 {
		return 0, ErrInvalid
	}
	if err = tx.observeTime(now); err != nil {
		return 0, err
	}
	return now.UnixMilli(), nil
}

// 868/870 preserve the draft's FULL security boundary: the native connection's
// synchronous mode is asserted independently of cached frame settings. 869/871
// are NORMAL and cannot borrow it.
func mgLCControlPurpose(ctx context.Context, turn *commandTurn, purpose CommandID) error {
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != purpose ||
		!turn.transaction.policyVerified || !turn.transaction.begun || turn.transaction.done {
		return ErrInvalid
	}
	kind, ok := mgLCControlKindFor(purpose)
	if !ok {
		return ErrInvalid
	}
	expected := Normal
	if kind == mgLCControlSaveKind || kind == mgLCControlRetireKind {
		expected = Full
	}
	tx := turn.transaction
	if tx.descriptor.Durability != expected || tx.conn == nil {
		return ErrInvalid
	}
	if expected == Full {
		if tx.settings.Synchronous != int(sqliteconn.Full) || tx.writer == nil {
			return ErrInvalid
		}
		cfg := tx.writer.config
		cfg.Synchronous = sqliteconn.Full
		native, err := sqliteconn.Assert(ctx, tx.conn, cfg)
		if err != nil {
			return err
		}
		if native.Synchronous != int(sqliteconn.Full) {
			return ErrInvalid
		}
	}
	return turn.checkLifetimes(ctx)
}

func scanMGLCControlChannel(row commandRow) (out mgLCControlChannel, err error) {
	err = row.Scan(&out.id, &out.revision, &out.config, &out.enabled, &out.position, &out.name, &out.head, &out.state, &out.health, &out.through, &out.removed)
	if err != nil {
		return mgLCControlChannel{}, err
	}
	if !out.valid() {
		return mgLCControlChannel{}, ErrConflict
	}
	return out, nil
}

func readMGLCControlChannel(ctx context.Context, turn *commandTurn, id string) (out mgLCControlChannel, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := mgLCControlRow(ctx, turn, mgLCControlRead, id)
	if err != nil {
		return out, err
	}
	out, err = scanMGLCControlChannel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return mgLCControlChannel{}, ErrConflict
	}
	return out, err
}

// readMGLCControlDirectory returns the complete configured directory in
// position order, bounded at 64. It reads both enabled partitions through the
// lc_directory index and never scans schedule children.
func readMGLCControlDirectory(ctx context.Context, turn *commandTurn) (out []mgLCControlChannel, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	var all []mgLCControlChannel
	for _, enabled := range []int64{0, 1} {
		rows, err := mgLCControlQuery(ctx, turn, mgLCControlDirectory, enabled)
		if err != nil {
			return nil, err
		}
		count := 0
		for rows.Next() {
			var ch mgLCControlChannel
			if err := rows.Scan(&ch.id, &ch.revision, &ch.config, &ch.enabled, &ch.position, &ch.name, &ch.head, &ch.state, &ch.health, &ch.through, &ch.removed); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if !ch.valid() || ch.enabled != enabled || ch.removed != 0 {
				_ = rows.Close()
				return nil, ErrConflict
			}
			all = append(all, ch)
			count++
			if count >= mgLCControlDirectoryLimit {
				_ = rows.Close()
				return nil, ErrConflict
			}
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return nil, err
		}
	}
	if len(all) > int(mgLCControlMaxChannels) {
		return nil, ErrConflict
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].position != all[j].position {
			return all[i].position < all[j].position
		}
		return all[i].id < all[j].id
	})
	return all, nil
}

type mgLCControlPendingRow struct {
	id, config, status, error, lease string
	configRevision, leaseUntil       int64
	last, ack                        int64
	stageRevision, entries, bytes    int64
	entryDigest                      []byte
	receiptRows, receiptBytes        int64
	reclaimAfter                     int64
	reclaimDue                       sql.NullInt64
}

func (p mgLCControlPendingRow) owner(b mgLCControlChannel) bool {
	return p.id != "" && p.configRevision == b.revision && p.status == "pending" && p.lease != "" && p.leaseUntil > 0 &&
		p.last >= 1 && p.ack >= 0 && p.ack <= p.last && p.stageRevision >= 1 && p.entries >= 0 && p.bytes >= 0 &&
		len(p.entryDigest) == 32 && p.receiptRows >= 0 && p.receiptBytes >= 0 && p.reclaimAfter >= 0 && p.reclaimAfter <= p.ack &&
		(!p.reclaimDue.Valid || p.reclaimDue.Int64 >= 0)
}

func scanMGLCControlPending(row commandRow) (out mgLCControlPendingRow, err error) {
	err = row.Scan(&out.id, &out.configRevision, &out.config, &out.status, &out.error, &out.lease, &out.leaseUntil,
		&out.last, &out.ack, &out.stageRevision, &out.entries, &out.bytes, &out.entryDigest,
		&out.receiptRows, &out.receiptBytes, &out.reclaimAfter, &out.reclaimDue)
	if err != nil {
		return mgLCControlPendingRow{}, err
	}
	if len(out.entryDigest) != 32 {
		return mgLCControlPendingRow{}, ErrConflict
	}
	return out, nil
}

func readMGLCControlPending(ctx context.Context, turn *commandTurn, channel string) (out mgLCControlPendingRow, present bool, err error) {
	defer func() {
		value, failure := finishCommand(ctx, commandLifetime(turn), struct {
			Row     mgLCControlPendingRow
			Present bool
		}{out, present}, err)
		out, present, err = value.Row, value.Present, failure
	}()
	row, err := mgLCControlRow(ctx, turn, mgLCControlPending, channel)
	if err != nil {
		return out, false, err
	}
	out, err = scanMGLCControlPending(row)
	if errors.Is(err, sql.ErrNoRows) {
		return mgLCControlPendingRow{}, false, nil
	}
	if err != nil {
		return mgLCControlPendingRow{}, false, err
	}
	return out, true, nil
}

// supersedeMGLCPending invalidates exactly the pending row named by the channel
// revision. Lease, receipt counters, staged bytes and data digest are compared
// on both sides and must survive unchanged.
func supersedeMGLCPending(ctx context.Context, turn *commandTurn, b mgLCControlChannel, p mgLCControlPendingRow, code string) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if !p.owner(b) || p.configRevision != b.revision || code == "" {
		return ErrConflict
	}
	row, err := mgLCControlRow(ctx, turn, mgLCControlSupersede, code, p.id, b.id, p.configRevision, p.lease, p.leaseUntil,
		p.last, p.ack, p.stageRevision, p.entries, p.bytes, p.entryDigest, p.receiptRows, p.receiptBytes, p.reclaimAfter)
	if err != nil {
		return err
	}
	after, err := scanMGLCControlPending(row)
	if err != nil {
		return err
	}
	if after.id != p.id || after.configRevision != p.configRevision || after.config != p.config || after.error != code ||
		after.lease != p.lease || after.leaseUntil != p.leaseUntil || after.last != p.last || after.ack != p.ack ||
		after.stageRevision != p.stageRevision || after.entries != p.entries || after.bytes != p.bytes ||
		!bytes.Equal(after.entryDigest, p.entryDigest) || after.receiptRows != p.receiptRows ||
		after.receiptBytes != p.receiptBytes || after.reclaimAfter != p.reclaimAfter || after.reclaimDue != p.reclaimDue {
		return ErrConflict
	}
	if after.status != "superseded" {
		return ErrConflict
	}
	row, err = mgLCControlRow(ctx, turn, mgLCControlGeneration, p.id)
	if err != nil {
		return err
	}
	readback, err := scanMGLCControlPending(row)
	if err != nil || readback.id != after.id || readback.config != after.config || readback.status != after.status ||
		readback.error != after.error || readback.lease != after.lease || readback.leaseUntil != after.leaseUntil ||
		readback.last != after.last || readback.ack != after.ack || readback.stageRevision != after.stageRevision ||
		readback.entries != after.entries || readback.bytes != after.bytes || !bytes.Equal(readback.entryDigest, after.entryDigest) ||
		readback.receiptRows != after.receiptRows || readback.receiptBytes != after.receiptBytes ||
		readback.reclaimAfter != after.reclaimAfter || readback.reclaimDue != after.reclaimDue {
		return ErrConflict
	}
	return nil
}

func mgLCControlChannelState(enabled int64) string {
	if enabled == 1 {
		return "generating"
	}
	return "disabled"
}

// saveMGLCControlChannel creates the no-head birth or edits an existing
// channel. The generated active_generation_ref stays NULL until a later
// publication sets the stock text head; this effect never invents a head.
func saveMGLCControlChannel(ctx context.Context, turn *commandTurn, w mgLCControlWitness, c mgLCControlConfig) (out mgLCControlResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if w.channel.revision == 0 {
		if w.channel.id == "" || len(w.directory) != 0 || !mgSourceText(w.channel.id, true, 128) {
			return out, ErrInvalid
		}
		if _, err := readMGLCControlChannel(ctx, turn, w.channel.id); err == nil {
			return out, ErrConflict
		} else if !errors.Is(err, ErrConflict) {
			return out, err
		}
		directory, err := readMGLCControlDirectory(ctx, turn)
		if err != nil {
			return out, err
		}
		if int64(len(directory)) >= mgLCControlMaxChannels {
			return out, &Refusal{Code: RefusalCapacity}
		}
		row, err := mgLCControlRow(ctx, turn, mgLCControlInsert, w.channel.id, int64(1), c.canonical, c.enabled, c.position, c.name, "", mgLCControlChannelState(c.enabled))
		if err != nil {
			return out, err
		}
		actual, err := scanMGLCControlChannel(row)
		if err != nil {
			return out, err
		}
		want := mgLCControlChannel{id: w.channel.id, revision: 1, config: c.canonical, enabled: c.enabled, position: c.position,
			name: c.name, state: mgLCControlChannelState(c.enabled), head: ""}
		if actual != want {
			return out, ErrConflict
		}
		now, err := mgLCControlTime(ctx, turn)
		if err != nil {
			return out, err
		}
		return mgLCControlResult{channel: actual, applied: now}, nil
	}
	if !w.channel.valid() || w.channel.removed != 0 || len(w.directory) != 0 {
		return out, ErrInvalid
	}
	native, err := readMGLCControlChannel(ctx, turn, w.channel.id)
	if err != nil {
		return out, err
	}
	if native != w.channel || native.revision == math.MaxInt64 {
		return out, ErrConflict
	}
	pending, present, err := readMGLCControlPending(ctx, turn, native.id)
	if err != nil {
		return out, err
	}
	if present {
		if pending.configRevision != native.revision {
			return out, ErrConflict
		}
		if err := supersedeMGLCPending(ctx, turn, native, pending, "configuration-changed"); err != nil {
			return out, err
		}
	}
	next := native
	next.revision++
	next.config, next.name, next.position, next.enabled = c.canonical, c.name, c.position, c.enabled
	next.state, next.health = mgLCControlChannelState(c.enabled), ""
	row, err := mgLCControlRow(ctx, turn, mgLCControlUpdate,
		next.revision, next.config, next.enabled, next.position, next.name, next.state, next.health, next.removed,
		native.id, native.revision, native.config, native.enabled, native.position, native.name, native.state, native.health, native.removed, native.head)
	if err != nil {
		return out, err
	}
	actual, err := scanMGLCControlChannel(row)
	if err != nil {
		return out, err
	}
	if actual != next {
		return out, ErrConflict
	}
	now, err := mgLCControlTime(ctx, turn)
	if err != nil {
		return out, err
	}
	return mgLCControlResult{channel: actual, applied: now}, nil
}

// requestMGLCControlRegeneration supersedes at most one pending build at the
// exact current revision and marks the channel generating. The caller composes
// the immutable generation birth/enqueue on the same turn (MG-22 begin).
func requestMGLCControlRegeneration(ctx context.Context, turn *commandTurn, w mgLCControlWitness) (out mgLCControlResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if !w.channel.valid() || w.channel.removed != 0 || len(w.directory) != 0 {
		return out, ErrInvalid
	}
	native, err := readMGLCControlChannel(ctx, turn, w.channel.id)
	if err != nil {
		return out, err
	}
	if native != w.channel || native.enabled != 1 || native.revision == math.MaxInt64 {
		return out, ErrConflict
	}
	pending, present, err := readMGLCControlPending(ctx, turn, native.id)
	if err != nil {
		return out, err
	}
	if present {
		if pending.configRevision != native.revision {
			return out, ErrConflict
		}
		if err := supersedeMGLCPending(ctx, turn, native, pending, "explicit-regeneration"); err != nil {
			return out, err
		}
	}
	next := native
	next.state, next.health = "generating", ""
	row, err := mgLCControlRow(ctx, turn, mgLCControlUpdate,
		next.revision, next.config, next.enabled, next.position, next.name, next.state, next.health, next.removed,
		native.id, native.revision, native.config, native.enabled, native.position, native.name, native.state, native.health, native.removed, native.head)
	if err != nil {
		return out, err
	}
	actual, err := scanMGLCControlChannel(row)
	if err != nil {
		return out, err
	}
	if actual != next {
		return out, ErrConflict
	}
	now, err := mgLCControlTime(ctx, turn)
	if err != nil {
		return out, err
	}
	return mgLCControlResult{channel: actual, applied: now}, nil
}

// retireMGLCControlChannel leaves every generation, entry, receipt, lease and
// playback reference in place. Any retained reference, even one whose lease
// already passed, refuses retirement: expiry is not proof of physical exit and
// this task must not perform a release.
func retireMGLCControlChannel(ctx context.Context, turn *commandTurn, w mgLCControlWitness) (out mgLCControlResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if !w.channel.valid() || w.channel.removed != 0 || len(w.directory) != 0 {
		return out, ErrInvalid
	}
	native, err := readMGLCControlChannel(ctx, turn, w.channel.id)
	if err != nil {
		return out, err
	}
	if native != w.channel || native.removed != 0 || native.revision == math.MaxInt64 {
		return out, ErrConflict
	}
	row, err := mgLCControlRow(ctx, turn, mgLCControlRefs, native.id)
	if err != nil {
		return out, err
	}
	var playback string
	if err := row.Scan(&playback); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if playback != "" {
		return out, ErrConflict
	}
	pending, present, err := readMGLCControlPending(ctx, turn, native.id)
	if err != nil {
		return out, err
	}
	if present {
		if pending.configRevision != native.revision {
			return out, ErrConflict
		}
		if err := supersedeMGLCPending(ctx, turn, native, pending, "channel-retired"); err != nil {
			return out, err
		}
	}
	next := native
	next.revision++
	next.enabled, next.removed, next.state, next.health = 0, 1, "removed", ""
	row, err = mgLCControlRow(ctx, turn, mgLCControlUpdate,
		next.revision, next.config, next.enabled, next.position, next.name, next.state, next.health, next.removed,
		native.id, native.revision, native.config, native.enabled, native.position, native.name, native.state, native.health, native.removed, native.head)
	if err != nil {
		return out, err
	}
	actual, err := scanMGLCControlChannel(row)
	if err != nil {
		return out, err
	}
	if actual != next || actual.head != native.head {
		return out, ErrConflict
	}
	now, err := mgLCControlTime(ctx, turn)
	if err != nil {
		return out, err
	}
	return mgLCControlResult{channel: actual, applied: now}, nil
}

// reorderMGLCControlChannels is one atomic compare-and-swap of the entire small
// directory. It moves positions and configuration text only; seeds, programme
// identity, entries and accepted playback references are untouched. A pending
// build follows its channel to the new revision instead of being superseded.
func reorderMGLCControlChannels(ctx context.Context, turn *commandTurn, w mgLCControlWitness, order []mgLCControlOrder) (out mgLCControlResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	native, err := readMGLCControlDirectory(ctx, turn)
	if err != nil {
		return out, err
	}
	if len(native) != len(w.directory) || len(native) != len(order) {
		return out, ErrConflict
	}
	witness := map[string]mgLCControlChannel{}
	for _, ch := range w.directory {
		witness[ch.id] = ch
	}
	current := map[string]mgLCControlChannel{}
	for _, ch := range native {
		if witness[ch.id] != ch {
			return out, ErrConflict
		}
		current[ch.id] = ch
	}
	if len(current) != len(native) {
		return out, ErrConflict
	}
	seen := map[string]bool{}
	for _, o := range order {
		if seen[o.channel.id] || current[o.channel.id] != o.channel {
			return out, ErrConflict
		}
		seen[o.channel.id] = true
	}
	if len(seen) != len(native) {
		return out, ErrConflict
	}
	result := make([]mgLCControlChannel, 0, len(order))
	for n, o := range order {
		before := current[o.channel.id]
		if o.config.name != before.name {
			return out, ErrInvalid
		}
		next := before
		next.revision++
		next.position = int64(n)
		next.config = o.config.canonical
		row, err := mgLCControlRow(ctx, turn, mgLCControlUpdate,
			next.revision, next.config, next.enabled, next.position, next.name, next.state, next.health, next.removed,
			before.id, before.revision, before.config, before.enabled, before.position, before.name, before.state, before.health, before.removed, before.head)
		if err != nil {
			return out, err
		}
		actual, err := scanMGLCControlChannel(row)
		if err != nil {
			return out, err
		}
		if actual != next {
			return out, ErrConflict
		}
		pending, present, err := readMGLCControlPending(ctx, turn, before.id)
		if err != nil {
			return out, err
		}
		if present {
			if pending.configRevision != before.revision {
				return out, ErrConflict
			}
			row, err := mgLCControlRow(ctx, turn, mgLCControlPendingAlign, next.revision, next.config, before.id, before.revision)
			if err != nil {
				return out, err
			}
			after, err := scanMGLCControlPending(row)
			if err != nil {
				return out, err
			}
			if after.id != pending.id || after.configRevision != next.revision || after.config != next.config ||
				after.status != pending.status || after.lease != pending.lease || after.leaseUntil != pending.leaseUntil ||
				after.last != pending.last || after.ack != pending.ack || after.stageRevision != pending.stageRevision ||
				after.entries != pending.entries || after.bytes != pending.bytes || !bytes.Equal(after.entryDigest, pending.entryDigest) ||
				after.receiptRows != pending.receiptRows || after.receiptBytes != pending.receiptBytes ||
				after.reclaimAfter != pending.reclaimAfter || after.reclaimDue != pending.reclaimDue {
				return out, ErrConflict
			}
		}
		result = append(result, actual)
	}
	now, err := mgLCControlTime(ctx, turn)
	if err != nil {
		return out, err
	}
	return mgLCControlResult{directory: result, applied: now}, nil
}

// Canonical request/result encoding for the closed control receipts. Lengths
// and fixed scalars only; no JSON/reflection result registry.
func appendMGLCControlText(dst []byte, s string) []byte {
	dst = binary.BigEndian.AppendUint64(dst, uint64(len(s)))
	return append(dst, s...)
}

func appendMGLCControlChannel(dst []byte, ch mgLCControlChannel) []byte {
	for _, s := range []string{ch.id, ch.config, ch.name, ch.state, ch.health, ch.head} {
		dst = appendMGLCControlText(dst, s)
	}
	for _, n := range []int64{ch.revision, ch.enabled, ch.position, ch.through, ch.removed} {
		dst = binary.BigEndian.AppendUint64(dst, uint64(n))
	}
	return dst
}

func mgLCControlRequestDigest(kind mgLCControlKind, w mgLCControlWitness, in mgLCControlInput) RequestDigest {
	p := []byte("mg-lc-control-request-v1\x00")
	p = appendMGLCControlText(p, string(kind))
	p = append(p, w.issuer[:]...)
	p = appendMGLCControlChannel(p, w.channel)
	p = binary.BigEndian.AppendUint64(p, uint64(len(w.directory)))
	for _, ch := range w.directory {
		p = appendMGLCControlChannel(p, ch)
	}
	if in.config != nil {
		p = binary.BigEndian.AppendUint64(p, 1)
		p = appendMGLCControlText(p, in.config.canonical)
		p = appendMGLCControlText(p, in.config.name)
		p = binary.BigEndian.AppendUint64(p, uint64(in.config.enabled))
		p = binary.BigEndian.AppendUint64(p, uint64(in.config.position))
	} else {
		p = binary.BigEndian.AppendUint64(p, 0)
	}
	p = binary.BigEndian.AppendUint64(p, uint64(len(in.order)))
	for _, o := range in.order {
		p = appendMGLCControlChannel(p, o.channel)
		p = appendMGLCControlText(p, o.config.canonical)
		p = appendMGLCControlText(p, o.config.name)
		p = binary.BigEndian.AppendUint64(p, uint64(o.config.enabled))
		p = binary.BigEndian.AppendUint64(p, uint64(o.config.position))
	}
	return sha256.Sum256(p)
}

func mgLCControlResultBytes(s mgLCControlSaved) []byte {
	p := []byte("mg-lc-control-result-v1\x00")
	p = appendMGLCControlText(p, string(s.kind))
	p = append(p, s.command.identity.Incarnation[:]...)
	p = binary.BigEndian.AppendUint64(p, uint64(s.command.identity.Sequence))
	p = append(p, s.command.identity.OperationID[:]...)
	p = append(p, s.command.request[:]...)
	p = binary.BigEndian.AppendUint64(p, uint64(s.result.applied))
	p = appendMGLCControlChannel(p, s.result.channel)
	p = binary.BigEndian.AppendUint64(p, uint64(len(s.result.directory)))
	for _, ch := range s.result.directory {
		p = appendMGLCControlChannel(p, ch)
	}
	return p
}

func mgLCControlResultDigest(s mgLCControlSaved) ResultDigest {
	p := []byte("mg-lc-control-result-digest-v1\x00")
	p = append(p, mgLCControlResultBytes(s)...)
	return sha256.Sum256(p)
}

func mgLCControlRequestKey(c mgLCControlCommand) string {
	return hex.EncodeToString(c.identity.OperationID[:])
}

// decodeMGLCControlResult parses a closed stored body back into native facts.
// It performs no SQL and accepts no caller-supplied authority; the caller
// re-encodes and compares the complete bytes, so any edited field fails.
func decodeMGLCControlResult(body []byte, c mgLCControlCommand, kind mgLCControlKind) (out mgLCControlSaved, err error) {
	if len(body) == 0 || len(body) > mgLCBytes {
		return out, ErrResolveRequired
	}
	const magic = "mg-lc-control-result-v1\x00"
	if !bytes.HasPrefix(body, []byte(magic)) {
		return out, ErrResolveRequired
	}
	r := bytes.NewReader(body[len(magic):])
	text := func() (string, error) {
		var n uint64
		if binary.Read(r, binary.BigEndian, &n) != nil || n > mgLCBytes || n > uint64(r.Len()) {
			return "", ErrResolveRequired
		}
		b := make([]byte, int(n))
		if _, err := io.ReadFull(r, b); err != nil {
			return "", ErrResolveRequired
		}
		return string(b), nil
	}
	number := func() (int64, error) {
		var n uint64
		if binary.Read(r, binary.BigEndian, &n) != nil {
			return 0, ErrResolveRequired
		}
		return int64(n), nil
	}
	// The raw channel parser validates nothing: the closed result kind decides
	// whether this entry is a primary channel or a directory member.
	channel := func() (mgLCControlChannel, error) {
		var ch mgLCControlChannel
		for _, target := range []*string{&ch.id, &ch.config, &ch.name, &ch.state, &ch.health, &ch.head} {
			s, err := text()
			if err != nil {
				return ch, ErrResolveRequired
			}
			*target = s
		}
		for _, target := range []*int64{&ch.revision, &ch.enabled, &ch.position, &ch.through, &ch.removed} {
			n, err := number()
			if err != nil {
				return ch, ErrResolveRequired
			}
			*target = n
		}
		return ch, nil
	}
	storedKind, err := text()
	if err != nil {
		return out, ErrResolveRequired
	}
	if mgLCControlKind(storedKind) != kind || !kind.valid() {
		return out, ErrConflict
	}
	var incarnation [16]byte
	if _, err := io.ReadFull(r, incarnation[:]); err != nil {
		return out, ErrResolveRequired
	}
	if incarnation != c.identity.Incarnation {
		return out, ErrConflict
	}
	sequence, err := number()
	if err != nil {
		return out, ErrResolveRequired
	}
	if sequence != c.identity.Sequence {
		return out, ErrConflict
	}
	var operation [16]byte
	if _, err := io.ReadFull(r, operation[:]); err != nil {
		return out, ErrResolveRequired
	}
	if operation != c.identity.OperationID {
		return out, ErrConflict
	}
	var request [32]byte
	if _, err := io.ReadFull(r, request[:]); err != nil {
		return out, ErrResolveRequired
	}
	if request != c.request {
		return out, ErrConflict
	}
	applied, err := number()
	if err != nil || applied <= 0 {
		return out, ErrResolveRequired
	}
	ch, err := channel()
	if err != nil {
		return out, ErrResolveRequired
	}
	count, err := number()
	if err != nil || count < 0 || count > mgLCControlMaxChannels {
		return out, ErrResolveRequired
	}
	var directory []mgLCControlChannel
	for n := int64(0); n < count; n++ {
		entry, err := channel()
		if err != nil || !entry.valid() {
			return out, ErrResolveRequired
		}
		directory = append(directory, entry)
	}
	if r.Len() != 0 {
		return out, ErrResolveRequired
	}
	// The stored result shape is closed by the kind: a reorder owns only the
	// directory snapshot (zero primary channel); every single-channel effect
	// owns only its primary channel (empty directory).
	if kind == mgLCControlReorderKind {
		if ch != (mgLCControlChannel{}) || len(directory) == 0 {
			return out, ErrResolveRequired
		}
	} else if !ch.valid() || len(directory) != 0 {
		return out, ErrResolveRequired
	}
	out = mgLCControlSaved{command: c, kind: kind, result: mgLCControlResult{channel: ch, directory: directory, applied: applied}}
	if !bytes.Equal(mgLCControlResultBytes(out), body) {
		return mgLCControlSaved{}, ErrResolveRequired
	}
	out.digest = mgLCControlResultDigest(out)
	return out, nil
}
