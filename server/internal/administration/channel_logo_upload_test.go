package administration

import (
	"bytes"
	"context"
	"database/sql"
	"image"
	"image/png"
	"testing"
)

func TestIdenticalChannelLogoUploadKeepsRevision(t *testing.T) {
	s, db, _ := newService(t)
	if _, err := db.Exec(`INSERT INTO libraries(id,name,kind,root) VALUES('music','Music','music','/music')`); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	auth := func(context.Context, *sql.Tx) error { return nil }
	first, err := s.UploadChannelLogo(context.Background(), auth, "channel-1", encoded.Bytes(), nil, "upload-first")
	if err != nil || first.Revision != 1 {
		t.Fatalf("first upload: %+v %v", first, err)
	}
	second, err := s.UploadChannelLogo(context.Background(), auth, "channel-1", encoded.Bytes(), &first.Revision, "upload-again")
	if err != nil || second != first {
		t.Fatalf("identical upload changed the logo: %+v, %+v, %v", first, second, err)
	}
	var revision int64
	if err := db.QueryRow(`SELECT revision FROM admin_channel_logos WHERE channel_id='channel-1'`).Scan(&revision); err != nil || revision != 1 {
		t.Fatalf("stored revision %d: %v", revision, err)
	}
}

func TestChannelLogoFileLockIsReleasedBeforeWriteTransaction(t *testing.T) {
	s, _, _ := newService(t)
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := s.UploadChannelLogo(context.Background(), func(context.Context, *sql.Tx) error {
			close(entered)
			<-release
			return nil
		}, "channel-1", encoded.Bytes(), nil, "upload-lock")
		done <- err
	}()
	<-entered
	free := s.files.TryLock()
	if free {
		s.files.Unlock()
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !free {
		t.Fatal("channel-logo upload held the artwork file lock in its write transaction")
	}
}
