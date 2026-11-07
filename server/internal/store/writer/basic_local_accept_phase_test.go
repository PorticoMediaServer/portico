package writer

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Reuse the FULL executor/Captured NORMAL component fixtures. These effects
// intentionally stop before activation and do not claim a complete command.
func basicLocalAcceptFixture(t *testing.T) (*executor, *basicNFOSet, *basicArtworkInput) {
	t.Helper()
	w, activation := basicActivationFixture(t)
	old := seedBasicNFO(t, w)
	after := old
	after.locator, after.payload, after.observedAt = "locator-2", `{"title":"Arrival"}`, ""
	nfo, err := prepareBasicNFOSet(activation.source, "basic-library", []basicNFORow{old}, []basicNFORow{after}, &basicNFOStatus{"observed", ""}, basicNFOStatus{"observed", "video_nfo_fields_skipped"}, true)
	if err != nil {
		t.Fatal(err)
	}
	art, err := prepareBasicArtwork(basicArtworkInput{source: activation.source, entityID: 31, ownerSourceRevision: 1, ownerArtworkRevision: 1, ownerArtworkIncarnation: [16]byte(bytes.Repeat([]byte{0x31}, 16)), jobID: "local-job", role: "poster", imageID: "chosen-image", origin: "local:chosen-image", locale: "und", effectiveLanguage: "en-US", sourceFence: "native-owner-fence"})
	if err != nil {
		t.Fatal(err)
	}
	return w, nfo, art
}

func basicLocalAcceptDefs() []stmt.Definition {
	defs := append(basicStreamStageDefinitions(), basicNFODefinitions()...)
	return append(defs, basicArtworkDefinitions()...)
}

func basicLocalAcceptAssertFence(t *testing.T, w *executor, wantPhase int64) {
	t.Helper()
	var epoch, pending, phase, expected int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT a.info_epoch,a.info_publication_pending,c.phase,c.expected_epoch FROM catalog_assets a JOIN catalog_info_changes c ON c.asset_id=a.id WHERE c.id=51`).Scan(&epoch, &pending, &phase, &expected); err != nil {
		t.Fatal(err)
	}
	if epoch != 17 || pending != 1 || phase != wantPhase || expected != 17 {
		t.Fatal(epoch, pending, phase, expected)
	}
}

func TestBasicLocalAcceptPhaseThreeEffectsRemainFenced(t *testing.T) {
	w, nfo, art := basicLocalAcceptFixture(t)
	// Existing physical work remains outside this helper's custody scope.
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO artwork_jobs(id,kind,entity_id,role,choice,image_id,attribution,origin,provider,source_fence,created_at,job_revision) VALUES('locked-job','entity',31,'poster','locked','owner-image','','upload:owner','upload','owner-fence','owner-time',19)`); err != nil {
		t.Fatal(err)
	}
	// Keep real matched and locked serving selections through acquisition.
	digest := strings.Repeat("a", 64)
	for _, q := range []string{
		`INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at) VALUES(?,'image/png',1,1,1,'old-time')`,
		`INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at) VALUES(?,'image/png',1,1,?, ?, ?,1280,0,0,1,'old-time')`,
	} {
		args := []any{digest}
		if strings.Contains(q, "artwork_sources") {
			args = []any{digest, digest, digest, digest}
		}
		if _, err := w.db.ExecContext(t.Context(), q, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, choice := range []string{"matched", "locked"} {
		if _, err := w.db.ExecContext(t.Context(), `INSERT INTO artwork_selections(kind,entity_id,role,choice,digest,provider,image_id,origin,attribution,source_fence,revision,actor,observed_at) VALUES('entity',31,'poster',?,?,'local','serving-image','local:serving','','serving-fence',9,'owner','old-time')`, choice, digest); err != nil {
			t.Fatal(err)
		}
	}
	art.selectionRevision = 9
	turn := basicComponentTurn(t, w, basicLocalAcceptDefs())
	gotNFO, err := replaceBasicNFOEvidenceForAccept(t.Context(), turn, nfo, 2000)
	if err != nil || gotNFO != (basicNFOProgress{1, 1}) {
		t.Fatal(gotNFO, err)
	}
	gotArt, err := queueBasicLocalArtworkForAccept(t.Context(), turn, art, 2000)
	if err != nil || gotArt != (basicArtworkProgress{"local-job", 1, 9}) {
		t.Fatal(gotArt, err)
	}
	basicMediaCommit(t, turn)
	basicLocalAcceptAssertFence(t, w, 3)
	var selections int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM artwork_selections WHERE entity_id=31 AND role='poster' AND choice IN('matched','locked') AND digest=? AND image_id='serving-image' AND revision=9 AND source_fence='serving-fence' AND actor='owner' AND observed_at='old-time'`, digest).Scan(&selections); err != nil || selections != 2 {
		t.Fatal(selections, err)
	}

	var locator, payload, observed, issue string
	if err := w.db.QueryRowContext(t.Context(), `SELECT e.locator,e.payload,e.observed_at,s.issue FROM video_nfo_evidence e JOIN video_nfo_status s USING(library_id,asset_id) WHERE e.library_id='basic-library' AND e.asset_id='basic-asset'`).Scan(&locator, &payload, &observed, &issue); err != nil {
		t.Fatal(err)
	}
	if locator != "locator-2" || payload != `{"title":"Arrival"}` || observed != "1970-01-01T00:00:02Z" || issue != "video_nfo_fields_skipped" {
		t.Fatal(locator, payload, observed, issue)
	}
	var image, object, revision, created string
	var applied int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT image_id,source_object_id,source_object_revision,created_at,job_next_attempt_ms FROM artwork_jobs WHERE id='local-job'`).Scan(&image, &object, &revision, &created, &applied); err != nil {
		t.Fatal(err)
	}
	if image != "chosen-image" || object != "basic-object" || revision != "object:019" || created != observed || applied != 2000 {
		t.Fatal(image, object, revision, created, applied)
	}
	var jobRevision int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT image_id,job_revision FROM artwork_jobs WHERE id='locked-job'`).Scan(&image, &jobRevision); err != nil || image != "owner-image" || jobRevision != 19 {
		t.Fatal(image, jobRevision, err)
	}
}

func TestBasicLocalAcceptPhaseAndSourceRefuseWithoutEffects(t *testing.T) {
	for _, name := range []string{"old_phase_zero", "accept_wrong_phase", "accept_wrong_source", "active_matched_job"} {
		t.Run(name, func(t *testing.T) {
			w, nfo, art := basicLocalAcceptFixture(t)
			phase := int64(3)
			switch name {
			case "accept_wrong_phase":
				phase = 0
				if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_info_changes SET phase=0 WHERE id=51`); err != nil {
					t.Fatal(err)
				}
			case "accept_wrong_source":
				nfo.source.objectRevision = "wrong-revision"
				art.source.objectRevision = "wrong-revision"
			case "active_matched_job":
				if _, err := w.db.ExecContext(t.Context(), `INSERT INTO artwork_jobs(id,kind,entity_id,role,choice,image_id,attribution,origin,provider,source_fence,created_at,job_revision) VALUES('old-job','entity',31,'poster','matched','old-image','','local:old','local','old-fence','old-time',19)`); err != nil {
					t.Fatal(err)
				}
			}
			turn := basicComponentTurn(t, w, basicLocalAcceptDefs())
			if name != "active_matched_job" {
				apply := replaceBasicNFOEvidenceForAccept
				if name == "old_phase_zero" {
					apply = applyBasicNFO
				}
				got, err := apply(t.Context(), turn, nfo, 2000)
				if !errors.Is(err, ErrConflict) || got != (basicNFOProgress{}) {
					t.Fatal(got, err)
				}
			}
			queue := queueBasicLocalArtworkForAccept
			if name == "old_phase_zero" {
				queue = queueBasicLocalArtwork
			}
			got, err := queue(t.Context(), turn, art, 2000)
			if !errors.Is(err, ErrConflict) || got != (basicArtworkProgress{}) {
				t.Fatal(got, err)
			}
			// Commit the refused effect bodies to prove they performed no writes.
			basicMediaCommit(t, turn)
			basicLocalAcceptAssertFence(t, w, phase)
			var locator, payload string
			if err := w.db.QueryRowContext(t.Context(), `SELECT locator,payload FROM video_nfo_evidence WHERE library_id='basic-library' AND asset_id='basic-asset'`).Scan(&locator, &payload); err != nil || locator != "locator-1" || payload != `{"title":"Old"}` {
				t.Fatal(locator, payload, err)
			}
			var count int64
			if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM artwork_jobs WHERE id='local-job'`).Scan(&count); err != nil || count != 0 {
				t.Fatal(count, err)
			}
		})
	}
}
