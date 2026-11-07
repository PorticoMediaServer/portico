package read

import (
	"context"
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

type rsByteBoundarySnapshots struct {
	*governor.Snapshots
	after func()
	calls int
}

func (s *rsByteBoundarySnapshots) Read(ctx context.Context, r governor.Request, b time.Duration, read func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	result, err := s.Snapshots.Read(ctx, r, b, read)
	s.calls++
	if s.calls == 1 && s.after != nil {
		s.after()
	}
	return result, err
}
func (s *rsByteBoundarySnapshots) ReadForecasted(ctx context.Context, r governor.Request, b time.Duration, f governor.SnapshotForecast, read func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	result, err := s.Snapshots.ReadForecasted(ctx, r, b, f, read)
	s.calls++
	if s.calls == 1 && s.after != nil {
		s.after()
	}
	return result, err
}

func TestRSPeoplePreflightsNamesAndCreditText(t *testing.T) {
	f, r := newRSPeopleFixture(t, false)
	name, job, credited := "é\x00Person", "Réalisateur\x00界", "Credited\x00é"
	biography := strings.Repeat("界", 100)
	f.exec(t, `UPDATE catalog_people SET name=?,biography=? WHERE id=1`, name, biography)
	f.exec(t, `UPDATE catalog_credit_labels SET label=? WHERE id=3`, job)
	f.exec(t, `UPDATE catalog_credits SET credited_name=? WHERE image_id=100`, credited)
	rows, err := f.conn.QueryContext(t.Context(), rsPersonSQL, int64(1))
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
		t.Fatal("missing person")
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
	in := PersonCreditsInput{View: f.input, Person: 1}
	in.View.Limit = 1
	p, cost, err := r.credits(t.Context(), foregroundRequest(), time.Second, f.output, in)
	if err != nil || p.Person == nil || p.Person.Name != name || p.Person.Biography != biography || len(p.Credits) != 1 || p.Credits[0].Item.ID != 100 || p.Credits[0].CreditedName != credited || p.Credits[0].Role != job {
		t.Fatal("wide person/credit bytes changed", p, err)
	}
	if len(cost.LookupWindows) != 2 || cost.LookupWindows[1].NativeReservedBytes != want {
		t.Fatal("actual largest point not reserved", want, cost)
	}
	for _, point := range []struct {
		name string
		args []any
	}{{"rs.people.person", []any{int64(1)}}, {"rs.people.credit", []any{int64(100), int64(1), int64(1), int64(1), int64(1)}}} {
		_, err := f.snapshots.ReadForecasted(t.Context(), foregroundRequest(), time.Second, governor.SnapshotForecast{NativeRowBytes: 1, MaxQueries: 2}, func(scope *governor.ReadScope) error {
			rows, _, err := queryReadScope(scope, point.name, point.args...)
			if !errors.Is(err, governor.ErrSnapshotBudget) || rows != nil {
				t.Fatal("person/credit copied without preflight permission", point.name, rows, err)
			}
			return err
		})
		if !errors.Is(err, governor.ErrSnapshotBudget) {
			t.Fatal(point.name, err)
		}
	}
}
