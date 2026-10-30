package mediaanalysis

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// capturedSource retains a STRM descriptor and the exact, policy-checked remote
// version while a request runs. Network IO is always outside SQL transactions.
type capturedSource struct {
	source     Source
	input      Input
	invocation *ReadInvocation
}

func (s *Service) Capture(ctx context.Context, a Access, t Target) (Access, func(), error) {
	noop := func() {}
	if a.captured != nil {
		return a, noop, nil
	}
	gated, e := s.begin(ctx, a)
	if e != nil {
		return a, noop, e
	}
	tx := gated.Tx()
	preflight := t
	preflight.MappingRevision = ""
	v, e := s.resolve(ctx, tx, a, preflight)
	var facts ReadFacts
	if e == nil && v.Container == "strm" {
		facts, e = readFactsTx(ctx, tx, v.RootID, v.ObjectID, v.Revision, a.ItemID, v.ID)
		if e == storage.ErrRemoteAnalysisDisabled {
			e = ErrUnsupported
		}
		if e == nil && (facts.Size != v.Size || facts.ModifiedNS != v.Modified) {
			e = ErrConflict
		}
	}
	gated.Rollback()
	if e != nil {
		return a, noop, e
	}
	if v.Container != "strm" {
		return a, noop, nil
	}
	// This is an actual authorized source-verification request, not playback.
	// Freeze the pre-capture Access so rechecking never recurses into itself or
	// adopts a new caller/source after a descriptor/remote target await.
	originalAccess := a
	ctx, invocation := s.readContext(ctx, facts, func(check context.Context, tx *sql.Tx) error {
		if err := originalAccess.Authorize(tx); err != nil {
			return err
		}
		if err := checkReadFactsTx(check, tx, facts); err != nil {
			return err
		}
		now, err := s.resolve(check, tx, originalAccess, preflight)
		if err != nil {
			return err
		}
		if now.ID != v.ID || now.ObjectID != v.ObjectID || now.RootID != v.RootID || now.Revision != v.Revision || now.Configuration != v.Configuration || now.Incarnation != v.Incarnation || now.Size != v.Size || now.Modified != v.Modified {
			return ErrConflict
		}
		return nil
	})
	input, e := s.options.Open(ctx, a.ItemID, v.ID)
	if e != nil {
		invocation.Close()
		return a, noop, e
	}
	input = &authorityInput{Input: input, invocation: invocation}
	close := func() { _ = input.Close(); _ = invocation.Close() }
	if e = input.Validate(ctx); e != nil {
		close()
		return a, noop, e
	}
	v.Evidence = input.Evidence()
	if assurance(v.Evidence) != "strong_version" {
		close()
		return a, noop, ErrUnsupported
	}
	a.captured = &capturedSource{v, input, invocation}
	return a, close, nil
}
func (a Access) validateCapture(ctx context.Context) error {
	if a.captured != nil {
		return a.captured.input.Validate(ctx)
	}
	return ctx.Err()
}

// RequestProbeRefresh is used only by the explicit queue/retry transaction, not
// by browsing. A changed target must pass Basic again before any derived work.
func (s *Service) RequestProbeRefresh(ctx context.Context, tx *sql.Tx, a Access, t Target) error {
	if e := s.AuthorizeTarget(ctx, tx, a, t); e != nil {
		return e
	}
	v, e := s.resolve(ctx, tx, a, t)
	if e != nil {
		return e
	}
	if v.NeedsProbe {
		_, e = tx.ExecContext(ctx, `UPDATE inventory_objects SET analysis_revision='',analysis_state='pending' WHERE id=? AND revision=?`, v.ObjectID, v.Revision)
	}
	return e
}
