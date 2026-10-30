package remotemedia

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOperationBudgetSharedStreamMetadataAndCancellation(t *testing.T) {
	life, stop := context.WithCancel(context.Background())
	defer stop()
	budget := NewOperationBudget(life, 1)
	content, err := NewServeEndpoint(12345, "private", "fixture", budget)
	if err != nil {
		t.Fatal(err)
	}
	defer content.Close()
	metadata, err := NewRcloneMetadata(12346, "private", "fixture", budget, "media:")
	if err != nil {
		t.Fatal(err)
	}
	defer metadata.Close()
	content.client.Transport = serveRoundTrip(func(*http.Request) (*http.Response, error) {
		header := http.Header{}
		header.Set("ETag", `"fixture"`)
		return &http.Response{StatusCode: 200, ContentLength: 16, Header: header, Body: io.NopCloser(strings.NewReader("sixteen-byte-src"))}, nil
	})
	var requests atomic.Int32
	metadata.endpoint.client.Transport = serveRoundTrip(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return nil, errors.New("metadata must not dispatch while stream owns cap1")
	})
	check := func(ctx context.Context) error { return ctx.Err() }
	stream, err := content.OpenFromStart(life, "one", `"fixture"`, 16, check)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	ctx, cancel := context.WithTimeout(life, 20*time.Millisecond)
	defer cancel()
	if _, err = metadata.Stat(ctx, "one", check); !errors.Is(err, context.DeadlineExceeded) || requests.Load() != 0 {
		t.Fatal("metadata observer bypassed stream slot", err, requests.Load())
	}
	if counts := budget.Counts(); counts.Active != 1 || counts.Peak != 1 {
		t.Fatal(counts)
	}
	stop()
	deadline := time.Now().Add(time.Second)
	for budget.Counts().Active != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if counts := budget.Counts(); counts.Active != 0 {
		t.Fatal("ended source lifetime retained progressive operation", counts)
	}
	if _, err = metadata.Stat(context.Background(), "one", check); !errors.Is(err, context.Canceled) || requests.Load() != 0 {
		t.Fatal("retired source admitted a replacement operation", err)
	}
}

func TestOperationBudgetNestedFenceAndExpiredScope(t *testing.T) {
	budget := NewOperationBudget(context.Background(), 1)
	scope, release, err := budget.enter(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, nested, err := budget.enter(scope)
	if err != nil || budget.Counts().Active != 1 {
		t.Fatal("nested metadata fence consumed second slot", err)
	}
	nested()
	if budget.Counts().Active != 1 {
		t.Fatal("nested metadata released outer read")
	}
	release()
	if _, _, err = budget.enter(scope); !errors.Is(err, context.Canceled) {
		t.Fatal("escaped old scope revived a permit", err)
	}
	for _, unknown := range []*OperationBudget{nil, {}, NewOperationBudget(nil, 1), NewOperationBudget(context.Background(), 0)} {
		if _, _, err = unknown.enter(context.Background()); !errors.Is(err, ErrPolicy) {
			t.Fatal("unknown capacity/lifetime became a budget", err)
		}
	}
}

func TestOperationBudgetProgressiveEOFKeepsExactTerminalRead(t *testing.T) {
	budget := NewOperationBudget(context.Background(), 1)
	scope, release, err := budget.enter(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	body := retainOperationBody(scope, io.NopCloser(strings.NewReader("source")), release)
	var exact [6]byte
	if _, err = io.ReadFull(body, exact[:]); err != nil || string(exact[:]) != "source" {
		t.Fatal(err)
	}
	var extra [1]byte
	for range 2 {
		if n, err := body.Read(extra[:]); n != 0 || err != io.EOF {
			t.Fatal("budget release changed a verified EOF", n, err)
		}
	}
	body.Close()
	if counts := budget.Counts(); counts.Active != 0 || counts.Peak != 1 {
		t.Fatal(counts)
	}
}

func TestOperationBudgetMixedEndpointsNeverReceiveIndependentPools(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	budget := NewOperationBudget(ctx, 2)
	content, err := NewServeEndpoint(12345, "private", "fixture", budget)
	if err != nil {
		t.Fatal(err)
	}
	observer, err := NewServeEndpoint(12345, "private", "fixture", budget)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := NewRcloneMetadata(12346, "private", "fixture", budget, "media:")
	if err != nil {
		t.Fatal(err)
	}
	defer content.Close()
	defer observer.Close()
	defer metadata.Close()
	var calls atomic.Int32
	barrier := make(chan struct{})
	transport := serveRoundTrip(func(r *http.Request) (*http.Response, error) {
		if calls.Add(1) == 2 {
			close(barrier)
		}
		select {
		case <-barrier:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		if value := budget.Counts(); value.Active < 1 || value.Active > 2 {
			t.Error("unadmitted operation dispatched", value)
		}
		body, status, length := "", 200, int64(-1)
		header := http.Header{}
		switch {
		case r.URL.Path == "/operations/fsinfo":
			body = `{"Name":"media","Hashes":[],"Features":{"SlowHash":true}}`
		case r.URL.Path == "/operations/stat":
			body = `{"item":{"Path":"one","Size":16,"ModTime":"2026-10-23T10:00:00Z"}}`
		case r.URL.Path == "/operations/list":
			body = `{"list":[{"Path":"one","Size":16,"ModTime":"2026-10-23T10:00:00Z","IsDir":false}]}`
		case r.Method == "PROPFIND":
			status = 207
			body = `<d:multistatus xmlns:d="DAV:"><d:response><d:href>/one</d:href><d:propstat><d:prop><d:resourcetype/><d:getcontentlength>16</d:getcontentlength><d:getetag>"fixture"</d:getetag></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response></d:multistatus>`
			if r.Header.Get("Depth") == "1" {
				body = strings.Replace(body, `<d:response>`, `<d:response><d:href>/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response><d:response>`, 1)
			}
		case r.Header.Get("Range") != "":
			status, body, length = 206, "same", 4
			header.Set("Content-Range", "bytes 0-3/16")
			header.Set("ETag", `"fixture"`)
		default:
			t.Error("unexpected operation", r.Method, r.URL.Path)
		}
		return &http.Response{StatusCode: status, Header: header, ContentLength: length, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	content.client.Transport, observer.client.Transport, metadata.endpoint.client.Transport = transport, transport, transport
	check := func(ctx context.Context) error { return ctx.Err() }
	functions := []func() error{
		func() error { _, err := content.ReadRange(ctx, "one", `"fixture"`, 16, 0, 4, check); return err },
		func() error { _, err := content.ReadRange(ctx, "one", `"fixture"`, 16, 0, 4, check); return err },
		func() error { _, err := observer.Stat(ctx, "one", check); return err },
		func() error { return content.VisitSnapshot(ctx, "", check, func(DAVEntry) error { return nil }) },
		func() error { _, err := metadata.Stat(ctx, "one", check); return err },
		func() error {
			return metadata.VisitDirectory(ctx, "", check, func(RcloneFileMetadata) error { return nil })
		},
	}
	var done sync.WaitGroup
	for _, call := range functions {
		done.Go(func() {
			if err := call(); err != nil {
				t.Error(err)
			}
		})
	}
	done.Wait()
	if counts := budget.Counts(); counts.Capacity != 2 || counts.Peak != 2 || counts.Active != 0 {
		t.Fatal(counts)
	}
	// The same source's reconnect fence may itself observe fresh metadata.
	if _, err = content.ReadRange(ctx, "one", `"fixture"`, 16, 0, 4, func(ctx context.Context) error {
		_, err := metadata.Stat(ctx, "one", check)
		return err
	}); err != nil {
		t.Fatal("nested reconnect metadata fence deadlocked", err)
	}
}
