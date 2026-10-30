package remotemedia

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// RcloneMetadata uses the private authenticated RC listener in the same upstream
// rclone serve process. It exposes no arbitrary command/URL/configuration API.
// Stat creates a backend object rather than reading VFS's served metadata. This
// improves observations, but does not make subsequent range reads atomic.
type RcloneMetadata struct {
	endpoint *ServeEndpoint
	fs       string
}

type RcloneCapabilities struct {
	Name          string
	SlowHash      bool
	HashCostKnown bool
	Hashes        []string
}

type RcloneFileMetadata struct {
	Path, ID  string
	Size      int64
	Modified  time.Time
	Directory bool
	Hashes    []ProviderHash
}

func NewRcloneMetadata(port int, username, password string, budget *OperationBudget, filesystem string) (*RcloneMetadata, error) {
	// The filesystem is a private named configuration section and root, never a
	// URL, on-the-fly backend or caller-selected RC filesystem.
	colon := strings.IndexByte(filesystem, ':')
	if colon < 1 || len(filesystem) > 8192 || strings.ContainsAny(filesystem, "\x00\r\n\\") || !cleanDAVRelative(strings.TrimPrefix(filesystem[colon+1:], "/")) {
		return nil, ErrPolicy
	}
	for _, c := range filesystem[:colon] {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return nil, ErrPolicy
		}
	}
	endpoint, err := NewServeEndpoint(port, username, password, budget)
	if err != nil {
		return nil, err
	}
	return &RcloneMetadata{endpoint: endpoint, fs: filesystem}, nil
}

func (m *RcloneMetadata) Close() { m.endpoint.Close() }

func (m *RcloneMetadata) request(ctx context.Context, operation string, parameters any, result any, check func(context.Context) error) error {
	if check == nil {
		return ErrPolicy
	}
	if err := check(ctx); err != nil {
		return err
	}
	requestContext := ctx
	ctx = context.WithValue(ctx, serveReconnectKey{}, func() error { return check(requestContext) })
	raw, err := json.Marshal(parameters)
	if err != nil || len(raw) > 32<<10 {
		return ErrPolicy
	}
	target := m.endpoint.root
	target.Path += operation
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(raw))
	if err != nil {
		return ErrPolicy
	}
	req.Header.Set("Authorization", m.endpoint.authorization)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	response, err := m.endpoint.client.Do(req)
	if err != nil {
		if errors.Is(err, ErrObservationChanged) {
			return ErrObservationChanged
		}
		return boundRequestError(ctx, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		// RC's text may contain provider URLs or configuration. It is never
		// surfaced as a provider quota/deadline or copied into errors/logs.
		return serveResponseError(response)
	}
	if response.Header.Get("Content-Encoding") != "" {
		return ErrDAVResponse
	}
	raw, err = io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	defer clear(raw)
	if err != nil || len(raw) > 1<<20 || json.Unmarshal(raw, result) != nil {
		return ErrDAVResponse
	}
	return check(ctx)
}

func (m *RcloneMetadata) Capabilities(ctx context.Context, check func(context.Context) error) (RcloneCapabilities, error) {
	ctx, release, err := m.endpoint.budget.enter(ctx)
	if err != nil {
		return RcloneCapabilities{}, err
	}
	defer release()
	var raw struct {
		Name     string
		Hashes   []string
		Features struct{ SlowHash *bool }
	}
	err = m.request(ctx, "operations/fsinfo", map[string]any{"fs": m.fs}, &raw, check)
	if err != nil {
		return RcloneCapabilities{}, err
	}
	if len(raw.Hashes) > 16 || raw.Name == "" || len(raw.Name) > 64 {
		return RcloneCapabilities{}, ErrDAVResponse
	}
	capability := RcloneCapabilities{Name: raw.Name, SlowHash: true, Hashes: append([]string(nil), raw.Hashes...)}
	if raw.Features.SlowHash != nil {
		capability.SlowHash, capability.HashCostKnown = *raw.Features.SlowHash, true
	}
	return capability, nil
}

func (m *RcloneMetadata) Stat(ctx context.Context, relative string, check func(context.Context) error) (RcloneFileMetadata, error) {
	if relative == "" || !cleanDAVRelative(relative) || strings.Contains(relative, ":") {
		return RcloneFileMetadata{}, ErrPolicy
	}
	ctx, release, err := m.endpoint.budget.enter(ctx)
	if err != nil {
		return RcloneFileMetadata{}, err
	}
	defer release()
	capability, err := m.Capabilities(ctx, check)
	if err != nil {
		return RcloneFileMetadata{}, err
	}
	var raw struct {
		Item *struct {
			Path, ID string
			Size     int64
			ModTime  string
			IsDir    bool
			Hashes   map[string]string
		}
	}
	// SlowHash means calculating a hash can read the object (or execute a remote
	// hashing command). Bounded decode never authorizes that whole-file work.
	// FilesOnly also prevents upstream StatJSON from swallowing a NewObject
	// failure and then listing its parent as though the request succeeded.
	// Directory membership has its separate bounded listing path.
	err = m.request(ctx, "operations/stat", map[string]any{"fs": m.fs, "remote": relative, "opt": map[string]any{"showHash": capability.HashCostKnown && !capability.SlowHash, "noMimeType": true, "filesOnly": true}}, &raw, check)
	if err != nil {
		return RcloneFileMetadata{}, err
	}
	if raw.Item == nil {
		return RcloneFileMetadata{}, ErrObservationChanged
	}
	i := raw.Item
	if i.Path != relative || i.Size < 0 || len(i.ID) > 4096 || strings.ContainsAny(i.ID, "\x00\r\n") || len(i.Hashes) > 16 {
		return RcloneFileMetadata{}, ErrDAVResponse
	}
	out := RcloneFileMetadata{Path: i.Path, ID: i.ID, Size: i.Size, Directory: i.IsDir}
	if i.ModTime != "" {
		out.Modified, err = time.Parse(time.RFC3339Nano, i.ModTime)
		if err != nil {
			return RcloneFileMetadata{}, ErrDAVResponse
		}
	}
	for kind, value := range i.Hashes {
		if value != "" {
			out.Hashes = append(out.Hashes, ProviderHash{Kind: strings.ToLower(kind), Value: value})
		}
	}
	// Canonical validation bounds opaque hash values and rejects duplicates.
	canonical, err := (FileObservation{SourceID: "metadata", Incarnation: "metadata", Object: relative, Size: i.Size, ProviderHashes: out.Hashes}).Canonical()
	if err != nil {
		return RcloneFileMetadata{}, ErrDAVResponse
	}
	out.Hashes = canonical.ProviderHashes
	return out, nil
}
