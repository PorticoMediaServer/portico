package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestSeparateConversionDefaultsKeepGeneratedOutputOutsideData(t *testing.T) {
	env := func(key string) string { return map[string]string{"LOCALAPPDATA": `C:\Users\owner\AppData\Local`}[key] }
	if got := separateConversions("windows", env, "", false); got != `C:\Users\owner\AppData\Local\Portico Transcode` {
		t.Fatal(got)
	}
	if got := separateConversions("darwin", env, "/Users/owner", false); got != filepath.Join("/Users/owner", "Library", "Caches", "Portico Transcode") {
		t.Fatal(got)
	}
	if got := separateConversions("linux", env, "/home/owner", false); got != filepath.Join("/home/owner", ".cache", "portico") {
		t.Fatal(got)
	}
	service := func(key string) string {
		if key == "PORTICO_SERVICE" {
			return "1"
		}
		return ""
	}
	if got := separateConversions("linux", service, "", false); got != "/var/cache/portico" {
		t.Fatal(got)
	}
	if got := separateConversions("windows", service, "", false); got != `C:\ProgramData\Portico Transcode` {
		t.Fatal(got)
	}
	if got := separateConversions("linux", env, "", true); got != "/transcode/portico-conversions" {
		t.Fatal(got)
	}
}

func TestUnwritableSeparateConversionsFallBackToTheDataFolder(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(file, []byte("kept"), 0600); err != nil {
		t.Fatal(err)
	}
	fallback := filepath.Join(root, "data", "transcodes")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if got := conversionOutput(filepath.Join(file, "output"), fallback, log); got != fallback {
		t.Fatal(got)
	}
}
