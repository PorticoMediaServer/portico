package main

import (
	"bytes"
	"log"
	"os"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/servicelog"
)

func TestPackagedMessageLogDoesNotDuplicateRuntimeOutput(t *testing.T) {
	for _, setting := range []string{"", "off"} {
		t.Run(setting, func(t *testing.T) {
			t.Setenv("PORTICO_LOG_STDERR", setting)
			old := log.Writer()
			defer log.SetOutput(old)
			var stderr bytes.Buffer
			log.SetOutput(&stderr)
			state := t.TempDir()
			log.Print("early-startup-evidence")
			recorder := servicelog.New(servicelog.Options{Directory: state, FileBytes: 200, FileCount: 2})
			installMessageLogOutput(recorder)
			log.Print("runtime-evidence")
			if err := recorder.Close(); err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(stderr.Bytes(), []byte("early-startup-evidence")) {
				t.Fatal("startup evidence lost")
			}
			if bytes.Contains(stderr.Bytes(), []byte("runtime-evidence")) != (setting != "off") {
				t.Fatalf("runtime output duplicated: %s", stderr.String())
			}
			data, err := os.ReadFile(storagepaths.Path(state, "messages.log"))
			if err != nil || !bytes.Contains(data, []byte("runtime-evidence")) {
				t.Fatalf("runtime evidence not persisted: %s %v", data, err)
			}
		})
	}
}
