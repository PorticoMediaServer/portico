package metadata

import (
	"context"
	"math"
	"sort"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// K follows the existing client/UI's 64-value display contract. K+1 is an
// overflow witness, never an exact count. A saturated scalar set is discarded.
const bulkPreviewAggregateBytes int64 = 8 << 20

type bulkPreviewCounter struct {
	value string
	count int64
	seen  uint64
}

type bulkPreviewAggregateField struct {
	values                 []bulkPreviewCounter
	entries                []bulkPreviewCounter
	locked                 int64
	many, entriesTruncated bool
}

type bulkPreviewAggregate struct {
	fields      map[string]*bulkPreviewAggregateField
	reserve     func(int64) error
	used, peak  int64
	observation uint64
}

func (m *bulkPreviewAggregate) charge(n int64) error {
	if n < 0 || n > bulkPreviewAggregateBytes-m.used {
		return readprep.ErrCapacity
	}
	if m.reserve != nil {
		if err := m.reserve(m.used + n); err != nil {
			return err
		}
	}
	m.used += n
	m.peak = max(m.peak, m.used)
	return nil
}

func (m *bulkPreviewAggregate) free(n int64) error {
	m.used -= n
	// Memory was detached/cleared before releasing its reservation. A closed
	// computation will be reclaimed by readprep; shrinking never bypasses cap.
	if m.reserve != nil {
		return m.reserve(m.used)
	}
	return nil
}

func previewAddCount(value *int64, n int64) error {
	if n <= 0 || *value > math.MaxInt64-n {
		return ErrRepairInput
	}
	*value += n
	return nil
}

func (m *bulkPreviewAggregate) field(name string) (*bulkPreviewAggregateField, error) {
	if m.fields == nil {
		if err := m.charge(64 << 10); err != nil {
			return nil, err
		}
		m.fields = make(map[string]*bulkPreviewAggregateField)
	}
	f := m.fields[name]
	if f == nil {
		// Fixed capacity, including headers, map buckets and output/sort overlap.
		if err := m.charge(4096 + 256*int64(2*(bulkPreviewValues+1)) + int64(len(name))); err != nil {
			return nil, err
		}
		f = &bulkPreviewAggregateField{values: make([]bulkPreviewCounter, 0, bulkPreviewValues+1), entries: make([]bulkPreviewCounter, 0, bulkPreviewValues+1)}
		m.fields[strings.Clone(name)] = f
	}
	return f, nil
}

// Keep owned strings and conservative escaping/normalization/output overlap.
// Borrowed source substrings never become retained map keys or slice entries.
func (m *bulkPreviewAggregate) copyValue(v string) (string, error) {
	if int64(len(v)) > (bulkPreviewAggregateBytes-256)/32 {
		return "", readprep.ErrCapacity
	}
	if err := m.charge(256 + 32*int64(len(v))); err != nil {
		return "", err
	}
	return strings.Clone(v), nil
}

func (m *bulkPreviewAggregate) add(ctx context.Context, observations []bulkPreviewObservation) error {
	for _, o := range observations {
		if err := ctx.Err(); err != nil {
			return err
		}
		if o.count <= 0 {
			return ErrRepairInput
		}
		if m.observation == math.MaxUint64 {
			return ErrRepairInput
		}
		m.observation++
		f, err := m.field(o.field)
		if err != nil {
			return err
		}
		if o.locked {
			if err = previewAddCount(&f.locked, o.count); err != nil {
				return err
			}
		}
		if !f.many {
			found := false
			for i := range f.values {
				if f.values[i].value == o.value {
					found = true
					if err = previewAddCount(&f.values[i].count, o.count); err != nil {
						return err
					}
					break
				}
			}
			if !found {
				if len(f.values) == bulkPreviewValues {
					// The K+1th distinct key proves many. Do not copy it or
					// retain/count further scalar distinct values for this field.
					f.many = true
					var released int64
					for _, v := range f.values {
						released += 256 + 32*int64(len(v.value))
					}
					clear(f.values)
					f.values = nil
					if err = m.free(released); err != nil {
						return err
					}
				} else {
					value, err := m.copyValue(o.value)
					if err != nil {
						return err
					}
					f.values = append(f.values, bulkPreviewCounter{value: value, count: o.count})
				}
			}
		}
		// The fixed retained counters carry a per-observation marker. This
		// deduplicates even a long genre list in O(E log K) without an O(E)
		// side map or quadratic comparisons of the whole source list.
		for _, entry := range o.entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			index := sort.Search(len(f.entries), func(i int) bool { return f.entries[i].value >= entry })
			if index < len(f.entries) && f.entries[index].value == entry {
				if f.entries[index].seen == m.observation {
					continue
				}
				if err = previewAddCount(&f.entries[index].count, o.count); err != nil {
					return err
				}
				f.entries[index].seen = m.observation
				continue
			}
			if len(f.entries) == bulkPreviewValues {
				f.entriesTruncated = true
				if index == len(f.entries) {
					continue
				}
			}
			var released int64
			if len(f.entries) == bulkPreviewValues {
				released = 256 + 32*int64(len(f.entries[len(f.entries)-1].value))
			}
			value, err := m.copyValue(entry)
			if err != nil {
				return err
			}
			if len(f.entries) < bulkPreviewValues {
				f.entries = append(f.entries, bulkPreviewCounter{})
			}
			copy(f.entries[index+1:], f.entries[index:len(f.entries)-1])
			f.entries[index] = bulkPreviewCounter{value: value, count: o.count, seen: m.observation}
			if released > 0 {
				if err = m.free(released); err != nil {
					return err
				}
			}
			// The entry cutoff only decreases. Every final displayed key was
			// retained from its first occurrence, so its count remains exact.
		}
	}
	return nil
}

func (m *bulkPreviewAggregate) result(schema []RepairFieldSpec) (map[string]BulkPreviewField, error) {
	if err := m.charge(4096 * int64(len(schema))); err != nil {
		return nil, err
	}
	out := make(map[string]BulkPreviewField, len(schema))
	for _, spec := range schema {
		f := m.fields[spec.Field]
		if f == nil {
			return nil, ErrRepairInput
		}
		field := BulkPreviewField{State: "shared", DistinctCount: int64(len(f.values)), Locked: f.locked, Values: []BulkPreviewValue{}, Entries: []BulkPreviewValue{}, Truncated: f.entriesTruncated}
		if f.many {
			field.State, field.DistinctCount, field.AtLeast, field.Truncated = "many", 0, bulkPreviewValues+1, true
		} else {
			if len(f.values) > 1 {
				field.State = "mixed"
			}
			sort.Slice(f.values, func(i, j int) bool { return f.values[i].value < f.values[j].value })
			for _, v := range f.values {
				field.Values = append(field.Values, BulkPreviewValue{v.value, v.count})
			}
			if field.State == "shared" && len(field.Values) > 0 {
				v := field.Values[0].Value
				field.Value = &v
			}
		}
		for _, v := range f.entries {
			field.Entries = append(field.Entries, BulkPreviewValue{v.value, v.count})
		}
		out[spec.Field] = field
	}
	return out, nil
}
