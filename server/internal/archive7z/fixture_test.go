package archive7z

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/readingtest"
)

// The bounded worker is the server's only CB7 reader, so this calls its own
// list and page entry points rather than the third-party reader directly. The
// process limits are applied by Main around a real invocation; parsing here is
// the same code path without mutating the test process's address-space limit.
func TestWorkerReadsFixtureCB7(t *testing.T) {
	raw := readingtest.CB7()
	if !bytes.HasPrefix(raw, []byte{'7', 'z', 0xbc, 0xaf, 0x27, 0x1c}) {
		t.Fatal("fixture is not a 7z container")
	}
	var listed bytes.Buffer
	if err := list(bytes.NewReader(raw), &listed); err != nil {
		t.Fatal(err)
	}
	var reply struct {
		Error   string
		Members []Member
	}
	if err := json.Unmarshal(listed.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Error != "" {
		t.Fatal(reply.Error)
	}
	if len(reply.Members) != 3 {
		t.Fatalf("members=%+v", reply.Members)
	}
	sizes := map[string]int64{}
	for _, member := range reply.Members {
		sizes[member.Name] = member.Size
	}
	if sizes["page1.png"] <= 0 || sizes["page1.png"] != sizes["page2.png"] || sizes["ComicInfo.xml"] <= 0 {
		t.Fatalf("members=%+v", reply.Members)
	}
	for _, name := range []string{"page1.png", "page2.png"} {
		var body bytes.Buffer
		if err := page(bytes.NewReader(raw), &body, name); err != nil {
			t.Fatal(name, err)
		}
		if int64(body.Len()) != sizes[name] || !bytes.HasPrefix(body.Bytes(), []byte("\x89PNG\r\n\x1a\n")) {
			t.Fatalf("%s: %d bytes, want a %d-byte PNG", name, body.Len(), sizes[name])
		}
	}
}
