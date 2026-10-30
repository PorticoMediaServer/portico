package httpapi

import (
	"bytes"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

var fixtureTemplates *catalogtest.Templates

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--portico-storage-helper" {
		if storage.Helper(os.Stdin, os.Stdout) != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	// The test binary is also the decoder sandbox's helper, like the server
	// binary: without this the channel runtime's confinement probe fails here and
	// every real-runtime channel test skips.
	if handled, err := decoder.RunHelper(os.Args[1:]); handled {
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	persistence.UseSchemaTemplate()
	fixtureTemplates = catalogtest.NewTemplates()
	code := m.Run()
	fixtureTemplates.Close()
	os.Exit(code)
}

type revokeDuringRead struct {
	*bytes.Reader
	revoke func()
}

func (r revokeDuringRead) Read(p []byte) (int, error) {
	n, e := r.Reader.Read(p)
	r.revoke()
	return n, e
}
func TestSourceReadRevocationDiscardsReturnedBytes(t *testing.T) {
	revoked := false
	denied := errors.New("revoked")
	r := &guardedReadSeeker{ReadSeeker: revokeDuringRead{bytes.NewReader([]byte("private")), func() { revoked = true }}, check: func() error {
		if revoked {
			return denied
		}
		return nil
	}}
	b := make([]byte, 32)
	n, e := r.Read(b)
	if n != 0 || !errors.Is(e, denied) {
		t.Fatalf("read %d %v", n, e)
	}
	if _, e = r.Seek(0, io.SeekStart); e != nil {
		t.Fatal(e)
	}
	n, e = r.Read(b)
	if n != 0 || !errors.Is(e, denied) {
		t.Fatal("revoked read admitted")
	}
}
