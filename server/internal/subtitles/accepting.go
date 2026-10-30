package subtitles

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// SelectionAcceptance belongs to the actual current session-command owner.
// It cannot replace the subtitle actor/resource/source/CAS checks. Private disc
// transitions require it so legacy generation, immutable child binding and V1
// presentation acceptance all commit in one writer. No callback performs IO.
type SelectionAcceptance interface {
	CheckTx(context.Context, *sql.Tx) error
	BeforeGenerationTx(context.Context, *sql.Tx, string, int) error
	AfterGenerationTx(context.Context, *sql.Tx, string, int, string) (string, error)
	AcceptTx(context.Context, *sql.Tx, PlaybackPlan) error
}

type selectionAcceptanceKey struct{}

func selectionAcceptance(ctx context.Context) SelectionAcceptance {
	a, _ := ctx.Value(selectionAcceptanceKey{}).(SelectionAcceptance)
	return a
}

func (s *Service) SelectAccepting(ctx context.Context, p identity.Principal, item, session string, m SelectRequest, a SelectionAcceptance) (PlaybackPlan, error) {
	if a == nil {
		return PlaybackPlan{}, ErrInput
	}
	return s.selectPlayback(context.WithValue(ctx, selectionAcceptanceKey{}, a), p, item, session, m, false)
}

func (s *Service) checkSelectionAcceptanceTx(ctx context.Context, tx *sql.Tx, session string) error {
	_, private, err := s.privateDiscPresentationTx(ctx, tx, session)
	if err != nil {
		return err
	}
	a := selectionAcceptance(ctx)
	if private && a == nil {
		return ErrUnsupported
	}
	if a != nil {
		return a.CheckTx(ctx, tx)
	}
	return nil
}
