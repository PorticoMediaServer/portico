package playback

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
)

func TestDirectStartObservesHardwareOutsideWriter(t *testing.T) {
	db, service, principal, _, item, _ := activityFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	calls := 0
	detector := &decoder.HardwareDetector{RuntimeIdentity: func(check context.Context, _ string) (string, error) {
		calls++
		if dbwork.Snapshot(check) != nil {
			t.Error("identity work received a SQL carrier")
		}
		// A genuine writer can be taken while hashing, even on the warm start.
		bounded, stop := context.WithTimeout(check, 250*time.Millisecond)
		defer stop()
		writer, err := dbwork.Begin(bounded, db, dbwork.ClassEstablishedPlayback)
		if err != nil {
			t.Error("identity observation held the playback writer", err)
			return "", err
		}
		_ = writer.Rollback()
		return "", errors.New("test host has no qualified hardware identity")
	}}
	cfg := DefaultDeliveryConfiguration()
	cfg.HardwareBackend = "auto"
	service.ConfigureDelivery(fixedSettings{cfg}, nil, detector, "/test/ffmpeg")
	for _, key := range []string{"first-direct", "warm-direct"} {
		session, err := service.CreateV1(ctx, principal, item, key, V1Choice{}, nil)
		if err != nil || session.Mode != "direct" {
			t.Fatal(session, err)
		}
	}
	if calls != 2 {
		t.Fatalf("identity observations: %d", calls)
	}
}

func TestDeliveryPreflightRechecksActualAcceptingConfiguration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, mutation := range []string{
		`UPDATE console_settings_revision SET revision=revision+1 WHERE singleton=1`,
		`UPDATE playback_owner_policy SET revision=revision+1 WHERE singleton=1`,
		`UPDATE playback_owner_policy SET transcoding_enabled=1-transcoding_enabled WHERE singleton=1`,
	} {
		t.Run(mutation, func(t *testing.T) {
			db, service, _, _, _, _ := activityFixture(t)
			original, err := service.prepareDelivery(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			writer, err := dbwork.Begin(original, db, dbwork.ClassEstablishedPlayback)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = writer.Rollback() })
			if _, err = service.checkedDeliveryTx(writer.Context(), writer.Tx()); err != nil {
				t.Fatal(err)
			}
			if _, err = writer.Tx().ExecContext(writer.Context(), mutation); err != nil {
				t.Fatal(err)
			}
			if _, err = service.checkedDeliveryTx(writer.Context(), writer.Tx()); !errors.Is(err, ErrStaleOffer) {
				t.Fatal("uncommitted accepting drift admitted", err)
			}
		})
	}
}

type originalDeliverySettings struct {
	cfg   DeliveryConfiguration
	reads int
}

func (s *originalDeliverySettings) Delivery() DeliveryConfiguration {
	panic("cached delivery used for accepting proof")
}
func (s *originalDeliverySettings) DeliveryTx(ctx context.Context, tx *sql.Tx) (DeliveryConfiguration, error) {
	if dbwork.Snapshot(ctx) != tx {
		return DeliveryConfiguration{}, ErrStaleOffer
	}
	s.reads++
	return s.cfg, ctx.Err()
}

func TestDeliveryPreflightUsesSameTransactionSettingsAndRefusesForeignScope(t *testing.T) {
	db, service, _, _, _, _ := activityFixture(t)
	settings := &originalDeliverySettings{cfg: DefaultDeliveryConfiguration()}
	service.ConfigureDelivery(settings, nil, nil, "")
	ctx, err := service.prepareDelivery(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	writer, err := dbwork.Begin(ctx, db, dbwork.ClassEstablishedPlayback)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Rollback() })
	if _, err = service.checkedDeliveryTx(writer.Context(), writer.Tx()); err != nil {
		t.Fatal(err)
	}
	if settings.reads != 2 {
		t.Fatal(settings.reads)
	}
	settings.cfg.HardwareBackend = "software"
	if _, err = service.checkedDeliveryTx(writer.Context(), writer.Tx()); !errors.Is(err, ErrStaleOffer) {
		t.Fatal(err)
	}
	if _, err = New(db).checkedDeliveryTx(writer.Context(), writer.Tx()); !errors.Is(err, ErrStaleOffer) {
		t.Fatal("foreign service accepted", err)
	}
	if _, err = service.prepareDelivery(writer.Context()); err != nil {
		t.Fatal("existing private preflight lost", err)
	}
	carried, err := dbwork.BindSnapshot(context.Background(), writer.Tx())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.prepareDelivery(carried); !errors.Is(err, ErrStaleOffer) {
		t.Fatal("carried SQL attempted preflight", err)
	}
}

func TestDeliveryPreflightRefusesReplacedToolchainAndRetiredCaller(t *testing.T) {
	db, service, _, _, _, _ := activityFixture(t)
	prior := decoder.CurrentToolchain()
	t.Cleanup(func() { decoder.RestoreToolchain(prior) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	original, err := service.prepareDelivery(ctx)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := dbwork.Begin(original, db, dbwork.ClassEstablishedPlayback)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Rollback() })
	decoder.RestoreToolchain(&decoder.ToolchainFacts{Identity: "replacement"})
	if _, err = service.checkedDeliveryTx(writer.Context(), writer.Tx()); !errors.Is(err, ErrStaleOffer) {
		t.Fatal("replaced inventory admitted", err)
	}
	decoder.RestoreToolchain(prior)
	cancel()
	if _, err = service.checkedDeliveryTx(writer.Context(), writer.Tx()); !errors.Is(err, context.Canceled) {
		t.Fatal("retired caller admitted", err)
	}
}
