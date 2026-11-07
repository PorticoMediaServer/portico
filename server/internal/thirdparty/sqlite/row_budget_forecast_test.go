package sqlite

import (
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"
)

func TestRowBudgetNativeForecastBeforeFirstCopy(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		forecast    int64
		refuse      bool
	}{
		{"aggregate-over", "SELECT 7,'ab',x'000102'", 4, true},
		{"exact", "SELECT 7,'ab',x'000102'", 5, false},
		{"zero-over", "SELECT 7,'x'", 0, true},
		{"zero-null", "SELECT 7,NULL", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, connection := fixedBudgetConn(t, 65536, 128)
			err := connection.Raw(func(raw any) error {
				c := raw.(*conn)
				budget, err := NewRowBudgetWithNativeLimit(1, 64<<10, tc.forecast)
				if err != nil {
					return err
				}
				rows, err := c.QueryContext(WithRowBudget(t.Context(), budget), tc.query, nil)
				if err != nil {
					return err
				}
				defer rows.Close()
				values := make([]driver.Value, len(rows.Columns()))
				err = rows.Next(values)
				if tc.refuse {
					if !errors.Is(err, ErrRowBudget) {
						t.Fatal("native forecast ignored", err)
					}
					// Even the leading scalar must remain untouched: checking after
					// converting/copying some columns is already too late.
					if !reflect.DeepEqual(values, make([]driver.Value, len(values))) {
						t.Fatal("forecast checked after copying started", values)
					}
					return nil
				}
				if err != nil || values[0] != int64(7) {
					t.Fatal("legal exact forecast refused", values, err)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			assertFixed(t, connection, 65536, 128)
		})
	}
}

func TestRowBudgetDefaultHasNoNativeForecast(t *testing.T) {
	_, connection := fixedBudgetConn(t, 65536, 128)
	budget, err := NewRowBudget(1, 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	var text string
	if err = connection.QueryRowContext(WithRowBudget(t.Context(), budget), "SELECT 'larger than a zero-byte forecast'").Scan(&text); err != nil || text != "larger than a zero-byte forecast" {
		t.Fatal("default row budget gained a native forecast", text, err)
	}
	assertFixed(t, connection, 65536, 128)
}
