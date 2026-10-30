package remotesources

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/rcloneprogram"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// This controlled serve child qualifies actual resource/process/save plumbing,
// not a provider or actor-content grant. The owner callback is the real current
// identity/session-family reader used by the save transaction tests.
func candidateWorkflow(t *testing.T) (*Service, mounts.Mount, string, func(*sql.Tx) error, func()) {
	t.Helper()
	f, managed, identities, owner, _, binary := nativeSaveFixture(t)
	authorize := func(tx *sql.Tx) error { return currentLocalOwnerTx(context.Background(), tx, identities, owner) }
	config := "[native]\ntype=sftp\nhost=192.168.42.7\n"
	receipt, err := managed.Command(context.Background(), "actual-owner-fixture", "", mounts.Command{OperationID: nativeSaveOperation(50), Action: "create", Name: "Fixture", Executable: binary, Remote: "native:/", Config: config}, authorize)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.s.RefreshMounts(context.Background()); err != nil {
		t.Fatal(err)
	}
	life, cancel := context.WithCancel(context.Background())
	if err = f.s.startReadRuntime(life); err != nil {
		t.Fatal(err)
	}
	cleanup := func() { cancel(); f.s.stopServeProcesses() }
	t.Cleanup(cleanup)
	if _, err = f.s.InspectRoot(life, receipt.Mount.MountPath); err != nil {
		t.Fatal(err)
	}
	return f.s, *receipt.Mount, binary, authorize, cleanup
}

func candidateHeldStream(t *testing.T, s *Service, m mounts.Mount) (*rcloneprogram.ServeProcess, io.ReadCloser) {
	t.Helper()
	s.serveMu.Lock()
	process := s.serveLeases[m.ID].process
	s.serveMu.Unlock()
	endpoint, err := process.Endpoint(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	body, err := endpoint.OpenFromStart(context.Background(), "held-stream", `"fixture"`, 1, process.Check)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { body.Close() })
	return process, body
}

func TestCandidateSourceTestDrainsThenResumesExistingProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, m, binary, authorize, _ := candidateWorkflow(t)
	process, body := candidateHeldStream(t, s, m)
	input := mounts.SourceCandidateInput{Configuration: []byte("[native]\ntype=sftp\nhost=192.168.42.7\n"), Remote: "native:/", Current: &m}
	candidate, err := s.prepareSourceCandidate(context.Background(), input, authorize)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- candidate.Test(context.Background(), rcloneprogram.Program{Path: binary}, m.ExecutableDigest)
	}()
	deadline := time.After(time.Second)
	for {
		s.serveMu.Lock()
		draining := s.sourceTests[m.ID] != nil
		s.serveMu.Unlock()
		if draining {
			break
		}
		select {
		case <-deadline:
			t.Fatal("no source drain")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	select {
	case err := <-done:
		t.Fatal("test bypassed admitted stream", err)
	case <-time.After(30 * time.Millisecond):
	}
	body.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("test did not finish after stream drain")
	}
	if err = process.Check(context.Background()); err != nil {
		t.Fatal("connection test retired existing process", err)
	}
	endpoint, err := process.Endpoint(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = endpoint.Stat(context.Background(), "", process.Check); err != nil {
		t.Fatal("queued reader could not resume", err)
	}
}

func TestCandidateSourceTestStuckReaderBusyAndTwentySecondChildTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, m, binary, authorize, _ := candidateWorkflow(t)
	process, body := candidateHeldStream(t, s, m)
	input := mounts.SourceCandidateInput{Configuration: []byte("[native]\ntype=sftp\nhost=192.168.42.7\n"), Remote: "native:/", Current: &m}
	candidate, err := s.prepareSourceCandidate(context.Background(), input, authorize)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err = candidate.Test(context.Background(), rcloneprogram.Program{Path: binary}, m.ExecutableDigest)
	if !errors.Is(err, storage.ErrBusy) || time.Since(start) < 5*time.Second || time.Since(start) > 6*time.Second {
		t.Fatal("stuck drain was not bounded Busy", err, time.Since(start))
	}
	if err = process.Check(context.Background()); err != nil {
		t.Fatal("stuck reader force-stopped", err)
	}
	body.Close()
	input.Remote = "native:__delay20__"
	candidate, err = s.prepareSourceCandidate(context.Background(), input, authorize)
	if err != nil {
		t.Fatal(err)
	}
	start = time.Now()
	done := make(chan error, 1)
	go func() {
		done <- candidate.Test(context.Background(), rcloneprogram.Program{Path: binary}, m.ExecutableDigest)
	}()
	deadline := time.After(time.Second)
	for {
		s.serveMu.Lock()
		testing := s.sourceTests[m.ID] != nil
		s.serveMu.Unlock()
		if testing {
			break
		}
		select {
		case <-deadline:
			t.Fatal("no exclusive test")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	endpoint, err := process.Endpoint(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	queued := make(chan error, 1)
	go func() { _, err := endpoint.Stat(context.Background(), "", process.Check); queued <- err }()
	select {
	case err := <-queued:
		t.Fatal("queued stat bypassed test", err)
	case <-time.After(30 * time.Millisecond):
	}
	if err = <-done; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("20s test did not return timeout", err)
	}
	if elapsed := time.Since(start); elapsed < 15*time.Second || elapsed > 17*time.Second {
		t.Fatal("wrong hold duration", elapsed)
	}
	select {
	case err := <-queued:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout retained source hold")
	}
	if err = process.Check(context.Background()); err != nil {
		t.Fatal("timeout retired original epoch", err)
	}
	if err = candidate.CheckAfterTest(context.Background()); err == nil {
		t.Fatal("timed-out test authorized publication")
	}
}

func TestCandidateExclusiveScopeRetiresWithServiceEvenWithoutCurrentProcess(t *testing.T) {
	s := &Service{}
	hold, finish, err := s.beginCandidateTest(context.Background(), "", ReadLimits{Connections: 1, RequestsPerSecond: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	s.stopServeProcesses()
	select {
	case <-hold.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("cold-source candidate survived service retirement")
	}
	if _, _, err = s.beginCandidateTest(context.Background(), "", ReadLimits{Connections: 1}); !errors.Is(err, storage.ErrRemoteOffline) {
		t.Fatal("retired service admitted another test", err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		s.serveMu.Lock()
		pending := len(s.sourceTests)
		s.serveMu.Unlock()
		if pending == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ended hold retained source queue without caller cleanup")
		}
		time.Sleep(time.Millisecond)
	}
	finish()
	s.serveMu.Lock()
	count := len(s.sourceTests)
	s.serveMu.Unlock()
	if count != 0 {
		t.Fatal("retired test retained queue", count)
	}
}
