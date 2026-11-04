//go:build portico_rar7z

package reading

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/readingtest"
	"testing"
)

func TestDiagnosticCB7Worker(t *testing.T) {
	raw := readingtest.CB7()
	source := stagingReader{Reader: bytes.NewReader(raw), local: true}
	listed, err := runCB7Helper(context.Background(), source, int64(len(raw)), "list")
	if err != nil {
		t.Fatal(err)
	}
	var reply cb7ListReply
	if err = json.Unmarshal(listed, &reply); err != nil || reply.Error != "" || len(reply.Members) != 3 {
		t.Fatalf("%s %v", listed, err)
	}
	for _, name := range []string{"page1.png", "page2.png"} {
		body, err := runCB7Helper(context.Background(), source, int64(len(raw)), "page", name)
		if err != nil || !bytes.HasPrefix(body, []byte("\x89PNG\r\n\x1a\n")) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}
