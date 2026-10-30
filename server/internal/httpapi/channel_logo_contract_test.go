package httpapi

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http/httptest"
	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"testing"
)

func TestChannelLogoOptionalRevisionContract(t *testing.T) {
	d, mux, owner, _ := notificationFixture(t)
	d.Administration = administration.NewAt(d.DB, t.TempDir())
	d.channelAdministrationRoutes(mux)
	upload := func(revision *string, operation string, shade uint8, want int) int64 {
		t.Helper()
		img := image.NewRGBA(image.Rect(0, 0, 16, 16))
		img.Set(0, 0, color.RGBA{R: shade, A: 255})
		var encoded, body bytes.Buffer
		if err := png.Encode(&encoded, img); err != nil {
			t.Fatal(err)
		}
		form := multipart.NewWriter(&body)
		file, err := form.CreateFormFile("file", "logo.png")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = file.Write(encoded.Bytes()); err != nil {
			t.Fatal(err)
		}
		if revision != nil {
			if err = form.WriteField("expectedRevision", *revision); err != nil {
				t.Fatal(err)
			}
		}
		if err = form.WriteField("operationId", operation); err != nil {
			t.Fatal(err)
		}
		if err = form.Close(); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest("POST", "/v1/admin/library-channels/channel-1/logo", &body)
		req.Header.Set("Authorization", "Bearer "+owner.AccessToken)
		req.Header.Set("Content-Type", form.FormDataContentType())
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s: %d %s", operation, w.Code, w.Body.String())
		}
		assertSpecResponse(t, "POST", "/v1/admin/library-channels/{id}/logo", w)
		var out struct {
			Result struct {
				Revision int64 `json:"revision"`
			} `json:"result"`
		}
		if err = json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.Result.Revision
	}
	if rev := upload(nil, "logo-first", 1, 200); rev != 1 {
		t.Fatal(rev)
	}
	if rev := upload(nil, "logo-replace", 2, 200); rev != 2 {
		t.Fatal(rev)
	}
	if rev := upload(nil, "logo-replace", 2, 200); rev != 2 {
		t.Fatal("retry changed revision", rev)
	}
	zero, two := "0", "2"
	upload(&zero, "logo-stale", 3, 409)
	if rev := upload(&two, "logo-fenced", 3, 200); rev != 3 {
		t.Fatal(rev)
	}
	for _, value := range []string{"", "-1", "bad", "9223372036854775808"} {
		upload(&value, "logo-invalid", 4, 400)
	}
	upload(nil, "logo-replace", 4, 409)
}
