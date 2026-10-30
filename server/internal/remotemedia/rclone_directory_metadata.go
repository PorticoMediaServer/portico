package remotemedia

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"strings"
	"time"
)

// VisitDirectory reads one upstream directory metadata result through the same
// private rclone process. The sink is private, duplicate-refusing staging only;
// EOF plus the final fence is required before publishing anything. Upstream's
// operations/list buffers its result inside rclone; this is not a claim of
// constant upstream memory or a provider's atomic directory revision.
func (m *RcloneMetadata) VisitDirectory(ctx context.Context, relative string, check func(context.Context) error, visit func(RcloneFileMetadata) error) error {
	if check == nil || visit == nil || !cleanDAVRelative(relative) || strings.Contains(relative, ":") {
		return ErrPolicy
	}
	ctx, release, err := m.endpoint.budget.enter(ctx)
	if err != nil {
		return err
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	capability, err := m.Capabilities(ctx, check)
	if err != nil {
		return err
	}
	return m.visitDirectory(ctx, relative, check, visit, map[string]any{"recurse": false, "noMimeType": true, "showHash": capability.HashCostKnown && !capability.SlowHash})
}

func (m *RcloneMetadata) visitDirectory(ctx context.Context, relative string, check func(context.Context) error, visit func(RcloneFileMetadata) error, options map[string]any) error {
	if err := check(ctx); err != nil {
		return err
	}
	call := ctx
	ctx = context.WithValue(ctx, serveReconnectKey{}, func() error { return check(call) })
	ctx = context.WithValue(ctx, serveListingReadKey{}, true)
	parameters, err := json.Marshal(map[string]any{"fs": m.fs, "remote": relative, "opt": options})
	if err != nil {
		return ErrPolicy
	}
	target := m.endpoint.root
	target.Path += "operations/list"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(parameters))
	if err != nil {
		return ErrPolicy
	}
	req.Header.Set("Authorization", m.endpoint.authorization)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	client := m.endpoint.client
	if transport, ok := client.Transport.(*http.Transport); ok {
		owned := transport.Clone()
		owned.ResponseHeaderTimeout = 0
		defer owned.CloseIdleConnections()
		copy := *client
		copy.Transport = owned
		client = &copy
	}
	response, err := client.Do(req)
	if err != nil {
		if errors.Is(err, ErrObservationChanged) {
			return ErrObservationChanged
		}
		return boundRequestError(ctx, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return serveResponseError(response)
	}
	if response.Header.Get("Content-Encoding") != "" {
		return ErrDAVResponse
	}
	if err = visitDirectoryMetadata(ctx, response.Body, relative, visit); err != nil {
		return err
	}
	return check(ctx)
}

func visitDirectoryMetadata(ctx context.Context, input io.Reader, directory string, visit func(RcloneFileMetadata) error) error {
	wire := &io.LimitedReader{R: input, N: 1<<30 + 1}
	step := &listingStepReader{reader: wire, left: 64 << 10}
	dec := json.NewDecoder(step)
	first, err := dec.Token()
	if err != nil || first != json.Delim('{') {
		return ErrDAVResponse
	}
	name, err := dec.Token()
	if err != nil || name != "list" {
		return ErrDAVResponse
	}
	first, err = dec.Token()
	if err != nil || first != json.Delim('[') {
		return ErrDAVResponse
	}
	count := 0
	for dec.More() {
		if err = ctx.Err(); err != nil {
			return err
		}
		step.left = 64 << 10
		var raw json.RawMessage
		if err = dec.Decode(&raw); err != nil || len(raw) > 64<<10 || wire.N <= 0 {
			return ErrDAVResponse
		}
		var item struct {
			Path, ID, ModTime string
			Size              *int64
			IsDir             bool
			Hashes            map[string]string
		}
		if json.Unmarshal(raw, &item) != nil || item.Size == nil || !cleanDAVRelative(item.Path) || strings.Contains(item.Path, ":") || path.Dir(item.Path) != emptyDir(directory) || item.Path == directory || *item.Size < 0 && !item.IsDir || len(item.ID) > 4096 || strings.ContainsAny(item.ID, "\x00\r\n") || len(item.Hashes) > 16 {
			return ErrDAVResponse
		}
		value := RcloneFileMetadata{Path: item.Path, ID: item.ID, Size: max(0, *item.Size), Directory: item.IsDir}
		if item.ModTime != "" {
			value.Modified, err = time.Parse(time.RFC3339Nano, item.ModTime)
			if err != nil {
				return ErrDAVResponse
			}
		}
		for kind, hash := range item.Hashes {
			if hash != "" {
				value.Hashes = append(value.Hashes, ProviderHash{Kind: strings.ToLower(kind), Value: hash})
			}
		}
		facts, err := (FileObservation{SourceID: "metadata", Incarnation: "metadata", Object: item.Path, Size: value.Size, ProviderHashes: value.Hashes}).Canonical()
		if err != nil {
			return ErrDAVResponse
		}
		value.Hashes = facts.ProviderHashes
		count++
		if count > 2_000_000 {
			return ErrDAVResponse
		}
		if err = visit(value); err != nil {
			return err
		}
	}
	step.left = 64 << 10
	end, err := dec.Token()
	if err != nil || end != json.Delim(']') {
		return ErrDAVResponse
	}
	end, err = dec.Token()
	if err != nil || end != json.Delim('}') {
		return ErrDAVResponse
	}
	var extra any
	if dec.Decode(&extra) != io.EOF || wire.N <= 0 {
		return ErrDAVResponse
	}
	return ctx.Err()
}
