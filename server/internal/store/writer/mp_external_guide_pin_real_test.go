//go:build mp15integration

package writer

import (
	"context"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/store/mediacompose"
	"os"
	"path/filepath"
	"testing"
)

// Separate native integration target: importing concrete storage registers
// legacy dbwork SQL functions globally. Default writer fixtures must not import
// it; their unchanged function-list goldens are checked in the default target.
// This test uses the REAL supervisor/process and full F bootstrap, no stubbed
// runner, table overlay, substitute parent, exit callback or caller exit boolean.
func TestMPGuideRealPhysicalComposition(t *testing.T) {
	e, _, s, q := mpGuidePinFixture(t)
	cmd, ready, releaseProcess := mpPhysicalBlockedCommand(t)
	files, err := os.OpenRoot(filepath.Dir(ready))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { files.Close() })
	concrete := &storage.Supervisor{Limit: 1}
	runner := mediacompose.PhysicalRunner(concrete)
	execution, err := s.start(mpPhysicalRoot{q.Occurrence.MediaSessionID, q.Occurrence.ExecutionID, q.Occurrence.Generation}, runner, cmd, files, []string{filepath.Base(ready)})
	if err != nil {
		t.Fatal(err)
	}
	mpPhysicalAwaitFile(t, ready)
	// Install occurs during the live execution; retirement does not exist at start.
	mpGuideInstallForTest(t, e, q)
	reader, err := execution.readers.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Release()
	window, err := execution.windows.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer window.Release()
	mpGuideStopForTest(t, e)
	ctx, cancel := context.WithCancel(t.Context())
	done := mpPhysicalJoinAsync(ctx, execution)
	mpPhysicalAssertHeld(t, execution, done)
	releaseProcess()
	<-execution.returned
	<-execution.retired
	mpPhysicalAssertHeld(t, execution, done)
	cancel()
	held := <-done
	if held.o != nil || !errors.Is(held.err, context.Canceled) || len(s.registry.executions) != 1 {
		t.Fatal("reader/window lifetime refunded", held)
	}
	if _, err = os.Stat(ready); err != nil {
		t.Fatal("cleanup preceded remaining owners", err)
	}
	reader.Release()
	window.Release()
	observation, err := execution.join(t.Context())
	if err != nil || observation == nil {
		t.Fatal(observation, err)
	}
	if _, err = os.Stat(ready); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cleanup witness is not actual", err)
	}
	turn := mpGuideTurn(t, e, 727)
	effect, err := releaseMPExternalGuidePin(t.Context(), turn, s, observation, q.Occurrence, mpPhysicalKey{media: q.Occurrence.MediaSessionID, execution: q.Occurrence.ExecutionID, request: "stop", generation: 2, revision: 2})
	if err != nil || effect.Rows != 1 || effect.release == nil {
		t.Fatal(effect, err)
	}
	// The registry must retain ownership even after the native release SQL, until
	// this exact W commit has succeeded and its physical return has completed.
	if len(s.registry.executions) != 1 {
		t.Fatal("precommit refund")
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	if err = releaseMPPhysicalAfterCommit(turn, effect.release); err != nil {
		t.Fatal(err)
	}
	if len(s.registry.executions) != 0 {
		t.Fatal("committed physical root retained")
	}
	turn = mpGuideTurn(t, e, IARetireStep)
	reclaim, err := reclaimMPReleasedGuidePin(t.Context(), turn, q.Occurrence)
	if err != nil || reclaim.Rows != 1 || reclaim.VariableBytes != effect.AfterBytes {
		t.Fatal(reclaim, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
}
