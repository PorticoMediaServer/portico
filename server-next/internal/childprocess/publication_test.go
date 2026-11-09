package childprocess

import (
	"context"
	"errors"
	"os/exec"
	"testing"
)

func TestPublicationRefusalPrecedesActualProcessStart(t *testing.T) {
	refused := errors.New("retired original occurrence")
	calls := 0
	ctx := WithPublication(context.Background(), func() (func(), error) { calls++; return nil, refused })
	cmd := exec.Command("this-command-must-never-start")
	if err := Start(ctx, cmd); !errors.Is(err, refused) {
		t.Fatal(err)
	}
	if calls != 1 || cmd.Process != nil {
		t.Fatal("refused original spawned a process")
	}
}

func TestPublicationNestedLifetimeRefusalReleasesOnlyOriginal(t *testing.T) {
	releases := 0
	refused := errors.New("successor must not borrow this epoch")
	original := WithPublication(context.Background(), func() (func(), error) { return func() { releases++ }, nil })
	ctx := WithPublication(original, func() (func(), error) { return nil, refused })
	if _, err := AcquirePublication(ctx); !errors.Is(err, refused) {
		t.Fatal(err)
	}
	if releases != 1 {
		t.Fatal("original publication leaked")
	}
}
