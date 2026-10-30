package mediaartifact

import (
	"context"
	"errors"
	"io"
	"testing"
)

func TestReadDescriptorKeepsIndependentReadOnlyCursorsAndPhysicalLeases(t *testing.T) {
	s := fixture(t)
	object := seal(t, s, "captured-disc-bytes")
	first, err := s.OpenDescriptor(context.Background(), object)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := s.OpenDescriptor(context.Background(), object)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err = first.File().Write([]byte("replace")); err == nil {
		t.Fatal("helper descriptor could mutate closed bytes")
	}
	if _, err = io.ReadFull(first.File(), make([]byte, 4)); err != nil {
		t.Fatal(err)
	}
	all, err := io.ReadAll(second.File())
	if err != nil || string(all) != "captured-disc-bytes" {
		t.Fatal("one helper's cursor moved another invocation")
	}
	first.Close()
	first.Close()
	if err = s.Remove(object); !errors.Is(err, ErrLeased) {
		t.Fatal("closing one helper released another's physical lease")
	}
	if err = second.Close(); err != nil {
		t.Fatal(err)
	}
	if err = s.Remove(object); err != nil {
		t.Fatal(err)
	}
}
