package read

import (
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"strings"
	"testing"
	"time"
)

func TestRSDetailReservesActualPointOctets(t *testing.T) {
	f, r := newRSDetailFixture(t, false)
	text := "é\x00x" + strings.Repeat("界", 20)
	f.exec(t, `UPDATE catalog_entities SET overview=? WHERE id=1101`, text)
	// Independent decoded row lengths from the tiny writer fixture, rather than
	// column maxima or the production projection/mapping helper.
	rows, err := f.conn.QueryContext(t.Context(), TitleFactsDefinitions()[0].SQL, int64(1101), int64(1), int64(1))
	if err != nil {
		t.Fatal(err)
	}
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	raw := make([]any, len(columns))
	dest := make([]any, len(raw))
	for i := range raw {
		dest[i] = &raw[i]
	}
	if !rows.Next() {
		t.Fatal("missing source row")
	}
	if err = rows.Scan(dest...); err != nil {
		t.Fatal(err)
	}
	var want int64
	for _, v := range raw {
		switch v := v.(type) {
		case string:
			want += int64(len(v))
		case []byte:
			want += int64(len(v))
		}
	}
	if err = rows.Close(); err != nil {
		t.Fatal(err)
	}
	d, cost, err := r.facts(t.Context(), foregroundRequest(), time.Second, f.output, TitleFactsInput{View: f.input, Item: 1101})
	if err != nil || d.Item.Overview != text || len(cost.LookupWindows) != 2 {
		t.Fatal("exact-byte facts", d, cost, err)
	}
	if got := cost.LookupWindows[1].NativeReservedBytes; got != want || got <= 0 || got >= f.snapshots.NativeRowReservation() {
		t.Fatalf("native reservation got%d want%d conservative%d", got, want, f.snapshots.NativeRowReservation())
	}
	if cost.LookupStatements != 9 || cost.PublicationStatements != 6 {
		t.Fatal("point pair statement accounting", cost)
	}
}

func TestRSBytePointRefusesBeforePayloadWhenForecastIsSmall(t *testing.T) {
	f, _ := newRSDetailFixture(t, false)
	result, err := f.snapshots.ReadForecasted(t.Context(), foregroundRequest(), time.Second, governor.SnapshotForecast{NativeRowBytes: 1, MaxQueries: 2}, func(scope *governor.ReadScope) error {
		rows, _, err := queryReadScope(scope, "rs.detail.facts", int64(1101), int64(1), int64(1))
		if !errors.Is(err, governor.ErrSnapshotBudget) || rows != nil {
			t.Fatal("payload decoded without actual-byte permission", rows, err)
		}
		return err
	})
	if !errors.Is(err, governor.ErrSnapshotBudget) || result.Refusal != nil {
		t.Fatal(result, err)
	}
}
