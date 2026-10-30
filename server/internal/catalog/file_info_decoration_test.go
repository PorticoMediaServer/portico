package catalog

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

type decorationTestStream struct {
	index int64
	kind  string
}

func decorationTestOrderedPayload(total, eligible, first int64, occurrences ...fileInfoOccurrence) []byte {
	count := len(occurrences)
	payload := make([]byte, 28+16*count)
	payload[0], payload[1] = 2, byte(count)
	binary.BigEndian.PutUint64(payload[4:12], uint64(total))
	binary.BigEndian.PutUint64(payload[12:20], uint64(eligible))
	binary.BigEndian.PutUint64(payload[20:28], uint64(first))
	for i, occurrence := range occurrences {
		binary.BigEndian.PutUint64(payload[28+16*i:36+16*i], uint64(occurrence.Item))
		binary.BigEndian.PutUint64(payload[36+16*i:44+16*i], uint64(occurrence.Asset))
	}
	return payload
}

func decorationTestGroupPayload(files, eligible, total, first int64, groups ...fileInfoGroupNumeric) []byte {
	count := len(groups)
	payload := make([]byte, 36+fileInfoGroupRecordBytes*count)
	payload[0], payload[1] = fileInfoGroupWireVersion, byte(count)
	binary.BigEndian.PutUint64(payload[4:12], uint64(files))
	binary.BigEndian.PutUint64(payload[12:20], uint64(eligible))
	binary.BigEndian.PutUint64(payload[20:28], uint64(total))
	binary.BigEndian.PutUint64(payload[28:36], uint64(first))
	for i, group := range groups {
		base := 36 + fileInfoGroupRecordBytes*i
		binary.BigEndian.PutUint64(payload[base:base+8], uint64(group.ShapeHead))
		binary.BigEndian.PutUint64(payload[base+8:base+16], uint64(group.Head))
		binary.BigEndian.PutUint64(payload[base+16:base+24], uint64(group.Shape))
		binary.BigEndian.PutUint64(payload[base+24:base+32], uint64(group.OfferedMembers))
		binary.BigEndian.PutUint64(payload[base+32:base+40], uint64(group.Members))
		binary.BigEndian.PutUint64(payload[base+40:base+48], uint64(group.SizeBytes))
		binary.BigEndian.PutUint64(payload[base+48:base+56], uint64(group.DurationMS))
		payload[base+56] = group.Type
		binary.BigEndian.PutUint64(payload[base+57:base+65], uint64(group.SizeHigh))
		binary.BigEndian.PutUint64(payload[base+65:base+73], uint64(group.DurationHigh))
		if group.DurationBad {
			payload[base+73] = 1
		}
	}
	return payload
}

func decorationTestAccepted(t *testing.T, c *catalogtest.Catalog, asset int64, token string) compactcatalog.InfoAsset {
	t.Helper()
	var out compactcatalog.InfoAsset
	err := dbwork.WithReadSnapshot(context.Background(), c.DB, func(ctx context.Context) error {
		var err error
		out, err = compactcatalog.ReadInfoAsset(ctx, dbwork.Snapshot(ctx), asset, token, false)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func decorationTestRawStats(t *testing.T, c *catalogtest.Catalog, asset int64) (int64, int64) {
	t.Helper()
	var size, modified int64
	if err := c.DB.QueryRow(`SELECT size,modified_ns FROM catalog_assets WHERE id=?`, asset).Scan(&size, &modified); err != nil {
		t.Fatal(err)
	}
	return size, modified
}

func decorationTestMedia(t *testing.T, c *catalogtest.Catalog, item catalogtest.Item, size, modified int64, streams []decorationTestStream, subtitles int) {
	t.Helper()
	c.Exec(`INSERT OR REPLACE INTO asset_stream_facts(asset_id,revision,size,modified_ns,fingerprint) VALUES(?,1,?,?,'decoration-fixture')`, item.Token, size, modified)
	c.Exec(`DELETE FROM asset_streams WHERE asset_id=?`, item.Token)
	for _, stream := range streams {
		c.Exec(`INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced) VALUES(?,?,?,'aac','eng','',2,'stereo',0,0)`, item.Token, stream.index, stream.kind)
	}
	c.Exec(`INSERT OR REPLACE INTO asset_subtitle_facts(asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint) VALUES(?,1,?,?,0,0,'unknown','decoration-fixture')`, item.Token, size, modified)
	c.Exec(`DELETE FROM asset_subtitles WHERE asset_id=?`, item.Token)
	for i := 0; i < subtitles; i++ {
		c.Exec(`INSERT INTO asset_subtitles(id,asset_id,origin,locator,stream_index,format,language,title,is_default,is_forced,reason) VALUES(?,?,'embedded',?,?,'srt','en','',0,0,'')`,
			fmt.Sprintf("%s-sub-%d", item.Token, i), item.Token, fmt.Sprintf("loc-%d", i), i)
	}
}

func decorationTestStreamRowID(t *testing.T, c *catalogtest.Catalog, token string, index int64) int64 {
	t.Helper()
	var rowid int64
	if err := c.DB.QueryRow(`SELECT rowid FROM asset_streams WHERE asset_id=? AND stream_index=?`, token, index).Scan(&rowid); err != nil {
		t.Fatal(err)
	}
	return rowid
}

func decorationTestSubtitleRowID(t *testing.T, c *catalogtest.Catalog, token string, ordinal int) int64 {
	t.Helper()
	var rowid int64
	if err := c.DB.QueryRow(`SELECT rowid FROM asset_subtitles WHERE asset_id=? ORDER BY id LIMIT 1 OFFSET ?`, token, ordinal).Scan(&rowid); err != nil {
		t.Fatal(err)
	}
	return rowid
}

func decorationTestOldDelta(t *testing.T, c *catalogtest.Catalog, asset int64, token string, slot, phase, size, modified int64) int64 {
	t.Helper()
	c.Exec(`INSERT OR REPLACE INTO catalog_info_changes(asset_id,asset_token,slot,phase,expected_epoch,fingerprint,created_ms) VALUES(?,?,?,?,1,zeroblob(32),0)`, asset, token, slot, phase)
	var change int64
	if err := c.DB.QueryRow(`SELECT id FROM catalog_info_changes WHERE asset_id=? AND asset_token=? AND slot=?`, asset, token, slot).Scan(&change); err != nil {
		t.Fatal(err)
	}
	c.Exec(`INSERT INTO catalog_info_scalar_old(change_id,field,kind,old_integer) VALUES(?,2,1,?)`, change, size)
	c.Exec(`INSERT INTO catalog_info_scalar_old(change_id,field,kind,old_integer) VALUES(?,3,1,?)`, change, modified)
	return change
}

type decorationTestComputation struct {
	payload []byte
	bytes   int64
}

func (c *decorationTestComputation) AccountedBytes() int64 { return c.bytes }

func (c *decorationTestComputation) Step(_ context.Context, budget *readprep.Budget) (readprep.StepResult, error) {
	if err := budget.Resize(c.bytes); err != nil {
		return readprep.StepResult{}, err
	}
	payload := c.payload
	c.payload = nil
	return readprep.StepResult{Done: true, Payload: payload}, nil
}

type decorationTestSurface struct {
	rows                    []fileInfoOrderedRow
	index                   int
	sequence                int64
	bytes                   int64
	reads, checks, compares atomic.Int64
}

func (s *decorationTestSurface) AccountedBytes() int64 {
	if s.bytes == 0 {
		return 4096
	}
	return s.bytes
}

func (s *decorationTestSurface) Check(context.Context) error {
	s.checks.Add(1)
	return nil
}

func (s *decorationTestSurface) Next(context.Context) (fileInfoOrderedRow, bool, error) {
	s.reads.Add(1)
	if s.index == len(s.rows) {
		return fileInfoOrderedRow{}, false, nil
	}
	row := s.rows[s.index]
	s.index++
	s.sequence++
	row.Sequence = s.sequence
	return row, true, nil
}

func (s *decorationTestSurface) Compare(_ context.Context, a, b fileInfoOccurrence) (int, error) {
	s.compares.Add(1)
	if a.Item < b.Item {
		return -1, nil
	}
	if a.Item > b.Item {
		return 1, nil
	}
	if a.Asset < b.Asset {
		return -1, nil
	}
	if a.Asset > b.Asset {
		return 1, nil
	}
	return 0, nil
}

func decorationTestPlan(payload []byte, bytes int64) readprep.Plan {
	return readprep.Plan{InitialBytes: bytes, New: func(_ context.Context, budget *readprep.Budget) (readprep.Computation, error) {
		if err := budget.Resize(bytes); err != nil {
			return nil, err
		}
		return &decorationTestComputation{payload: payload, bytes: bytes}, nil
	}}
}

func decorationTestRun(t *testing.T, plan readprep.Plan, maxBytes int64) ([]byte, error) {
	t.Helper()
	store, err := readprep.New(readprep.Config{Workers: 1, MaxBytes: maxBytes})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	handle, err := store.Submit(ctx, "decoration-private", readprep.Key{Namespace: "decoration-test", Basis: "original-opening", Canonical: "window"}, plan)
	if err != nil {
		return nil, err
	}
	defer store.Cancel(ctx, "decoration-private", handle)
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for {
		result, err := store.Poll(ctx, "decoration-private", handle)
		if err != nil {
			return nil, err
		}
		if result.State == readprep.Ready {
			raw := append([]byte(nil), result.Payload...)
			result.Release()
			return raw, nil
		}
		result.Release()
		select {
		case <-timeout.C:
			t.Fatal("decorated preparation did not finish")
		default:
			runtime.Gosched()
		}
	}
}

func decorationTestPayloadRun(t *testing.T, c *catalogtest.Catalog, payload []byte, lane byte, target fileInfoOccurrence, check fileInfoBasisCheck) ([]byte, []byte, []fileInfoDecoration, fileInfoOccurrence) {
	t.Helper()
	plan, err := fileInfoDecoratedPlan(decorationTestPlan(payload, 64<<10), c.DB, lane, target, 64, check)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := decorationTestRun(t, plan, 512<<10)
	if err != nil {
		t.Fatal(err)
	}
	base, decorations, winner, err := decodeFileInfoDecoratedSummary(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(base, payload) {
		t.Fatalf("numeric payload changed:\n got %x\nwant %x", base, payload)
	}
	return raw, base, decorations, winner
}

func decorationTestSurfacePayloadRun(t *testing.T, c *catalogtest.Catalog, payload []byte, lane byte, target fileInfoOccurrence, options fileInfoDecorationOptions) ([]byte, []byte, []fileInfoDecoration, fileInfoOccurrence, int64) {
	t.Helper()
	plan, err := fileInfoDecoratedSurfacePlan(decorationTestPlan(payload, 64<<10), c.DB, lane, target, 64, decorationTestNoopCheck, options)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := decorationTestRun(t, plan, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	base, decorations, winner, err := decodeFileInfoDecoratedSummary(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(base, payload) {
		t.Fatalf("numeric payload changed:\n got %x\nwant %x", base, payload)
	}
	return raw, base, decorations, winner, plan.InitialBytes
}

func decorationTestNoopCheck(context.Context) error { return nil }

func TestFileInfoDecoratedPlanOrderedLanesKeepExactOccurrences(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("decoration", "Decoration", "movie", "/decoration")
	first := c.Movie(library, "/decoration/first.mkv", "First", 2026)
	second := c.Movie(library, "/decoration/second.mkv", "Second", 2026)
	c.Drain()
	firstSize, firstModified := decorationTestRawStats(t, c, first.Asset)
	secondSize, secondModified := decorationTestRawStats(t, c, second.Asset)
	// The subtitle-type row between the two audio rows is one raw stream event;
	// the audio decision follows LIMIT, so both audio rows still count.
	decorationTestMedia(t, c, first, firstSize, firstModified, []decorationTestStream{
		{index: 0, kind: "video"},
		{index: 1, kind: "audio"},
		{index: 2, kind: "subtitle"},
		{index: 3, kind: "audio"},
	}, 3)
	decorationTestMedia(t, c, second, secondSize, secondModified, []decorationTestStream{
		{index: 5, kind: "video"},
		{index: 8, kind: "audio"},
	}, 2)
	firstVideo := decorationTestStreamRowID(t, c, first.Token, 0)
	secondVideo := decorationTestStreamRowID(t, c, second.Token, 5)
	payload := decorationTestOrderedPayload(2, 2, 1,
		fileInfoOccurrence{Item: first.ID, Asset: first.Asset},
		fileInfoOccurrence{Item: second.ID, Asset: second.Asset},
	)
	for _, lane := range []byte{fileInfoLaneFiles, fileInfoLaneSources, fileInfoLaneMembers} {
		t.Run(fmt.Sprint(lane), func(t *testing.T) {
			_, base, decorations, _ := decorationTestPayloadRun(t, c, payload, lane, fileInfoOccurrence{Item: first.ID}, decorationTestNoopCheck)
			summary, err := decodeFileInfoOrderedSummary(base)
			if err != nil || len(summary.Occurrences) != 2 {
				t.Fatalf("base summary %+v: %v", summary, err)
			}
			want := []fileInfoDecoration{
				{Occurrence: fileInfoOccurrence{Item: first.ID, Asset: first.Asset}, Audio: 2, Subtitles: 3, VideoRow: firstVideo},
				{Occurrence: fileInfoOccurrence{Item: second.ID, Asset: second.Asset}, Audio: 1, Subtitles: 2, VideoRow: secondVideo},
			}
			if !reflect.DeepEqual(decorations, want) {
				t.Fatalf("decorations %+v want %+v", decorations, want)
			}
		})
	}
}

func TestFileInfoDecoratedPlanGroupsUseRootAndGroupHead(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("decoration-groups", "Decoration Groups", "movie", "/decoration-groups")
	movie := c.Movie(library, "/decoration-groups/film.mkv", "Film", 2026)
	c.Drain()
	size, modified := decorationTestRawStats(t, c, movie.Asset)
	decorationTestMedia(t, c, movie, size, modified, []decorationTestStream{
		{index: 0, kind: "video"},
		{index: 1, kind: "audio"},
		{index: 4, kind: "audio"},
	}, 2)
	video := decorationTestStreamRowID(t, c, movie.Token, 0)
	group := fileInfoGroupNumeric{
		ShapeHead: movie.Asset, Head: movie.Asset, Shape: 7,
		OfferedMembers: 1, Members: 1, Type: fileInfoGroupSingleton,
	}
	payload := decorationTestGroupPayload(1, 1, 1, 1, group)
	// The root item and the head asset are deliberately different numbers. A
	// wrapper that used ShapeHead as the occurrence item would fail here.
	_, base, decorations, _ := decorationTestPayloadRun(t, c, payload, fileInfoLaneGroups, fileInfoOccurrence{Item: movie.ID}, decorationTestNoopCheck)
	summary, err := decodeFileInfoGroupSummary(base)
	if err != nil || len(summary.Groups) != 1 {
		t.Fatalf("base groups %+v: %v", summary, err)
	}
	want := []fileInfoDecoration{{Occurrence: fileInfoOccurrence{Item: movie.ID, Asset: movie.Asset}, Audio: 2, Subtitles: 2, VideoRow: video}}
	if !reflect.DeepEqual(decorations, want) {
		t.Fatalf("group decorations %+v want %+v", decorations, want)
	}
}

func TestFileInfoDecoratedPlanUsesAcceptedOLDSlotOrder(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("decoration-old", "Decoration Old", "movie", "/decoration-old")
	movie := c.Movie(library, "/decoration-old/film.mkv", "Film", 2026)
	c.Drain()
	active := decorationTestOldDelta(t, c, movie.Asset, movie.Token, 0, 1, 2000, 2)
	queued := decorationTestOldDelta(t, c, movie.Asset, movie.Token, 1, 4, 3000, 3)
	payload := decorationTestOrderedPayload(1, 1, 1, fileInfoOccurrence{Item: movie.ID, Asset: movie.Asset})
	for _, tc := range []struct {
		name         string
		activePhase  int64
		queuedPhase  int64
		wantSize     int64
		wantModified int64
	}{
		{"active-slot0-prefence", 1, 4, 2000, 2},
		{"queued-slot1-after-activation", 4, 1, 3000, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c.Exec(`UPDATE catalog_info_changes SET phase=? WHERE id=?`, tc.activePhase, active)
			c.Exec(`UPDATE catalog_info_changes SET phase=? WHERE id=?`, tc.queuedPhase, queued)
			accepted := decorationTestAccepted(t, c, movie.Asset, movie.Token)
			if accepted.Size != tc.wantSize || accepted.ModifiedNS != tc.wantModified {
				t.Fatalf("accepted frame %+v want size%d modified%d", accepted, tc.wantSize, tc.wantModified)
			}
			decorationTestMedia(t, c, movie, accepted.Size, accepted.ModifiedNS, []decorationTestStream{
				{index: 0, kind: "video"},
				{index: 1, kind: "audio"},
			}, 2)
			_, _, decorations, _ := decorationTestPayloadRun(t, c, payload, fileInfoLaneFiles, fileInfoOccurrence{Item: movie.ID}, decorationTestNoopCheck)
			if len(decorations) != 1 || decorations[0].Audio != 1 || decorations[0].Subtitles != 2 || decorations[0].VideoRow != decorationTestStreamRowID(t, c, movie.Token, 0) {
				t.Fatalf("accepted OLD frame was not used: %+v", decorations)
			}
		})
	}
}

func TestFileInfoDecoratedPlanParentMismatchZerosLanes(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("decoration-mismatch", "Decoration Mismatch", "movie", "/decoration-mismatch")
	movie := c.Movie(library, "/decoration-mismatch/film.mkv", "Film", 2026)
	c.Drain()
	size, modified := decorationTestRawStats(t, c, movie.Asset)
	decorationTestMedia(t, c, movie, size, modified, []decorationTestStream{
		{index: 0, kind: "video"},
		{index: 1, kind: "audio"},
	}, 2)
	payload := decorationTestOrderedPayload(1, 1, 1, fileInfoOccurrence{Item: movie.ID, Asset: movie.Asset})

	// Each lane is independently truthful: a mismatched stream parent zeroes the
	// audio/video pair while a matching subtitle parent still counts subtitles.
	c.Exec(`UPDATE asset_stream_facts SET size=size+1 WHERE asset_id=?`, movie.Token)
	_, _, decorations, _ := decorationTestPayloadRun(t, c, payload, fileInfoLaneFiles, fileInfoOccurrence{Item: movie.ID}, decorationTestNoopCheck)
	if len(decorations) != 1 || decorations[0].Audio != 0 || decorations[0].VideoRow != 0 || decorations[0].Subtitles != 2 {
		t.Fatalf("stream parent mismatch %+v", decorations)
	}

	// No technical parent row at all is the same truthful zero.
	c.Exec(`DELETE FROM asset_stream_facts WHERE asset_id=?`, movie.Token)
	c.Exec(`DELETE FROM asset_subtitle_facts WHERE asset_id=?`, movie.Token)
	_, _, decorations, _ = decorationTestPayloadRun(t, c, payload, fileInfoLaneFiles, fileInfoOccurrence{Item: movie.ID}, decorationTestNoopCheck)
	if len(decorations) != 1 || decorations[0].Audio != 0 || decorations[0].VideoRow != 0 || decorations[0].Subtitles != 0 {
		t.Fatalf("missing parent rows %+v", decorations)
	}

	// A materializing slot-0 change is the exact state ReadInfoAsset refuses.
	// Decoration must not guess a technical lane from the raw asset row.
	decorationTestMedia(t, c, movie, size, modified, []decorationTestStream{{index: 0, kind: "video"}, {index: 1, kind: "audio"}}, 2)
	c.Exec(`INSERT OR REPLACE INTO catalog_info_changes(asset_id,asset_token,slot,phase,expected_epoch,fingerprint,created_ms) VALUES(?,?,0,2,1,zeroblob(32),0)`, movie.Asset, movie.Token)
	_, _, decorations, _ = decorationTestPayloadRun(t, c, payload, fileInfoLaneFiles, fileInfoOccurrence{Item: movie.ID}, decorationTestNoopCheck)
	if len(decorations) != 1 || decorations[0].Audio != 0 || decorations[0].VideoRow != 0 || decorations[0].Subtitles != 0 {
		t.Fatalf("materializing parent %+v", decorations)
	}
}

func TestFileInfoDecoratedPlanWrapsProductionOrderedPlan(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("decoration-production", "Decoration Production", "movie", "/decoration-production")
	movie := c.Movie(library, "/decoration-production/film.mkv", "Film", 2026)
	c.Drain()
	size, modified := decorationTestRawStats(t, c, movie.Asset)
	decorationTestMedia(t, c, movie, size, modified, []decorationTestStream{
		{index: 3, kind: "audio"},
		{index: 7, kind: "video"},
	}, 1)
	source, err := newFileInfoFlatSource(c.DB, movie.ID, false, 64, decorationTestNoopCheck, nil)
	if err != nil {
		t.Fatal(err)
	}
	numeric, err := fileInfoOrderedPlan(source, fileInfoOrderedSpec{Limit: 10, Direction: fileInfoForward})
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := fileInfoDecoratedPlan(numeric, c.DB, fileInfoLaneFiles, fileInfoOccurrence{Item: movie.ID}, 64, decorationTestNoopCheck)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := decorationTestRun(t, wrapped, 512<<10)
	if err != nil {
		t.Fatal(err)
	}
	base, decorations, _, err := decodeFileInfoDecoratedSummary(raw)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := decodeFileInfoOrderedSummary(base)
	if err != nil || len(summary.Occurrences) != 1 || summary.Occurrences[0] != (fileInfoOccurrence{Item: movie.ID, Asset: movie.Asset}) {
		t.Fatalf("production base %+v: %v", summary, err)
	}
	want := fileInfoDecoration{
		Occurrence: fileInfoOccurrence{Item: movie.ID, Asset: movie.Asset},
		Audio:      1, Subtitles: 1, VideoRow: decorationTestStreamRowID(t, c, movie.Token, 7),
	}
	if len(decorations) != 1 || decorations[0] != want {
		t.Fatalf("production decorations %+v want %+v", decorations, want)
	}
}

func TestFileInfoDecoratedSurfacePlanWalksWholeSurfaceAndKeepsWinner(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("decoration-surface", "Decoration Surface", "movie", "/decoration-surface")
	movie := c.Movie(library, "/decoration-surface/film.mkv", "Film", 2026)
	c.Drain()
	size, modified := decorationTestRawStats(t, c, movie.Asset)
	decorationTestMedia(t, c, movie, size, modified, []decorationTestStream{{index: 0, kind: "video"}, {index: 1, kind: "audio"}}, 1)
	resident := fileInfoOccurrence{Item: movie.ID, Asset: movie.Asset}
	payload := decorationTestOrderedPayload(1, 1, 1, resident)
	surface := &decorationTestSurface{bytes: 4096, rows: []fileInfoOrderedRow{
		{Occurrence: fileInfoOccurrence{Item: movie.ID, Asset: 40}, Match: true},
		{Occurrence: fileInfoOccurrence{Item: movie.ID, Asset: 30}, Match: true},
		{Occurrence: fileInfoOccurrence{Item: movie.ID, Asset: 10}, Match: false},
		{Occurrence: fileInfoOccurrence{Item: movie.ID, Asset: 50}, Match: true},
	}}
	var discCalls atomic.Int64
	disc := func(_ context.Context, occurrence fileInfoOccurrence) (bool, error) {
		discCalls.Add(1)
		return occurrence.Asset == 30 || occurrence.Asset == 40, nil
	}
	_, _, decorations, winner, initial := decorationTestSurfacePayloadRun(t, c, payload, fileInfoLaneFiles, fileInfoOccurrence{Item: movie.ID}, fileInfoDecorationOptions{Surface: surface, DiscCheck: disc})
	wantInitial := int64(64<<10) + fileInfoDecorationFixedBytes + 64 + 4096
	if initial != wantInitial {
		t.Fatalf("initial bytes %d want %d", initial, wantInitial)
	}
	if winner != (fileInfoOccurrence{Item: movie.ID, Asset: 30}) {
		t.Fatalf("winner %+v, want the whole-surface minimum not the resident page", winner)
	}
	if len(decorations) != 1 || decorations[0].Occurrence != resident {
		t.Fatalf("decorations %+v must stay the resident occurrence", decorations)
	}
	if discCalls.Load() != 3 {
		t.Fatalf("discCheck calls %d, want one per matched surface occurrence", discCalls.Load())
	}
	if surface.compares.Load() != 1 {
		t.Fatalf("surface compares %d, want one candidate comparison", surface.compares.Load())
	}
	if surface.reads.Load() < 5 || surface.checks.Load() < 2 {
		t.Fatalf("surface reads/checks %d/%d", surface.reads.Load(), surface.checks.Load())
	}
}

func TestFileInfoDecoratedSurfacePlanSkipsForNonOwner(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("decoration-nonowner", "Decoration NonOwner", "movie", "/decoration-nonowner")
	movie := c.Movie(library, "/decoration-nonowner/film.mkv", "Film", 2026)
	c.Drain()
	size, modified := decorationTestRawStats(t, c, movie.Asset)
	decorationTestMedia(t, c, movie, size, modified, []decorationTestStream{{index: 0, kind: "audio"}}, 0)
	resident := fileInfoOccurrence{Item: movie.ID, Asset: movie.Asset}
	payload := decorationTestOrderedPayload(1, 1, 1, resident)
	surface := &decorationTestSurface{rows: []fileInfoOrderedRow{{Occurrence: resident, Match: true}}}
	_, _, decorations, winner, initial := decorationTestSurfacePayloadRun(t, c, payload, fileInfoLaneFiles, fileInfoOccurrence{Item: movie.ID}, fileInfoDecorationOptions{})
	if winner != (fileInfoOccurrence{}) {
		t.Fatalf("non-owner winner %+v", winner)
	}
	if len(decorations) != 1 || decorations[0].Occurrence != resident {
		t.Fatalf("non-owner decorations %+v", decorations)
	}
	if surface.reads.Load() != 0 || surface.checks.Load() != 0 {
		t.Fatal("non-owner surface was walked")
	}
	if want := int64(64<<10) + fileInfoDecorationFixedBytes + 64; initial != want {
		t.Fatalf("non-owner initial bytes %d want %d", initial, want)
	}
}

func TestFileInfoDecoratedSurfacePlanChargesSurfaceBeforeWork(t *testing.T) {
	c := catalogtest.Open(t)
	payload := decorationTestOrderedPayload(1, 1, 1, fileInfoOccurrence{Item: 1, Asset: 2})
	surface := &decorationTestSurface{bytes: 1 << 20}
	plan, err := fileInfoDecoratedSurfacePlan(decorationTestPlan(payload, 64<<10), c.DB, fileInfoLaneFiles, fileInfoOccurrence{Item: 1}, 64, decorationTestNoopCheck,
		fileInfoDecorationOptions{Surface: surface, DiscCheck: func(context.Context, fileInfoOccurrence) (bool, error) { return false, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(64<<10) + fileInfoDecorationFixedBytes + 64 + (1 << 20); plan.InitialBytes != want {
		t.Fatalf("initial bytes %d want %d", plan.InitialBytes, want)
	}
	store, err := readprep.New(readprep.Config{Workers: 1, MaxBytes: 512 << 10})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, err = store.Submit(context.Background(), "decoration-private", readprep.Key{Namespace: "decoration-test", Basis: "original-opening", Canonical: "window"}, plan)
	if !errors.Is(err, readprep.ErrCapacity) {
		t.Fatalf("got %v want capacity", err)
	}
	if surface.reads.Load() != 0 || surface.checks.Load() != 0 {
		t.Fatal("surface work ran before its whole charge was admitted")
	}
}

func TestFileInfoDecoratedSurfacePlanUsesFreshFlatSurface(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("decoration-flat", "Decoration Flat", "movie", "/decoration-flat")
	movie := c.Movie(library, "/decoration-flat/film.mkv", "Film", 2026)
	c.Drain()
	secondAsset, _ := c.File(movie.ID, "/decoration-flat/second.mkv", 5400)
	c.Drain()
	c.Exec(`UPDATE catalog_asset_links SET available=1 WHERE entity_id=?`, movie.ID)
	c.Exec(`UPDATE catalog_assets SET info_order_available=1 WHERE id IN (?,?)`, movie.Asset, secondAsset)
	size, modified := decorationTestRawStats(t, c, movie.Asset)
	decorationTestMedia(t, c, movie, size, modified, []decorationTestStream{{index: 0, kind: "audio"}}, 0)
	resident := fileInfoOccurrence{Item: movie.ID, Asset: movie.Asset}
	second := fileInfoOccurrence{Item: movie.ID, Asset: secondAsset}
	payload := decorationTestOrderedPayload(1, 1, 1, resident)
	surface, err := newFileInfoFlatSource(c.DB, movie.ID, false, 64, decorationTestNoopCheck, nil)
	if err != nil {
		t.Fatal(err)
	}
	cmp, err := surface.Compare(context.Background(), resident, second)
	if err != nil {
		t.Fatal(err)
	}
	want := resident
	if cmp > 0 {
		want = second
	}
	var discCalls atomic.Int64
	disc := func(context.Context, fileInfoOccurrence) (bool, error) {
		discCalls.Add(1)
		return true, nil
	}
	_, _, decorations, winner, _ := decorationTestSurfacePayloadRun(t, c, payload, fileInfoLaneFiles, fileInfoOccurrence{Item: movie.ID}, fileInfoDecorationOptions{Surface: surface, DiscCheck: disc})
	if winner != want {
		t.Fatalf("flat surface winner %+v want %+v", winner, want)
	}
	if discCalls.Load() != 2 {
		t.Fatalf("flat surface disc checks %d, want every matched file", discCalls.Load())
	}
	if len(decorations) != 1 || decorations[0].Occurrence != resident {
		t.Fatalf("flat surface decorations %+v", decorations)
	}
}

func TestFileInfoDecoratedSurfacePlanRequiresBothOrNeither(t *testing.T) {
	c := catalogtest.Open(t)
	payload := decorationTestOrderedPayload(1, 1, 1, fileInfoOccurrence{Item: 1, Asset: 2})
	disc := func(context.Context, fileInfoOccurrence) (bool, error) { return false, nil }
	base := decorationTestPlan(payload, 64<<10)
	for _, tc := range []struct {
		name    string
		options fileInfoDecorationOptions
		wantErr bool
	}{
		{"surface only", fileInfoDecorationOptions{Surface: &decorationTestSurface{}}, true},
		{"disc only", fileInfoDecorationOptions{DiscCheck: disc}, true},
		{"negative surface bytes", fileInfoDecorationOptions{Surface: &decorationTestSurface{bytes: -1}, DiscCheck: disc}, true},
		{"both", fileInfoDecorationOptions{Surface: &decorationTestSurface{}, DiscCheck: disc}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := fileInfoDecoratedSurfacePlan(base, c.DB, fileInfoLaneFiles, fileInfoOccurrence{Item: 1}, 0, decorationTestNoopCheck, tc.options)
			if tc.wantErr {
				if !errors.Is(err, readprep.ErrInvalid) {
					t.Fatalf("got %v want invalid", err)
				}
				return
			}
			if err != nil || plan.New == nil {
				t.Fatalf("valid options rejected: %v", err)
			}
		})
	}
}

func TestFileInfoDecoratedPlanRejectsInvalidConstruction(t *testing.T) {
	c := catalogtest.Open(t)
	payload := decorationTestOrderedPayload(1, 1, 1, fileInfoOccurrence{Item: 1, Asset: 2})
	valid := decorationTestPlan(payload, 64<<10)
	noNew := valid
	noNew.New = nil
	negative := valid
	negative.InitialBytes = -1
	for _, tc := range []struct {
		name    string
		plan    readprep.Plan
		db      *sql.DB
		lane    byte
		target  fileInfoOccurrence
		capture int64
		check   fileInfoBasisCheck
	}{
		{"no underlying factory", noNew, c.DB, fileInfoLaneFiles, fileInfoOccurrence{Item: 1}, 0, decorationTestNoopCheck},
		{"negative underlying bytes", negative, c.DB, fileInfoLaneFiles, fileInfoOccurrence{Item: 1}, 0, decorationTestNoopCheck},
		{"no database", valid, nil, fileInfoLaneFiles, fileInfoOccurrence{Item: 1}, 0, decorationTestNoopCheck},
		{"no check", valid, c.DB, fileInfoLaneFiles, fileInfoOccurrence{Item: 1}, 0, nil},
		{"negative capture", valid, c.DB, fileInfoLaneFiles, fileInfoOccurrence{Item: 1}, -1, decorationTestNoopCheck},
		{"audio lane", valid, c.DB, fileInfoLaneAudio, fileInfoOccurrence{Item: 1, Asset: 2}, 0, decorationTestNoopCheck},
		{"disc lane", valid, c.DB, fileInfoLaneDisc, fileInfoOccurrence{Item: 1, Asset: 2}, 0, decorationTestNoopCheck},
		{"zero target item", valid, c.DB, fileInfoLaneFiles, fileInfoOccurrence{}, 0, decorationTestNoopCheck},
		{"negative target asset", valid, c.DB, fileInfoLaneFiles, fileInfoOccurrence{Item: 1, Asset: -1}, 0, decorationTestNoopCheck},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := fileInfoDecoratedPlan(tc.plan, tc.db, tc.lane, tc.target, tc.capture, tc.check); !errors.Is(err, readprep.ErrInvalid) {
				t.Fatalf("got %v want invalid", err)
			}
		})
	}
}

func TestFileInfoDecoratedPlanAdmitsWholeChargeBeforeWork(t *testing.T) {
	c := catalogtest.Open(t)
	payload := decorationTestOrderedPayload(1, 1, 1, fileInfoOccurrence{Item: 1, Asset: 2})
	var made atomic.Int64
	base := readprep.Plan{InitialBytes: 64 << 10, New: func(context.Context, *readprep.Budget) (readprep.Computation, error) {
		made.Add(1)
		return &decorationTestComputation{payload: payload, bytes: 64 << 10}, nil
	}}
	plan, err := fileInfoDecoratedPlan(base, c.DB, fileInfoLaneFiles, fileInfoOccurrence{Item: 1}, 64, decorationTestNoopCheck)
	if err != nil {
		t.Fatal(err)
	}
	store, err := readprep.New(readprep.Config{Workers: 1, MaxBytes: 48 << 10})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, err = store.Submit(context.Background(), "decoration-private", readprep.Key{Namespace: "decoration-test", Basis: "original-opening", Canonical: "window"}, plan)
	if !errors.Is(err, readprep.ErrCapacity) {
		t.Fatalf("got %v want capacity", err)
	}
	if made.Load() != 0 {
		t.Fatal("underlying factory ran before the whole wrapper charge was admitted")
	}
}

func TestFileInfoDecoratedPlanChecksBeforeAndAfterEveryQuantum(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("decoration-check", "Decoration Check", "movie", "/decoration-check")
	movie := c.Movie(library, "/decoration-check/film.mkv", "Film", 2026)
	c.Drain()
	size, modified := decorationTestRawStats(t, c, movie.Asset)
	decorationTestMedia(t, c, movie, size, modified, []decorationTestStream{{index: 0, kind: "audio"}}, 0)
	payload := decorationTestOrderedPayload(1, 1, 1, fileInfoOccurrence{Item: movie.ID, Asset: movie.Asset})
	var checks atomic.Int64
	check := func(context.Context) error { checks.Add(1); return nil }
	decorationTestPayloadRun(t, c, payload, fileInfoLaneFiles, fileInfoOccurrence{Item: movie.ID}, check)
	if checks.Load() < 4 {
		t.Fatalf("check called %d times, want before/after factory and quantum", checks.Load())
	}
}

func TestFileInfoDecoratedSummaryDecode(t *testing.T) {
	base := decorationTestOrderedPayload(1, 1, 1, fileInfoOccurrence{Item: 7, Asset: 9})
	raw := append(append([]byte{}, base...), 1)
	winner := make([]byte, 16)
	binary.BigEndian.PutUint64(winner[0:8], 5)
	binary.BigEndian.PutUint64(winner[8:16], 6)
	raw = append(raw, winner...)
	record := make([]byte, fileInfoDecorationRecordBytes)
	binary.BigEndian.PutUint64(record[0:8], 7)
	binary.BigEndian.PutUint64(record[8:16], 9)
	binary.BigEndian.PutUint64(record[16:24], 2)
	binary.BigEndian.PutUint64(record[24:32], 3)
	binary.BigEndian.PutUint64(record[32:40], 4)
	raw = append(raw, record...)
	gotBase, decorations, gotWinner, err := decodeFileInfoDecoratedSummary(raw)
	if err != nil || !bytes.Equal(gotBase, base) || gotWinner != (fileInfoOccurrence{Item: 5, Asset: 6}) || !reflect.DeepEqual(decorations, []fileInfoDecoration{{Occurrence: fileInfoOccurrence{Item: 7, Asset: 9}, Audio: 2, Subtitles: 3, VideoRow: 4}}) {
		t.Fatalf("decode %x %+v %+v %v", gotBase, decorations, gotWinner, err)
	}
	groupBase := decorationTestGroupPayload(0, 0, 0, 0)
	groupRaw := append(append(append([]byte{}, groupBase...), 0), make([]byte, 16)...)
	gotBase, decorations, gotWinner, err = decodeFileInfoDecoratedSummary(groupRaw)
	if err != nil || !bytes.Equal(gotBase, groupBase) || len(decorations) != 0 || gotWinner != (fileInfoOccurrence{}) {
		t.Fatalf("empty group decode %x %+v %+v %v", gotBase, decorations, gotWinner, err)
	}

	badNegative := append([]byte{}, raw...)
	badNegative[len(badNegative)-fileInfoDecorationRecordBytes+16] = 0x80
	badWinner := append([]byte{}, raw...)
	badWinner[len(base)+1] = 0x80
	badHalfWinner := append([]byte{}, raw...)
	binary.BigEndian.PutUint64(badHalfWinner[len(base)+9:len(base)+17], 0)
	oldGroup := append([]byte{}, groupBase...)
	oldGroup[0] = 1
	oldGroupRaw := append(append(oldGroup, 0), make([]byte, 16)...)
	for _, bad := range [][]byte{
		nil,
		{2},
		{2, 1},
		{3, 0},
		raw[:len(raw)-1],
		append(append([]byte{}, raw...), 0),
		badNegative,
		badWinner,
		badHalfWinner,
		oldGroupRaw,
	} {
		if _, _, _, err := decodeFileInfoDecoratedSummary(bad); !errors.Is(err, readprep.ErrInvalid) {
			t.Fatalf("malformed %x: %v", bad, err)
		}
	}
}

func TestFileInfoDecoratedPlanQueriesSeekRawRowsWithoutSort(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("decoration-plan", "Decoration Plan", "movie", "/decoration-plan")
	movie := c.Movie(library, "/decoration-plan/film.mkv", "Film", 2026)
	c.Drain()
	size, modified := decorationTestRawStats(t, c, movie.Asset)
	decorationTestMedia(t, c, movie, size, modified, []decorationTestStream{
		{index: 0, kind: "video"},
		{index: 1, kind: "audio"},
		{index: 3, kind: "audio"},
	}, 1)
	subtitle := decorationTestSubtitleRowID(t, c, movie.Token, 0)
	for _, tc := range []struct {
		name  string
		query string
		args  []any
		want  []string
	}{
		{"parent", fileInfoDecorationParentQuery, []any{movie.Asset}, []string{"SEARCH a USING INTEGER PRIMARY KEY", "catalog_info_changes", "asset_stream_facts", "asset_subtitle_facts"}},
		{"stream-first", fileInfoDecorationStreamFirst, []any{movie.Asset}, []string{"SEARCH s USING", "asset_streams", "asset_id=?"}},
		{"stream-seek", fileInfoDecorationStreamSeek, []any{movie.Asset, int64(1)}, []string{"SEARCH s USING", "asset_streams", "stream_index>?"}},
		{"subtitle-first", fileInfoDecorationSubtitleFirst, []any{movie.Asset}, []string{"SEARCH s USING", "asset_subtitles_asset", "asset_id=?"}},
		{"subtitle-seek", fileInfoDecorationSubtitleSeek, []any{subtitle, movie.Asset, movie.Asset, subtitle, movie.Asset}, []string{"asset_subtitles_asset", "id>?"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			details := decorationTestPlanDetails(t, c.DB, tc.query, tc.args...)
			if strings.Contains(details, "TEMP") {
				t.Fatalf("temporary sort/materialization:\n%s", details)
			}
			for _, line := range strings.Split(details, "\n") {
				if strings.Contains(line, "SCAN ") && !strings.Contains(line, "SCAN CONSTANT ROW") {
					t.Fatalf("unbounded scan:\n%s", details)
				}
			}
			for _, want := range tc.want {
				if !strings.Contains(details, want) {
					t.Fatalf("plan missing %q:\n%s", want, details)
				}
			}
		})
	}
}

func decorationTestPlanDetails(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	rows, err := db.Query(`EXPLAIN QUERY PLAN `+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	details := []string{}
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(details, "\n")
}
