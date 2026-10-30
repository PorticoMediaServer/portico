//go:build linux || darwin

package operations

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
)

func TestConsoleMeasurementsUseServerRatesAndMediaVolumes(t *testing.T) {
	root := t.TempDir()
	db, err := persistence.Open(storagepaths.Path(root, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, dir := range []string{"films", "music"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO libraries(id,name,kind,root) VALUES('films','Films','movie',?),('music','Music','music',?)`, filepath.Join(root, "films"), filepath.Join(root, "music")); err != nil {
		t.Fatal(err)
	}
	m := NewMeasurements(db, root)
	now := time.Now()
	m.Clock = func() time.Time { return now }
	m.ActiveTranscodes = func(context.Context) (int, error) { return 2, nil }
	first, err := m.Read(context.Background(), "network")
	if err != nil {
		t.Fatal(err)
	}
	if first.Facts["bytesInPerSecond"].State != "unavailable" {
		t.Fatalf("first sample claimed an instantaneous rate: %+v", first.Facts["bytesInPerSecond"])
	}
	m.Received.Store(1000)
	m.Sent.Store(400)
	now = now.Add(2 * time.Second)
	network, err := m.Read(context.Background(), "network")
	if err != nil {
		t.Fatal(err)
	}
	if network.Facts["bytesInPerSecond"].Value != float64(500) || network.Facts["bytesOutPerSecond"].Value != float64(200) || network.Facts["activeTranscodes"].Value != 2 {
		t.Fatalf("network facts: %+v", network.Facts)
	}
	if network.Facts["bytesInPerSecond"].Reason != "Average since the previous console measurement." {
		t.Fatalf("rate lacks its averaging label: %+v", network.Facts["bytesInPerSecond"])
	}
	storage, err := m.Read(context.Background(), "storage")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"databaseBytes", "databaseWALBytes"} {
		if storage.Facts[key].State != "available" {
			t.Fatalf("mapped database fact %s: %+v", key, storage.Facts[key])
		}
	}
	volumes, ok := storage.Facts["mediaVolumes"].Value.([]mediaVolumeSpace)
	if !ok || len(volumes) != 1 || len(volumes[0].LibraryIDs) != 2 || volumes[0].AvailableBytes == 0 {
		t.Fatalf("media volumes: %+v", storage.Facts["mediaVolumes"])
	}
	resources, err := m.Read(context.Background(), "resources")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"goroutines", "processCPU", "capacityPolicy", "goHeap", "goReserved"} {
		if _, present := resources.Facts[key]; present {
			t.Errorf("diagnostic %s remained on resources", key)
		}
	}
}

func TestMediaVolumeProbeReleasesDatabaseConnectionBeforeStalledStatfs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`INSERT INTO libraries(id,name,kind,root) VALUES('network','Network','movie','/stalled-mount')`); err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	m := NewMeasurements(db, root)
	started := make(chan struct{}, 1)
	release := make(chan struct{}, 1)
	defer func() {
		select {
		case release <- struct{}{}:
		default:
		}
	}()
	var calls atomic.Int32
	m.VolumeProbe = func(string) (string, uint64, error) {
		calls.Add(1)
		started <- struct{}{}
		<-release
		return "network-volume", 123, nil
	}
	done := make(chan Fact, 1)
	go func() { done <- m.mediaVolumes(context.Background()) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("volume probe did not start")
	}
	queryCtx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	var count int
	if err = db.QueryRowContext(queryCtx, `SELECT count(*) FROM libraries`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("statfs pinned the only DB connection: %d %v", count, err)
	}
	select {
	case fact := <-done:
		if fact.State != "unavailable" {
			t.Fatalf("stalled probe reported %+v", fact)
		}
	case <-time.After(350 * time.Millisecond):
		t.Fatal("stalled mount blocked console read")
	}
	_ = m.mediaVolumes(context.Background())
	if calls.Load() != 1 {
		t.Fatalf("stalled mount launched %d probes", calls.Load())
	}
	release <- struct{}{}
	// The next read gets the completed cached result without another statfs.
	deadline := time.Now().Add(time.Second)
	for {
		fact := m.mediaVolumes(context.Background())
		if fact.State == "available" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("completed probe not cached: %+v", fact)
		}
		time.Sleep(time.Millisecond)
	}
	if calls.Load() != 1 {
		t.Fatalf("cached result launched %d probes", calls.Load())
	}
}
