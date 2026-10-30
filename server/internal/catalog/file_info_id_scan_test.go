package catalog

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"reflect"
	"runtime"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

type fileInfoIDTestSource struct {
	ids       []int64
	match     func(int64) bool
	reads     atomic.Int64
	checks    atomic.Int64
	staleAt   int64
	badCursor bool
}

func (s *fileInfoIDTestSource) AccountedBytes() int64 { return 128 + int64(cap(s.ids))*8 }
func (s *fileInfoIDTestSource) Check(context.Context) error {
	if n := s.checks.Add(1); s.staleAt > 0 && n >= s.staleAt {
		return readprep.ErrStale
	}
	return nil
}
func (s *fileInfoIDTestSource) Next(_ context.Context, has bool, after int64) (int64, bool, bool, error) {
	s.reads.Add(1)
	i := 0
	if has {
		i = sort.Search(len(s.ids), func(i int) bool { return s.ids[i] > after })
		if s.badCursor {
			return after, true, true, nil
		}
	}
	if i == len(s.ids) {
		return 0, false, false, nil
	}
	id := s.ids[i]
	return id, s.match == nil || s.match(id), true, nil
}

func fileInfoIDTestRun(t *testing.T, source fileInfoIDSource, spec fileInfoIDSpec) (fileInfoIDSummary, error) {
	t.Helper()
	store, err := readprep.New(readprep.Config{Workers: 1, MaxBytes: 128 << 10})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	plan, err := fileInfoIDPlan(source, spec)
	if err != nil {
		return fileInfoIDSummary{}, err
	}
	ctx := context.Background()
	h, err := store.Submit(ctx, "private-caller", readprep.Key{Namespace: "file-info-id-test", Basis: "exact-source", Canonical: "window"}, plan)
	if err != nil {
		return fileInfoIDSummary{}, err
	}
	defer store.Cancel(ctx, "private-caller", h)
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		result, err := store.Poll(ctx, "private-caller", h)
		if err != nil {
			return fileInfoIDSummary{}, err
		}
		if result.State == readprep.Ready {
			defer result.Release()
			if err := result.Reserve(800); err != nil {
				return fileInfoIDSummary{}, err
			}
			if len(result.Payload) > 20+8*100 {
				t.Fatal("summary retained something beyond bounded numeric IDs")
			}
			return decodeFileInfoIDs(result.Payload)
		}
		result.Release()
		select {
		case <-timer.C:
			t.Fatal("ID preparation did not finish")
		default:
			runtime.Gosched()
		}
	}
}

func TestFileInfoIDWindowsExactTotalsAndDirections(t *testing.T) {
	ids := make([]int64, 1000)
	for i := range ids {
		ids[i] = int64(i)
	}
	// Only even IDs are accepted, so the 1-based global ordinal of v is v/2+1.
	for _, tc := range []struct {
		name           string
		spec           fileInfoIDSpec
		want           []int64
		first          int64
		earlier, later bool
	}{
		{"initial", fileInfoIDSpec{Limit: 3, Direction: fileInfoForward}, []int64{0, 2, 4}, 1, false, true},
		{"after", fileInfoIDSpec{Limit: 3, Direction: fileInfoForward, HasAnchor: true, Anchor: 4}, []int64{6, 8, 10}, 4, true, true},
		{"reload", fileInfoIDSpec{Limit: 3, Direction: fileInfoReload, HasAnchor: true, Anchor: 4}, []int64{4, 6, 8}, 3, true, true},
		{"before", fileInfoIDSpec{Limit: 3, Direction: fileInfoBackward, HasAnchor: true, Anchor: 10}, []int64{4, 6, 8}, 3, true, true},
		{"first-before", fileInfoIDSpec{Limit: 3, Direction: fileInfoBackward, HasAnchor: true, Anchor: 4}, []int64{0, 2}, 1, false, true},
		{"before-last", fileInfoIDSpec{Limit: 3, Direction: fileInfoBackward, HasAnchor: true, Anchor: 998}, []int64{992, 994, 996}, 497, true, true},
		{"last-after", fileInfoIDSpec{Limit: 3, Direction: fileInfoForward, HasAnchor: true, Anchor: 994}, []int64{996, 998}, 499, true, false},
		{"final-short-after", fileInfoIDSpec{Limit: 3, Direction: fileInfoForward, HasAnchor: true, Anchor: 996}, []int64{998}, 500, true, false},
		{"final-short-reload", fileInfoIDSpec{Limit: 3, Direction: fileInfoReload, HasAnchor: true, Anchor: 994}, []int64{994, 996, 998}, 498, true, false},
		// An empty page reports no ordinal; its edges stay the conservative
		// per-row aggregation already used by the accepted direction logic.
		{"empty-after", fileInfoIDSpec{Limit: 3, Direction: fileInfoForward, HasAnchor: true, Anchor: 998}, []int64{}, 0, true, true},
		{"empty-before", fileInfoIDSpec{Limit: 3, Direction: fileInfoBackward, HasAnchor: true, Anchor: 0}, []int64{}, 0, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := &fileInfoIDTestSource{ids: ids, match: func(id int64) bool { return id%2 == 0 }}
			got, err := fileInfoIDTestRun(t, source, tc.spec)
			if err != nil {
				t.Fatal(err)
			}
			if got.Total != 500 || !reflect.DeepEqual(got.IDs, tc.want) || got.FirstOrdinal != tc.first || got.Earlier != tc.earlier || got.Later != tc.later {
				t.Fatalf("got %+v, expected IDs%v first%d edges%v/%v", got, tc.want, tc.first, tc.earlier, tc.later)
			}
			if source.reads.Load() != 1001 {
				t.Fatalf("raw rejects/EOF were skipped: %d reads", source.reads.Load())
			}
			if source.checks.Load() < 17 { // >=8 bounded quanta, each with before/after check.
				t.Fatal("whole source was consumed in one quantum")
			}
		})
	}
}

func TestFileInfoIDAnchorRequiresAnAcceptedRawID(t *testing.T) {
	source := &fileInfoIDTestSource{ids: []int64{0, 1, 2, 3, 4, 5, 6}, match: func(id int64) bool { return id%2 == 0 }}
	// An anchor that never arrives as an accepted row proves the caller's cursor
	// is not in this lane, so the window is stale even at genuine EOF.
	for _, spec := range []fileInfoIDSpec{
		{Limit: 2, Direction: fileInfoForward, HasAnchor: true, Anchor: 5},   // present but rejected
		{Limit: 2, Direction: fileInfoForward, HasAnchor: true, Anchor: 8},   // beyond the final raw value
		{Limit: 2, Direction: fileInfoForward, HasAnchor: true, Anchor: -1},  // before the first raw value
		{Limit: 2, Direction: fileInfoBackward, HasAnchor: true, Anchor: -2}, // no accepted predecessor
		{Limit: 2, Direction: fileInfoReload, HasAnchor: true, Anchor: 7},    // never stored
	} {
		if _, err := fileInfoIDTestRun(t, source, spec); !errors.Is(err, readprep.ErrStale) {
			t.Fatalf("anchor %+v accepted: %v", spec, err)
		}
	}
	// The accepted edges still resolve at any page position.
	for _, tc := range []struct {
		spec  fileInfoIDSpec
		want  []int64
		first int64
	}{
		{fileInfoIDSpec{Limit: 2, Direction: fileInfoForward, HasAnchor: true, Anchor: 0}, []int64{2, 4}, 2},
		{fileInfoIDSpec{Limit: 2, Direction: fileInfoReload, HasAnchor: true, Anchor: 6}, []int64{6}, 4},
		{fileInfoIDSpec{Limit: 2, Direction: fileInfoBackward, HasAnchor: true, Anchor: 6}, []int64{2, 4}, 2},
	} {
		got, err := fileInfoIDTestRun(t, source, tc.spec)
		if err != nil || !reflect.DeepEqual(got.IDs, tc.want) || got.FirstOrdinal != tc.first {
			t.Fatalf("%+v: %+v %v", tc.spec, got, err)
		}
	}
}

func TestFileInfoIDWindowsIntegerEdgesAndBasisDrift(t *testing.T) {
	source := &fileInfoIDTestSource{ids: []int64{math.MinInt64, -1, 0, math.MaxInt64}}
	got, err := fileInfoIDTestRun(t, source, fileInfoIDSpec{Limit: 100, Direction: fileInfoForward})
	if err != nil || !reflect.DeepEqual(got.IDs, source.ids) || got.Total != 4 || got.Earlier || got.Later {
		t.Fatal(got, err)
	}
	for _, n := range []int64{1, 2, 3} {
		source = &fileInfoIDTestSource{ids: []int64{1, 2}, staleAt: n}
		_, err = fileInfoIDTestRun(t, source, fileInfoIDSpec{Limit: 1, Direction: fileInfoForward})
		if !errors.Is(err, readprep.ErrStale) {
			t.Fatalf("basis drift at check%d: %v", n, err)
		}
	}
	source = &fileInfoIDTestSource{ids: []int64{1, 2}, badCursor: true}
	if _, err = fileInfoIDTestRun(t, source, fileInfoIDSpec{Limit: 1, Direction: fileInfoForward}); err == nil {
		t.Fatal("non-progressing source was accepted")
	}
}

func fileInfoIDTestPayload(version byte, count int, earlier, later byte, total, first int64, ids ...int64) []byte {
	payload := make([]byte, 20+8*count)
	payload[0], payload[1], payload[2], payload[3] = version, byte(count), earlier, later
	binary.BigEndian.PutUint64(payload[4:12], uint64(total))
	binary.BigEndian.PutUint64(payload[12:20], uint64(first))
	for i, id := range ids {
		binary.BigEndian.PutUint64(payload[20+8*i:], uint64(id))
	}
	return payload
}

func TestFileInfoIDWindowsMalformedPayloadAndAdmission(t *testing.T) {
	valid := fileInfoIDTestPayload(2, 2, 1, 1, 9, 4, 11, 13)
	summary, err := decodeFileInfoIDs(valid)
	if err != nil || summary.Total != 9 || summary.FirstOrdinal != 4 || !reflect.DeepEqual(summary.IDs, []int64{11, 13}) {
		t.Fatal("valid summary rejected", summary, err)
	}
	empty, err := decodeFileInfoIDs(fileInfoIDTestPayload(2, 0, 0, 0, 0, 0))
	if err != nil || empty.Total != 0 || empty.FirstOrdinal != 0 || len(empty.IDs) != 0 {
		t.Fatal("valid empty summary rejected", empty, err)
	}
	for _, raw := range [][]byte{
		nil,
		make([]byte, 12),
		make([]byte, 20),
		{1, 255, 0, 0},
		append([]byte{1, 101}, make([]byte, 826)...),
		// Corrupt version and length must be rejected before any allocation.
		fileInfoIDTestPayload(1, 2, 0, 0, 9, 4, 11, 13),
		fileInfoIDTestPayload(3, 2, 0, 0, 9, 4, 11, 13),
		valid[:len(valid)-1],
		append(append([]byte{}, valid...), 0),
		// Ordinal range must agree with the reported count and total.
		fileInfoIDTestPayload(2, 2, 0, 0, 9, 0, 11, 13),
		fileInfoIDTestPayload(2, 2, 0, 0, 9, 9, 11, 13),
		fileInfoIDTestPayload(2, 2, 0, 0, 2, 2, 11, 13),
		fileInfoIDTestPayload(2, 1, 0, 0, 3, -1, 11),
		fileInfoIDTestPayload(2, 0, 0, 0, 3, 1),
	} {
		if _, err := decodeFileInfoIDs(raw); err == nil {
			t.Fatal("malformed ID payload accepted", raw)
		}
	}
	for _, limit := range []int{0, 101, -1} {
		if _, err := fileInfoIDPlan(&fileInfoIDTestSource{}, fileInfoIDSpec{Limit: limit, Direction: fileInfoForward}); !errors.Is(err, readprep.ErrInvalid) {
			t.Fatal(limit, err)
		}
	}
	store, err := readprep.New(readprep.Config{MaxBytes: 4096, Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	source := &fileInfoIDTestSource{ids: []int64{1}}
	plan, err := fileInfoIDPlan(source, fileInfoIDSpec{Limit: 1, Direction: fileInfoForward})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Submit(context.Background(), "caller", readprep.Key{Namespace: "id-test", Basis: "basis", Canonical: "window"}, plan)
	if !errors.Is(err, readprep.ErrCapacity) || source.reads.Load() != 0 || source.checks.Load() != 0 {
		t.Fatal("allocation/read happened before budget admission", err)
	}
}
