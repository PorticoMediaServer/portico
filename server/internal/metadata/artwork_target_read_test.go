package metadata

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Compare the artwork admission seam with the full editor inside the same
// snapshot. The trace API only collects SQL text here; no EXPLAIN is executed.
func artworkTargetReadParity(t *testing.T, db *sql.DB, target RepairTarget, want error) ([]string, []string) {
	t.Helper()
	gated, err := dbwork.BeginSnapshot(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer gated.Rollback()
	fullCtx, fullTrace := dbwork.TraceQueryPlans(context.Background())
	full, fullErr := readRepairEntity(fullCtx, gated.Tx(), target)
	leanCtx, leanTrace := dbwork.TraceQueryPlans(context.Background())
	leanErr := validateArtworkTarget(leanCtx, gated.Tx(), target)
	if fullErr == nil && (len(full.schema) == 0 || len(full.fields) != len(full.schema) || full.stamp == "") {
		t.Fatal("editor projection lost schema, fields or revision stamp", full)
	}
	for name, got := range map[string]error{"editor": fullErr, "artwork": leanErr} {
		if (want == nil && got != nil) || (want != nil && !errors.Is(got, want)) {
			t.Fatalf("%s target %+v: got %v, want %v", name, target, got, want)
		}
	}
	return fullTrace(), leanTrace()
}

func artworkTargetTraceContains(trace []string, part string) bool {
	for _, statement := range trace {
		if strings.Contains(statement, part) {
			return true
		}
	}
	return false
}

func artworkTargetInstall(t *testing.T, s *Service, target RepairTarget, raw []byte) string {
	t.Helper()
	object, err := s.installArtwork(raw, 20, 20)
	if err != nil {
		t.Fatal(err)
	}
	entity := resolveArtworkTestEntity(t, s.db, target)
	integrationExec(t, s.db, `INSERT OR IGNORE INTO artwork_blobs(digest,mime,width,height,bytes,created_at) VALUES(?,'image/png',20,20,?,'2026-10-27T00:00:00Z')`, object.digest, object.size)
	integrationExec(t, s.db, fixtureArtworkSourceSQL, object.digest)
	candidate := "target-read-" + target.ID
	integrationExec(t, s.db, `INSERT INTO artwork_candidates(id,kind,entity_id,role,subject,provider,image_id,origin,attribution,source_fence,observed_at) VALUES(?,?,?,'poster','','fixture',?,'local','fixture','target-read','now')`, candidate, target.Kind, entity, candidate)
	integrationExec(t, s.db, `INSERT INTO artwork_selections(kind,entity_id,role,subject,candidate_id,digest,locked,revision,actor,observed_at) VALUES(?,?, 'poster','',?,?,0,1,'fixture','now')`, target.Kind, entity, candidate, object.digest)
	return object.digest
}

func artworkTargetAssertBytes(t *testing.T, s *Service, target RepairTarget, digest string, raw []byte) {
	t.Helper()
	ctx, trace := dbwork.TraceQueryPlans(context.Background())
	gotDigest, mime, err := s.EntityArtworkIdentity(ctx, target, "poster", "", false)
	if err != nil || gotDigest != digest || mime != "image/png" {
		t.Fatalf("identity: %s %s %v", gotDigest, mime, err)
	}
	for _, version := range []string{"", digest} {
		f, mime, err := s.EntityArtworkVariant(ctx, target, "poster", "", ArtworkSmall, version)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(f)
		f.Close()
		if err != nil || mime != "image/png" || !bytes.Equal(got, raw) {
			t.Fatalf("bytes: mime=%s err=%v", mime, err)
		}
	}
	// These positive reads have a selected object, so no legacy local-art lookup
	// should read details either. This also pins both public serving entrypoints.
	if artworkTargetTraceContains(trace(), "catalog_item_details") {
		t.Fatal("served artwork loaded editor details", trace())
	}
	if _, _, err := s.EntityArtworkVariant(context.Background(), target, "poster", "", ArtworkSmall, strings.Repeat("f", 64)); !errors.Is(err, ErrArtworkVersion) {
		t.Fatal("version guard", err)
	}
}

func TestArtworkTargetReadNonReadingParity(t *testing.T) {
	s, db := editorFixture(t)
	names := editorNames(t, db)
	cases := []struct {
		namespace, name string
		saved           int
	}{
		{"item", "movie", 1}, {"item", "episode", 3}, {"item", "song", 2}, {"item", "part", 2},
		{"show", "show", 3}, {"season", "season", 3}, {"album", "album", 3}, {"artist", "artist", 3}, {"book", "book", 3},
	}
	raw := p08cPNG(t, 20, 20)
	targets := map[string]RepairTarget{}
	var digest string
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := editorTarget(names, tc.namespace, tc.name)
			targets[tc.name] = target
			full, lean := artworkTargetReadParity(t, db, target, nil)
			if len(full)-len(lean) != tc.saved {
				t.Fatalf("removed statements=%d want %d; full=%v lean=%v", len(full)-len(lean), tc.saved, full, lean)
			}
			for _, irrelevant := range []string{"catalog_item_details", "SELECT number,air_date", "SELECT track_number,disc_number", "SELECT n.number", "SELECT part_number", "SELECT overview", "SELECT original_title", "SELECT number,title", "SELECT author,narrator"} {
				if artworkTargetTraceContains(lean, irrelevant) {
					t.Fatalf("loaded editable fields: %s", irrelevant)
				}
			}
			if tc.namespace == "item" && !artworkTargetTraceContains(lean, "metadata_publication_heads") {
				t.Fatal("required item head omitted")
			}
			// A container's descendant-file stamp fences editor revisions only;
			// artwork admission must not walk every descendant to discard it.
			if tc.namespace != "item" && !artworkTargetTraceContains(full, "WITH RECURSIVE members") {
				t.Fatal("editor lost its source fingerprint")
			}
			if artworkTargetTraceContains(lean, "WITH RECURSIVE members") {
				t.Fatal("artwork admission walked the descendant files")
			}
			digest = artworkTargetInstall(t, s, target, raw)
			artworkTargetAssertBytes(t, s, target, digest, raw)
			// Retired non-Reading targets were accepted by this metadata layer before;
			// outer library/profile/visibility admission remains independently required.
			entity := resolveArtworkTestEntity(t, db, target)
			integrationExec(t, db, `UPDATE catalog_entities SET retired=1 WHERE id=?`, entity)
			artworkTargetReadParity(t, db, target, nil)
			artworkTargetAssertBytes(t, s, target, digest, raw)
			integrationExec(t, db, `UPDATE catalog_entities SET retired=0 WHERE id=?`, entity)
		})
	}
	// Preserve the existing episode, album and audiobook-parent selections when
	// the requested item has no selection of its own.
	for _, name := range []string{"episode", "song", "part"} {
		target := targets[name]
		integrationExec(t, db, `DELETE FROM artwork_selections WHERE kind=? AND entity_id=?`, target.Kind, resolveArtworkTestEntity(t, db, target))
		if name == "song" || name == "part" {
			parent := targets[map[string]string{"song": "album", "part": "book"}[name]]
			integrationExec(t, db, `UPDATE artwork_selections SET role='cover' WHERE kind=? AND entity_id=?`, parent.Kind, resolveArtworkTestEntity(t, db, parent))
		}
		artworkTargetAssertBytes(t, s, target, digest, raw)
	}
}

func TestArtworkTargetReadAdmissionErrors(t *testing.T) {
	s, db := editorFixture(t)
	names := editorNames(t, db)
	movie := editorTarget(names, "item", "movie")
	cases := []struct {
		name   string
		target RepairTarget
		want   error
	}{
		{"wrong-kind", RepairTarget{"album", movie.ID}, sql.ErrNoRows},
		{"container-as-item", editorTarget(names, "item", "album"), sql.ErrNoRows},
		{"malformed-id", RepairTarget{"item", "unknown"}, sql.ErrNoRows},
		{"unknown-id", RepairTarget{"item", strings.Repeat("A", 22)}, sql.ErrNoRows},
		{"invalid-namespace", RepairTarget{"invalid", movie.ID}, ErrRepairInput},
		{"unknown-before-namespace", RepairTarget{"invalid", "unknown"}, sql.ErrNoRows},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			artworkTargetReadParity(t, db, tc.target, tc.want)
			if _, _, err := s.EntityArtworkIdentity(context.Background(), tc.target, "poster", "", false); !errors.Is(err, tc.want) {
				t.Fatal("identity", err)
			}
			if _, _, err := s.EntityArtworkVariant(context.Background(), tc.target, "poster", "", ArtworkSmall, ""); !errors.Is(err, tc.want) {
				t.Fatal("serve", err)
			}
		})
	}
	artworkTargetInstall(t, s, movie, p08cPNG(t, 20, 20))
	integrationExec(t, db, `DELETE FROM metadata_publication_heads WHERE item_id=?`, resolveArtworkTestEntity(t, db, movie))
	artworkTargetReadParity(t, db, movie, sql.ErrNoRows)
	if _, _, err := s.EntityArtworkIdentity(context.Background(), movie, "poster", "", false); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("missing head identity", err)
	}
	if _, _, err := s.EntityArtworkVariant(context.Background(), movie, "poster", "", ArtworkSmall, ""); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("missing head serve", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := s.EntityArtworkIdentity(cancelled, movie, "poster", "", false); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled read", err)
	}
}

func TestArtworkTargetReadReadingFallbackParity(t *testing.T) {
	for _, tc := range []struct {
		kind    compactcatalog.Kind
		library string
	}{{compactcatalog.Ebook, "book"}, {compactcatalog.Comic, "comic"}} {
		t.Run(tc.library, func(t *testing.T) {
			s, c, target := readingOwnerFixture(t, tc.kind, tc.library)
			if err := s.SetArtworkDirectory(t.TempDir()); err != nil {
				t.Fatal(err)
			}
			seedReadingSourceValues(t, c, target)
			readFull := func() repairEntity {
				t.Helper()
				gated, err := dbwork.BeginSnapshot(context.Background(), c.DB)
				if err != nil {
					t.Fatal(err)
				}
				defer gated.Rollback()
				ent, err := readRepairEntity(context.Background(), gated.Tx(), target)
				if err != nil {
					t.Fatal(err)
				}
				return ent
			}
			original := readFull()
			if original.fields["author"] != "Genuine Author" || original.readingAutomatic["author"] != "Genuine Author" {
				t.Fatal("current Reading source facts lost", original)
			}
			full, lean := artworkTargetReadParity(t, c.DB, target, nil)
			if strings.Join(full, "\n") != strings.Join(lean, "\n") {
				t.Fatal("Reading no longer uses full validation", full, lean)
			}
			for _, proof := range []string{"reading_source_values", "reading_publication_types", "metadata_owner_fields", "SELECT retired", "WITH RECURSIVE members"} {
				if !artworkTargetTraceContains(lean, proof) {
					t.Fatal("Reading proof missing", proof)
				}
			}
			raw := p08cPNG(t, 20, 20)
			digest := artworkTargetInstall(t, s, target, raw)
			got, mime, err := s.EntityArtworkIdentity(context.Background(), target, "poster", "", false)
			if err != nil || got != digest || mime != "image/png" {
				t.Fatal("Reading without AV head", got, mime, err)
			}
			f, _, err := s.EntityArtworkVariant(context.Background(), target, "poster", "", ArtworkSmall, digest)
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(f)
			f.Close()
			if err != nil || !bytes.Equal(data, raw) {
				t.Fatal("Reading bytes", err)
			}
			// Invalid owner facts are an admission error, not an irrelevant editor read.
			c.Exec(`INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at) VALUES('item',?,'publicationType','invalid','',1,'fixture','now')`, c.ID(target.ID))
			artworkTargetReadParity(t, c.DB, target, ErrRepairInput)
			if _, _, err := s.EntityArtworkIdentity(context.Background(), target, "poster", "", false); !errors.Is(err, ErrRepairInput) {
				t.Fatal("invalid Reading owner", err)
			}
			if _, _, err := s.EntityArtworkVariant(context.Background(), target, "poster", "", ArtworkSmall, ""); !errors.Is(err, ErrRepairInput) {
				t.Fatal("invalid Reading owner serve", err)
			}
			c.Exec(`DELETE FROM metadata_owner_fields WHERE entity_id=?`, c.ID(target.ID))
			// Withdrawing automatic evidence is still accepted, with no AV-head adoption.
			c.Exec(`UPDATE inventory_objects SET retired=1 WHERE id='reading-object'`)
			if ent := readFull(); ent.fields["author"] != "" || ent.readingAutomatic["author"] != "" {
				t.Fatal("retired source supplied automatic facts", ent)
			}
			c.Exec(`UPDATE catalog_assets SET size=size+1 WHERE id=(SELECT asset_id FROM reading_publications WHERE item_id=?)`, c.ID(target.ID))
			if ent := readFull(); ent.stamp == original.stamp {
				t.Fatal("Reading physical source change retained fingerprint")
			}
			artworkTargetReadParity(t, c.DB, target, nil)
			c.Exec(`UPDATE catalog_entities SET retired=1 WHERE id=?`, c.ID(target.ID))
			artworkTargetReadParity(t, c.DB, target, sql.ErrNoRows)
			if _, _, err := s.EntityArtworkVariant(context.Background(), target, "poster", "", ArtworkSmall, ""); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("retired Reading served", err)
			}
			var heads int
			if err := c.DB.QueryRow(`SELECT count(*) FROM metadata_publication_heads WHERE item_id=?`, c.ID(target.ID)).Scan(&heads); err != nil || heads != 0 {
				t.Fatal("Reading adopted AV head", heads, err)
			}
		})
	}
}
