package httpapi

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// Setup from outside the server's network needs the setup code from the server's log;
// from home or the local network it needs nothing. A code works once, is replaced in the
// log, admits only its own peer at its own origin, and guesses are rate-limited.
func TestSetupCodeAdmitsOutsideSetup(t *testing.T) {
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	id, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	operations.WireSetupRemote(id)
	host, err := hosted.New(db, id, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	h := New(Dependencies{DB: db, Identity: id, Catalog: catalog.New(db), Hosted: host})
	var mu sync.Mutex
	var logged bytes.Buffer
	log.SetOutput(writerFunc(func(p []byte) (int, error) { mu.Lock(); defer mu.Unlock(); return logged.Write(p) }))
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	call := func(address, peer, path, code string, body map[string]any) *httptest.ResponseRecorder {
		if code != "" {
			body["setupCode"] = code
		}
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", address+path, bytes.NewReader(raw))
		r.RemoteAddr = peer
		r.Header.Set("Origin", address)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	refused := func(w *httptest.ResponseRecorder, code string) {
		t.Helper()
		if w.Code != http.StatusForbidden || !bytes.Contains(w.Body.Bytes(), []byte(`"`+code+`"`)) {
			t.Fatalf("want %s: %d %s", code, w.Code, w.Body)
		}
	}
	token := func(w *httptest.ResponseRecorder) string {
		t.Helper()
		var out struct {
			Token string `json:"setupToken"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out.Token) != 43 {
			t.Fatalf("setup start: %d %s", w.Code, w.Body)
		}
		return out.Token
	}
	const outside, address = "203.0.113.9:5000", "http://203.0.113.5:32500"
	// From the local network: no code.
	token(call("http://192.168.1.50:32500", "192.168.1.20:5000", "/v1/setup/browser", "", map[string]any{}))
	// From outside: the code is asked for, and a wrong one says so.
	refused(call(address, outside, "/v1/setup/browser", "", map[string]any{}), "setup_code_required")
	refused(call(address, outside, "/v1/setup/browser", "AAAA-AAAA", map[string]any{}), "setup_code_invalid")
	code, err := id.RotateSetupCode(t.Context())
	if err != nil || code == "" {
		t.Fatal(err)
	}
	first := token(call(address, outside, "/v1/setup/browser", code, map[string]any{}))
	mu.Lock()
	lines := logged.String()
	mu.Unlock()
	next := regexp.MustCompile(`Portico setup code: ([0-9A-Z]{4}-[0-9A-Z]{4})`).FindStringSubmatch(lines)
	if !strings.Contains(lines, "Setup code used.") || next == nil || next[1] == code {
		t.Fatalf("the log names the replacement code: %q", lines)
	}
	// Admitted: this peer carries on without the code, setup's own steps included.
	if w := call(address, outside, "/v1/setup", "", map[string]any{"requestId": "66666666-6666-4666-8666-666666666666", "setupToken": first, "name": "My media", "authMode": "hosted", "interactive": true}); w.Code/100 != 2 {
		t.Fatalf("an admitted peer's setup step: %d %s", w.Code, w.Body)
	}
	// The used code doesn't work again, from anyone; another peer needs the new code.
	refused(call(address, "198.51.100.7:5000", "/v1/setup/browser", code, map[string]any{}), "setup_code_invalid")
	// The same peer at another origin (a rebinding name) is not admitted.
	refused(call("http://evil.example:32500", outside, "/v1/setup/browser", "", map[string]any{}), "setup_code_required")
	token(call(address, "198.51.100.7:5000", "/v1/setup/start-over", next[1], map[string]any{}))
	// Guessing is rate-limited per peer.
	limited := false
	for i := 0; i < 12; i++ {
		w := call(address, "198.51.100.200:5000", "/v1/setup/browser", "ZZZZ-ZZZZ", map[string]any{})
		if w.Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("guessing the setup code is not rate-limited")
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
