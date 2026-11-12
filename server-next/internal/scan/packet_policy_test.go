package scan_test

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
	"github.com/PorticoMediaServer/portico/server-next/internal/remote"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

type failingPacketProber struct {
	failure error
	calls   int
}

func (p *failingPacketProber) Probe(ctx context.Context, path string) (scan.Facts, error) {
	p.calls++
	found := mediafacts.Facts{Container: "wav", AudioCodec: "pcm_s16le", Duration: 12.5,
		Streams: []mediafacts.Stream{{Type: "audio", Codec: "pcm_s16le"}}}
	probe := mediafacts.Probe{ScanCommand: func(context.Context, string, []string, string, mediafacts.ScanMode, io.Writer) (bool, error) {
		return true, p.failure
	}}
	err := probe.CompleteAudioPackets(ctx, path, &found)
	return scan.FromMedia(found), err
}

// Optional failure must neither skip a book nor leave it unread and retry every scan.
func TestFailedPacketMeasurementIsStoredWithoutSkippingOrRetry(t *testing.T) {
	for name, failure := range map[string]error{"exit": &exec.ExitError{}, "deadline": context.DeadlineExceeded, "read": errors.New("read failed")} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, db.LibraryAudiobooks)
			f.write("A Book.wav")
			p := &failingPacketProber{failure: failure}
			f.s.Prober = p
			first := f.scan()
			if first.Probed != 1 || first.Unread != 0 {
				t.Fatalf("readable book not stored: %+v", first)
			}
			page, err := f.d.Items(ctx, f.viewer, db.ItemQuery{Library: f.lib.PublicID, Kinds: []db.ItemKind{db.KindAudiobook}, Limit: 10})
			if err != nil || len(page.Items) != 1 {
				t.Fatalf("book disappeared: %+v, %v", page, err)
			}
			item, err := f.d.Item(ctx, f.viewer, page.Items[0].PublicID)
			if err != nil || !item.Available || item.Versions[0].DurationMS != 12500 {
				t.Fatalf("ordinary facts lost: %+v, %v", item, err)
			}
			if next := f.scan(); next.Probed != 0 || next.Unread != 0 || p.calls != 1 {
				t.Fatalf("unchanged scan retried: %+v, calls %d", next, p.calls)
			}
		})
	}
}

type packetReader struct{ ordinary, complete int }

func (p *packetReader) ProbeReader(context.Context, io.ReaderAt, int64) (scan.Facts, error) {
	p.ordinary++
	return scan.Facts{Container: "mp3", DurationMS: 1000, Streams: []db.Stream{{Kind: db.StreamAudio, Codec: "mp3"}}}, nil
}
func (p *packetReader) ProbeAudioPacketsReader(ctx context.Context, file io.ReaderAt, size int64) (scan.Facts, error) {
	p.complete++
	facts, err := p.ProbeReader(ctx, file, size)
	p.ordinary--
	facts.Packets = &db.Packets{LengthUS: 1026000}
	return facts, err
}

// The source is truly remote in the database too: upgrade must not clear its probe,
// even if the owner later changes the depth to Complete. Scan all files owns rereads.
func TestRemotePacketMeasurementRequiresCompleteAudiobooksAndKeepsOldProbes(t *testing.T) {
	for _, kind := range []string{db.LibraryAudiobooks, db.LibraryMusic} {
		for _, depth := range []string{db.DepthBasic, db.DepthComplete} {
			t.Run(kind+"/"+depth, func(t *testing.T) {
				f := newFixture(t, kind)
				f.write("A Book.mp3")
				source, err := f.d.CreateRemoteSource(ctx, db.NewID("src"), db.NewRemoteSource{Kind: "webdav", Name: "Remote", Config: `{}`, Secret: []byte("test"), Now: time.Now()})
				if err != nil {
					t.Fatal(err)
				}
				lib, err := f.d.NewLibrary(ctx, db.NewLibrary{Name: "Remote", Kind: kind, Options: db.LibraryOptions{Depth: depth}, RemoteFolders: []db.RemoteFolder{{SourceID: source.ID, Path: ""}}})
				if err != nil {
					t.Fatal(err)
				}
				store := &storage{root: f.root}
				store.slowed.Store(true)
				p := &packetReader{}
				f.s.ReaderProber = p
				f.s.Remote = func(context.Context, int64) (remote.Storage, time.Duration, error) { return store, time.Hour, nil }
				if stats, err := f.s.Scan(ctx, lib); err != nil || stats.Probed != 1 {
					t.Fatalf("first scan: %+v, %v", stats, err)
				}
				wantComplete := 0
				if kind == db.LibraryAudiobooks && depth == db.DepthComplete {
					wantComplete = 1
				}
				if p.complete != wantComplete || p.ordinary != 1-wantComplete {
					t.Fatalf("ordinary %d, packet %d", p.ordinary, p.complete)
				}
				opens := store.opens.Load()
				lib.Options.Depth = db.DepthComplete
				for _, scheduled := range []bool{false, true} {
					f.s.Scheduled = scheduled
					if stats, err := f.s.Scan(ctx, lib); err != nil || stats.Probed != 0 || store.opens.Load() != opens {
						t.Fatalf("upgrade reread remote file: %+v, opens %d, %v", stats, store.opens.Load(), err)
					}
				}
			})
		}
	}
}
