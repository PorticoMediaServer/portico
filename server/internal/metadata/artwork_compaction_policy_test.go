package metadata

import (
	"context"
	"database/sql"
	"fmt"
	"image"
	"math/rand"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// A shrunk policy re-makes the large slot of an owner upload from its large
// file. The source, its small slot, its lock and its accepted dimensions stay.
func TestArtworkCompactionShrinksSelectedUploadAndKeepsItsSource(t *testing.T) {
	s, db, target := repairFixture(t)
	ctx := context.Background()
	before, err := s.RepairState(ctx, target, allowAll)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UploadArtwork(ctx, target, "poster", "", before.Revision, editorJPEG(t, 2700, 1080), editorActor(), allowAll); err != nil {
		t.Fatal(err)
	}
	var source, small, large string
	if err = db.QueryRow(`SELECT s.digest,o.small_blob,o.large_blob FROM artwork_selections s JOIN artwork_sources o ON o.digest=s.digest WHERE s.role='poster'`).Scan(&source, &small, &large); err != nil {
		t.Fatal(err)
	}
	setArtworkPolicy(t, s, 1280)
	if err = s.CompactArtworkStep(ctx); err != nil {
		t.Fatal(err)
	}
	var digest, newSmall, newLarge string
	var width, smallWidth, edge int
	var locked bool
	if err = db.QueryRow(`SELECT s.digest,s.locked,o.small_blob,o.large_blob,o.large_edge,l.width,m.width FROM artwork_selections s JOIN artwork_sources o ON o.digest=s.digest JOIN artwork_blobs l ON l.digest=o.large_blob JOIN artwork_blobs m ON m.digest=o.small_blob WHERE s.role='poster'`).Scan(&digest, &locked, &newSmall, &newLarge, &edge, &width, &smallWidth); err != nil {
		t.Fatal(err)
	}
	if digest != source || !locked || newSmall != small || newLarge == large || edge != 1280 || width != 1280 || smallWidth != artworkSmallEdge {
		t.Fatal("compaction changed the source, its small slot or the owner lock", digest == source, locked, newSmall == small, newLarge == large, edge, width, smallWidth)
	}
	var uploadedWidth, uploadedHeight int
	if err = db.QueryRow(`SELECT width,height FROM artwork_uploads`).Scan(&uploadedWidth, &uploadedHeight); err != nil || uploadedWidth != 2700 || uploadedHeight != 1080 {
		t.Fatal("upload no longer describes the accepted image", uploadedWidth, uploadedHeight, err)
	}
	var violations int
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		violations++
	}
	err = rows.Err()
	rows.Close()
	if err != nil || violations != 0 {
		t.Fatal("compaction left broken references", violations, err)
	}
}

// A large file that already fits the smaller policy is kept as it is; one that
// cannot meet the smaller cap at the quality floor is refused and recorded,
// and its pair stays live.
func TestArtworkCompactionKeepsFittingFilesAndRefusesUnreachableCaps(t *testing.T) {
	s, db, _ := repairFixture(t)
	ctx := context.Background()
	setArtworkPolicy(t, s, 2560)
	noise := image.NewNRGBA(image.Rect(0, 0, 1600, 1000))
	random := rand.New(rand.NewSource(88))
	for i := 0; i < len(noise.Pix); i += 4 {
		noise.Pix[i], noise.Pix[i+1], noise.Pix[i+2], noise.Pix[i+3] = byte(random.Intn(256)), byte(random.Intn(256)), byte(random.Intn(256)), 255
	}
	loud := publishTestArtwork(t, s, encodeTestPNG(t, noise), "2020-01-01T00:00:00Z")
	if loud.large.size <= artworkLargeCap[artworkLargeEdge]*3/2 {
		t.Fatal("fixture did not exceed the 1920 allowance", loud.large.size)
	}
	quiet := publishTestArtwork(t, s, editorJPEG(t, 1000, 600), "2020-01-01T00:00:00Z")
	setArtworkPolicy(t, s, 1920)
	for n := 0; n < 3; n++ {
		if err := s.CompactArtworkStep(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var large, reason string
	var edge int
	if err := db.QueryRow(`SELECT o.large_blob,o.large_edge,f.reason FROM artwork_sources o JOIN artwork_compaction_failures f ON f.digest=o.digest WHERE o.digest=?`, loud.source).Scan(&large, &edge, &reason); err != nil || large != loud.large.digest || edge != 2560 || reason != "cap_unreachable" {
		t.Fatal("unreachable cap was not refused with its pair kept", large == loud.large.digest, edge, reason, err)
	}
	if err := db.QueryRow(`SELECT large_blob,large_edge FROM artwork_sources WHERE digest=?`, quiet.source).Scan(&large, &edge); err != nil || large != quiet.large.digest || edge != 1920 {
		t.Fatal("a fitting large file was re-encoded", large == quiet.large.digest, edge, err)
	}
	var refused int
	if err := db.QueryRow(`SELECT count FROM artwork_encoding_counters WHERE slot='large' AND format='jpeg' AND outcome='cap_unreachable'`).Scan(&refused); err != nil || refused != 1 {
		t.Fatal("refusal was not counted", refused, err)
	}
	if s.artworkCompactionHasBacklog() {
		t.Fatal("the sweep did not idle")
	}
}

// The walk reads one keyed row per step, in large-edge order, and stops
// querying once it reaches the end until the policy changes.
func TestArtworkCompactionWalkIsKeyedAndStopsAtTheEnd(t *testing.T) {
	s := artworkPolicyService(t)
	ctx := context.Background()
	if err := dbwork.WithWriteTx(ctx, s.db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		for n := 0; n < 3; n++ {
			digest := fmt.Sprintf("%064x", n)
			for _, statement := range []string{fixtureArtworkBlobSQL, fixtureArtworkSourceSQL} {
				if _, err := tx.ExecContext(ctx, statement, digest); err != nil {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, `UPDATE artwork_sources SET large_edge=? WHERE digest=?`, []int{1280, 2560, 1920}[n], digest); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	policy := artworkEncodingPolicy{edge: 1280, ceiling: 300000}
	for _, want := range []string{fmt.Sprintf("%064x", 2), fmt.Sprintf("%064x", 1)} {
		measured, cost := dbwork.Measure(ctx)
		candidate, err := s.nextArtworkCompaction(measured, policy)
		if err != nil || candidate != want || !s.artworkCompactionHasBacklog() {
			t.Fatal("walk order", candidate, want, err)
		}
		if got := cost().Statements; got != 1 {
			t.Fatal("a step performed more than one read", got)
		}
	}
	if _, err := s.nextArtworkCompaction(ctx, policy); err != sql.ErrNoRows || s.artworkCompactionHasBacklog() {
		t.Fatal("exhausted walk did not idle", err)
	}
	idle, cost := dbwork.Measure(ctx)
	if _, err := s.nextArtworkCompaction(idle, policy); err != sql.ErrNoRows || cost().Statements != 0 {
		t.Fatal("completed sweep queried again", cost(), err)
	}
	candidate, err := s.nextArtworkCompaction(ctx, artworkEncodingPolicy{edge: 1920, ceiling: 500000})
	if err != nil || candidate != fmt.Sprintf("%064x", 1) {
		t.Fatal("a policy change did not restart the walk", candidate, err)
	}
}
