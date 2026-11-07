package writer

import (
	"errors"
	"testing"
)

func TestMPVersionWorkerRejectsOtherStreams(t *testing.T) {
	for _, kind := range []CommandID{740, 741, 744, 746, 748, 750, 788} {
		if mpVersionWorkerPurpose(kind) {
			t.Fatalf("borrowed stream %d", kind)
		}
		v := mpWMediaPayload{header: mpPrepReceiptHeader{kind: kind}}
		if _, err := storeMPVersionWorkerPayload(t.Context(), nil, "version", mpMediaWorkerBefore{}, mpMediaWorkerAdmission{}, v); !errors.Is(err, ErrInvalid) {
			t.Fatal(kind, err)
		}
	}
	for _, kind := range []CommandID{745, 749} {
		if !mpVersionWorkerPurpose(kind) {
			t.Fatal(kind)
		}
	}
}

// Runner-only: prepares every new version statement against the actual F
// candidate. Missing F skips explicitly; this does not qualify W or media IO.
func TestMPVersionNativeFSQL(t *testing.T) {
	w := mpNativeFExecutor(t)
	for _, d := range mpVersionWorkerPayloadDefinitions() {
		rows, err := w.db.QueryContext(t.Context(), "EXPLAIN "+d.SQL, make([]any, len(d.Params))...)
		if err != nil {
			t.Fatal(d.ID, err)
		}
		for rows.Next() {
		}
		if err = errors.Join(rows.Err(), rows.Close()); err != nil {
			t.Fatal(d.ID, err)
		}
	}
}
