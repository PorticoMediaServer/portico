package httpapi

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

// IM04: a subtitle file can be saved as multipart/form-data — a bounded JSON
// metadata part, then the raw file — so binary files never travel as base64
// inside JSON. The JSON form keeps working.
func TestSubtitleMultipartUpload(t *testing.T) {
	f, _, _, source := v1SubtitleFixture(t)
	path := "/v1/items/" + f.items[0] + "/subtitles"
	metadata := func(operation string, extra string) string {
		return `{"operationId":"` + operation + `","sourceId":"` + source + `","scope":"personal","format":"srt","language":"en","title":"English","rights":"Owner transcript","offsetUs":"0"` + extra + `}`
	}
	type part struct{ name, file, encoding, body string }
	send := func(method, path string, parts ...part) *httptest.ResponseRecorder {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		for _, p := range parts {
			header := textproto.MIMEHeader{}
			disposition := `form-data; name="` + p.name + `"`
			if p.file != "" {
				disposition += `; filename="` + p.file + `"`
			}
			header.Set("Content-Disposition", disposition)
			if p.encoding != "" {
				header.Set("Content-Transfer-Encoding", p.encoding)
			}
			w, err := writer.CreatePart(header)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write([]byte(p.body))
		}
		_ = writer.Close()
		r := httptest.NewRequest(method, path, &body)
		r.Header.Set("Authorization", "Bearer "+f.owner.AccessToken)
		r.Header.Set("Content-Type", writer.FormDataContentType())
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		return w
	}
	srt := "1\n00:00:00,000 --> 00:00:02,000\nHello\n"
	w := send("POST", path, part{name: "metadata", body: metadata("multipart-english", "")}, part{name: "file", file: "english.srt", body: srt})
	if w.Code != 200 {
		t.Fatalf("multipart upload: %d %s", w.Code, w.Body.String())
	}
	var receipt subtitles.Receipt
	if err := json.Unmarshal(w.Body.Bytes(), &receipt); err != nil || receipt.ResourceID == "" || receipt.Revision != 1 {
		t.Fatalf("receipt: %v %s", err, w.Body.String())
	}
	// A replacement goes to the resource's own route.
	w = send("PUT", path+"/"+receipt.ResourceID, part{name: "metadata", body: metadata("multipart-replace", `,"expectedRevision":1`)}, part{name: "file", file: "english.srt", body: strings.Replace(srt, "Hello", "Hi", 1)})
	if w.Code != 200 {
		t.Fatalf("multipart replace: %d %s", w.Code, w.Body.String())
	}
	for name, parts := range map[string][]part{
		"bytes inside metadata":  {{name: "metadata", body: metadata("bad-1", `,"content":"x"`)}, {name: "file", file: "a.srt", body: srt}},
		"file before metadata":   {{name: "file", file: "a.srt", body: srt}, {name: "metadata", body: metadata("bad-2", "")}},
		"no file":                {{name: "metadata", body: metadata("bad-3", "")}},
		"empty file":             {{name: "metadata", body: metadata("bad-4", "")}, {name: "file", file: "a.srt", body: ""}},
		"encoded file":           {{name: "metadata", body: metadata("bad-5", "")}, {name: "file", file: "a.srt", encoding: "base64", body: "MQ=="}},
		"unknown part":           {{name: "metadata", body: metadata("bad-6", "")}, {name: "file", file: "a.srt", body: srt}, {name: "extra", body: "x"}},
		"repeated file":          {{name: "metadata", body: metadata("bad-7", "")}, {name: "file", file: "a.srt", body: srt}, {name: "file", file: "b.srt", body: srt}},
		"companion without file": {{name: "metadata", body: metadata("bad-8", "")}, {name: "companion", file: "a.sub", body: "x"}},
		"unknown metadata field": {{name: "metadata", body: metadata("bad-9", `,"surprise":1`)}, {name: "file", file: "a.srt", body: srt}},
	} {
		if w := send("POST", path, parts...); w.Code != 400 {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	big := part{name: "file", file: "big.sup", body: strings.Repeat("x", subtitles.MaxBinaryBytes+1)}
	if w := send("POST", path, part{name: "metadata", body: metadata("too-big", "")}, big); w.Code < 400 || w.Code == 200 || w.Code == 400 {
		t.Fatalf("an oversize file is a capacity refusal: %d %s", w.Code, w.Body.String())
	}
}
