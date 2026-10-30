package mediaanalysis

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/sonic"
)

func (s *Service) sonicRuntime() *sonic.Runtime {
	if s == nil {
		return nil
	}
	s.sonicMu.RLock()
	defer s.sonicMu.RUnlock()
	return s.options.Sonic
}

// SonicRuntimeAvailable is the same verified readiness fact published as
// SonicStatusView.available. It reads only the current runtime's cached state:
// no database, model loading, inference, scheduling or library-policy decision.
func (s *Service) SonicRuntimeAvailable() bool {
	return s.sonicRuntime().Availability().State == "ready"
}

// RetrySonic reloads only the server's immutable installation options. It
// accepts no client path or model and starts no worker. A single fixed numeric
// patch verifies the actual CPU loader; it is not a song result or progress.
// Existing pending inventory work remains the sole scheduled producer. Source
// authorization is checked before reading installation files and again in the
// transaction requesting a refresh; a revoked owner cannot publish a reload.
func (s *Service) RetrySonic(ctx context.Context, library string, authorize func(*sql.Tx) error) error {
	if s == nil || s.db == nil || authorize == nil {
		return ErrInput
	}
	check := func(tx *sql.Tx) error {
		if err := authorize(tx); err != nil {
			return err
		}
		_, err := catalog.SonicSettingsTx(ctx, tx, library)
		return err
	}
	tx, release, err := dbwork.BeginRead(ctx, s.db)
	if err != nil {
		return err
	}
	err = check(tx)
	release()
	if err != nil {
		return err
	}
	select {
	case s.slot <- struct{}{}:
		defer func() { <-s.slot }()
	default:
		return ErrConflict
	}
	if s.options.OpenSonic == nil {
		return sonic.ErrUnavailable
	}
	runtime, loadErr := s.options.OpenSonic()
	if loadErr != nil {
		runtime = nil
	}
	if loadErr == nil && runtime == nil {
		loadErr = sonic.ErrUnavailable
	}
	if loadErr == nil {
		_, loadErr = runtime.Run(ctx, "engine-verification", nil, func(yield func([]float32) error) error {
			return yield(make([]float32, sonic.PatchFrames*sonic.MelBands))
		})
	}
	if err = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		if err := check(tx); err != nil {
			return err
		}
		if loadErr == nil {
			return catalog.QueueSonicRefreshTx(ctx, tx)
		}
		return nil
	}); err != nil {
		return err
	}
	// No analysis can hold a model binding while the slot is held. Readers see
	// either complete runtime object. Only the actual numeric probe marks it
	// ready; its synthetic vector is never stored or counted as a song.
	s.sonicMu.Lock()
	s.options.Sonic = runtime
	s.sonicMu.Unlock()
	return loadErr
}
