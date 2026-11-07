package writer

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func basicArtworkFixture(t *testing.T) (*executor, *basicArtworkInput) {
	t.Helper()
	w, accepted := basicOrdinaryInputAcceptFixture(t)
	in, err := prepareBasicArtwork(basicArtworkInput{source: accepted.source, entityID: 31, ownerSourceRevision: 1, ownerArtworkRevision: 1, ownerArtworkIncarnation: [16]byte(bytes.Repeat([]byte{0x31}, 16)), jobID: "local-job", role: "poster", imageID: "chosen-image", origin: "local:chosen-image", locale: "und", effectiveLanguage: "en-US", sourceFence: "native-owner-fence"})
	if err != nil {
		t.Fatal(err)
	}
	return w, in
}
func basicArtworkTestDefs() []stmt.Definition {
	return append(basicStreamStageDefinitions(), basicArtworkDefinitions()...)
}
func TestBasicArtworkNativeChosenJobPreservesLock(t *testing.T) {
	w, in := basicArtworkFixture(t)
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO artwork_jobs(id,kind,entity_id,role,choice,image_id,attribution,origin,provider,source_fence,created_at,job_revision) VALUES('locked-job','entity',31,'poster','locked','owner-image','','upload:owner','upload','owner-fence','owner-time',19)`); err != nil {
		t.Fatal(err)
	}
	turn := basicBoundedComponentTurn(t, w, basicArtworkTestDefs())
	got, err := queueBasicLocalArtwork(t.Context(), turn, in, 2000)
	if err != nil || got != (basicArtworkProgress{"local-job", 1, 0}) {
		t.Fatal(got, err)
	}
	basicMediaCommit(t, turn)
	var image, object, revision, created, provider, choice string
	var epoch, jobRevision, selectionRevision int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT image_id,source_object_id,source_object_revision,created_at,provider,choice,job_revision,selection_revision FROM artwork_jobs WHERE id='local-job'`).Scan(&image, &object, &revision, &created, &provider, &choice, &jobRevision, &selectionRevision); err != nil {
		t.Fatal(err)
	}
	if image != "chosen-image" || object != "basic-object" || revision != "object:019" || created != "1970-01-01T00:00:02Z" || provider != "local" || choice != "matched" || jobRevision != 1 || selectionRevision != 0 {
		t.Fatal(image, object, revision, created, provider, choice, jobRevision, selectionRevision)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT image_id,job_revision FROM artwork_jobs WHERE id='locked-job'`).Scan(&image, &jobRevision); err != nil || image != "owner-image" || jobRevision != 19 {
		t.Fatal(image, jobRevision, err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT info_epoch FROM catalog_assets WHERE id=41`).Scan(&epoch); err != nil || epoch != 17 {
		t.Fatal(epoch, err)
	}
}
func TestBasicArtworkNativeOwnerDriftAndExistingJobRefuse(t *testing.T) {
	for _, name := range []string{"owner_revision", "subject_library", "existing_matched"} {
		t.Run(name, func(t *testing.T) {
			w, in := basicArtworkFixture(t)
			switch name {
			case "owner_revision":
				if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_entities SET artwork_revision=2 WHERE id=31`); err != nil {
					t.Fatal(err)
				}
			case "subject_library":
				in.source.libraryID = 8
			case "existing_matched":
				if _, err := w.db.ExecContext(t.Context(), `INSERT INTO artwork_jobs(id,kind,entity_id,role,choice,image_id,attribution,origin,provider,source_fence,created_at,job_revision) VALUES('old-job','entity',31,'poster','matched','old-image','','local:old','local','old-fence','old-time',19)`); err != nil {
					t.Fatal(err)
				}
			}
			turn := basicBoundedComponentTurn(t, w, basicArtworkTestDefs())
			got, err := queueBasicLocalArtwork(t.Context(), turn, in, 2000)
			if !errors.Is(err, ErrConflict) || got != (basicArtworkProgress{}) {
				t.Fatal(got, err)
			}
			_ = turn.closeRows()
			_, _ = turn.transaction.rollback(err)
			var count int64
			if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM artwork_jobs WHERE id='local-job'`).Scan(&count); err != nil || count != 0 {
				t.Fatal(count, err)
			}
		})
	}
}
func TestBasicArtworkWholeBytePreflightAndCanonicalRole(t *testing.T) {
	_, in := basicArtworkFixture(t)
	copy := *in
	copy.origin = strings.Repeat("x", 33554432)
	if _, err := prepareBasicArtwork(copy); !errors.Is(err, ErrCap) {
		t.Fatal(err)
	}
	copy = *in
	copy.role = "thumbnail"
	if _, err := prepareBasicArtwork(copy); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}
