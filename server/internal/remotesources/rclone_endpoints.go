package remotesources

import (
	"encoding/csv"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// configuredBackend identifies only selected config graph leaves. It contains
// no credential or permission. Unknown HTTP service endpoints remain public-only
// through the enforcing proxy; they never receive an invented private approval.
type configuredBackend struct {
	Kind         string
	Remote       string
	Native       remotemedia.NativeEndpoint
	Origins      []string
	RawRootKnown bool
}

func webDAVLeafCount(leaves []configuredBackend) int {
	count := 0
	for _, leaf := range leaves {
		if leaf.Kind == "webdav" {
			count++
		}
	}
	return count
}

func parseServeConfiguration(raw []byte) (map[string]map[string]string, error) {
	if len(raw) == 0 || len(raw) > 64<<10 {
		return nil, storage.ErrRemoteConfig
	}
	sections := map[string]map[string]string{}
	section := ""
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.ContainsAny(line, "\r\x00") {
			return nil, storage.ErrRemoteConfig
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
			if section == "" || sections[section] != nil {
				return nil, storage.ErrRemoteConfig
			}
			sections[section] = map[string]string{}
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if !ok || section == "" || key == "" {
			return nil, storage.ErrRemoteConfig
		}
		if _, duplicate := sections[section][key]; duplicate {
			return nil, storage.ErrRemoteConfig
		}
		sections[section][key] = value
	}
	return sections, nil
}

func configuredBackends(raw []byte, remote string) ([]configuredBackend, error) {
	sections, err := parseServeConfiguration(raw)
	if err != nil {
		return nil, err
	}
	active := map[string]bool{}
	visited := map[string]bool{}
	out := []configuredBackend{}
	rawRootKnown := true
	var walk func(string, int) error
	walk = func(remote string, depth int) error {
		name, suffix, ok := strings.Cut(remote, ":")
		if !ok || name == "" || strings.ContainsAny(name, ",/\\") || depth > 16 || active[name] || sections[name] == nil {
			return storage.ErrRemoteConfig
		}
		if visited[remote] {
			return nil
		}
		active[name] = true
		defer delete(active, name)
		fields := sections[name]
		for key, value := range fields {
			if strings.HasPrefix(key, "override.") || strings.Contains(key, "command") || key == "ssh" && value != "" || key == "unix_socket" && value != "" || key == "http_proxy" && value != "" || key == "auth_redirect" && value != "false" && value != "" {
				return storage.ErrRemoteConfig
			}
		}
		kind := fields["type"]
		switch kind {
		case "alias", "crypt", "chunker", "hasher", "compress", "cache":
			if suffix != "" {
				rawRootKnown = false
			}
			if err := walk(fields["remote"], depth+1); err != nil {
				return err
			}
		case "union", "combine":
			if suffix != "" {
				rawRootKnown = false
			}
			reader := csv.NewReader(strings.NewReader(fields["upstreams"]))
			reader.Comma = ' '
			reader.TrimLeadingSpace = true
			reader.FieldsPerRecord = -1
			members, err := reader.Read()
			if err != nil {
				return storage.ErrRemoteConfig
			}
			if _, err = reader.Read(); err != io.EOF {
				return storage.ErrRemoteConfig
			}
			count := 0
			for _, member := range members {
				if member == "" {
					continue
				}
				if kind == "combine" {
					prefix, value, ok := strings.Cut(member, "=")
					if !ok || prefix == "" {
						return storage.ErrRemoteConfig
					}
					member = value
				}
				if kind == "union" {
					for _, tag := range []string{":ro", ":nc", ":writeback"} {
						member = strings.TrimSuffix(member, tag)
					}
				}
				if err := walk(member, depth+1); err != nil {
					return err
				}
				count++
			}
			if count == 0 {
				return storage.ErrRemoteConfig
			}
		case "sftp", "smb":
			port := 22
			if kind == "smb" {
				port = 445
			}
			if fields["port"] != "" {
				n, err := strconv.Atoi(fields["port"])
				if err != nil {
					return storage.ErrRemoteConfig
				}
				port = n
			}
			endpoint := remotemedia.NativeEndpoint{Scheme: kind, Host: fields["host"], Port: port}
			if _, err := endpoint.Root(); err != nil {
				return storage.ErrRemoteConfig
			}
			out = append(out, configuredBackend{Kind: kind, Remote: remote, Native: endpoint})
		case "s3", "b2", "drive", "onedrive", "dropbox", "webdav", "http":
			leaf := configuredBackend{Kind: kind, Remote: remote}
			// Standard backend URL fields, including configured OAuth endpoints. Any
			// unknown/dynamically discovered endpoint is still checked by the proxy.
			for _, key := range []string{"url", "endpoint", "auth_url", "token_url", "root_url", "upload_url"} {
				raw := fields[key]
				if raw == "" {
					continue
				}
				u, err := url.Parse(raw)
				if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
					return storage.ErrRemoteConfig
				}
				// Canonical approval parsing validates all other host/path/port facts.
				origin, err := remotemedia.EndpointOrigin(raw)
				if err != nil {
					return storage.ErrRemoteConfig
				}
				leaf.Origins = append(leaf.Origins, origin)
			}
			if (kind == "webdav" || kind == "http") && fields["url"] == "" {
				return storage.ErrRemoteConfig
			}
			out = append(out, leaf)
		default:
			return storage.ErrRemoteConfig
		}
		visited[remote] = true
		if len(visited) > 64 || len(out) > 32 {
			return storage.ErrRemoteConfig
		}
		return nil
	}
	if err := walk(remote, 0); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, storage.ErrRemoteConfig
	}
	for i := range out {
		out[i].RawRootKnown = rawRootKnown
	}
	return out, nil
}
