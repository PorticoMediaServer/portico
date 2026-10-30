package catalog

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"runtime"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// groupInfoTestSource is a literal in-memory catalogue. It offers exactly the
// two raw walks and the two point comparators the reducer is allowed to use, so
// every raw row and every comparison is counted independently of SQL.
type groupTestFile struct {
	Shape, Part, Asset int64
	Offered, Available bool
	Size               int64
	Duration           float64
	Height             int64
	OrderAvailable     int64
	Token              string
}

type groupInfoTestSource struct {
	files              []groupTestFile
	reads, comparisons atomic.Int64
	checks             atomic.Int64
	staleAt            int64
}

func (s *groupInfoTestSource) AccountedBytes() int64 { return 512 }

func (s *groupInfoTestSource) Check(context.Context) error {
	if n := s.checks.Add(1); s.staleAt > 0 && n >= s.staleAt {
		return readprep.ErrStale
	}
	return nil
}

func groupInfoTestRow(f groupTestFile) fileInfoGroupRow {
	return fileInfoGroupRow{Shape: f.Shape, Part: f.Part, Asset: f.Asset, Offered: f.Offered, Available: f.Available, Size: f.Size, Duration: f.Duration}
}

func groupInfoTestAfter(a, b fileInfoGroupKey) bool {
	if a.Shape != b.Shape {
		return a.Shape > b.Shape
	}
	if a.Part != b.Part {
		return a.Part > b.Part
	}
	return a.Asset > b.Asset
}

func (s *groupInfoTestSource) NextPart(_ context.Context, after fileInfoGroupKey, hasAfter bool) (fileInfoGroupRow, bool, error) {
	s.reads.Add(1)
	var best *groupTestFile
	for i := range s.files {
		f := &s.files[i]
		if hasAfter && (f.Part < after.Part || f.Part == after.Part && f.Asset <= after.Asset) {
			continue
		}
		if best == nil || f.Part < best.Part || f.Part == best.Part && f.Asset < best.Asset {
			best = f
		}
	}
	if best == nil {
		return fileInfoGroupRow{}, false, nil
	}
	return groupInfoTestRow(*best), true, nil
}

func (s *groupInfoTestSource) NextShape(_ context.Context, after fileInfoGroupKey, hasAfter bool) (fileInfoGroupRow, bool, error) {
	s.reads.Add(1)
	var best *groupTestFile
	for i := range s.files {
		f := &s.files[i]
		key := fileInfoGroupKey{Shape: f.Shape, Part: f.Part, Asset: f.Asset}
		if hasAfter && !groupInfoTestAfter(key, after) {
			continue
		}
		if best == nil {
			best = f
			continue
		}
		if f.Shape < best.Shape || f.Shape == best.Shape && (f.Part < best.Part || f.Part == best.Part && f.Asset < best.Asset) {
			best = f
		}
	}
	if best == nil {
		return fileInfoGroupRow{}, false, nil
	}
	return groupInfoTestRow(*best), true, nil
}

func (s *groupInfoTestSource) NextShapeFile(_ context.Context, shape int64, after fileInfoGroupKey, hasAfter bool) (fileInfoGroupRow, bool, error) {
	s.reads.Add(1)
	var best *groupTestFile
	for i := range s.files {
		f := &s.files[i]
		if f.Shape != shape {
			continue
		}
		if hasAfter && (f.Part < after.Part || f.Part == after.Part && f.Asset <= after.Asset) {
			continue
		}
		if best == nil || f.Part < best.Part || f.Part == best.Part && f.Asset < best.Asset {
			best = f
		}
	}
	if best == nil {
		return fileInfoGroupRow{}, false, nil
	}
	return groupInfoTestRow(*best), true, nil
}

func groupInfoTestOrder(a, b groupTestFile) int {
	if a.Part != b.Part {
		if a.Part < b.Part {
			return -1
		}
		return 1
	}
	if a.OrderAvailable != b.OrderAvailable {
		if a.OrderAvailable > b.OrderAvailable {
			return -1
		}
		return 1
	}
	if a.Height != b.Height {
		if a.Height > b.Height {
			return -1
		}
		return 1
	}
	if a.Size != b.Size {
		if a.Size > b.Size {
			return -1
		}
		return 1
	}
	if a.Token < b.Token {
		return -1
	}
	if a.Token > b.Token {
		return 1
	}
	return 0
}

func (s *groupInfoTestSource) byAsset(asset int64) (groupTestFile, bool) {
	for _, f := range s.files {
		if f.Asset == asset {
			return f, true
		}
	}
	return groupTestFile{}, false
}

func (s *groupInfoTestSource) CompareFiles(_ context.Context, a, b int64) (int, error) {
	s.comparisons.Add(1)
	fa, oka := s.byAsset(a)
	fb, okb := s.byAsset(b)
	if !oka || !okb {
		return 0, readprep.ErrStale
	}
	return groupInfoTestOrder(fa, fb), nil
}

func (s *groupInfoTestSource) CompareGroups(ctx context.Context, a, b fileInfoOccurrence) (int, error) {
	cmp, err := s.CompareFiles(ctx, a.Item, b.Item)
	if err != nil || cmp != 0 {
		return cmp, err
	}
	return s.CompareFiles(ctx, a.Asset, b.Asset)
}

type groupOracleFile struct {
	Asset, Shape, Part int64
	Available          bool
	Size               int64
	DurationMS         int64
	Height             int64
	OrderAvailable     int64
	Token              string
}

func groupOracleLess(a, b groupOracleFile) bool {
	if a.Part != b.Part {
		return a.Part < b.Part
	}
	if a.OrderAvailable != b.OrderAvailable {
		return a.OrderAvailable > b.OrderAvailable
	}
	if a.Height != b.Height {
		return a.Height > b.Height
	}
	if a.Size != b.Size {
		return a.Size > b.Size
	}
	return a.Token < b.Token
}

func groupOracleDistinct(g []groupOracleFile) bool {
	seen := map[int64]bool{}
	for _, f := range g {
		if seen[f.Part] {
			return false
		}
		seen[f.Part] = true
	}
	return true
}

// groupOracleGroups is a literal translation of playbackv1 versionsOf plus the
// native link.available intersection and zero-member drop. It is the independent
// expected directory: global composite, shape-first order, duplicates as
// singletons, then the original offered head facts.
func groupOracleGroups(offered []groupOracleFile) []fileInfoGroupNumeric {
	sort.SliceStable(offered, func(i, j int) bool { return groupOracleLess(offered[i], offered[j]) })
	out := []fileInfoGroupNumeric{}
	if len(offered) == 0 {
		return out
	}
	sum := func(g []groupOracleFile) (members, size, duration int64) {
		for _, f := range g {
			if f.Available {
				members++
			}
			size += f.Size
			duration += f.DurationMS
		}
		return members, size, duration
	}
	if len(offered) > 1 && groupOracleDistinct(offered) {
		members, size, duration := sum(offered)
		if members > 0 {
			out = append(out, fileInfoGroupNumeric{ShapeHead: offered[0].Asset, Head: offered[0].Asset, Shape: offered[0].Shape, OfferedMembers: int64(len(offered)), Members: members, SizeBytes: size, DurationMS: duration, Type: fileInfoGroupGlobalComposite})
		}
		return out
	}
	order := []int64{}
	byShape := map[int64][]groupOracleFile{}
	for _, f := range offered {
		if _, ok := byShape[f.Shape]; !ok {
			order = append(order, f.Shape)
		}
		byShape[f.Shape] = append(byShape[f.Shape], f)
	}
	for _, shape := range order {
		g := byShape[shape]
		if len(g) > 1 && groupOracleDistinct(g) {
			members, size, duration := sum(g)
			if members > 0 {
				out = append(out, fileInfoGroupNumeric{ShapeHead: g[0].Asset, Head: g[0].Asset, Shape: shape, OfferedMembers: int64(len(g)), Members: members, SizeBytes: size, DurationMS: duration, Type: fileInfoGroupShapeComposite})
			}
			continue
		}
		for _, f := range g {
			if !f.Available {
				continue
			}
			out = append(out, fileInfoGroupNumeric{ShapeHead: g[0].Asset, Head: f.Asset, Shape: shape, OfferedMembers: 1, Members: 1, SizeBytes: f.Size, DurationMS: f.DurationMS, Type: fileInfoGroupSingleton})
		}
	}
	return out
}

func groupOracleFromFiles(files []groupTestFile) (int64, int64, []fileInfoGroupNumeric) {
	var raw, eligible int64
	offered := []groupOracleFile{}
	for _, f := range files {
		raw++
		if !f.Offered {
			continue
		}
		if f.Available {
			eligible++
		}
		milliseconds, _ := fileInfoGroupMilliseconds(f.Duration)
		offered = append(offered, groupOracleFile{Asset: f.Asset, Shape: f.Shape, Part: f.Part, Available: f.Available, Size: f.Size, DurationMS: milliseconds, Height: f.Height, OrderAvailable: f.OrderAvailable, Token: f.Token})
	}
	return raw, eligible, groupOracleGroups(offered)
}

func groupInfoTestRun(t *testing.T, source fileInfoGroupSource, canManage, canSplitKind bool, spec fileInfoGroupSpec) (fileInfoGroupSummary, error) {
	t.Helper()
	plan, err := fileInfoGroupPlanSource(source, canManage, canSplitKind, spec)
	if err != nil {
		return fileInfoGroupSummary{}, err
	}
	return groupInfoPollPlan(t, plan)
}

func groupInfoPollPlan(t *testing.T, plan readprep.Plan) (fileInfoGroupSummary, error) {
	t.Helper()
	store, err := readprep.New(readprep.Config{Workers: 1, MaxBytes: 512 << 10})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	handle, err := store.Submit(ctx, "group-private", readprep.Key{Namespace: "group-test", Basis: "original-info-head", Canonical: "window"}, plan)
	if err != nil {
		return fileInfoGroupSummary{}, err
	}
	defer store.Cancel(ctx, "group-private", handle)
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for {
		result, err := store.Poll(ctx, "group-private", handle)
		if err != nil {
			return fileInfoGroupSummary{}, err
		}
		if result.State == readprep.Ready {
			defer result.Release()
			if err := result.Reserve(36 + fileInfoGroupRecordBytes*100); err != nil {
				return fileInfoGroupSummary{}, err
			}
			if len(result.Payload) > 36+fileInfoGroupRecordBytes*100 {
				t.Fatal("group summary retained labels or unbounded descriptors")
			}
			return decodeFileInfoGroupSummary(result.Payload)
		}
		result.Release()
		select {
		case <-timeout.C:
			t.Fatal("group preparation did not finish")
		default:
			runtime.Gosched()
		}
	}
}

func groupTestFileAt(asset, shape, part, size, height int64, available bool) groupTestFile {
	return groupTestFile{Shape: shape, Part: part, Asset: asset, Offered: true, Available: available, Size: size, Height: height, Duration: 1, OrderAvailable: 1, Token: fmt.Sprintf("t%05d", asset)}
}

func TestFileInfoGroupMatchesLegacyOptionsGrouping(t *testing.T) {
	files := []groupTestFile{
		groupTestFileAt(10, 1, 0, 100, 1080, true),
		groupTestFileAt(30, 1, 0, 100, 1080, true),
		groupTestFileAt(20, 2, 0, 100, 1080, true),
		groupTestFileAt(40, 2, 0, 100, 1080, true),
		groupTestFileAt(50, 3, 0, 300, 2160, true),
		groupTestFileAt(60, 3, 1, 301, 2160, true),
		groupTestFileAt(70, 4, 0, 400, 2160, false),
		groupTestFileAt(80, 4, 1, 401, 2160, true),
		groupTestFileAt(90, 5, 0, 500, 480, false),
		groupTestFileAt(95, 5, 1, 501, 480, false),
	}
	notOffered := groupTestFileAt(100, 1, 5, 999, 1080, true)
	notOffered.Offered = false
	files = append(files, notOffered)
	raw, eligible, want := groupOracleFromFiles(files)
	source := &groupInfoTestSource{files: files}
	page, err := groupInfoTestRun(t, source, true, true, fileInfoGroupSpec{Limit: 100, Direction: fileInfoForward})
	if err != nil {
		t.Fatal(err)
	}
	if page.Files != raw || page.Eligible != eligible || page.Total != int64(len(want)) || page.FirstOrdinal != 1 {
		t.Fatalf("global counts %+v want files%d eligible%d total%d", page, raw, eligible, len(want))
	}
	if !reflect.DeepEqual(page.Groups, want) {
		t.Fatalf("groups %+v want %+v", page.Groups, want)
	}
	// Shape-first order keeps both of shape 1's singletons before shape 2's
	// first member; a global merge of singleton heads would say 10,20,30,40.
	position := func(head int64) int {
		for i, group := range page.Groups {
			if group.Head == head {
				return i
			}
		}
		return -1
	}
	i10, i30, i20, i40 := position(10), position(30), position(20), position(40)
	if !(i10 >= 0 && i10 < i30 && i30 < i20 && i20 < i40) {
		t.Fatalf("directory order is not shape-first: %+v", page.Groups)
	}
	if !page.CanSplit {
		t.Fatal("manageable splittable surface with a one-member group lost split")
	}
}

func TestFileInfoGroupGlobalCompositeCoversMixedShapes(t *testing.T) {
	files := []groupTestFile{
		groupTestFileAt(10, 1, 0, 100, 1080, true),
		groupTestFileAt(20, 2, 1, 200, 2160, false),
		groupTestFileAt(30, 3, 2, 300, 720, true),
		groupTestFileAt(40, 1, 3, 400, 1080, true),
	}
	raw, eligible, want := groupOracleFromFiles(files)
	source := &groupInfoTestSource{files: files}
	page, err := groupInfoTestRun(t, source, true, true, fileInfoGroupSpec{Limit: 100, Direction: fileInfoForward})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || !reflect.DeepEqual(page.Groups, want) || page.Files != raw || page.Eligible != eligible {
		t.Fatalf("global composite %+v want %+v", page, want)
	}
	group := page.Groups[0]
	if group.Type != fileInfoGroupGlobalComposite || group.ShapeHead != 10 || group.Head != 10 || group.Shape != 1 || group.OfferedMembers != 4 || group.Members != 3 || group.SizeBytes != 1000 {
		t.Fatalf("mixed-shape global group %+v", group)
	}
}

func TestFileInfoGroupHeadUnavailableAndEligibleDrop(t *testing.T) {
	files := []groupTestFile{
		// Composite head is unavailable but its partner survives.
		groupTestFileAt(10, 1, 0, 100, 2160, false),
		groupTestFileAt(11, 1, 1, 101, 2160, true),
		// Every member is unavailable: the group must disappear.
		groupTestFileAt(20, 2, 0, 200, 1080, false),
		groupTestFileAt(21, 2, 1, 201, 1080, false),
		// A displayed singleton keeps the surface non-empty.
		groupTestFileAt(30, 3, 0, 300, 720, true),
	}
	raw, eligible, want := groupOracleFromFiles(files)
	if len(want) != 2 || want[0].Head != 10 || want[0].Members != 1 || want[0].OfferedMembers != 2 || want[1].Head != 30 {
		t.Fatalf("oracle fixture is not discriminating: %+v", want)
	}
	source := &groupInfoTestSource{files: files}
	page, err := groupInfoTestRun(t, source, true, true, fileInfoGroupSpec{Limit: 100, Direction: fileInfoForward})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || page.Eligible != eligible || page.Files != raw || !reflect.DeepEqual(page.Groups, want) {
		t.Fatalf("displayed intersection %+v want %+v", page, want)
	}
}

func TestFileInfoGroupSplitNeedsManageKindTotalAndSingleMember(t *testing.T) {
	files := []groupTestFile{
		groupTestFileAt(10, 1, 0, 100, 2160, true),
		groupTestFileAt(11, 1, 1, 101, 2160, true),
		groupTestFileAt(20, 2, 0, 200, 720, true),
	}
	for _, tc := range []struct {
		manage, kind bool
		want         bool
	}{{true, true, true}, {false, true, false}, {true, false, false}, {false, false, false}} {
		source := &groupInfoTestSource{files: files}
		page, err := groupInfoTestRun(t, source, tc.manage, tc.kind, fileInfoGroupSpec{Limit: 100, Direction: fileInfoForward})
		if err != nil {
			t.Fatal(err)
		}
		if page.CanSplit != tc.want {
			t.Fatalf("manage=%v kind=%v split=%v want %v", tc.manage, tc.kind, page.CanSplit, tc.want)
		}
	}
	// One surviving group is never splittable even with a one-member shape.
	single := []groupTestFile{groupTestFileAt(10, 1, 0, 100, 2160, true), groupTestFileAt(11, 1, 1, 101, 2160, false)}
	page, err := groupInfoTestRun(t, &groupInfoTestSource{files: single}, true, true, fileInfoGroupSpec{Limit: 100, Direction: fileInfoForward})
	if err != nil || page.Total != 1 || page.CanSplit {
		t.Fatalf("single group split: %+v %v", page, err)
	}
}

func TestFileInfoGroupPerPartMillisecondsTruncateBeforeSum(t *testing.T) {
	files := []groupTestFile{
		{Shape: 1, Part: 0, Asset: 10, Offered: true, Available: true, Size: 1, Height: 1080, Duration: 1.0004, OrderAvailable: 1, Token: "t10"},
		{Shape: 1, Part: 1, Asset: 11, Offered: true, Available: true, Size: 1, Height: 1080, Duration: 2.0006, OrderAvailable: 1, Token: "t11"},
		{Shape: 2, Part: 0, Asset: 20, Offered: true, Available: true, Size: 1, Height: 720, Duration: 0.0009, OrderAvailable: 1, Token: "t20"},
	}
	page, err := groupInfoTestRun(t, &groupInfoTestSource{files: files}, true, true, fileInfoGroupSpec{Limit: 100, Direction: fileInfoForward})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Groups) != 2 || page.Groups[0].DurationMS != 3000 || page.Groups[1].DurationMS != 0 {
		t.Fatalf("per-part millisecond truncation %+v", page.Groups)
	}
}

func TestFileInfoGroupPagesMatchAnchorDirections(t *testing.T) {
	files := []groupTestFile{
		groupTestFileAt(10, 1, 0, 100, 1080, true),
		groupTestFileAt(11, 1, 0, 101, 1080, true),
		groupTestFileAt(12, 1, 1, 102, 1080, true),
		groupTestFileAt(20, 2, 0, 200, 2160, true),
		groupTestFileAt(21, 2, 1, 201, 2160, true),
		groupTestFileAt(30, 3, 0, 300, 720, true),
		groupTestFileAt(31, 3, 0, 301, 720, true),
		groupTestFileAt(40, 4, 0, 400, 480, true),
		groupTestFileAt(50, 5, 0, 500, 480, false),
	}
	_, _, want := groupOracleFromFiles(files)
	if len(want) < 6 {
		t.Fatalf("anchor fixture too small: %+v", want)
	}
	for _, tc := range []struct {
		name      string
		direction byte
		anchor    int
	}{{"initial", fileInfoForward, -1}, {"after-first", fileInfoForward, 0}, {"after-middle", fileInfoForward, 3}, {"after-last", fileInfoForward, len(want) - 1}, {"before-first", fileInfoBackward, 0}, {"before-middle", fileInfoBackward, 3}, {"before-last", fileInfoBackward, len(want) - 1}, {"reload-first", fileInfoReload, 0}, {"reload-middle", fileInfoReload, 3}, {"reload-last", fileInfoReload, len(want) - 1}} {
		t.Run(tc.name, func(t *testing.T) {
			const limit = 3
			spec := fileInfoGroupSpec{Limit: limit, Direction: tc.direction}
			lo, hi := 0, min(limit, len(want))
			if tc.anchor >= 0 {
				spec.HasAnchor = true
				spec.Anchor = fileInfoGroupOccurrence(want[tc.anchor])
				switch tc.direction {
				case fileInfoForward:
					lo, hi = tc.anchor+1, min(tc.anchor+1+limit, len(want))
				case fileInfoBackward:
					lo, hi = max(0, tc.anchor-limit), tc.anchor
				case fileInfoReload:
					lo, hi = tc.anchor, min(tc.anchor+limit, len(want))
				}
			}
			page, err := groupInfoTestRun(t, &groupInfoTestSource{files: files}, true, true, spec)
			if err != nil {
				t.Fatal(err)
			}
			wantFirst := int64(0)
			if hi > lo {
				wantFirst = int64(lo + 1)
			}
			if page.Total != int64(len(want)) || !reflect.DeepEqual(page.Groups, want[lo:hi]) || page.FirstOrdinal != wantFirst || page.Earlier != (lo > 0) || page.Later != (hi < len(want)) {
				t.Fatalf("page %+v want %+v first%d earlier%v later%v", page, want[lo:hi], wantFirst, lo > 0, hi < len(want))
			}
			if !page.CanSplit {
				t.Fatal("one-member shapes exist but split was denied")
			}
		})
	}
}

func TestFileInfoGroupRejectsStaleUnknownAnchorAndNonprogress(t *testing.T) {
	files := []groupTestFile{groupTestFileAt(10, 1, 0, 100, 1080, true), groupTestFileAt(20, 2, 0, 200, 720, true)}
	for _, source := range []*groupInfoTestSource{{files: files, staleAt: 1}, {files: files, staleAt: 2}, {files: files, staleAt: 3}} {
		if _, err := groupInfoTestRun(t, source, true, true, fileInfoGroupSpec{Limit: 1, Direction: fileInfoForward}); err == nil {
			t.Fatal("stale basis succeeded")
		}
	}
	if _, err := groupInfoTestRun(t, &groupInfoTestSource{files: files}, true, true, fileInfoGroupSpec{Limit: 1, Direction: fileInfoForward, HasAnchor: true, Anchor: fileInfoOccurrence{Item: 1, Asset: 2}}); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("unknown anchor accepted", err)
	}
	empty, err := groupInfoTestRun(t, &groupInfoTestSource{}, true, true, fileInfoGroupSpec{Limit: 100, Direction: fileInfoForward})
	if err != nil || empty.Total != 0 || empty.FirstOrdinal != 0 || len(empty.Groups) != 0 || empty.Earlier || empty.Later {
		t.Fatal(empty, err)
	}
}

func TestFileInfoGroupRawWorkStaysBoundedAsShapesVary(t *testing.T) {
	run := func(shapes, perShape int) (int64, int64, fileInfoGroupSummary) {
		files := []groupTestFile{}
		asset := int64(1)
		for shape := 0; shape < shapes; shape++ {
			for part := 0; part < perShape; part++ {
				files = append(files, groupTestFileAt(asset, int64(shape+1), int64(part), asset, 1080, true))
				asset++
			}
		}
		source := &groupInfoTestSource{files: files}
		page, err := groupInfoTestRun(t, source, true, true, fileInfoGroupSpec{Limit: 100, Direction: fileInfoForward})
		if err != nil {
			t.Fatal(err)
		}
		return source.reads.Load(), source.comparisons.Load(), page
	}
	flatReads, flatComparisons, flat := run(600, 1)
	groupedReads, groupedComparisons, grouped := run(60, 10)
	const totalFiles = 600
	if flat.Total != totalFiles || grouped.Total != 60 {
		t.Fatalf("fixture totals flat%d grouped%d", flat.Total, grouped.Total)
	}
	for _, got := range []struct {
		name               string
		reads, comparisons int64
		shapes             int
		groups             int64
	}{{"flat", flatReads, flatComparisons, totalFiles, flat.Total}, {"grouped", groupedReads, groupedComparisons, 60, grouped.Total}} {
		if got.reads > 3*totalFiles+int64(got.shapes)+4 {
			t.Fatalf("%s raw visits %d exceed 3F+shapes", got.name, got.reads)
		}
		if got.comparisons > 4*totalFiles+16*got.groups+32 {
			t.Fatalf("%s comparisons %d exceed linear bound", got.name, got.comparisons)
		}
	}
	if flatReads != groupedReads {
		t.Fatalf("raw visits changed with shape count: flat %d grouped %d", flatReads, groupedReads)
	}
	// The flat fixture spends F+1 reads in the part pass (F rows plus EOF) and
	// F+2 in the shape pass: F rows, then one EOF that closes the last current
	// shape, then one more EOF that proves the resumed pass is exhausted. The
	// second EOF is a fixed stage event, so 2F+3 is exact rather than 2F+2.
	if want := int64(2*totalFiles + 3); flatReads != want {
		t.Fatalf("flat singleton raw reads %d want %d", flatReads, want)
	}
}

func groupInfoTestPayload(version byte, count int, flags byte, files, eligible, total, first int64, groups ...fileInfoGroupNumeric) []byte {
	payload := make([]byte, 36+fileInfoGroupRecordBytes*count)
	payload[0], payload[1], payload[2] = version, byte(count), flags
	binary.BigEndian.PutUint64(payload[4:12], uint64(files))
	binary.BigEndian.PutUint64(payload[12:20], uint64(eligible))
	binary.BigEndian.PutUint64(payload[20:28], uint64(total))
	binary.BigEndian.PutUint64(payload[28:36], uint64(first))
	for i := 0; i < count; i++ {
		g, base := groups[i], 36+fileInfoGroupRecordBytes*i
		binary.BigEndian.PutUint64(payload[base:base+8], uint64(g.ShapeHead))
		binary.BigEndian.PutUint64(payload[base+8:base+16], uint64(g.Head))
		binary.BigEndian.PutUint64(payload[base+16:base+24], uint64(g.Shape))
		binary.BigEndian.PutUint64(payload[base+24:base+32], uint64(g.OfferedMembers))
		binary.BigEndian.PutUint64(payload[base+32:base+40], uint64(g.Members))
		binary.BigEndian.PutUint64(payload[base+40:base+48], uint64(g.SizeBytes))
		binary.BigEndian.PutUint64(payload[base+48:base+56], uint64(g.DurationMS))
		payload[base+56] = g.Type
		binary.BigEndian.PutUint64(payload[base+57:base+65], uint64(g.SizeHigh))
		binary.BigEndian.PutUint64(payload[base+65:base+73], uint64(g.DurationHigh))
		if g.DurationBad {
			payload[base+73] = 1
		}
	}
	return payload
}

func TestFileInfoGroupSummaryValidationAndAdmission(t *testing.T) {
	composite := fileInfoGroupNumeric{ShapeHead: 1, Head: 1, Shape: 1, OfferedMembers: 2, Members: 2, SizeBytes: 10, DurationMS: 20, Type: fileInfoGroupShapeComposite}
	singleton := fileInfoGroupNumeric{ShapeHead: 3, Head: 4, Shape: 3, OfferedMembers: 1, Members: 1, SizeBytes: 5, DurationMS: 6, Type: fileInfoGroupSingleton}
	valid := groupInfoTestPayload(fileInfoGroupWireVersion, 2, 4, 9, 4, 3, 2, composite, singleton)
	summary, err := decodeFileInfoGroupSummary(valid)
	if err != nil || summary.Files != 9 || summary.Eligible != 4 || summary.Total != 3 || summary.FirstOrdinal != 2 || !summary.CanSplit || len(summary.Groups) != 2 || !reflect.DeepEqual(summary.Groups, []fileInfoGroupNumeric{composite, singleton}) {
		t.Fatal("valid summary rejected", summary, err)
	}
	empty, err := decodeFileInfoGroupSummary(groupInfoTestPayload(fileInfoGroupWireVersion, 0, 0, 0, 0, 0, 0))
	if err != nil || empty.FirstOrdinal != 0 || len(empty.Groups) != 0 {
		t.Fatal("valid empty summary rejected", empty, err)
	}
	badHead := composite
	badHead.Head = 2
	badSingleton := singleton
	badSingleton.OfferedMembers = 2
	duplicate := singleton
	duplicate.ShapeHead, duplicate.Head = composite.ShapeHead, composite.Head
	for _, payload := range [][]byte{
		nil,
		make([]byte, 36),
		groupInfoTestPayload(2, 2, 4, 9, 4, 3, 2, composite, singleton),
		groupInfoTestPayload(fileInfoGroupWireVersion, 2, 8, 9, 4, 3, 2, composite, singleton),
		append(groupInfoTestPayload(fileInfoGroupWireVersion, 2, 4, 9, 4, 3, 2, composite, singleton), 0),
		groupInfoTestPayload(fileInfoGroupWireVersion, 1, 0, 9, 4, 9, 10, composite),
		groupInfoTestPayload(fileInfoGroupWireVersion, 2, 0, 3, 4, 3, 1, composite, singleton),
		groupInfoTestPayload(fileInfoGroupWireVersion, 2, 0, 9, 4, 5, 1, composite, singleton),
		groupInfoTestPayload(fileInfoGroupWireVersion, 2, 0, 9, 4, 3, 0, composite, singleton),
		groupInfoTestPayload(fileInfoGroupWireVersion, 2, 0, 9, 4, 3, 3, composite, singleton),
		groupInfoTestPayload(fileInfoGroupWireVersion, 2, 0, 9, 4, 3, 2, badHead, singleton),
		groupInfoTestPayload(fileInfoGroupWireVersion, 2, 0, 9, 4, 3, 2, composite, badSingleton),
		groupInfoTestPayload(fileInfoGroupWireVersion, 2, 0, 9, 4, 3, 2, composite, duplicate),
	} {
		if _, err := decodeFileInfoGroupSummary(payload); err == nil {
			t.Fatal("malformed group summary accepted", payload)
		}
	}
	store, err := readprep.New(readprep.Config{Workers: 1, MaxBytes: 8192})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	source := &groupInfoTestSource{}
	plan, err := fileInfoGroupPlanSource(source, true, true, fileInfoGroupSpec{Limit: 1, Direction: fileInfoForward})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Submit(context.Background(), "caller", readprep.Key{Namespace: "group", Basis: "basis", Canonical: "window"}, plan); !errors.Is(err, readprep.ErrCapacity) || source.reads.Load() != 0 || source.checks.Load() != 0 {
		t.Fatal("group work started before memory admission", err)
	}
}

// TestFileInfoGroupDescriptorSingletonHeadMayDifferFromShapeHead pins the
// clustered-singleton rule: a shape's later offered head is still a valid
// singleton member, so only the membership counts and type make it valid. The
// composite types keep the stricter ShapeHead==Head identity.
func TestFileInfoGroupDescriptorSingletonHeadMayDifferFromShapeHead(t *testing.T) {
	singleton := fileInfoGroupNumeric{
		ShapeHead: 7, Head: 11, Shape: 7,
		OfferedMembers: 1, Members: 1, SizeBytes: 5, DurationMS: 6, Type: fileInfoGroupSingleton,
	}
	if !fileInfoGroupDescriptorOK(singleton) {
		t.Fatalf("clustered singleton head %+v rejected", singleton)
	}
	summary, err := decodeFileInfoGroupSummary(groupInfoTestPayload(fileInfoGroupWireVersion, 1, 0, 1, 1, 1, 1, singleton))
	if err != nil || !reflect.DeepEqual(summary.Groups, []fileInfoGroupNumeric{singleton}) {
		t.Fatalf("clustered singleton summary %+v %v", summary, err)
	}
	badMembership := singleton
	badMembership.OfferedMembers, badMembership.Members = 2, 2
	if fileInfoGroupDescriptorOK(badMembership) {
		t.Fatalf("multi-member singleton %+v accepted", badMembership)
	}
	if _, err := decodeFileInfoGroupSummary(groupInfoTestPayload(fileInfoGroupWireVersion, 1, 0, 2, 2, 1, 1, badMembership)); err == nil {
		t.Fatal("multi-member singleton summary accepted")
	}
	composite := singleton
	composite.OfferedMembers, composite.Members, composite.Type = 2, 2, fileInfoGroupShapeComposite
	if fileInfoGroupDescriptorOK(composite) {
		t.Fatalf("composite with a different group head %+v accepted", composite)
	}
}

// groupInfoTestExact materialises the fixed 128-bit value the reducer keeps as
// two signed words. It is the independent big.Int oracle used by the exact-total
// tests; the reducer itself allocates no big integer.
func groupInfoTestExact(low, high int64) *big.Int {
	exact := new(big.Int).SetInt64(high)
	exact.Lsh(exact, 64)
	return exact.Add(exact, big.NewInt(low))
}

func groupInfoTestBigSum(values ...int64) *big.Int {
	sum := new(big.Int)
	for _, value := range values {
		sum.Add(sum, big.NewInt(value))
	}
	return sum
}

func TestFileInfoGroupFixedSumCarriesExact128BitValue(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []int64
	}{
		{"two-max", []int64{math.MaxInt64, math.MaxInt64}},
		{"negative-underflow", []int64{math.MinInt64, math.MinInt64}},
		{"repeated-positive", []int64{math.MaxInt64, math.MaxInt64, math.MaxInt64}},
		{"mixed-chain", []int64{math.MaxInt64, math.MaxInt64, math.MaxInt64, -1}},
		{"repeated-negative", []int64{math.MinInt64, math.MinInt64, math.MinInt64}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got fileInfoGroupFixedSum
			for _, value := range tc.values {
				got.Add(value)
			}
			want := groupInfoTestBigSum(tc.values...)
			if exact := groupInfoTestExact(got.Low, got.High); exact.Cmp(want) != 0 {
				t.Fatalf("sum %+v = %s want %s", got, exact, want)
			}
		})
	}
	var twoMax fileInfoGroupFixedSum
	twoMax.Add(math.MaxInt64)
	twoMax.Add(math.MaxInt64)
	if twoMax.Low != -2 || twoMax.High != 1 {
		t.Fatalf("two MaxInt64 = %+v want low -2 high 1", twoMax)
	}
}

func TestFileInfoGroupCompositeExactTotalsMatchBigInt(t *testing.T) {
	build := func(sizes []int64, durations []float64) []groupTestFile {
		files := make([]groupTestFile, len(sizes))
		for i := range sizes {
			files[i] = groupTestFile{
				Shape: 1, Part: int64(i), Asset: int64(10 + i),
				Offered: true, Available: true, Size: sizes[i], Duration: durations[i],
				Height: 1080, OrderAvailable: 1, Token: fmt.Sprintf("t%02d", 10+i),
			}
		}
		return files
	}
	for _, tc := range []struct {
		name      string
		sizes     []int64
		durations []float64
	}{
		{"two-max", []int64{math.MaxInt64, math.MaxInt64}, []float64{1, 1}},
		{"negative-underflow", []int64{math.MinInt64, math.MinInt64}, []float64{1, 1}},
		{"repeated-overflow-chain", []int64{math.MaxInt64, math.MaxInt64, math.MaxInt64, -1}, []float64{1, 1, 1, 1}},
		{"duration-sum-overflow", []int64{1, 1}, []float64{9e15, 9e15}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page, err := groupInfoTestRun(t, &groupInfoTestSource{files: build(tc.sizes, tc.durations)}, true, true, fileInfoGroupSpec{Limit: 100, Direction: fileInfoForward})
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Groups) != 1 || page.Groups[0].Type != fileInfoGroupGlobalComposite {
				t.Fatalf("exact fixture groups %+v", page.Groups)
			}
			group := page.Groups[0]
			if got, want := groupInfoTestExact(group.SizeBytes, group.SizeHigh), groupInfoTestBigSum(tc.sizes...); got.Cmp(want) != 0 {
				t.Fatalf("size exact %s want %s (%+v)", got, want, group)
			}
			if group.DurationBad {
				t.Fatalf("in-range durations marked bad: %+v", group)
			}
			wantDuration := new(big.Int)
			for _, seconds := range tc.durations {
				milliseconds, ok := fileInfoGroupMilliseconds(seconds)
				if !ok {
					t.Fatalf("fixture duration %v unexpectedly bad", seconds)
				}
				wantDuration.Add(wantDuration, big.NewInt(milliseconds))
			}
			if got := groupInfoTestExact(group.DurationMS, group.DurationHigh); got.Cmp(wantDuration) != 0 {
				t.Fatalf("duration exact %s want %s (%+v)", got, wantDuration, group)
			}
			switch tc.name {
			case "two-max":
				if group.SizeBytes != -2 || group.SizeHigh != 1 {
					t.Fatalf("two MaxInt64 size words %+v", group)
				}
			case "duration-sum-overflow":
				if group.DurationHigh != 1 || group.DurationMS >= 0 {
					t.Fatalf("duration carry state %+v", group)
				}
			}
		})
	}
}

func TestFileInfoGroupDurationConversionAndBadPropagation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		seconds float64
		ok      bool
	}{
		{"nan", math.NaN(), false},
		{"positive-infinity", math.Inf(1), false},
		{"negative-infinity", math.Inf(-1), false},
		{"large-finite-positive", 9.3e15, false},
		{"large-finite-negative", -9.3e15, false},
		{"in-range-finite", 9e15, true},
		{"zero", 0, true},
		{"truncating", 1.0009, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			milliseconds, ok := fileInfoGroupMilliseconds(tc.seconds)
			if ok != tc.ok {
				t.Fatalf("conversion ok=%v want %v (%v ms)", ok, tc.ok, milliseconds)
			}
			if !ok && milliseconds != 0 {
				t.Fatalf("bad conversion kept a value: %d", milliseconds)
			}
		})
	}

	files := []groupTestFile{
		{Shape: 1, Part: 0, Asset: 10, Offered: true, Available: true, Size: 1, Duration: math.NaN(), Height: 1080, OrderAvailable: 1, Token: "t10"},
		{Shape: 1, Part: 1, Asset: 11, Offered: true, Available: true, Size: 1, Duration: 1, Height: 1080, OrderAvailable: 1, Token: "t11"},
		{Shape: 2, Part: 0, Asset: 20, Offered: true, Available: true, Size: 1, Duration: math.Inf(1), Height: 720, OrderAvailable: 1, Token: "t20"},
		{Shape: 2, Part: 0, Asset: 21, Offered: true, Available: true, Size: 1, Duration: 2, Height: 720, OrderAvailable: 1, Token: "t21"},
	}
	page, err := groupInfoTestRun(t, &groupInfoTestSource{files: files}, true, true, fileInfoGroupSpec{Limit: 100, Direction: fileInfoForward})
	if err != nil {
		t.Fatalf("bad duration failed the basis instead of the duration state: %v", err)
	}
	if len(page.Groups) != 3 {
		t.Fatalf("bad duration groups %+v", page.Groups)
	}
	composite := page.Groups[0]
	if composite.Type != fileInfoGroupShapeComposite || !composite.DurationBad || composite.DurationMS != 1000 || composite.DurationHigh != 0 {
		t.Fatalf("shape composite bad duration state %+v", composite)
	}
	if first := page.Groups[1]; first.Head != 20 || first.Type != fileInfoGroupSingleton || !first.DurationBad || first.DurationMS != 0 {
		t.Fatalf("bad singleton duration state %+v", first)
	}
	if second := page.Groups[2]; second.Head != 21 || second.Type != fileInfoGroupSingleton || second.DurationBad || second.DurationMS != 2000 {
		t.Fatalf("good singleton duration state %+v", second)
	}
}

func TestFileInfoGroupSummaryWireV3RoundTripAndValidation(t *testing.T) {
	group := fileInfoGroupNumeric{
		ShapeHead: 5, Head: 5, Shape: 5, OfferedMembers: 2, Members: 2,
		SizeBytes: -2, SizeHigh: 1, DurationMS: -1, DurationHigh: 2,
		DurationBad: true, Type: fileInfoGroupGlobalComposite,
	}
	payload := groupInfoTestPayload(fileInfoGroupWireVersion, 1, 0, 2, 2, 1, 1, group)
	summary, err := decodeFileInfoGroupSummary(payload)
	if err != nil || len(summary.Groups) != 1 || summary.Groups[0] != group {
		t.Fatalf("v3 round trip %+v %v", summary, err)
	}
	if got := groupInfoTestExact(group.SizeBytes, group.SizeHigh); got.String() != "18446744073709551614" {
		t.Fatalf("negative low misread: %s want 18446744073709551614", got)
	}

	badFlag := append([]byte{}, payload...)
	badFlag[36+73] = 2
	oldVersion := append([]byte{}, payload...)
	oldVersion[0] = 1
	overCount := make([]byte, 36+fileInfoGroupRecordBytes*101)
	overCount[0], overCount[1] = fileInfoGroupWireVersion, 101
	short := payload[:len(payload)-1]
	for _, bad := range [][]byte{badFlag, oldVersion, overCount, short} {
		if _, err := decodeFileInfoGroupSummary(bad); !errors.Is(err, readprep.ErrInvalid) {
			t.Fatalf("malformed v3 frame accepted: %x", bad)
		}
	}
}
