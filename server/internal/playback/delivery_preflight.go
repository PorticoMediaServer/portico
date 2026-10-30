package playback

import (
	"context"
	"database/sql"
	"maps"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
)

// Production settings supply a DB-only read of the accepting transaction.
// Fixed settings used by embedded compositions retain their ordinary value.
type transactionalDeliverySettings interface {
	DeliveryTx(context.Context, *sql.Tx) (DeliveryConfiguration, error)
}

type deliveryState struct {
	configuration                   DeliveryConfiguration
	settingsRevision, ownerRevision int64
	enabled                         bool
}

type deliveryPreflight struct {
	service    *Service
	state      deliveryState
	hardware   decoder.HardwareProbe
	toolchain  *decoder.ToolchainFacts
	detector   *decoder.HardwareDetector
	executable string
}
type deliveryPreflightKey struct{}

func (s *Service) deliveryStateTx(ctx context.Context, tx *sql.Tx) (deliveryState, error) {
	var out deliveryState
	bound, err := dbwork.BindSnapshot(ctx, tx)
	if err != nil {
		return out, err
	}
	ctx = bound
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if settings, ok := s.deliverySettings.(transactionalDeliverySettings); ok {
		configuration, err := settings.DeliveryTx(ctx, tx)
		if err != nil {
			return out, err
		}
		out.configuration = configuration.Normalized()
	} else {
		out.configuration = deliveryConfiguration(s.deliverySettings)
	}
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM console_settings_revision WHERE singleton=1`).Scan(&out.settingsRevision); err != nil {
		return out, err
	}
	err = tx.QueryRowContext(ctx, `SELECT revision,transcoding_enabled FROM playback_owner_policy WHERE singleton=1`).Scan(&out.ownerRevision, &out.enabled)
	return out, err
}

// prepareDelivery never hashes files while holding a SQL snapshot or writer.
// The result is planning input only; all actor/source/acceptance checks remain.
func (s *Service) prepareDelivery(ctx context.Context) (context.Context, error) {
	if ctx == nil {
		return nil, ErrStaleOffer
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if prior, ok := ctx.Value(deliveryPreflightKey{}).(*deliveryPreflight); ok && prior.service == s {
		return ctx, nil
	}
	if dbwork.Snapshot(ctx) != nil {
		return nil, ErrStaleOffer
	}
	tx, done, err := dbwork.BeginRead(ctx, s.db)
	if err != nil {
		return nil, err
	}
	state, err := s.deliveryStateTx(ctx, tx)
	done()
	if err != nil {
		return nil, err
	}
	tools := decoder.CurrentToolchain()
	hardware, err := s.selectHardware(ctx, state.configuration)
	if err != nil {
		return nil, err
	}
	if tools != decoder.CurrentToolchain() {
		return nil, ErrStaleOffer
	}
	// Probe cache maps are not borrowed mutable planning values.
	hardware.DecodeCodecs = maps.Clone(hardware.DecodeCodecs)
	hardware.OutputEncoders = maps.Clone(hardware.OutputEncoders)
	hardware.OutputQuality = maps.Clone(hardware.OutputQuality)
	return context.WithValue(ctx, deliveryPreflightKey{}, &deliveryPreflight{service: s, state: state, hardware: hardware, toolchain: tools, detector: s.hardware, executable: s.hardwareFFmpeg}), nil
}

func (s *Service) checkedDeliveryTx(ctx context.Context, tx *sql.Tx) (*deliveryPreflight, error) {
	if ctx == nil || tx == nil {
		return nil, ErrStaleOffer
	}
	prior, ok := ctx.Value(deliveryPreflightKey{}).(*deliveryPreflight)
	if !ok || prior.service != s || prior.detector != s.hardware || prior.executable != s.hardwareFFmpeg || prior.toolchain != decoder.CurrentToolchain() {
		return nil, ErrStaleOffer
	}
	bound, err := dbwork.BindSnapshot(ctx, tx)
	if err != nil {
		return nil, err
	}
	current, err := s.deliveryStateTx(bound, tx)
	if err != nil {
		return nil, err
	}
	if current != prior.state {
		return nil, ErrStaleOffer
	}
	return prior, ctx.Err()
}
