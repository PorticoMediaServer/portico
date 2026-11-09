package writer

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strings"
	"unicode/utf8"
)

type mpGuidePinSelection struct {
	Occurrence                           mpGuidePinOccurrence
	SourceID, GuideGeneration, ChannelID string
	ProgrammeID                          sql.NullString
	CapturedMS                           int64
}
type mpGuidePinEffect struct {
	Rows, BeforeBytes, AfterBytes, PhysicalBeforeBytes, PhysicalAfterBytes int64
	Noop                                                                   string
	release                                                                *mpPhysicalRelease
}

const (
	mpGuideInstallPhysical  stmt.ID = "mp.guide_pin.install_physical"
	mpGuideInstallSource    stmt.ID = "mp.guide_pin.install_source"
	mpGuideInstallChannel   stmt.ID = "mp.guide_pin.install_channel"
	mpGuideInstallProgramme stmt.ID = "mp.guide_pin.install_programme"
	mpGuideInstall          stmt.ID = "mp.guide_pin.install"
	mpGuideReleaseHeader    stmt.ID = "mp.guide_pin.release_header"
	mpGuideReleaseReaders   stmt.ID = "mp.guide_pin.release_readers"
	mpGuideReleaseWindow    stmt.ID = "mp.guide_pin.release_window"
	mpGuideReleasePhysical  stmt.ID = "mp.guide_pin.release_physical"
	mpGuideRecordExit       stmt.ID = "mp.guide_pin.record_exit"
	mpGuideRelease          stmt.ID = "mp.guide_pin.release"
)

func mpGuideID(s string) bool {
	return len(s) > 0 && len(s) <= 512 && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}
func mpGuidePurpose(t *commandTurn, p CommandID) error {
	if t == nil || t.transaction == nil || t.transaction.descriptor.ID != p || !t.transaction.begun || t.transaction.done {
		return ErrInvalid
	}
	return nil
}

// Fresh-only MP10 source-pin effect, after W/MG current disclosure, guide/source
// selection and receipt classification in the SAME turn. No public constructor,
// grant or Applied driver is introduced. MP10's accepting authority remains a
// composition gate. Every frozen F install predicate is rechecked by its exact
// indexed point before INSERT; a single writer turn prevents intervening drift.
func installMPExternalGuidePin(ctx context.Context, t *commandTurn, q mpGuidePinSelection) (out mpGuidePinEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpGuidePurpose(t, 720); err != nil {
		return out, err
	}
	k := q.Occurrence
	if !k.valid() || !mpGuideID(q.SourceID) || !mpGuideID(q.GuideGeneration) || !mpGuideID(q.ChannelID) || q.CapturedMS <= 0 || q.CapturedMS > 9999999999999 || q.ProgrammeID.Valid && !mpGuideID(q.ProgrammeID.String) || !q.ProgrammeID.Valid && q.ProgrammeID.String != "" {
		return out, ErrInvalid
	}
	if _, err = wc008DraftInteger(ctx, t, mpGuideInstallPhysical, k.MediaSessionID, k.Generation, k.ExecutionID, k.SourceRequestID); err != nil {
		return out, err
	}
	if _, err = wc008DraftInteger(ctx, t, mpGuideInstallSource, q.SourceID, q.GuideGeneration, q.GuideGeneration, q.SourceID); err != nil {
		return out, err
	}
	if _, err = wc008DraftInteger(ctx, t, mpGuideInstallChannel, q.GuideGeneration, q.ChannelID); err != nil {
		return out, err
	}
	var programme any
	if q.ProgrammeID.Valid {
		programme = q.ProgrammeID.String
		if _, err = wc008DraftInteger(ctx, t, mpGuideInstallProgramme, q.GuideGeneration, programme, q.ChannelID); err != nil {
			return out, err
		}
	}
	n, err := wc008DraftInteger(ctx, t, mpGuideInstall, k.MediaSessionID, k.Generation, k.ExecutionID, k.SourceRequestID, q.SourceID, q.GuideGeneration, q.ChannelID, programme, q.CapturedMS, k.Generation, k.ExecutionID, q.SourceID, q.GuideGeneration, q.ChannelID, programme)
	if err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if n <= 0 || n > 8192 {
		return out, ErrResolveRequired
	}
	return mpGuidePinEffect{Rows: 1, AfterBytes: n}, t.checkLifetimes(ctx)
}

// The actual MP05 join and cleanup commitment precedes this turn. A lease,
// cancel, worker ACK or logical successor cannot create the private observation.
// Exit+pin release+receipt/debt must COMMIT together before the outer owner calls
// releaseMPPhysicalAfterCommit with out.release. Rollback/unknown keeps custody.
func releaseMPExternalGuidePin(ctx context.Context, t *commandTurn, s *mpPhysicalSupervisor, o *mpPhysicalObservation, k mpGuidePinOccurrence, retirement mpPhysicalKey) (out mpGuidePinEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpGuidePurpose(t, 727); err != nil {
		return out, err
	}
	if !k.valid() {
		return out, ErrInvalid
	}
	row, err := wc008DraftQuery(ctx, t, mpGuideReleaseHeader, k.MediaSessionID, k.Generation, k.ExecutionID, k.SourceRequestID)
	if err != nil {
		return out, err
	}
	var released, captured int64
	if err = row.Scan(&out.BeforeBytes, &released, &captured); errors.Is(err, sql.ErrNoRows) {
		return mpGuidePinEffect{Noop: "absent"}, nil
	} else if err != nil {
		return out, err
	}
	if out.BeforeBytes <= 0 || out.BeforeBytes > 8192 {
		return out, ErrResolveRequired
	}
	out.AfterBytes = out.BeforeBytes
	if released > 0 {
		out.Noop = "already_released"
		return out, nil
	}
	if retirement.media != k.MediaSessionID || retirement.generation != k.Generation || retirement.execution != k.ExecutionID {
		return out, ErrDisclosureDenied
	}
	v, err := consumeMPPhysicalObservation(ctx, t, s, o, retirement)
	if err != nil {
		return out, err
	}
	row, err = wc008DraftQuery(ctx, t, mpGuideReleaseReaders, k.MediaSessionID)
	if err != nil {
		return out, err
	}
	var one int64
	if err = row.Scan(&one); err == nil {
		return out, ErrConflict
	} else if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	// At most eight metadata windows plus one lookahead, never an unbounded
	// NOT EXISTS scan. MP18 drains larger completed sets before retrying release.
	after := int64(-1)
	for n := 0; n < 9; n++ {
		row, err = wc008DraftQuery(ctx, t, mpGuideReleaseWindow, k.MediaSessionID, after)
		if err != nil {
			return out, err
		}
		var start, exited, cleaned int64
		if err = row.Scan(&start, &exited, &cleaned); errors.Is(err, sql.ErrNoRows) {
			err = nil
			break
		} else if err != nil {
			return out, err
		}
		if n == 8 {
			return out, ErrCap
		}
		if exited != 1 || cleaned != 1 {
			return out, ErrConflict
		}
		after = start
	}
	row, err = wc008DraftQuery(ctx, t, mpGuideReleasePhysical, k.MediaSessionID, k.Generation, k.ExecutionID, k.SourceRequestID)
	if err != nil {
		return out, err
	}
	var exited, exitMS int64
	var supervisor []byte
	var digest string
	if err = row.Scan(&out.PhysicalBeforeBytes, &exited, &exitMS, &supervisor, &digest); err != nil {
		return out, err
	}
	now := t.transaction.writer.now()
	if err = t.transaction.observeTime(now); err != nil {
		return out, err
	}
	ms := now.UnixMilli()
	if ms <= 0 || ms < captured || ms > 9999999999999 {
		return out, ErrConflict
	}
	want := hex.EncodeToString(v.digest[:])
	out.PhysicalAfterBytes = out.PhysicalBeforeBytes
	if exited == k.Generation {
		if string(supervisor) != string(v.supervisor[:]) || digest != want || exitMS <= 0 || exitMS > ms {
			return out, ErrDisclosureDenied
		}
	} else {
		row, err = wc008DraftQuery(ctx, t, mpGuideRecordExit, v.supervisor[:], ms, want, int64(len(o.execution.cleanup)), k.MediaSessionID, v.consumption.key.revision, k.Generation, k.ExecutionID, retirement.request)
		if err != nil {
			return out, err
		}
		if err = row.Scan(&out.PhysicalAfterBytes); err != nil {
			return out, errors.Join(ErrConflict, err)
		}
	}
	out.AfterBytes, err = wc008DraftInteger(ctx, t, mpGuideRelease, nil, nil, nil, nil, nil, nil, ms, k.MediaSessionID, k.Generation, k.ExecutionID, k.SourceRequestID)
	if err != nil {
		return out, err
	}
	if out.AfterBytes <= 0 || out.AfterBytes > out.BeforeBytes {
		return out, ErrResolveRequired
	}
	out.release, err = sealMPPhysicalRelease(ctx, t, v)
	if err != nil {
		return out, err
	}
	out.Rows = 1
	return out, t.checkLifetimes(ctx)
}
