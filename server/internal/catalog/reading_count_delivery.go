package catalog

import (
	"context"
	"fmt"
	"strings"
)

type readingCountReadMemoryKey struct{}
type readingCountReadMemory struct {
	resize ReadingCountResize
	bytes  int64
}

// WithReadingCountReadMemory installs a request/constructor-scoped reservation
// callback. Never retain a foundation Budget in a Service beyond that call.
func (s *Service) WithReadingCountReadMemory(resize ReadingCountResize) *Service {
	if resize == nil {
		return s
	}
	return s.WithContext(context.WithValue(s.Context(), readingCountReadMemoryKey{}, &readingCountReadMemory{resize: resize}))
}

// reserveReadingCountRows probes the exact bounded page SQL's selected scalar
// lengths before its normal Go row/DTO hydration. It neither counts the whole
// set nor retains source values. The same snapshot supplies both passes.
func (s *Service) reserveReadingCountRows(query string, args []any, extra ...int) error {
	tracker, _ := s.Context().Value(readingCountReadMemoryKey{}).(*readingCountReadMemory)
	if tracker == nil {
		return nil
	}
	n := 9
	if len(extra) > 0 {
		n += extra[0]
	}
	columns := make([]string, n)
	terms := make([]string, n)
	for i := range columns {
		columns[i] = fmt.Sprintf("c%d", i)
		terms[i] = "COALESCE(length(CAST(" + columns[i] + " AS BLOB)),0)"
	}
	// Ready attribution is a single PK row, not a history or whole-view join.
	measure := `WITH measured(` + strings.Join(columns, ",") + `) AS (` + query + `) SELECT ` + strings.Join(terms, "+") + `+COALESCE((SELECT COALESCE(length(CAST(m.author_value AS BLOB)),0)+COALESCE(length(CAST(m.series_value AS BLOB)),0)+COALESCE(length(CAST(m.series_id AS BLOB)),0) FROM reading_attribution_members m JOIN reading_attribution_state st ON st.library_id=m.library_id WHERE m.library_id=(SELECT e.library_id FROM catalog_entities e WHERE e.id=c1) AND m.item_id=c1 AND m.generation=st.ready_generation AND st.input_generation=st.ready_generation LIMIT 1),0)+COALESCE((SELECT length(CAST(d.added_text AS BLOB)) FROM catalog_item_details d WHERE d.entity_id=c1),0) FROM measured`
	rows, err := s.read().Query(measure, args...)
	if err != nil {
		return err
	}
	var selected int64
	var largest int64
	for rows.Next() {
		var bytes int64
		if err = rows.Scan(&bytes); err != nil {
			rows.Close()
			return err
		}
		selected += bytes*32 + 4096
		if bytes*32 > largest {
			largest = bytes * 32
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	tracker.bytes += selected
	return tracker.resize(tracker.bytes, largest > ReadingCountMaximumBytes)
}

func (s *Service) readingCountMemoryActive() bool {
	tracker, _ := s.Context().Value(readingCountReadMemoryKey{}).(*readingCountReadMemory)
	return tracker != nil
}
func (s *Service) reserveReadingCountAux(bytes int64) error {
	tracker, _ := s.Context().Value(readingCountReadMemoryKey{}).(*readingCountReadMemory)
	if tracker == nil {
		return nil
	}
	tracker.bytes += bytes
	return tracker.resize(tracker.bytes, false)
}
