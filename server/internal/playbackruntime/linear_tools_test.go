package playbackruntime

import "testing"

// On a host without a sandbox Live TV must not require ELF library
// resolution: the libraries are only mounted into the sandbox, so unresolvable
// binaries leave Live TV available and keep the caller's extras.
func TestDecoderLibrariesSkippedWithoutSandbox(t *testing.T) {
	defer func(restore func(string) bool) { linearSandboxInUse = restore }(linearSandboxInUse)
	linearSandboxInUse = func(string) bool { return false }
	got, err := decoderLibraries("/nonexistent/ffmpeg", "/nonexistent/ffprobe", []string{"/extra/lib.so"})
	if err != nil {
		t.Fatalf("a baseline host resolves no libraries: %v", err)
	}
	if len(got) != 1 || got[0] != "/extra/lib.so" {
		t.Fatalf("caller extras lost: %q", got)
	}
	if got, err = decoderLibraries("/nonexistent/ffmpeg", "/nonexistent/ffprobe", nil); err != nil || len(got) != 0 {
		t.Fatalf("baseline without extras: %q %v", got, err)
	}
}
