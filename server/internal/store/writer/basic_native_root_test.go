package writer

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestBasicOriginalAcceptsHostNativeAbsoluteRoot(t *testing.T) {
	in, identity := basicStageFactCommandFixture()
	// In particular, a Windows volume root need not begin with '/'. This is
	// the host's native path check, not normalization or a new identity rule.
	root := filepath.Join(t.TempDir(), "library")
	in.original.source.resolvedRoot = []byte(root)
	command, err := newBasicStageCommand(identity, in)
	if err != nil || command == nil || string(command.request.original.source.resolvedRoot) != root {
		t.Fatal(command, err)
	}
	for _, tc := range []struct {
		root  []byte
		cause error
	}{
		{[]byte("relative"), ErrInvalid},
		{append([]byte(root), 0), ErrInvalid},
		{append([]byte(root), 0xff), ErrFilenameUnrepresentable},
	} {
		in.original.source.resolvedRoot = tc.root
		if command, err := newBasicStageCommand(identity, in); command != nil || !errors.Is(err, tc.cause) {
			t.Fatal(command, err, tc.cause)
		}
	}
}
