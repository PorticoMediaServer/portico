package subtitles

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"testing"
)

type managedFixtureRemote struct {
	storage.RemoteSources
	data     []byte
	snapshot storage.Snapshot
	reads    int
}

func (m *managedFixtureRemote) Handles(string) bool { return true }
func (m *managedFixtureRemote) Stat(context.Context, string) (storage.Snapshot, error) {
	return m.snapshot, nil
}
func (m *managedFixtureRemote) Acquire(context.Context, string, string) (storage.RemoteObject, error) {
	m.reads++
	return &managedFixtureObject{data: m.data, snapshot: m.snapshot}, nil
}

type managedFixtureObject struct {
	storage.RemoteObject
	data     []byte
	snapshot storage.Snapshot
}

func (m *managedFixtureObject) Snapshot() storage.Snapshot { return m.snapshot }
func (m *managedFixtureObject) Observe(context.Context) (storage.Snapshot, error) {
	return m.snapshot, nil
}
func (m *managedFixtureObject) OpenRange(_ context.Context, offset, length int64) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(m.data[offset : offset+length])), nil
}
func (m *managedFixtureObject) Close() error { return nil }
func TestManagedSidecarsRequireCommittedCompletePhysicalDirectory(t *testing.T) {
	f := newSubtitleFixture(t)
	for _, q := range []string{`INSERT INTO library_sources(id,library_id,configured_root,root,incarnation) VALUES('remote','library','/remote','/remote','v1')`, `INSERT INTO jobs(id,library_id,status,created_at) VALUES('scan','library','running','now')`, `INSERT INTO inventory_runs(job_id,source_id,source_generation,root_incarnation,root_identity,policy_revision) VALUES('scan','remote',1,'v1','',1)`, `INSERT INTO inventory_directories(job_id,relative_path,state) VALUES('scan','.','pending')`} {
		if _, e := f.db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	origin := &managedFixtureRemote{data: []byte(plainSubtitle), snapshot: storage.Snapshot{Path: "/remote/movie.en.srt", Name: "movie.en.srt", Size: int64(len(plainSubtitle)), ModifiedNS: 1, Revision: "v1"}}
	client := storage.New("")
	client.Remote = origin
	tx, e := f.db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	entries := []storage.Snapshot{origin.snapshot}
	for i := 0; i < 5000; i++ {
		name := fmt.Sprintf("unrelated-%05d.nfo", i)
		entries = append(entries, storage.Snapshot{Path: "/remote/" + name, Name: name})
	}
	for len(entries) > 0 {
		n := min(len(entries), 128)
		if e = CommitManagedPage(context.Background(), tx, "remote", "v1", "scan", "/remote", entries[:n]); e != nil {
			t.Fatal(e)
		}
		entries = entries[n:]
	}

	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	discover := func() *Inventory {
		t.Helper()
		out, e := DiscoverManaged(context.Background(), f.db, client, "remote", "v1", "scan", "", "/remote/movie.mp4", nil, 0, true)
		if e != nil {
			t.Fatal(e)
		}
		return out
	}
	if out := discover(); out.Status != "unavailable" || origin.reads != 0 {
		t.Fatal("partial page authorized sidecar discovery")
	}
	if _, e = f.db.Exec(`UPDATE inventory_directories SET state='done' WHERE job_id='scan'`); e != nil {
		t.Fatal(e)
	}
	out := discover()
	if out.Status != "known" || len(out.Tracks) != 1 || out.Tracks[0].Reason != "" || out.Tracks[0].Language != "en" || origin.reads != 1 {
		t.Fatalf("committed remote sidecar missing %+v reads=%d", out, origin.reads)
	}
	if _, e = f.db.Exec(`UPDATE library_sources SET incarnation='v2' WHERE id='remote'`); e != nil {
		t.Fatal(e)
	}
	if out = discover(); out.Status != "unavailable" {
		t.Fatal("old physical page accepted after reconfiguration")
	}
}
