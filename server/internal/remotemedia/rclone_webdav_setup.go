package remotemedia

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/csv"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// RcloneWebDAVSetup retains only a reproducible request to one saved WebDAV
// leaf. It is private setup custody, not a runtime transport or range grant.
// The raw leaf root and request headers must never enter public observations.
type RcloneWebDAVSetup struct {
	dav     DAV
	headers http.Header
}

func (d *RcloneWebDAVSetup) Diagnose(ctx context.Context, file DAVEntry, hold *ExclusiveTest, rps int, bps int64, check func(context.Context) error) (SetupRangeSupport, error) {
	if d == nil {
		return SetupRangeUnknown, ErrPolicy
	}
	// Only names with identical rclone/URL path encoding are reproduced here.
	if !setupRawPath(file.Relative) {
		return SetupRangeUnknown, nil
	}
	return d.dav.diagnoseSetupRanges(ctx, file, hold, rps, bps, check, d.headers)
}

func (d *RcloneWebDAVSetup) Close() {
	if d != nil {
		clear(d.headers)
		d.dav.config = DAVConfig{}
	}
}

// SetupWebDAV reads the named leaf configuration FROM the retained rclone
// process. There is no caller-selected RC operation, URL or credential input.
// nil,nil means the exact saved request cannot be reproduced: unknown, never
// conditional or unsupported. The caller must physically stop that process
// before calling Diagnose, retaining this same exclusive test hold throughout.
func (m *RcloneMetadata) SetupWebDAV(ctx context.Context, hold *ExclusiveTest, policy Policy, check func(context.Context) error) (*RcloneWebDAVSetup, error) {
	if m == nil || hold == nil || hold.Operations() != m.endpoint.budget || !policy.SourceEndpoints || check == nil {
		return nil, ErrPolicy
	}
	if err := hold.Context().Err(); err != nil {
		return nil, err
	}
	ctx, release, err := m.endpoint.budget.enter(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	name, _, _ := strings.Cut(m.fs, ":")
	var fields map[string]string
	if err = m.request(ctx, "config/get", map[string]string{"name": name}, &fields, check); err != nil {
		return nil, err
	}
	defer clear(fields)
	var info struct{ Name, Root string }
	if err = m.request(ctx, "operations/fsinfo", map[string]string{"fs": m.fs}, &info, check); err != nil {
		return nil, err
	}
	if info.Name != name {
		return nil, ErrObservationChanged
	}
	var options struct {
		Main *struct {
			UserAgent                         string
			InsecureSkipVerify, Cookie        *bool
			Headers, DownloadHeaders          []jsonHTTPOption
			CaCert                            []string
			ClientCert, ClientKey, ClientPass string
		}
	}
	if err = m.request(ctx, "options/get", map[string]string{"blocks": "main"}, &options, check); err != nil {
		return nil, err
	}
	main := options.Main
	if main == nil || main.InsecureSkipVerify == nil || *main.InsecureSkipVerify || main.Cookie == nil || *main.Cookie || len(main.Headers) != 0 || len(main.DownloadHeaders) != 0 || len(main.CaCert) != 0 || main.ClientCert != "" || main.ClientKey != "" || main.ClientPass != "" || main.UserAgent == "" || len(main.UserAgent) > 1024 || strings.ContainsAny(main.UserAgent, "\r\n\x00") {
		return nil, nil
	}
	policy.UserAgent = main.UserAgent
	return resolvedWebDAVSetup(fields, info.Root, policy)
}

type jsonHTTPOption struct{ Key, Value string }

func setupRawPath(value string) bool {
	if !cleanDAVRelative(value) {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~/", c)) {
			return false
		}
	}
	return true
}

func resolvedWebDAVSetup(fields map[string]string, root string, policy Policy) (*RcloneWebDAVSetup, error) {
	if len(fields) > 32 || fields["type"] != "webdav" || !setupRawPath(root) || fields["vendor"] != "" && fields["vendor"] != "other" || fields["encoding"] != "" && fields["encoding"] != "None" {
		return nil, nil
	}
	for key, value := range fields {
		if len(value) > 8192 {
			return nil, nil
		}
		switch key {
		case "type", "url", "vendor", "encoding", "user", "pass", "bearer_token", "headers":
		default:
			if value != "" {
				return nil, nil
			}
		}
	}
	u, err := url.Parse(fields["url"])
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || !setupRawPath(strings.Trim(u.Path, "/")) || u.Scheme != "https" && u.Scheme != "http" {
		return nil, nil
	}
	base := strings.TrimSuffix(u.String(), "/") + "/"
	// Endpoint admission and request-time DNS resolution happen only in
	// Diagnose, under the original test context and saved source approvals.
	headers := make(http.Header)
	headers.Set("Referer", base)
	if fields["user"] != "" || fields["pass"] != "" {
		password, ok := revealRcloneSetupPassword(fields["pass"])
		if !ok {
			return nil, nil
		}
		credentials := append(append([]byte(fields["user"]), ':'), password...)
		headers.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString(credentials))
		clear(credentials)
		clear(password)
	} else if fields["bearer_token"] != "" {
		headers.Set("Authorization", "Bearer "+fields["bearer_token"])
	}
	if fields["headers"] != "" {
		reader := csv.NewReader(strings.NewReader(fields["headers"]))
		values, err := reader.Read()
		if err != nil || len(values)%2 != 0 || len(values) > 32 {
			return nil, nil
		}
		if _, err = reader.Read(); err != io.EOF {
			return nil, nil
		}
		for i := 0; i < len(values); i += 2 {
			key := http.CanonicalHeaderKey(values[i])
			if !setupHeaderName(key) {
				return nil, nil
			}
			headers.Set(key, values[i+1])
		}
	}
	for _, values := range headers {
		for _, value := range values {
			if strings.ContainsAny(value, "\r\n\x00") {
				return nil, nil
			}
		}
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "/"
	if root != "" {
		u.Path += root + "/"
	}
	return &RcloneWebDAVSetup{dav: DAV{config: DAVConfig{Root: u.String()}, policy: policy}, headers: headers}, nil
}

func setupHeaderName(key string) bool {
	if key == "" {
		return false
	}
	for _, c := range key {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	switch key {
	case "Host", "Range", "Content-Length", "Transfer-Encoding", "Connection", "Accept-Encoding", "Trailer", "Upgrade", "Proxy-Authorization", "Proxy-Connection":
		return false
	}
	return true
}

// rclone's saved password representation uses AES-CTR with its public obscure
// key (rclone v1.75.2 fs/config/obscure, MIT). This decodes only the exact value
// returned by this retained process; it neither invokes nor guesses credentials.
func revealRcloneSetupPassword(value string) ([]byte, bool) {
	if value == "" {
		return nil, true
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) < aes.BlockSize || len(raw) > 8192 {
		clear(raw)
		return nil, false
	}
	key := []byte{0x9c, 0x93, 0x5b, 0x48, 0x73, 0x0a, 0x55, 0x4d, 0x6b, 0xfd, 0x7c, 0x63, 0xc8, 0x86, 0xa9, 0x2b, 0xd3, 0x90, 0x19, 0x8e, 0xb8, 0x12, 0x8a, 0xfb, 0xf4, 0xde, 0x16, 0x2b, 0x8b, 0x95, 0xf6, 0x38}
	block, err := aes.NewCipher(key)
	clear(key)
	if err != nil {
		clear(raw)
		return nil, false
	}
	cipher.NewCTR(block, raw[:aes.BlockSize]).XORKeyStream(raw[aes.BlockSize:], raw[aes.BlockSize:])
	password := append([]byte(nil), raw[aes.BlockSize:]...)
	clear(raw)
	return password, true
}
