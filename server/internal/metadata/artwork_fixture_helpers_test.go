package metadata

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"image"
	"image/png"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// fixtureArtworkSourceSQL publishes a file-less fixture source whose three
// slots all name one blob row with the same digest. Tests that only need a
// selectable source use it; tests about bytes use publishTestArtwork.
const fixtureArtworkBlobSQL = `INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at) VALUES(?1,'image/jpeg',1,1,1,'now') ON CONFLICT(digest) DO NOTHING`
const fixtureArtworkSourceSQL = `INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at) VALUES(?1,'image/jpeg',1,1,?1,?1,?1,1920,0,0,1,'now') ON CONFLICT(digest) DO NOTHING`

func insertFixtureArtworkSource(t testing.TB, db interface {
	Exec(string, ...any) (sql.Result, error)
}, digest string) {
	t.Helper()
	for _, statement := range []string{fixtureArtworkBlobSQL, fixtureArtworkSourceSQL} {
		if _, err := db.Exec(statement, digest); err != nil {
			t.Fatal(err)
		}
	}
}

// publishTestArtwork materialises raw through the production path and
// publishes its source, returning the material.
func publishTestArtwork(t testing.TB, s *Service, raw []byte, stamp string) artworkMaterial {
	t.Helper()
	ctx, _, err := s.CaptureArtworkContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.materializeArtwork(ctx, raw, map[string]bool{"jpeg": true, "png": true, "webp": true})
	if err != nil {
		t.Fatal(err)
	}
	if err = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		return publishArtworkSource(ctx, tx, m, stamp)
	}); err != nil {
		t.Fatal(err)
	}
	return m
}

// testSourceBlobs reads a published source's small, large and ambient blobs.
func testSourceBlobs(t testing.TB, db *sql.DB, source string) (small, large, ambient string) {
	t.Helper()
	if err := db.QueryRow(`SELECT small_blob,large_blob,ambient_blob FROM artwork_sources WHERE digest=?`, source).Scan(&small, &large, &ambient); err != nil {
		t.Fatal(err)
	}
	return small, large, ambient
}

func encodeTestPNG(t testing.TB, picture image.Image) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := png.Encode(&out, picture); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// normalizeArtwork is the arrival encoder's two slots for raw: large, small
// and the large slot's dimensions.
func normalizeArtwork(raw []byte) ([]byte, []byte, int, int, error) {
	encoded, _, _, _, err := encodeArtworkSource(context.Background(), raw, map[string]bool{"jpeg": true, "png": true})
	return encoded[1].data, encoded[0].data, encoded[1].width, encoded[1].height, err
}

func (s *Service) installArtwork(raw []byte, w, h int) (artworkInstalled, error) {
	return s.installArtworkFile(raw, w, h)
}

// EntityArtworkIdentity is the selected source and its large slot's MIME for a
// target, through the same admission and selection the reader uses.
func (s *Service) EntityArtworkIdentity(ctx context.Context, t RepairTarget, role, subject string, _ bool) (string, string, error) {
	if !validArtworkRole(role) || len(subject) > 160 {
		return "", "", ErrRepairInput
	}
	gated, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return "", "", err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	if err = validateArtworkTarget(ctx, tx, t); err != nil {
		return "", "", err
	}
	source, _, _, err := artworkSelection(ctx, tx, t, role, subject)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	_, mime, err := artworkSourceBlob(ctx, tx, source, ArtworkLarge)
	return source, mime, err
}

func waitForWriterWaiter(t *testing.T, class dbwork.Class) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if dbwork.WriteGate().Waiting()[class.String()] > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("write gate never queued a %s writer: %#v", class, dbwork.WriteGate().Waiting())
}
