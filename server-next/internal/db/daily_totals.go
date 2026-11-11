package db

import (
	"context"
	"database/sql"
	"strings"
)

// The server's lifetime statistics (daily_totals): a few totals for each day, never removed.

// DailyTotal is one total of one day.
type DailyTotal struct {
	Day    int // YYYYMMDD
	Metric string
	Value  int64
}

// AddDailyTotals adds counts to a day's totals and raises its highest levels (most) to what
// was reached, in one commit.
func (d *DB) AddDailyTotals(ctx context.Context, day int, counts, most map[string]int64) error {
	return d.write(ctx, Background, func(tx *sql.Tx) error {
		for metric, n := range counts {
			if _, err := tx.Exec(`INSERT INTO daily_totals (day, metric, value) VALUES (?, ?, ?)
				ON CONFLICT (day, metric) DO UPDATE SET value = value + excluded.value`, day, metric, n); err != nil {
				return err
			}
		}
		for metric, n := range most {
			if _, err := tx.Exec(`INSERT INTO daily_totals (day, metric, value) VALUES (?, ?, ?)
				ON CONFLICT (day, metric) DO UPDATE SET value = max(value, excluded.value)`, day, metric, n); err != nil {
				return err
			}
		}
		return nil
	})
}

// DailyTotals reads the totals of the days from first to last, both included, whose metric
// starts with prefix ("" for all), in day order.
func (d *DB) DailyTotals(ctx context.Context, first, last int, prefix string) ([]DailyTotal, error) {
	query, args := `SELECT day, metric, value FROM daily_totals WHERE day BETWEEN ? AND ?`, []any{first, last}
	if prefix != "" {
		escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(prefix)
		query, args = query+` AND metric LIKE ? ESCAPE '\'`, append(args, escaped+"%")
	}
	rows, err := d.read.QueryContext(ctx, query+` ORDER BY day`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DailyTotal
	for rows.Next() {
		var t DailyTotal
		if err := rows.Scan(&t.Day, &t.Metric, &t.Value); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// FirstDay is the first day the server has totals for, 0 when it has none.
func (d *DB) FirstDay(ctx context.Context) (int, error) {
	var day sql.NullInt64
	err := d.read.QueryRowContext(ctx, `SELECT min(day) FROM daily_totals`).Scan(&day)
	return int(day.Int64), err
}
