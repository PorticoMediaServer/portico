package rcloneprogram

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

const pacerReportFixture = `{"level":"debug","msg":"Rate limited, increasing sleep to 200ms","object":"pacer","objectType":"string","source":"fs/pacer.go:41"}` + "\n"

func TestServeReportsOnlyAcceptActualPacerShape(t *testing.T) {
	for _, altered := range []string{"level", "object", "type", "source", "message", "duration", "partial", "plain", "huge"} {
		t.Run(altered, func(t *testing.T) {
			r := &serveReports{}
			line := pacerReportFixture
			switch altered {
			case "level":
				line = strings.ReplaceAll(line, `"debug"`, `"error"`)
			case "object":
				line = strings.ReplaceAll(line, `"pacer"`, `"secret-provider-url"`)
			case "type":
				line = strings.ReplaceAll(line, `"string"`, `"*provider.Error"`)
			case "source":
				line = strings.ReplaceAll(line, `fs/pacer.go:41`, `backend/unknown.go:41`)
			case "message":
				line = strings.ReplaceAll(line, "Rate limited, increasing sleep to", "Reducing sleep to")
			case "duration":
				line = strings.ReplaceAll(line, "200ms", "0s")
			case "partial":
				line = strings.TrimSuffix(line, "\n")
			case "plain":
				line = "429 quota exhausted; retry after17 https://private/credential\n"
			case "huge":
				line = strings.Repeat("x", serveReportLineLimit+1) + pacerReportFixture
			}
			if n, err := r.Write([]byte(line)); n != len(line) || err != nil {
				t.Fatal(n, err)
			}
			if observation := r.observation(); observation != (PacerObservation{}) {
				t.Fatal("non-report became a provider observation", observation)
			}
			r.discardPartial()
			if len(r.line) != 0 || cap(r.line) != 0 {
				t.Fatal("partial private stderr retained after process retirement")
			}
		})
	}
}

func TestServeReportsFragmentedBoundsPrivacyAndRecoveryFence(t *testing.T) {
	r := &serveReports{}
	for _, value := range []byte(pacerReportFixture) {
		if n, err := r.Write([]byte{value}); n != 1 || err != nil {
			t.Fatal(n, err)
		}
	}
	first := r.observation()
	if first.Sequence != 1 || !first.Backoff || first.ObservedAt.IsZero() {
		t.Fatal("fragmented actual report lost", first)
	}
	private := `{"level":"error","msg":"private https://user:password@owner/path?key=secret","object":"owner-private"}` + "\n"
	r.Write([]byte(private))
	encoded, err := json.Marshal(r.observation())
	if err != nil || bytes.Contains(encoded, []byte("private")) || bytes.Contains(encoded, []byte("password")) || bytes.Contains(encoded, []byte("Retry")) {
		t.Fatal("private text or invented retry deadline retained", string(encoded), err)
	}
	if len(r.line) != 0 || bytes.Contains(r.line[:cap(r.line)], []byte("secret")) {
		t.Fatal("completed line retained private bytes")
	}
	r.Write([]byte(pacerReportFixture))
	r.backendObserved(first.Sequence)
	second := r.observation()
	if second.Sequence != 2 || !second.Backoff {
		t.Fatal("old success cleared a newer actual report", second)
	}
	r.backendObserved(second.Sequence)
	if latest := r.observation(); latest.Backoff || latest.Sequence != second.Sequence || latest.ObservedAt != second.ObservedAt {
		t.Fatal("fresh metadata success invented a new report", latest)
	}
}

func TestServeReportsConcurrentSnapshots(t *testing.T) {
	r := &serveReports{}
	var done sync.WaitGroup
	for range 3 {
		done.Go(func() {
			for range 100 {
				r.Write([]byte(pacerReportFixture))
				value := r.observation()
				r.backendObserved(value.Sequence)
			}
		})
	}
	done.Wait()
	if latest := r.observation(); latest.Sequence != 300 || latest.ObservedAt.IsZero() {
		t.Fatal("concurrent actual reports lost", latest)
	}
}
