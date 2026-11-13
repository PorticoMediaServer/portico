package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTheReleaseBinaryCarriesItsVerifiableIdentity(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "portico-server")
	root := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{42}, 32))
	source := strings.Repeat("a", 64)
	proof := "PORTICO_RELEASE_V2:" + source + ":test-build:1.2.3:" + root + ":END"
	prefix := "github.com/PorticoMediaServer/portico/server-next/internal/buildinfo."
	flags := "-s -w"
	for key, value := range map[string]string{"Version": "1.2.3", "BuildID": "test-build", "SourceDigest": source, "BuiltAt": "2026-11-13T00:00:00Z", "Proof": proof} {
		flags += " -X " + prefix + key + "=" + value
	}
	flags += " -X github.com/PorticoMediaServer/portico/server-next/internal/hosted.OfficialRoot=" + root
	if output, err := exec.Command("go", "build", "-tags", "release", "-trimpath", "-ldflags", flags, "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	output, err := exec.Command(binary, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("version: %v\n%s", err, output)
	}
	var info map[string]string
	if err := json.Unmarshal(output, &info); err != nil {
		t.Fatal(err)
	}
	if info["proof"] != proof || info["version"] != "1.2.3" || info["buildId"] != "test-build" || info["sourceDigest"] != source || info["builtAt"] != "2026-11-13T00:00:00Z" {
		t.Fatalf("identity: %v", info)
	}
	raw, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(raw, []byte(proof)) != 1 || bytes.Count(raw, []byte(root)) < 2 {
		t.Fatal("binary lacks the unique proof and separately used root required by the verifier")
	}
}
