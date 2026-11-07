package writer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

func TestQualificationPopulationDeterministic(t *testing.T) {
	var first qualificationPopulationFacts
	for i, seed := range []uint32{0, 0, 73} {
		w, _ := testCanonicalExecutor(t)
		facts := seedQualificationPopulation(t, w, qualificationPopulationSpec{Seed: seed, Residents: 32})
		if facts.Version != "canonical-movies-v1" || facts.LibraryID != 7 || facts.LibraryKey != "qualification-movies" || facts.Seed != seed || facts.Residents != 32 || facts.FirstID != 1_000_000_001 || facts.LastID != 1_000_000_032 {
			t.Fatal("population facts", facts)
		}
		if digest, err := hex.DecodeString(facts.LogicalSHA256); err != nil || len(digest) != sha256.Size {
			t.Fatal("logical digest", facts.LogicalSHA256, err)
		}
		if i == 0 {
			first = facts
		} else if i == 1 && !reflect.DeepEqual(first, facts) {
			t.Fatal("same seed/residents changed persisted logical population", first, facts)
		} else if i == 2 && first.LogicalSHA256 == facts.LogicalSHA256 {
			t.Fatal("different seed did not change persisted logical population")
		}
	}
}

func TestQualificationPopulationRowsAndPolicy(t *testing.T) {
	w, _ := testCanonicalExecutor(t)
	facts := seedQualificationPopulation(t, w, qualificationPopulationSpec{Seed: 17, Residents: 256})
	for _, table := range []string{"catalog_subjects", "catalog_entities", "catalog_assets", "catalog_asset_links", "catalog_scalar_contributions", "catalog_asset_contributions", "current_movies"} {
		if facts.Counts[table] != 256 {
			t.Fatalf("%s count=%d", table, facts.Counts[table])
		}
	}
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := sqliteconn.Assert(t.Context(), conn, w.config); err != nil {
		t.Fatal("seeding changed shared policy", err)
	}
	var coherent bool
	if err := conn.QueryRowContext(t.Context(), `SELECT
(SELECT count(*)=256 FROM catalog_scalar_current WHERE library_id=7 AND source=0 AND parent_id=contributor_id AND contributor_revision=1 AND membership_sequence=parent_id-1000000000
 AND duration IS NULL AND community_rating IS NULL AND critic_rating IS NULL AND resolution IS NULL AND latest_aired_key IS NULL AND admission_age IS NULL)
AND (SELECT count(*)=256 FROM catalog_asset_contributions_current c JOIN catalog_assets a ON a.id=c.asset_id WHERE c.library_id=7 AND c.parent_id=c.asset_id AND c.membership_refs=1 AND c.authored_refs=0 AND c.file_size=a.size AND c.file_size_revision=a.file_size_revision)
AND (SELECT count(*)=256 FROM catalog_entities WHERE library_id=7 AND kind=1 AND retired=0 AND missing_since_ms IS NULL AND admission_allow_age=-1 AND admission_known_age IS NULL AND admission_own_age IS NULL AND admission_member_count=0 AND admission_unrated_members=0 AND admission_min_known_age IS NULL AND resolution_key=0 AND duration_key=0)
AND (SELECT count(*)=256 FROM catalog_assets WHERE library_id=7 AND available=1 AND container='' AND video_codec='' AND audio_codec='' AND width=0 AND height=0 AND duration=0 AND info_owner_object IS NULL AND info_owner_revision IS NULL AND has_download_source_digests=0)
AND (SELECT count(*)=1 FROM catalog_age_item_counts WHERE library_id=7 AND kind=1 AND block_unrated=0 AND admission_age=-1 AND total=256)
AND (SELECT sequence=256 FROM catalog_membership_clock WHERE singleton=1)
AND (SELECT data_revision=257 AND data_order_revision=257 AND data_metadata_revision=257 AND age_input_revision=257 AND age_published_revision=257 AND age_pending_operations=0 AND root='/qualification-media' FROM libraries WHERE id='qualification-movies')
AND (SELECT root='/qualification-media' AND retired=0 FROM catalog_libraries WHERE id=7 AND library_id='qualification-movies')`).Scan(&coherent); err != nil || !coherent {
		t.Fatal("minimal current catalogue is incoherent", coherent, err)
	}
	// Walk all 256 actual rows rather than sampling only the endpoint IDs. This
	// independently invokes the real projection on persisted input strings.
	rows, err := conn.QueryContext(t.Context(), `SELECT e.id,e.title,e.sort_title,e.metadata_language,e.sort_key,e.sort_head,e.sort_key_keep_articles,e.sort_head_keep_articles,e.year,
e.public_id,s.public_id,e.metadata_incarnation,e.artwork_incarnation,e.playback_origin_incarnation,a.playback_origin_incarnation,l.playback_origin_incarnation,
a.token,a.path,a.size,a.modified_ns,e.file_size_key
FROM catalog_entities e JOIN catalog_subjects s ON s.id=e.id
JOIN catalog_asset_links l ON l.entity_id=e.id JOIN catalog_assets a ON a.id=l.asset_id ORDER BY e.id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ordinal := 0
	var articleVariants, explicitVariants, variedYears bool
	firstYear := 0
	for rows.Next() {
		ordinal++
		var id, size, modified, fileSize int64
		var metadata InventoryFilenameMetadata
		var year int
		var public, subjectPublic, incarnation, artwork, origin, assetOrigin, linkOrigin []byte
		var token, path string
		if err := rows.Scan(&id, &metadata.Title, &metadata.SortTitle, &metadata.MetadataLanguage, &metadata.SortKey, &metadata.SortHead, &metadata.SortKeyKeepArticles, &metadata.SortHeadKeepArticles, &year,
			&public, &subjectPublic, &incarnation, &artwork, &origin, &assetOrigin, &linkOrigin, &token, &path, &size, &modified, &fileSize); err != nil {
			t.Fatal(err)
		}
		projection, err := ProjectFilenameMetadata(metadata.Title, metadata.SortTitle, metadata.MetadataLanguage)
		if err != nil || projection != metadata {
			t.Fatal("persisted filename projection", id, err, metadata)
		}
		want := qualificationMovie(t, 17, ordinal)
		if id != 1_000_000_000+int64(ordinal) || year != want.year || metadata != want.metadata || token != want.token || path != want.path || size != want.size || fileSize != size || modified != want.modifiedNS ||
			!bytes.Equal(public, want.public[:]) || !bytes.Equal(subjectPublic, public) || !bytes.Equal(incarnation, want.incarnation[:]) || !bytes.Equal(artwork, want.artwork[:]) ||
			!bytes.Equal(origin, want.origin[:]) || !bytes.Equal(assetOrigin, want.assetOrigin[:]) || !bytes.Equal(linkOrigin, want.linkOrigin[:]) {
			t.Fatal("persisted deterministic movie differs", id)
		}
		if ordinal == 1 {
			firstYear = year
		}
		articleVariants = articleVariants || metadata.SortKey != metadata.SortKeyKeepArticles
		explicitVariants = explicitVariants || metadata.SortTitle != ""
		variedYears = variedYears || year != firstYear
	}
	if err := rows.Err(); err != nil || ordinal != 256 || !articleVariants || !explicitVariants || !variedYears {
		t.Fatal("row/projection coverage", ordinal, articleVariants, explicitVariants, variedYears, err)
	}
	// The seeder already performed full FK/integrity scans once. Exercise FK
	// enforcement directly here; no PRAGMA changes or second full scan.
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(t.Context(), `INSERT INTO catalog_asset_links(library_id,entity_id,asset_id) VALUES(7,1000000001,999999999)`); err == nil {
		t.Fatal("foreign keys were not enforced after seeding")
	}
}

func TestQualificationPopulationBounds(t *testing.T) {
	for _, n := range []int{-1, 0, 1_000_001} {
		if err := validateQualificationPopulationSpec(qualificationPopulationSpec{Residents: n}); err == nil {
			t.Fatalf("accepted invalid residents %d", n)
		}
	}
	// Upper bound validation is pure: this test never populates at scale.
	for _, n := range []int{1, 256, 1_000_000} {
		if err := validateQualificationPopulationSpec(qualificationPopulationSpec{Seed: 0, Residents: n}); err != nil {
			t.Fatal(err)
		}
	}
	w, _ := testCanonicalExecutor(t)
	facts := seedQualificationPopulation(t, w, qualificationPopulationSpec{Residents: 1})
	if facts.FirstID != facts.LastID || facts.Counts["current_movies"] != 1 {
		t.Fatal("minimum population", facts)
	}
}
