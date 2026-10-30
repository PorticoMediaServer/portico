package playback

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

func mustAudioExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, e := db.Exec(q, args...); e != nil {
		t.Fatal(e)
	}
}

// Conversion admission is the owner's policy, not a constant. It used to be two
// HLS sessions server-wide, repackaging included, with no setting that moved it.
func TestConversionAdmissionFollowsOwnerCeilings(t *testing.T) {
	db, s, p, _, item, _ := activityFixture(t)
	defer db.Close()
	s.ConfigureHLS(&HLS{})
	asset := firstCatalogAssetToken(t, db)
	// Unlimited by default: a household's worth of conversions is admitted.
	updateCatalogAsset(t, db, asset, func(a *compactcatalog.Asset) { a.Container, a.VideoCodec, a.AudioCodec = "mkv", "vp9", "opus" })
	for _, request := range []string{"one", "two", "three", "four"} {
		if _, e := s.Create(p, item, "automatic", request); e != nil {
			t.Fatal(request, e)
		}
	}
	// An owner ceiling of four is now full for another conversion...
	cfg := DefaultDeliveryConfiguration()
	cfg.MaxConversions = 4
	s.ConfigureDelivery(fixedSettings{cfg}, nil, nil, "")
	if _, e := s.Create(p, item, "automatic", "five"); !errors.Is(e, ErrConversionCapacity) {
		t.Fatal("owner conversion ceiling ignored", e)
	}
	var n int
	if e := db.QueryRow(`SELECT count(*) FROM playback_requests WHERE request_id LIKE '%five%'`).Scan(&n); e != nil || n != 0 {
		t.Fatal("a refused admission left a receipt", n, e)
	}
	// ...but never for a session that only repackages.
	updateCatalogAsset(t, db, asset, func(a *compactcatalog.Asset) { a.VideoCodec, a.AudioCodec = "h264", "aac" })
	session, e := s.Create(p, item, "automatic", "remux")
	if e != nil || session.Mode != "hls" {
		t.Fatal("a repackaging session was refused by the conversion ceiling", session, e)
	}
	for _, request := range []string{"remux-two", "remux-three", "remux-four"} {
		if _, err := s.Create(p, item, "automatic", request); err != nil {
			t.Fatal("remux counted as transcode", err)
		}
	}
	updateCatalogAsset(t, db, asset, func(a *compactcatalog.Asset) { a.AudioCodec = "opus" })
	if _, err := s.Create(p, item, "automatic", "audio-only"); err != nil {
		t.Fatal("audio-only counted as video transcode", err)
	}
	// The software ceiling counts software picture conversions only.
	cfg.MaxConversions, cfg.MaxSoftwareConversions = 0, 2
	s.ConfigureDelivery(fixedSettings{cfg}, nil, nil, "")
	updateCatalogAsset(t, db, asset, func(a *compactcatalog.Asset) { a.VideoCodec, a.AudioCodec = "vp9", "opus" })
	if _, e = s.Create(p, item, "automatic", "software"); !errors.Is(e, ErrConversionCapacity) {
		t.Fatal("software ceiling ignored", e)
	}
	// An unexpected query error cannot admit work as if nothing were running.
	mustAudioExec(t, db, `DROP TABLE playback_hls_reservations`)
	cfg.MaxSoftwareConversions = 0
	s.ConfigureDelivery(fixedSettings{cfg}, nil, nil, "")
	if _, e = s.Create(p, item, "automatic", "query-failed"); e == nil {
		t.Fatal("a failed admission write admitted the session")
	}
}

func TestAudioReservationSurvivesFailedCleanupAndSessionDeletion(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, s, p, _, item, _ := activityFixture(t)
	s.ConfigureHLS(&HLS{})
	asset := firstCatalogAssetToken(t, db)
	updateCatalogAsset(t, db, asset, func(a *compactcatalog.Asset) { a.Container = "mkv" })
	session, e := s.Create(p, item, "automatic", "cleanup")
	if e != nil {
		t.Fatal(e)
	}
	mustAudioExec(t, db, `DELETE FROM playback_sessions WHERE id=?`, session.ID)
	root := filepath.Join(t.TempDir(), "blocked")
	if e = os.WriteFile(root, []byte("block directory"), 0600); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	h := &HLS{db: db, root: root, active: map[string]context.CancelFunc{}}
	go func() { h.Run(ctx); close(done) }()
	time.Sleep(1100 * time.Millisecond)
	var n int
	if e = db.QueryRow(`SELECT count(*) FROM playback_hls_reservations WHERE session_id=?`, session.ID).Scan(&n); e != nil || n != 1 {
		t.Fatal("failed cleanup released reservation", n, e)
	}
	if e = os.Remove(root); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if e = db.QueryRow(`SELECT count(*) FROM playback_hls_reservations WHERE session_id=?`, session.ID).Scan(&n); e != nil {
			t.Fatal(e)
		}
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("successful cleanup retained reservation")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
}
