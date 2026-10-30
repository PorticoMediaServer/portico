package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func cleanupMutations(t *testing.T, c *catalogtest.Catalog, max int) {
	t.Helper()
	for i := 0; i < max; i++ {
		var active int
		if e := c.DB.QueryRow(`SELECT count(*) FROM console_operations WHERE kind='catalog-mutation' AND resource='cleanup' AND state IN('queued','running')`).Scan(&active); e != nil {
			t.Fatal(e)
		}
		if active == 0 {
			return
		}
		dbwork.ResetPlans()
		out, e := New(c.DB).MutationAdapter().Step(context.Background(), "cleanup")
		if e != nil {
			t.Fatal(e)
		}
		if rows := dbwork.Plans().Largest.Rows; rows > 24 {
			t.Fatalf("cleanup writer changed %d rows, cap24", rows)
		}
		if out.State == "succeeded" {
			return
		}
	}
	t.Fatal("cleanup did not exhaust actual debt")
}
func TestP252bMissingStaleDoesNotReadmitSameCutoff(t *testing.T) {
	c, _, songs := drainFixture(t, 1)
	song := songs[0]
	drainEvidence(t, c, "stale-missing", 1)
	drainPin(t, c, song, "stale-missing", false)
	asset, token := identityFile(t, c, "m", "/m/stale-missing.flac", 1)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.LinkAssetTx(ctx, tx, song, asset, compactcatalog.Link{})
	})
	markMissing(t, c, "m", token, time.Now().Add(-time.Hour))
	cutoff := time.Now()
	s := New(c.DB)
	// The source edit must follow the real pin flip. The coordinator spends one
	// admission and one complete pinned removal; the milestone is the missing
	// pin row, not a guessed call count.
	pumpFixture(t, "stale missing pin", pinnedRemovalTurns(1), func() bool {
		if _, e := s.EmptyMissing(context.Background(), "m", cutoff, nil); e != nil {
			t.Fatal(e)
		}
		return rows(t, c, `SELECT count(*) FROM mb_song_links WHERE item_id=?`, song) == 0
	})
	if rows(t, c, `SELECT count(*) FROM mb_song_links WHERE item_id=?`, song) != 0 {
		t.Fatal("stale command did not drain its pin before the source edit")
	}
	c.Exec(`UPDATE catalog_assets SET modified_ns=modified_ns+1 WHERE id=?`, asset)
	for i := 0; i < 6; i++ {
		out, e := New(c.DB).EmptyMissing(context.Background(), "m", cutoff, nil)
		if e != nil || out.Titles != 0 {
			t.Fatal("stale command deleted title", out, e)
		}
	}
	cleanupMutations(t, c, 30)
	if n := rows(t, c, `SELECT count(*) FROM catalog_entities WHERE id=?`, song); n != 1 {
		t.Fatal("same cutoff readmitted pin-free title")
	}
	if n := rows(t, c, `SELECT count(*) FROM catalog_asset_links WHERE entity_id=?`, song); n != 1 {
		t.Fatal("stale command forgot source custody")
	}
	if n := rows(t, c, `SELECT count(*) FROM catalog_entity_mutations WHERE owner_kind='missing' AND outcome='stale' AND acknowledged=0`); n != 1 {
		t.Fatal("stale cutoff proof lost", n)
	}
	out, e := New(c.DB).EmptyMissing(context.Background(), "m", cutoff.Add(time.Second), nil)
	if e != nil || out.Titles != 1 {
		t.Fatal("fresh admitted cutoff did not remove current title", out, e)
	}
	cleanupMutations(t, c, 40)
	if n := rows(t, c, `SELECT count(*) FROM catalog_entity_mutations WHERE owner_kind='missing'`); n != 0 {
		t.Fatal("superseded stale debt leaked", n)
	}
}
func TestP252bLibraryStaleCustodyPausesInsteadOfSpinning(t *testing.T) {
	c, _, songs := drainFixture(t, 1)
	song := songs[0]
	drainEvidence(t, c, "library-stale", 1)
	drainPin(t, c, song, "library-stale", false)
	asset, _ := identityFile(t, c, "m", "/m/lib-stale.flac", 1)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.LinkAssetTx(ctx, tx, song, asset, compactcatalog.Link{})
	})
	c.Exec(`UPDATE catalog_libraries SET retired=1 WHERE library_id='m'`)
	// The asset edit below is only meaningful once the library walk really
	// drained this title's pin; stop at that actual flip, not a guessed count.
	pumpFixture(t, "stale library pin", pinnedRemovalTurns(1), func() bool {
		c.Write(func(ctx context.Context, tx *sql.Tx) error { _, e := removeLibraryBatchTx(ctx, tx, "m"); return e })
		return rows(t, c, `SELECT count(*) FROM mb_song_links WHERE item_id=?`, song) == 0
	})
	if rows(t, c, `SELECT count(*) FROM mb_song_links WHERE item_id=?`, song) != 0 {
		t.Fatal("library removal did not drain its pin before the source edit")
	}
	c.Exec(`UPDATE catalog_assets SET modified_ns=modified_ns+1 WHERE id=?`, asset)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, e := New(c.DB).LibraryRemovalAdapter().Step(ctx, "m")
	if e != nil || out.State != "paused" {
		t.Fatal("stale library did not pause", out, e)
	}
	if n := rows(t, c, `SELECT count(*) FROM catalog_entities WHERE id=?`, song); n != 1 {
		t.Fatal("paused stale library deleted source")
	}
	if n := rows(t, c, `SELECT count(*) FROM catalog_entity_mutations WHERE owner_kind='library' AND status='terminal' AND outcome='stale' AND acknowledged=0`); n != 1 {
		t.Fatal("stale library proof lost", n)
	}
}
func TestP252bAdoptionCapsExcludedRawKeys(t *testing.T) {
	c, album, _ := drainFixture(t, 0)
	var library, artist int64
	c.DB.QueryRow(`SELECT library_id,parent_id FROM catalog_entities WHERE id=?`, album).Scan(&library, &artist)
	otherLibrary := c.Library("other", "Other", "music", "/other")
	otherArtist := c.Artist(otherLibrary, "Other")
	p := compactcatalog.ProviderIdentity{Provider: "musicbrainz", Kind: "release", ID: "raw-prefix"}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		if e := compactcatalog.RecordProviderIdentityTx(ctx, tx, album, p); e != nil {
			return e
		}
		for i := 0; i < 96; i++ {
			lib, art := otherLibrary, otherArtist.ID
			if i%2 == 1 {
				lib, art = library, artist
			}
			id, _, e := compactcatalog.EnsureEntityTx(ctx, tx, compactcatalog.Entity{Library: lib, Kind: compactcatalog.Album, Parent: art, Key: fmt.Sprint("excluded-", i), Title: "Excluded"})
			if e != nil {
				return e
			}
			if e = compactcatalog.SetFactsTx(ctx, tx, id, map[string]any{"artist_id": art, "local_key": fmt.Sprint(i)}); e != nil {
				return e
			}
			if e = compactcatalog.RecordProviderIdentityTx(ctx, tx, id, p); e != nil {
				return e
			}
			if lib == library {
				if _, e = tx.Exec(`INSERT INTO catalog_kept_apart VALUES(?)`, id); e != nil {
					return e
				}
			}
		}
		id, _, e := compactcatalog.EnsureEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Album, Parent: artist, Key: "eligible-last", Title: "Eligible"})
		if e != nil {
			return e
		}
		if e = compactcatalog.SetFactsTx(ctx, tx, id, map[string]any{"artist_id": artist, "local_key": "eligible"}); e != nil {
			return e
		}
		if e = compactcatalog.RecordProviderIdentityTx(ctx, tx, id, p); e != nil {
			return e
		}
		return enqueueIdentityAdoptionTx(ctx, tx, album, p)
	})
	var ref compactcatalog.MutationRef
	c.DB.QueryRow(`SELECT mutation_id,generation FROM catalog_entity_mutations WHERE mode='adopt'`).Scan(&ref.ID, &ref.Generation)
	mutationTurn(t, c, ref)
	last := int64(0)
	for i := 0; i < 3; i++ {
		dbwork.ResetPlans()
		step := mutationTurn(t, c, ref)
		var cursor int64
		var child sql.NullString
		c.DB.QueryRow(`SELECT cursor,current_child FROM catalog_entity_mutations WHERE mutation_id=?`, ref.ID).Scan(&cursor, &child)
		var keys int
		c.DB.QueryRow(`SELECT count(*) FROM catalog_external_ids WHERE provider='musicbrainz' AND provider_kind='release' AND provider_id='raw-prefix' AND entity_id>? AND entity_id<=?`, last, cursor).Scan(&keys)
		if keys != 32 || child.Valid || step.Complete {
			t.Fatalf("raw turn %d keys=%d child=%v step=%+v", i, keys, child, step)
		}
		if vm := dbwork.Plans().Largest.VMOps; vm > 40000 {
			t.Fatal("unbounded excluded prefix VM", vm)
		}
		last = cursor
	}
	for i := 0; i < 20; i++ {
		out, e := New(c.DB).MutationAdapter().Step(context.Background(), ref.ID)
		if e != nil {
			t.Fatal(e)
		}
		if out.State == "succeeded" {
			if n := rows(t, c, `SELECT count(*) FROM catalog_entities WHERE title='Eligible'`); n != 0 {
				t.Fatal("eligible tail skipped")
			}
			return
		}
	}
	t.Fatal("excluded-prefix adoption did not finish")
}
func TestP252bTypedMemberSeekAndEmptySeasonsAreBounded(t *testing.T) {
	c, album, _ := drainFixture(t, 0)
	measure := func() int64 {
		dbwork.ResetPlans()
		e := dbwork.WithWriteTx(context.Background(), c.DB, dbwork.ClassInteractive, func(tx *sql.Tx) error {
			_, _, e := nextMutationMemberTx(context.Background(), tx, album, compactcatalog.Album, 0)
			if e == sql.ErrNoRows {
				return nil
			}
			return e
		})
		if e != nil {
			t.Fatal(e)
		}
		return dbwork.Plans().Largest.VMOps
	}
	before := measure()
	var library int64
	c.DB.QueryRow(`SELECT library_id FROM catalog_entities WHERE id=?`, album).Scan(&library)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for i := 0; i < 600; i++ {
			song, _, e := compactcatalog.EnsureEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Track, Parent: album, Key: fmt.Sprint("member-", i), Title: "Song"})
			if e != nil {
				return e
			}
			if e = compactcatalog.SetFactsTx(ctx, tx, song, map[string]any{"album_id": album, "track_number": 600 - i}); e != nil {
				return e
			}
		}
		return nil
	})
	after := measure()
	t.Logf("typed member seek VM before=%d after600members=%d", before, after)
	if after > before+1000 {
		t.Fatalf("typed member seek sorted members: %d -> %d VM", before, after)
	}
	tv := c.Library("tv", "TV", "tv", "/tv")
	show := c.Show(tv, "Empty Show", 2020)
	for i := 0; i < 70; i++ {
		c.Season(show, i+1)
	}
	var ref compactcatalog.MutationRef
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var e error
		ref, e = compactcatalog.BeginEntityMutationTx(ctx, tx, compactcatalog.MutationIntent{Entity: show.ID, OwnerKind: "title", OwnerID: "empty-show", OwnerGeneration: 1, Mode: "members"})
		return e
	})
	mutationTurn(t, c, ref)
	mutationTurn(t, c, ref)
	for i := 0; i < 72; i++ {
		before := rows(t, c, `SELECT count(*) FROM catalog_seasons WHERE show_id=?`, show.ID)
		step := mutationTurn(t, c, ref)
		after := rows(t, c, `SELECT count(*) FROM catalog_seasons WHERE show_id=?`, show.ID)
		if before-after > 1 {
			t.Fatal("bulk season finalization", before, after)
		}
		if step.Complete {
			if after != 0 {
				t.Fatal("empty seasons dropped", after)
			}
			return
		}
	}
	t.Fatal("show did not finalize all empty seasons")
}
func TestP252bCleanupChildFirstRestartAndDurableOutcome(t *testing.T) {
	c, album, _ := drainFixture(t, 0)
	p := compactcatalog.ProviderIdentity{Provider: "musicbrainz", Kind: "release", ID: "gc"}
	var library, artist int64
	c.DB.QueryRow(`SELECT library_id,parent_id FROM catalog_entities WHERE id=?`, album).Scan(&library, &artist)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		if e := compactcatalog.RecordProviderIdentityTx(ctx, tx, album, p); e != nil {
			return e
		}
		for i := 0; i < 3; i++ {
			id, _, e := compactcatalog.EnsureEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Album, Parent: artist, Key: fmt.Sprint("gc-", i), Title: "GC"})
			if e != nil {
				return e
			}
			if e = compactcatalog.SetFactsTx(ctx, tx, id, map[string]any{"artist_id": artist, "local_key": fmt.Sprint(i)}); e != nil {
				return e
			}
			if e = compactcatalog.RecordProviderIdentityTx(ctx, tx, id, p); e != nil {
				return e
			}
		}
		return enqueueIdentityAdoptionTx(ctx, tx, album, p)
	})
	var root string
	c.DB.QueryRow(`SELECT mutation_id FROM catalog_entity_mutations WHERE parent_mutation_id IS NULL`).Scan(&root)
	for i := 0; i < 60; i++ {
		out, e := New(c.DB).MutationAdapter().Step(context.Background(), root)
		if e != nil {
			t.Fatal(e)
		}
		if out.State == "succeeded" {
			break
		}
		if i == 59 {
			t.Fatal("adoption pending")
		}
	}
	if n := rows(t, c, `SELECT count(*) FROM catalog_entity_mutations WHERE mutation_id=? AND acknowledged=1 AND child_count=3`, root); n != 1 {
		t.Fatal("owner outcome not durably acknowledged", n)
	}
	cleanupMutations(t, c, 100)
	if n := rows(t, c, `SELECT count(*) FROM catalog_entity_mutations WHERE family_id=?`, root); n != 0 {
		t.Fatal("terminal debt leaked", n)
	}
	out, e := New(c.DB).MutationAdapter().Step(context.Background(), root)
	if e != nil || out.State != "succeeded" {
		t.Fatal("completed operation replay depended on reaped root", out, e)
	}
	var phase string
	c.DB.QueryRow(`SELECT phase FROM console_operations WHERE kind='catalog-mutation' AND resource=?`, root).Scan(&phase)
	if !strings.HasPrefix(phase, "catalog ") {
		t.Fatal("durable console outcome lost", phase)
	}
}

func TestP252bAdminAnchorSurvivesReceiptPruning(t *testing.T) {
	c, _, songs := drainFixture(t, 3)
	owner := "receipt-command:" + strings.Repeat("a", 64)
	var plans []TitleDeletePlan
	for _, song := range songs {
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			p, e := CaptureTitleDeleteTx(ctx, tx, song)
			plans = append(plans, p)
			return e
		})
	}
	for _, plan := range plans {
		for i := 0; i < 8; i++ {
			var step compactcatalog.MutationStep
			c.Write(func(ctx context.Context, tx *sql.Tx) error {
				var e error
				step, e = DeleteApprovedTitleStepTx(ctx, tx, plan, owner)
				return e
			})
			if step.Complete {
				break
			}
			if i == 7 {
				t.Fatal("member pending")
			}
		}
	}
	var anchor string
	c.DB.QueryRow(`SELECT mutation_id FROM catalog_entity_mutations WHERE owner_kind='administration' AND parent_mutation_id IS NULL`).Scan(&anchor)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		if _, e := tx.Exec(`INSERT INTO admin_receipts VALUES('media-delete','receipt-command',?,'{}',1)`, strings.Repeat("a", 64)); e != nil {
			return e
		}
		return AcknowledgeAdministrationTx(ctx, tx, owner)
	})
	// Existing receipt caps may expire the response before the maintenance turn.
	c.Exec(`DELETE FROM admin_receipts WHERE operation_id='receipt-command'`)
	cleanupMutations(t, c, 40)
	if n := rows(t, c, `SELECT count(*) FROM catalog_entity_mutations WHERE family_id=?`, anchor); n != 0 {
		t.Fatal("receipt pruning stranded actual mutation debt", n)
	}
}
func TestP252bConsoleCompletionUsesRevisionAndCancellationCAS(t *testing.T) {
	c, album, _ := drainFixture(t, 0)
	p := compactcatalog.ProviderIdentity{Provider: "musicbrainz", Kind: "release", ID: "cas"}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		if e := compactcatalog.RecordProviderIdentityTx(ctx, tx, album, p); e != nil {
			return e
		}
		return enqueueIdentityAdoptionTx(ctx, tx, album, p)
	})
	var root string
	c.DB.QueryRow(`SELECT mutation_id FROM catalog_entity_mutations WHERE mode='adopt'`).Scan(&root)
	stale := false
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		custody, e := readMutationConsoleTx(ctx, tx, root)
		if e != nil {
			return e
		}
		if _, e = tx.Exec(`UPDATE console_operations SET revision=revision+1 WHERE id=?`, custody.ID); e != nil {
			return e
		}
		e = completeMutationConsoleTx(ctx, tx, custody, root, "retained")
		stale = errors.Is(e, compactcatalog.ErrMutationStale)
		if !stale {
			return fmt.Errorf("stale revision accepted: %v", e)
		}
		return nil
	})
	c.Exec(`UPDATE console_operations SET state='cancellation-requested',revision=revision+1 WHERE resource=?`, root)
	_, e := New(c.DB).MutationAdapter().Step(context.Background(), root)
	if !errors.Is(e, sql.ErrNoRows) {
		t.Fatalf("cancelled scheduler authority executed: %v", e)
	}
	if n := rows(t, c, `SELECT count(*) FROM catalog_entity_mutations WHERE mutation_id=? AND status='active' AND acknowledged=0`, root); n != 1 {
		t.Fatal("console pointer granted completion authority")
	}
}
func TestP252bAdministrativeZeroMemberCursorDoesNotAdoptNewMembers(t *testing.T) {
	c, album, songs := drainFixture(t, 1)
	var plan TitleDeletePlan
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var e error
		plan, e = CaptureTitleDeleteTx(ctx, tx, album)
		return e
	})
	owner := "empty-command:" + strings.Repeat("b", 64)
	for i := 0; i < 4; i++ {
		var step compactcatalog.MutationStep
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			var e error
			step, e = DeleteEmptyApprovedContainerStepTx(ctx, tx, plan, owner)
			return e
		})
		if step.Complete {
			if step.Outcome != "stale" {
				t.Fatal(step)
			}
			if n := rows(t, c, `SELECT count(*) FROM catalog_songs WHERE entity_id=?`, songs[0]); n != 1 {
				t.Fatal("zero-member command adopted new title")
			}
			return
		}
	}
	t.Fatal("zero-member refusal not terminal")
}

func TestP252bArtistMergeCapsWrongYearRawPrefix(t *testing.T) {
	c, targetAlbum, _ := drainFixture(t, 0)
	var library, targetArtist int64
	c.DB.QueryRow(`SELECT library_id,parent_id FROM catalog_entities WHERE id=?`, targetAlbum).Scan(&library, &targetArtist)
	var sourceArtist, sourceAlbum int64
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for i := 0; i < 100; i++ {
			id, _, e := compactcatalog.EnsureEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Album, Parent: targetArtist, Key: fmt.Sprint("wrong-year-", i), Title: "Other", Year: 1980})
			if e != nil {
				return e
			}
			if e = compactcatalog.SetFactsTx(ctx, tx, id, map[string]any{"artist_id": targetArtist, "local_key": fmt.Sprint(i), "edition_key": emptyAudioEdition}); e != nil {
				return e
			}
		}
		matched, _, e := compactcatalog.EnsureEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Album, Parent: targetArtist, Key: "later-match", Title: "Wanted", Year: 2001})
		if e != nil {
			return e
		}
		if e = compactcatalog.SetFactsTx(ctx, tx, matched, map[string]any{"artist_id": targetArtist, "local_key": "match", "edition_key": emptyAudioEdition}); e != nil {
			return e
		}
		sourceArtist, _, e = compactcatalog.EnsureEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Artist, Key: "source-artist", Title: "Source"})
		if e != nil {
			return e
		}
		if e = compactcatalog.SetFactsTx(ctx, tx, sourceArtist, map[string]any{"local_key": "source"}); e != nil {
			return e
		}
		sourceAlbum, _, e = compactcatalog.EnsureEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Album, Parent: sourceArtist, Key: "source-wanted", Title: "Wanted", Year: 2001})
		if e != nil {
			return e
		}
		return compactcatalog.SetFactsTx(ctx, tx, sourceAlbum, map[string]any{"artist_id": sourceArtist, "local_key": "source", "edition_key": emptyAudioEdition})
	})
	drainEvidence(t, c, "artist-cursor", 1)
	drainPin(t, c, sourceAlbum, "artist-cursor", true)
	var ref compactcatalog.MutationRef
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var e error
		ref, e = compactcatalog.BeginEntityMutationTx(ctx, tx, compactcatalog.MutationIntent{Entity: sourceArtist, Target: targetArtist, OwnerKind: "merge", OwnerID: "artist-prefix", OwnerGeneration: 1, Mode: "merge"})
		return e
	})
	mutationTurn(t, c, ref)
	mutationTurn(t, c, ref)
	last := int64(0)
	for i := 0; i < 3; i++ {
		dbwork.ResetPlans()
		step := mutationTurn(t, c, ref)
		var cursor, current int64
		var child sql.NullString
		c.DB.QueryRow(`SELECT cursor,current_key,current_child FROM catalog_entity_mutations WHERE mutation_id=?`, ref.ID).Scan(&cursor, &current, &child)
		var raw int
		c.DB.QueryRow(`SELECT count(*) FROM catalog_albums WHERE artist_id=? AND entity_id>? AND entity_id<=?`, targetArtist, last, cursor).Scan(&raw)
		if raw != 32 || current != sourceAlbum || child.Valid || step.Complete {
			t.Fatalf("raw target turn%d raw%d current%d child%v step%+v", i, raw, current, child, step)
		}
		if vm := dbwork.Plans().Largest.VMOps; vm > 30000 {
			t.Fatal("wrong-year prefix unbounded", vm)
		}
		last = cursor
	}
	for i := 0; i < 24; i++ {
		step := mutationTurn(t, c, ref)
		if step.Complete {
			if step.Outcome != "merged" {
				t.Fatal(step)
			}
			if n := rows(t, c, `SELECT count(*) FROM catalog_entities WHERE id=?`, sourceAlbum); n != 0 {
				t.Fatal("later valid target skipped")
			}
			if n := rows(t, c, `SELECT count(*) FROM catalog_merged_ids WHERE public_id IN(SELECT public_id FROM catalog_entities WHERE id=?)`, sourceAlbum); n != 0 {
				t.Fatal("unexpected new source lifetime")
			}
			return
		}
	}
	t.Fatal("artist cursor did not reach later match")
}
func TestP252bCommandAnchorAndMemberLookupsIgnoreLowSubjectPrefix(t *testing.T) {
	c, album, songs := drainFixture(t, 603)
	owner := "vm-command:" + strings.Repeat("c", 64)
	high := songs[len(songs)-1]
	var highPlan TitleDeletePlan
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var e error
		highPlan, e = CaptureTitleDeleteTx(ctx, tx, high)
		return e
	})
	for i := 0; i < 8; i++ {
		var step compactcatalog.MutationStep
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			var e error
			step, e = DeleteApprovedTitleStepTx(ctx, tx, highPlan, owner)
			return e
		})
		if step.Complete {
			break
		}
	}
	var anchor, family string
	c.DB.QueryRow(`SELECT mutation_id,family_id FROM catalog_entity_mutations WHERE owner_kind='administration' AND owner_id=? AND parent_mutation_id IS NULL`, owner).Scan(&anchor, &family)
	measure := func(song int64) int64 {
		var plan TitleDeletePlan
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			var e error
			plan, e = CaptureTitleDeleteTx(ctx, tx, song)
			return e
		})
		dbwork.ResetPlans()
		e := dbwork.WithWriteTx(context.Background(), c.DB, dbwork.ClassInteractive, func(tx *sql.Tx) error {
			_, e := DeleteApprovedTitleStepTx(context.Background(), tx, plan, owner)
			return e
		})
		if e != nil {
			t.Fatal(e)
		}
		return dbwork.Plans().Largest.VMOps
	}
	before := measure(songs[0])
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for _, song := range songs[2 : len(songs)-1] {
			if _, e := compactcatalog.BeginEntityMutationTx(ctx, tx, compactcatalog.MutationIntent{Entity: song, OwnerKind: "administration", OwnerID: owner, OwnerGeneration: 1, Mode: "remove", Parent: anchor, Family: family, CauseOperation: "vm-command"}); e != nil {
				return e
			}
		}
		return nil
	})
	after := measure(songs[1])
	t.Logf("actual member+anchor admission VM before=%d after600lowerSubjectChildren=%d", before, after)
	if after > before*2+1000 || after > 10000 {
		t.Fatalf("command prefix scanned: %d -> %d", before, after)
	}
	// The exact root lookup also serves the one final receipt acknowledgement.
	dbwork.ResetPlans()
	e := dbwork.WithWriteTx(context.Background(), c.DB, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		var id string
		return tx.QueryRowContext(context.Background(), `SELECT mutation_id FROM catalog_entity_mutations WHERE owner_kind='administration' AND owner_id=? AND owner_generation=1 AND parent_mutation_id IS NULL LIMIT 1`, owner).Scan(&id)
	})
	if e != nil {
		t.Fatal(e)
	}
	if vm := dbwork.Plans().Largest.VMOps; vm > 100 {
		t.Fatal("root lookup scanned child prefix", vm)
	}
	_ = album
}
