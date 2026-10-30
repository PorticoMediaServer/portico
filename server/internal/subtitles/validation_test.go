package subtitles

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func validationFixture(t *testing.T) (*subtitleFixture, validationIO) {
	t.Helper()
	f := newSubtitleFixture(t)
	var source, incarnation string
	if err := f.db.QueryRow(`SELECT id,incarnation FROM library_sources WHERE library_id='library' LIMIT 1`).Scan(&source, &incarnation); err != nil {
		t.Fatal(err)
	}
	f.catalog.Exec(`UPDATE library_sources SET root_identity='root-proof',identity_confirmed=1 WHERE id=?`, source)
	f.catalog.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES('validation-object',?,?,?,'movie.mp4','revision-one','{}',1000,1)`, source, f.item.Token, incarnation)
	f.catalog.Write(func(_ context.Context, tx *sql.Tx) error {
		return Persist(tx, f.item.Token, 1000, 1, &Inventory{Status: "known", TimingKnown: true, SourceEvidence: "existing-evidence", Tracks: []Track{{Origin: "embedded", Format: "srt", StreamIndex: 2, Language: "en"}}})
	})
	io := validationIO{stat: func(_ context.Context, v validationSource) (storage.Snapshot, error) {
		return storage.Snapshot{Size: v.size, ModifiedNS: v.modified, ObjectIdentity: "file-proof", Revision: "same"}, nil
	}, discover: func(_ context.Context, v validationSource) (*Inventory, error) {
		return &Inventory{Status: "known", TimingKnown: v.known, OriginUS: v.origin, Tracks: append(append([]Track{}, v.embedded...), Track{Origin: "sidecar", Locator: filepath.Join(filepath.Dir(v.path), "movie.fr.srt"), Format: "srt", StreamIndex: -1, Language: "fr", Digest: "sidecar-digest", Size: 40, ModifiedNS: 2})}, nil
	}}
	return f, io
}
func TestSubtitleValidationPreservesEmbeddedAndTechnicalFactsWithoutProbe(t *testing.T) {
	f, io := validationFixture(t)
	out, err := f.s.validateItem(context.Background(), f.p, f.item.Public, io)
	if err != nil || out.Sources != 1 {
		t.Fatalf("validate: %+v %v", out, err)
	}
	var embedded, sidecars int
	if err = f.db.QueryRow(`SELECT count(*) FROM asset_subtitles WHERE asset_id=? AND origin='embedded' AND stream_index=2`, f.item.Token).Scan(&embedded); err != nil {
		t.Fatal(err)
	}
	if err = f.db.QueryRow(`SELECT count(*) FROM asset_subtitles WHERE asset_id=? AND origin='sidecar' AND language='fr'`, f.item.Token).Scan(&sidecars); err != nil {
		t.Fatal(err)
	}
	var codec, evidence string
	var width int
	if err = f.db.QueryRow(`SELECT video_codec,width FROM catalog_assets WHERE token=?`, f.item.Token).Scan(&codec, &width); err != nil {
		t.Fatal(err)
	}
	if err = f.db.QueryRow(`SELECT evidence FROM subtitle_inventory_evidence WHERE asset_id=?`, f.item.Token).Scan(&evidence); err != nil {
		t.Fatal(err)
	}
	if embedded != 1 || sidecars != 1 || codec != "h264" || width != 1920 || evidence != "existing-evidence" {
		t.Fatal("subtitle-only validation changed or lost stored facts")
	}
	if f.s.Extractor != nil {
		t.Fatal("fixture unexpectedly installed a media extractor")
	}
}
func TestSubtitleValidationRefusesStaleSourceAndRetiredAuthorityBeforePublication(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, test := range []string{"generation", "authority", "filesystem", "incomplete"} {
		t.Run(test, func(t *testing.T) {
			f, io := validationFixture(t)
			discover := io.discover
			stats := 0
			stat := io.stat
			io.stat = func(ctx context.Context, v validationSource) (storage.Snapshot, error) {
				stats++
				out, err := stat(ctx, v)
				if test == "filesystem" && stats > 1 {
					out.ModifiedNS++
				}
				return out, err
			}
			io.discover = func(ctx context.Context, v validationSource) (*Inventory, error) {
				out, err := discover(ctx, v)
				switch test {
				case "generation":
					f.catalog.Exec(`UPDATE library_sources SET generation=generation+1 WHERE id=?`, v.source)
				case "authority":
					f.allowed = false
				case "incomplete":
					out.Status = "unavailable"
				}
				return out, err
			}
			if _, err := f.s.validateItem(context.Background(), f.p, f.item.Public, io); err == nil {
				t.Fatal("stale validation published")
			}
			var count int
			if err := f.db.QueryRow(`SELECT count(*) FROM asset_subtitles WHERE asset_id=? AND origin='sidecar'`, f.item.Token).Scan(&count); err != nil || count != 0 {
				t.Fatalf("failed validation changed subtitle state: %d %v", count, err)
			}
		})
	}
}
func TestSubtitleValidationAuthorizesBeforeIOAndRequiresStoredEvidence(t *testing.T) {
	f, io := validationFixture(t)
	io.stat = func(context.Context, validationSource) (storage.Snapshot, error) {
		t.Fatal("unauthorized or unproven source reached file I/O")
		return storage.Snapshot{}, nil
	}
	f.allowed = false
	if _, err := f.s.validateItem(context.Background(), f.p, f.item.Public, io); err == nil {
		t.Fatal("unauthorized validation accepted")
	}
	f.allowed = true
	f.catalog.Exec(`DELETE FROM asset_subtitle_facts WHERE asset_id=?`, f.item.Token)
	if _, err := f.s.validateItem(context.Background(), f.p, f.item.Public, io); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing facts must not trigger a probe: %v", err)
	}
}
