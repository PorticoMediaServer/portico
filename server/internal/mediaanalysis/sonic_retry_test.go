package mediaanalysis

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server/internal/sonic"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/testtier"
)

func TestSonicRetryChecksAuthorityBeforeInstallationAndBeforePublication(t *testing.T) {
	c := catalogtest.Open(t)
	c.Library("music", "Music", "music", "/music")
	loads := 0
	s := &Service{db: c.DB, slot: make(chan struct{}, 1), options: Options{OpenSonic: func() (*sonic.Runtime, error) { loads++; return nil, sonic.ErrUnavailable }}}
	ctx := context.Background()
	denied := errors.New("owner revoked")
	if err := s.RetrySonic(ctx, "music", nil); !errors.Is(err, ErrInput) || loads != 0 {
		t.Fatal(err, loads)
	}
	if err := s.RetrySonic(ctx, "music", func(*sql.Tx) error { return denied }); !errors.Is(err, denied) || loads != 0 {
		t.Fatal("installation read before authority", err, loads)
	}
	checks := 0
	if err := s.RetrySonic(ctx, "music", func(*sql.Tx) error {
		checks++
		if checks == 2 {
			return denied
		}
		return nil
	}); !errors.Is(err, denied) || loads != 1 || checks != 2 {
		t.Fatal("late revocation ignored", err, loads, checks)
	}
	if err := s.RetrySonic(ctx, "music", func(*sql.Tx) error { return nil }); !errors.Is(err, sonic.ErrUnavailable) || loads != 2 || s.SonicCanAttempt() {
		t.Fatal("unavailable reload claimed ready", err, loads)
	}
	s.slot <- struct{}{}
	if err := s.RetrySonic(ctx, "music", func(*sql.Tx) error { return nil }); !errors.Is(err, ErrConflict) || loads != 2 {
		t.Fatal("overlapped active analysis", err, loads)
	}
	<-s.slot
}

func TestSonicActualRetryVerifiesCPUWithoutPublishingSyntheticSong(t *testing.T) {
	testtier.Media(t, "actual original-model runtime loader verification")
	helper, model := os.Getenv("PORTICO_SONIC_HELPER_TEST"), os.Getenv("PORTICO_SONIC_MODEL_TEST")
	if helper == "" || model == "" {
		t.Skip("actual checked model/helper required")
	}
	c := catalogtest.Open(t)
	c.Library("music", "Music", "music", "/music")
	if _, err := mediaexec.Configure(mediaexec.Options{StateDir: canonicalFixtureDir(t), SkipOrphanKill: true}); err != nil {
		t.Fatal(err)
	}
	sup := &storage.Supervisor{Limit: 1}
	s := &Service{db: c.DB, slot: make(chan struct{}, 1), options: Options{OpenSonic: func() (*sonic.Runtime, error) {
		return sonic.OpenRuntime(sonic.RuntimeOptions{Helper: helper, ModelDirectory: model, Supervisor: sup})
	}}}
	checks := 0
	if err := s.RetrySonic(context.Background(), "music", func(*sql.Tx) error { checks++; return nil }); err != nil {
		t.Fatal(err)
	}
	if checks != 2 || !s.SonicCanAttempt() || !s.SonicRuntimeAvailable() || s.sonicRuntime().Availability().State != "ready" || sup.Active() != 0 {
		t.Fatal("not an actual retired loader probe", checks, s.sonicRuntime().Availability(), sup.Active())
	}
	var count int
	if err := c.DB.QueryRow(`SELECT count(*) FROM music_sonic_embeddings`).Scan(&count); err != nil || count != 0 || len(s.sonicProgress) != 0 {
		t.Fatal("synthetic probe counted as a song", count, err)
	}
}

func TestSonicRuntimeAvailableReadsOnlyVerifiedCachedState(t *testing.T) {
	var absent *Service
	if absent.SonicRuntimeAvailable() {
		t.Fatal("nil service claimed a verified engine")
	}
	s := &Service{options: Options{OpenSonic: func() (*sonic.Runtime, error) { t.Fatal("availability invoked loader"); return nil, nil }}}
	if s.SonicRuntimeAvailable() {
		t.Fatal("missing runtime claimed available")
	}
	// An unverified object permits an attempt, but has no successful numeric
	// probe. With no DB installed this read must still work without any SQL.
	s.options.Sonic = &sonic.Runtime{}
	if !s.SonicCanAttempt() || s.SonicRuntimeAvailable() {
		t.Fatal("admission substituted for verified readiness")
	}
}
