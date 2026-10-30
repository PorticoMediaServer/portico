package localmetadata

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/png"
	"testing"
)

func TestLocalArtworkRejectsUnboundedOrNonImageContent(t *testing.T) {
	service, err := New(t.TempDir(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.store(context.Background(), []byte("<html>not artwork</html>")); err == nil {
		t.Fatal("HTML accepted as cover")
	}
	var wide bytes.Buffer
	if err = png.Encode(&wide, image.NewRGBA(image.Rect(0, 0, 10000, 1))); err != nil {
		t.Fatal(err)
	}
	if _, err = service.store(context.Background(), wide.Bytes()); err == nil {
		t.Fatal("unbounded dimensions accepted")
	}
	if _, _, err = service.Open("../../secret"); err == nil {
		t.Fatal("art path traversal accepted")
	}
	var valid bytes.Buffer
	if err = png.Encode(&valid, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	key, err := service.store(context.Background(), valid.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	used, e := service.MeasuredArtworkUsage(context.Background())
	if e != nil || used != int64(valid.Len()) {
		t.Fatal("artwork ledger disagrees with stored bytes", used, e)
	}

	file, mime, err := service.Open(key)
	if err != nil || mime != "image/png" {
		t.Fatal(mime, err)
	}
	file.Close()
}

func TestArtworkCapacityCanBeRaisedWithoutDroppingExistingCovers(t *testing.T) {
	service, e := New(t.TempDir(), "", nil)
	if e != nil {
		t.Fatal(e)
	}
	limit := int64(1)
	service.ArtworkLimit = func(context.Context) int64 { return limit }
	var raw bytes.Buffer
	if e = png.Encode(&raw, image.NewRGBA(image.Rect(0, 0, 8, 8))); e != nil {
		t.Fatal(e)
	}
	if _, e = service.store(context.Background(), raw.Bytes()); e == nil {
		t.Fatal("capacity was ignored")
	}
	limit = 1024
	key, e := service.store(context.Background(), raw.Bytes())
	if e != nil || key == "" {
		t.Fatal(key, e)
	}
	limit = 1
	if repeated, e := service.store(context.Background(), raw.Bytes()); e != nil || repeated != key {
		t.Fatal("retained cover rejected", e)
	}
}

func TestWebPCoverRetainsOriginalAndMime(t *testing.T) {
	raw, err := base64.StdEncoding.DecodeString("UklGRjwAAABXRUJQVlA4IDAAAADQAQCdASoCAAIAAgA0JaACdLoB+AADsAD+8Oj3/yC5YXXI1/8gP+QH/ID/+PIAAAA=")
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(t.TempDir(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.store(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	f, mime, err := s.Open(key)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if mime != "image/webp" {
		t.Fatal(mime)
	}
	got := make([]byte, len(raw))
	if _, err = f.Read(got); err != nil || !bytes.Equal(got, raw) {
		t.Fatal("original image changed", err)
	}
}
