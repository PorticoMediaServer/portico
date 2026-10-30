package linearbuffer

import (
	"bytes"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func ptsPacket(pid int, video bool, pts int64) []byte {
	p := bytes.Repeat([]byte{0xff}, 188)
	p[0] = 0x47
	p[1] = 0x40 | byte(pid>>8)
	p[2] = byte(pid)
	p[3] = 0x10
	h := p[4:]
	h[0] = 0
	h[1] = 0
	h[2] = 1
	h[3] = 0xc0
	if video {
		h[3] = 0xe0
	}
	h[4] = 0
	h[5] = 0
	h[6] = 0x80
	h[7] = 0x80
	h[8] = 5
	v := pts & (ptsWrap - 1)
	h[9] = 0x21 | byte(v>>29)&0x0e
	h[10] = byte(v >> 22)
	h[11] = byte(v>>14)&0xfe | 1
	h[12] = byte(v >> 7)
	h[13] = byte(v<<1) | 1
	return p
}
func ts(start int64) []byte {
	return append(ptsPacket(256, true, start), ptsPacket(256, true, start+356400)...)
}
func testBuffer(t *testing.T, scheduled bool) *Buffer {
	t.Helper()
	parent := canonicalFixtureDir(t)
	if e := os.Chmod(parent, 0700); e != nil {
		t.Fatal(e)
	}
	b, e := New(Options{Parent: parent, OriginMS: time.Now().Add(-time.Hour).UnixMilli(), Retention: 12 * time.Second, MaxBytes: 64 << 20, Scheduled: scheduled})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { b.Close() })
	return b
}
func publisher(t *testing.T, b *Buffer, id int64, baseMS int64) *Publisher {
	t.Helper()
	p, e := b.Publisher(Producer{ID: id, Scheduled: true, TimelineBaseMS: baseMS, EndMS: baseMS + 120000}, func() error { return nil })
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { p.Close() })
	return p
}
func put(p *Publisher, name string, data []byte) int {
	r := httptest.NewRequest("PUT", "/", bytes.NewReader(data))
	r.URL.Path = name
	w := httptest.NewRecorder()
	p.ServeHTTP(w, r)
	return w.Code
}
func publish(t *testing.T, p *Publisher, n int, pts int64) {
	t.Helper()
	name := fmt.Sprintf("segment-%09d.ts", n)
	if s := put(p, name, ts(pts)); s != 201 {
		t.Fatalf("segment rejected %d", s)
	}
	if s := put(p, "index.m3u8", []byte(fmt.Sprintf("#EXTM3U\n#EXTINF:4.000000,\n%s\n", name))); s != 201 {
		t.Fatalf("manifest rejected %d", s)
	}
}
func TestReadPTSUsesPresentationNotAudioOrPacketOrder(t *testing.T) {
	raw := append(ptsPacket(300, false, 100), ptsPacket(256, true, 360000)...)
	raw = append(raw, ptsPacket(256, true, 180000)...)
	raw = append(raw, ptsPacket(256, true, 540000)...)
	c, e := ReadPTS(bytes.NewReader(raw))
	if e != nil || c.Min != 180000 || c.Max != 540000 {
		t.Fatalf("clock=%+v err=%v", c, e)
	}
	wrap := append(ptsPacket(256, true, ptsWrap-90000), ptsPacket(256, true, 90000)...)
	c, e = ReadPTS(bytes.NewReader(wrap))
	if e != nil || c.Max-c.Min != 180000 {
		t.Fatalf("wrap=%+v err=%v", c, e)
	}
	if _, e = ReadPTS(bytes.NewReader(raw[:len(raw)-1])); !errors.Is(e, ErrMedia) {
		t.Fatal("accepted truncated TS")
	}
}
func TestScheduledTimelineUsesSourcePTSAndKeepsDiscontinuities(t *testing.T) {
	b := testBuffer(t, true)
	base := b.options.OriginMS
	p := publisher(t, b, 1, base)
	publish(t, p, 0, 5*90000)
	w := b.Window()
	if w.StartUS != 5e6 || w.EndUS != 9e6 || !w.Ready {
		t.Fatalf("wrong media clock %+v", w)
	}
	q := publisher(t, b, 2, base+12000)
	publish(t, q, 0, 0)
	w = b.Window()
	if len(w.Gaps) != 1 || w.Gaps[0] != (Interval{9e6, 12e6}) {
		t.Fatalf("wrong gap %+v", w)
	}
	gap := int64(10e6)
	if _, e := b.Resolve(&gap); !errors.Is(e, ErrGap) {
		t.Fatalf("gap silently mapped: %v", e)
	}
	before := int64(4e6)
	if _, e := b.Resolve(&before); !errors.Is(e, ErrExpired) {
		t.Fatalf("expired target clamped: %v", e)
	}
	target, e := b.Resolve(nil)
	if e != nil || target != 15900000 {
		t.Fatalf("Go Live: %d %v", target, e)
	}
	body, e := b.Playlist()
	if e != nil || !strings.Contains(string(body), "#EXT-X-DISCONTINUITY\n") || !strings.Contains(string(body), time.UnixMilli(base+5000).UTC().Format(time.RFC3339Nano)) {
		t.Fatalf("manifest: %s (%v)", body, e)
	}
}
func TestExpiredReadersPinFilesUntilActualClose(t *testing.T) {
	b := testBuffer(t, true)
	p := publisher(t, b, 1, b.options.OriginMS)
	publish(t, p, 0, 0)
	r, e := b.Open("s1.ts")
	if e != nil {
		t.Fatal(e)
	}
	path := r.File.Name()
	for n := 1; n < 6; n++ {
		publish(t, p, n, int64(n*4*90000))
	}
	if _, e = b.Open("s1.ts"); !errors.Is(e, ErrExpired) {
		t.Fatalf("old URL remained readable: %v", e)
	}
	if _, e = os.Stat(path); e != nil {
		t.Fatal("pinned reader prematurely deleted", e)
	}
	b.Close()
	if _, e = os.Stat(path); e != nil {
		t.Fatal("Close deleted a pinned file", e)
	}
	r.Close()
	if _, e = os.Stat(b.Directory()); !os.IsNotExist(e) {
		t.Fatalf("directory survives last reader: %v", e)
	}
}
func TestPublisherImmutableAndAuthorityFenced(t *testing.T) {
	b := testBuffer(t, true)
	p := publisher(t, b, 1, b.options.OriginMS)
	publish(t, p, 0, 0)
	if s := put(p, "segment-000000000.ts", ts(0)); s != 201 {
		t.Fatalf("identical retry %d", s)
	}
	if s := put(p, "segment-000000000.ts", ts(90000)); s != 422 {
		t.Fatal("accepted replacement of published object")
	}
	p.check = func() error { return errors.New("revoked") }
	if s := put(p, "segment-000000001.ts", ts(360000)); s != 410 {
		t.Fatal("accepted revoked publisher")
	}
}
func TestManifestRejectsPathsKeysAndUnboundedData(t *testing.T) {
	for _, text := range []string{"#EXTM3U\n#EXTINF:4,\n../segment-000000000.ts\n", "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"http://secret\"\n#EXTINF:4,\nsegment-000000000.ts\n", "#EXTM3U\n#EXTINF:NaN,\nsegment-000000000.ts\n", "#EXTM3U\n#EXTINF:61,\nsegment-000000000.ts\n"} {
		if _, e := parseManifest([]byte(text), false); e == nil {
			t.Fatalf("accepted %q", text)
		}
	}
}
func TestScheduledFutureMediaDoesNotAdvertiseSeekableWindow(t *testing.T) {
	b := testBuffer(t, true)
	p := publisher(t, b, 1, time.Now().Add(time.Minute).UnixMilli())
	publish(t, p, 0, 0)
	if b.Window().Ready {
		t.Fatal("future data advertised as current")
	}
	if _, e := b.Resolve(nil); !errors.Is(e, ErrExpired) {
		t.Fatal(e)
	}
}
func TestBufferConcurrentReadClose(t *testing.T) {
	b := testBuffer(t, true)
	p := publisher(t, b, 1, b.options.OriginMS)
	publish(t, p, 0, 0)
	var wg sync.WaitGroup
	for n := 0; n < 16; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := b.Open("s1.ts")
			if e == nil {
				r.Close()
			}
			b.Window()
			b.Playlist()
		}()
	}
	b.Close()
	wg.Wait()
}

func TestSweepPreservesLiveOwnershipAndRemovesCrashedOwner(t *testing.T) {
	b := testBuffer(t, true)
	parent := b.options.Parent
	if e := Sweep(parent); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(b.Directory()); e != nil {
		t.Fatal("swept live peer", e)
	}
	// A closed ownership descriptor simulates process death, not normal Close.
	b.owner.Close()
	if e := Sweep(parent); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(b.Directory()); !os.IsNotExist(e) {
		t.Fatal("orphan survived", e)
	}
}

func TestManifestCanArriveBeforeItsSegment(t *testing.T) {
	b := testBuffer(t, true)
	p := publisher(t, b, 1, b.options.OriginMS)
	manifest := []byte("#EXTM3U\n#EXTINF:4,\nsegment-000000000.ts\n#EXTINF:4,\nsegment-000000001.ts\n")
	if status := put(p, "index.m3u8", manifest); status != 201 {
		t.Fatal(status)
	}
	if b.Window().Ready {
		t.Fatal("advertised incomplete media")
	}
	if status := put(p, "segment-000000001.ts", ts(360000)); status != 201 {
		t.Fatal(status)
	}
	if b.Window().Ready {
		t.Fatal("skipped missing leading segment")
	}
	if status := put(p, "segment-000000000.ts", ts(0)); status != 201 {
		t.Fatal(status)
	}
	if w := b.Window(); !w.Ready || w.EndUS != 8000000 {
		t.Fatalf("pending manifest not committed: %+v", w)
	}
	if e := p.Result(); e != nil {
		t.Fatal(e)
	}
}

// Produced is what producers have added, not what the window still holds: a
// short retention or a small byte cap must not make a long healthy run look
// like a few seconds of output.
func TestProducedCountsMediaThatRetentionHasAlreadyRemoved(t *testing.T) {
	second := time.Second.Microseconds()
	for name, c := range map[string]struct {
		retention time.Duration
		bytes     int64
	}{
		"short retention": {12 * time.Second, 1 << 20},
		"small byte cap":  {time.Hour, 16 << 20},
	} {
		t.Run(name, func(t *testing.T) {
			origin := time.Now().Add(-time.Hour)
			parent := canonicalFixtureDir(t)
			if e := os.Chmod(parent, 0700); e != nil {
				t.Fatal(e)
			}
			b, e := New(Options{Parent: parent, OriginMS: origin.UnixMilli(), Retention: c.retention, MaxBytes: 64 << 20})
			if e != nil {
				t.Fatal(e)
			}
			defer b.Close()
			at := origin.UnixMicro()
			run := func(producer int64, segments int) {
				for range segments {
					if e := b.append(&segment{producer: producer, startUS: at, endUS: at + 2*second, bytes: c.bytes, path: b.directory + "/absent"}); e != nil {
						t.Fatal(e)
					}
					at += 2 * second
				}
			}
			run(1, 6)
			before := b.Produced()
			if before != 12*time.Second {
				t.Fatalf("an earlier producer's output: %s", before)
			}
			// The next producer starts after a reconnect and runs for ten minutes.
			at += 40 * second
			run(2, 300)
			window := b.Window()
			if held := time.Duration(window.EndUS-window.StartUS) * time.Microsecond; held > time.Minute {
				t.Fatalf("the fixture kept %s; it must evict to prove anything", held)
			}
			if got := b.Produced() - before; got != 10*time.Minute {
				t.Fatalf("ten minutes of output was measured as %s", got)
			}
		})
	}
}

func TestConfiguredSourceBufferDelaysReadyAndGoLive(t *testing.T) {
	b := testBuffer(t, false)
	if e := b.SetLiveDelay(8); e != nil {
		t.Fatal(e)
	}
	base := b.options.OriginMS * 1000
	b.segments = []*segment{{startUS: base, endUS: base + 4_000_000, sequence: 1}}
	b.producedUS = 4_000_000
	if b.Window().Ready {
		t.Fatal("source ready before its configured buffer")
	}
	b.segments = append(b.segments, &segment{startUS: base + 4_000_000, endUS: base + 12_000_000, sequence: 2})
	b.producedUS = 12_000_000
	window := b.Window()
	if !window.Ready || window.LiveUS != 4_000_000 {
		t.Fatalf("%+v", window)
	}
	target, e := b.Resolve(nil)
	if e != nil || target != 2_000_000 {
		t.Fatalf("live target %d %v", target, e)
	}
	playlist, e := b.Playlist()
	if e != nil || !strings.Contains(string(playlist), "#EXT-X-START:TIME-OFFSET=-10.000000") {
		t.Fatalf("%s %v", playlist, e)
	}
}
func TestClocksMapMediaTimeAndPlaylistsStartWhereAsked(t *testing.T) {
	b := testBuffer(t, true)
	base := b.options.OriginMS
	p := publisher(t, b, 1, base)
	publish(t, p, 0, 5*90000)
	publish(t, p, 1, 9*90000)
	q := publisher(t, b, 2, base+20000)
	publish(t, q, 0, 0)
	clocks := b.Clocks()
	// Producer 1's media time is the channel's; producer 2's starts 20 s in.
	// (Retention has already let the first segment go.)
	if len(clocks) != 2 || clocks[0].OffsetUS != 0 || clocks[0].StartUS != 9e6 || clocks[0].EndUS != 13e6 || clocks[1].OffsetUS != 20e6 || clocks[1].StartUS != 20e6 {
		t.Fatalf("%+v", clocks)
	}
	at := int64(21e6)
	list, e := b.PlaylistFrom(&at)
	if e != nil || strings.Contains(string(list), "s2.ts") || !strings.Contains(string(list), "s3.ts") || !strings.Contains(string(list), "#EXT-X-MEDIA-SEQUENCE:3\n") {
		t.Fatalf("%s %v", list, e)
	}
	whole, _ := b.Playlist()
	if !strings.Contains(string(whole), "s2.ts") {
		t.Fatalf("%s", whole)
	}
}
