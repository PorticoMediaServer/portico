package childprocess

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
)

func TestStartedObserverOnlyAfterSuccessfulStartAndReleasedFence(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	released, calls := false, 0
	ctx := WithPublication(context.Background(), func() (func(), error) { return func() { released = true }, nil })
	ctx = WithStarted(ctx, func() {
		calls++
		if !released {
			t.Error("observer ran under publication fence")
		}
	})
	cmd := exec.Command(binary, "-test.run=^$")
	if err = Run(ctx, cmd); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	if err = Run(ctx, exec.Command("/missing-portico-command")); err == nil {
		t.Fatal("missing child started")
	}
	if calls != 1 {
		t.Fatal("failed Start was counted")
	}
	refused := errors.New("retired")
	denied := WithPublication(ctx, func() (func(), error) { return nil, refused })
	if err = Run(denied, exec.Command(binary, "-test.run=^$")); !errors.Is(err, refused) {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("refused child counted")
	}
}
