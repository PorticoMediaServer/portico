package writer

import (
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func basicNFOTestDefs() []stmt.Definition {
	return append(basicStreamStageDefinitions(), basicNFODefinitions()...)
}
func seedBasicNFO(t *testing.T, w *executor) basicNFORow {
	t.Helper()
	r := basicNFORow{"locator-1", "movie", "[null,null,null]", "digest-1", `{"title":"Old"}`, 42, -11, "old-time"}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO video_nfo_evidence VALUES('basic-library','basic-asset',?,?,?,?,?,?,?,?)`, basicNFOValues(r)...); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO video_nfo_status VALUES('basic-library','basic-asset','observed','')`); err != nil {
		t.Fatal(err)
	}
	return r
}
func TestBasicNFONativeCompleteSetReplacement(t *testing.T) {
	w, source := basicStreamFixture(t)
	old := seedBasicNFO(t, w)
	after := basicNFORow{"locator-2", "movie", "[null,null,null]", "digest-2", `{"title":"Arrival"}`, 42, -11, ""}
	prepared, err := prepareBasicNFOSet(source, "basic-library", []basicNFORow{old}, []basicNFORow{after}, &basicNFOStatus{"observed", ""}, basicNFOStatus{"observed", ""}, true)
	if err != nil {
		t.Fatal(err)
	}
	after.payload = `{"title":"Caller mutation"}`
	turn := basicComponentTurn(t, w, basicNFOTestDefs())
	got, err := applyBasicNFO(t.Context(), turn, prepared, 2000)
	if err != nil || got != (basicNFOProgress{1, 1}) {
		t.Fatal(got, err)
	}
	basicMediaCommit(t, turn)
	var payload, observed string
	var count, phase, epoch int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT payload,observed_at FROM video_nfo_evidence WHERE library_id='basic-library' AND asset_id='basic-asset'`).Scan(&payload, &observed); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM video_nfo_evidence`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT phase FROM catalog_info_changes WHERE id=51`).Scan(&phase); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT info_epoch FROM catalog_assets WHERE id=41`).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	if payload != `{"title":"Arrival"}` || observed != "1970-01-01T00:00:02Z" || count != 1 || phase != 0 || epoch != 17 {
		t.Fatal(payload, observed, count, phase, epoch)
	}
}
func TestBasicNFOHiddenOldRowAndWrongScopeRefuse(t *testing.T) {
	for _, wrongScope := range []bool{false, true} {
		t.Run(map[bool]string{false: "hidden_row", true: "wrong_public_library"}[wrongScope], func(t *testing.T) {
			w, source := basicStreamFixture(t)
			old := seedBasicNFO(t, w)
			var before []basicNFORow
			library := "basic-library"
			if wrongScope {
				before = []basicNFORow{old}
				library = "another-public-library"
				if _, err := w.db.ExecContext(t.Context(), `INSERT INTO libraries(id,name,kind,root) VALUES('another-public-library','Other','movie','/other')`); err != nil {
					t.Fatal(err)
				}
				if _, err := w.db.ExecContext(t.Context(), `INSERT INTO video_nfo_evidence SELECT 'another-public-library',asset_id,locator,kind,coordinate,digest,payload,source_size,source_modified,observed_at FROM video_nfo_evidence WHERE library_id='basic-library'`); err != nil {
					t.Fatal(err)
				}
				if _, err := w.db.ExecContext(t.Context(), `INSERT INTO video_nfo_status VALUES('another-public-library','basic-asset','observed','')`); err != nil {
					t.Fatal(err)
				}
			}
			prepared, err := prepareBasicNFOSet(source, library, before, nil, &basicNFOStatus{"observed", ""}, basicNFOStatus{"observed", ""}, true)
			if err != nil {
				t.Fatal(err)
			}
			turn := basicComponentTurn(t, w, basicNFOTestDefs())
			got, err := applyBasicNFO(t.Context(), turn, prepared, 2000)
			if !errors.Is(err, ErrConflict) || got != (basicNFOProgress{}) {
				t.Fatal(got, err)
			}
			_ = turn.closeRows()
			_, _ = turn.transaction.rollback(err)
			var count int64
			want := int64(1)
			if wrongScope {
				want = 2
			}
			if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM video_nfo_evidence`).Scan(&count); err != nil || count != want {
				t.Fatal(count, err)
			}
		})
	}
}
func TestBasicNFOKnownAbsenceAndIncompleteObservation(t *testing.T) {
	w, source := basicStreamFixture(t)
	old := seedBasicNFO(t, w)
	if _, err := prepareBasicNFOSet(source, "basic-library", []basicNFORow{old}, nil, nil, basicNFOStatus{"observed", ""}, false); !errors.Is(err, ErrInvalid) {
		t.Fatal("partial observation erased absence distinction", err)
	}
	prepared, err := prepareBasicNFOSet(source, "basic-library", []basicNFORow{old}, nil, &basicNFOStatus{"observed", ""}, basicNFOStatus{"observed", ""}, true)
	if err != nil {
		t.Fatal(err)
	}
	turn := basicComponentTurn(t, w, basicNFOTestDefs())
	got, err := applyBasicNFO(t.Context(), turn, prepared, 2000)
	if err != nil || got != (basicNFOProgress{1, 0}) {
		t.Fatal(got, err)
	}
	basicMediaCommit(t, turn)
	var count int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM video_nfo_evidence`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
func TestBasicNFOPreflightAndLateFailureRollback(t *testing.T) {
	w, source := basicStreamFixture(t)
	old := seedBasicNFO(t, w)
	oversized := old
	oversized.payload = strings.Repeat("x", 33554432)
	oversized.observedAt = ""
	if _, err := prepareBasicNFOSet(source, "basic-library", []basicNFORow{old}, []basicNFORow{oversized}, nil, basicNFOStatus{"observed", ""}, true); !errors.Is(err, ErrCap) {
		t.Fatal(err)
	}
	after := old
	after.locator = "locator-2"
	after.observedAt = ""
	prepared, err := prepareBasicNFOSet(source, "basic-library", []basicNFORow{old}, []basicNFORow{after}, &basicNFOStatus{"observed", ""}, basicNFOStatus{"observed", ""}, true)
	if err != nil {
		t.Fatal(err)
	}
	turn := basicComponentTurn(t, w, basicNFOTestDefs())
	// A missing terminal statement is a real pre-SQL refusal AFTER row effects.
	// It must clear the progress value and leave the whole source transaction
	// available for rollback, never a partially replaced visible NFO cohort.
	delete(turn.steps, basicNFOStatusCAS)
	got, err := applyBasicNFO(t.Context(), turn, prepared, 2000)
	if err == nil || got != (basicNFOProgress{}) {
		t.Fatal(got, err)
	}
	_ = turn.closeRows()
	_, _ = turn.transaction.rollback(err)
	var locator string
	if err := w.db.QueryRowContext(t.Context(), `SELECT locator FROM video_nfo_evidence`).Scan(&locator); err != nil || locator != old.locator {
		t.Fatal(locator, err)
	}
}

func TestBasicNFONativeLocatorTypedRefusal(t *testing.T) {
	for _, tc := range []struct {
		path string
		want error
	}{
		{string([]byte{'/', 'm', 0xff}), ErrFilenameUnrepresentable},
		{"/media/nul\x00.nfo", ErrInvalid},
	} {
		row := basicNFORow{locator: tc.path, kind: "movie", coordinate: "[null,null,null]", digest: "native-digest", payload: `{}`}
		got, err := prepareBasicNFOSet(basicStreamFactStage{}, "library", nil, []basicNFORow{row}, nil, basicNFOStatus{status: "observed"}, true)
		if !errors.Is(err, tc.want) || got != nil {
			t.Fatal(tc.want, got, err)
		}
		if tc.want == ErrInvalid && errors.Is(err, ErrFilenameUnrepresentable) {
			t.Fatal("NUL became filename poison", err)
		}
	}
}
