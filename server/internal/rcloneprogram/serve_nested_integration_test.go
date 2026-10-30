//go:build rcloneintegration && linux

package rcloneprogram

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"
	"time"
)

// This isolates small ZIP ReaderAt traffic and its fresh metadata callback at
// cap1. It is a controlled DAV transport test, not Reading actor authority.
func TestLockedDAVNestedSmallArchiveAtOneConnection(t *testing.T) {
	program, digest := lockedServeProgram(t)
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	member, err := writer.Create("pages/one.txt")
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("bounded member\n"), 100)
	if _, err = member.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	modified := time.Unix(1700000000, 0).UTC()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/child/" && r.URL.Path != "/child/small.cbz" {
			http.NotFound(w, r)
			return
		}
		if r.Method == "PROPFIND" {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusMultiStatus)
			fmt.Fprint(w, `<d:multistatus xmlns:d="DAV:">`)
			if r.URL.Path != "/child/small.cbz" {
				fmt.Fprintf(w, `<d:response><d:href>%s</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`, r.URL.Path)
			}
			if r.URL.Path == "/" && r.Header.Get("Depth") == "1" {
				fmt.Fprint(w, `<d:response><d:href>/child/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`)
			}
			if r.URL.Path == "/child/small.cbz" || r.URL.Path == "/child/" && r.Header.Get("Depth") == "1" {
				fmt.Fprintf(w, `<d:response><d:href>/child/small.cbz</d:href><d:propstat><d:prop><d:resourcetype/><d:getcontentlength>%d</d:getcontentlength><d:getlastmodified>%s</d:getlastmodified><d:getetag>"origin-one"</d:getetag></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`, archive.Len(), modified.Format(http.TimeFormat))
			}
			fmt.Fprint(w, `</d:multistatus>`)
			return
		}
		if r.URL.Path != "/child/small.cbz" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("ETag", `"origin-one"`)
		http.ServeContent(w, r, "small.cbz", modified, bytes.NewReader(archive.Bytes()))
	}))
	t.Cleanup(origin.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	spec, lease, _ := serveProcessFixture(t, "unused")
	spec.SourceID, spec.Remote = "nested-fixture", "media:"
	spec.Configuration = []byte("[media]\ntype=webdav\nvendor=other\nurl=" + origin.URL + "/\n")
	spec.ApprovedProgramDigest, spec.MetadataPort = digest, backendFixturePort(t)
	spec.Connections, spec.RequestsPerSecond = 1, 50
	spec.Enforce = func(command *exec.Cmd) error {
		if command.Path != program || spec.Remote != "media:" {
			return ErrServeProgram
		}
		return nil // Exact controlled loopback origin only; no production grant.
	}
	process, err := StartServe(ctx, Program{Path: program}, spec, lease)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		process.Stop()
		wait, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := process.Wait(wait); err != ErrServeStopped {
			t.Error("nested source did not physically retire", err)
		}
	})
	endpoint, err := process.Endpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := process.Metadata(ctx)
	if err != nil {
		t.Fatal(err)
	}
	observer, err := process.ObservationEndpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	const object = "child/small.cbz"
	served, err := observer.Stat(ctx, object, process.Check)
	if err != nil {
		t.Fatal(err)
	}
	freshCheck := func(ctx context.Context) error {
		upstream, err := metadata.Stat(ctx, object, process.Check)
		if err != nil {
			return fmt.Errorf("fresh upstream metadata: %w", err)
		}
		current, err := observer.Stat(ctx, object, process.Check)
		if err != nil {
			return fmt.Errorf("fresh served metadata: %w", err)
		}
		if upstream.Size != int64(archive.Len()) || !upstream.Modified.Equal(modified) || current.ETag != served.ETag || current.Size != upstream.Size {
			return fmt.Errorf("nested source observation changed")
		}
		return process.Check(ctx)
	}
	start := time.Now()
	reader, err := zip.NewReader(&controlledArchiveRange{ctx, endpoint.ReadRange, freshCheck, served.ETag, int64(archive.Len())}, int64(archive.Len()))
	if err != nil || len(reader.File) != 1 {
		t.Fatal("bounded nested ZIP directory", err)
	}
	body, err := reader.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(body)
	body.Close()
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatal("bounded nested ZIP member", err)
	}
	if counts := process.OperationCounts(); counts.Capacity != 1 || counts.Peak != 1 || counts.Active != 0 {
		t.Fatal("nested metadata escaped cap1", counts)
	}
	t.Logf("nested_archive_bytes=%d member_bytes=%d elapsed=%s", archive.Len(), len(got), time.Since(start))
}

type controlledArchiveRange struct {
	ctx   context.Context
	read  func(context.Context, string, string, int64, int64, int64, func(context.Context) error) ([]byte, error)
	check func(context.Context) error
	etag  string
	size  int64
}

func (r *controlledArchiveRange) ReadAt(p []byte, offset int64) (int, error) {
	length := min(int64(len(p)), r.size-offset)
	if length <= 0 {
		return 0, io.EOF
	}
	data, err := r.read(r.ctx, "child/small.cbz", r.etag, r.size, offset, length, r.check)
	if err != nil {
		return 0, err
	}
	n := copy(p, data)
	clear(data)
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
