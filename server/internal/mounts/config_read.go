package mounts

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrRemoteNotConfigured reports a remote name the server account's own
// configuration does not describe at all.
var ErrRemoteNotConfigured = errors.New("the remote is not in the server account's rclone configuration")

// wrapperKinds describe another remote instead of a storage service. Importing
// one needs the sections it points at, which this read does not copy yet.
var wrapperKinds = map[string]bool{"crypt": true, "alias": true, "union": true, "combine": true, "chunker": true, "hasher": true, "compress": true, "cache": true}

const (
	accountConfigLimit  = 1 << 20 // 1 MiB of INI is far more than a real account needs.
	sectionLimit        = 64 << 10
	configRemoteLimit   = 200
	encryptedValueMark  = "RCLONE_ENCRYPT_"
	configSectionMaxLen = 64
)

// ConfigRemote is one remote in the server account's own rclone configuration:
// its name, its declared type, and why Portico can or cannot import it. No value
// from its section is ever part of this projection.
type ConfigRemote struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Supported bool   `json:"supported"`
	Reason    string `json:"reason"`
}

// configSection is one checked INI section, kept in the exact lines the account
// wrote so the save path sees the same bytes the list was judged from.
type configSection struct {
	name         string
	kind         string
	lines        []string
	typed        bool
	encrypted    bool
	repeatedType bool
}

// parsedConfig is the one parse both the list and the selected section use, so
// their type and support evidence can never disagree.
type parsedConfig struct {
	sections  []configSection
	duplicate map[string]int
	malformed bool
}

// AccountRcloneConfigPath is the server account's own rclone configuration, the
// same fixed place rclone looks. It is never chosen by a request.
func AccountRcloneConfigPath() (string, bool) {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return "", false
	}
	return filepath.Join(dir, "rclone", "rclone.conf"), true
}

// ReadAccountRcloneConfig lists the account configuration's remotes. A missing
// file is an empty list; anything unreadable is an error. A file whose grammar
// is ambiguous lists nothing rather than guessing at its sections.
func ReadAccountRcloneConfig() (string, []ConfigRemote, bool, error) {
	path, ok := AccountRcloneConfigPath()
	if !ok {
		return "", []ConfigRemote{}, false, nil
	}
	raw, err := readAccountConfig(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", []ConfigRemote{}, false, nil
		}
		return path, nil, false, err
	}
	parsed := parseAccountConfig(string(raw))
	if parsed.malformed {
		return path, []ConfigRemote{}, false, nil
	}
	remotes, truncated := configRemotes(parsed)
	return path, remotes, truncated, nil
}

// ReadAccountRcloneSection returns the named section in plain rclone INI form,
// ready for the ordinary candidate test and save. The name only identifies a
// section when it occurs exactly once; values are passed through untouched and
// never returned to a client.
func ReadAccountRcloneSection(remote string) (string, error) {
	if !configSectionName(remote) {
		return "", ErrConfigInvalid
	}
	path, ok := AccountRcloneConfigPath()
	if !ok {
		return "", ErrRemoteNotConfigured
	}
	raw, err := readAccountConfig(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrRemoteNotConfigured
		}
		return "", err
	}
	parsed := parseAccountConfig(string(raw))
	if parsed.malformed || parsed.duplicate[remote] > 1 {
		return "", ErrConfigInvalid
	}
	var found *configSection
	for index := range parsed.sections {
		if parsed.sections[index].name == remote {
			found = &parsed.sections[index]
			break
		}
	}
	if found == nil {
		return "", ErrRemoteNotConfigured
	}
	if found.reason(false) != "" {
		return "", ErrConfigInvalid
	}
	config := "[" + remote + "]\n" + strings.Join(found.lines, "\n") + "\n"
	if len(config) > sectionLimit {
		return "", ErrConfigInvalid
	}
	if err = validateConfig(config, remote+":"); err != nil {
		return "", ErrConfigInvalid
	}
	return config, nil
}

// configRemotes projects every section, then reports whether the drawn list is
// the whole of it: the first configRemoteLimit names are shown and the rest are
// never silently dropped.
func configRemotes(parsed parsedConfig) ([]ConfigRemote, bool) {
	out := make([]ConfigRemote, 0, len(parsed.sections))
	for _, section := range parsed.sections {
		reason := section.reason(parsed.duplicate[section.name] > 1)
		out = append(out, ConfigRemote{Name: section.name, Kind: section.kind, Supported: reason == "", Reason: reason})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if len(out) > configRemoteLimit {
		return out[:configRemoteLimit], true
	}
	return out, false
}

func (s configSection) reason(duplicate bool) string {
	switch {
	case duplicate:
		return "duplicate"
	case s.encrypted:
		return "encrypted"
	case s.repeatedType:
		return "ambiguous"
	case s.kind == "" || !configurationKinds[s.kind]:
		return "kind"
	case wrapperKinds[s.kind]:
		return "wrapper"
	}
	return ""
}

// parseAccountConfig reads plain rclone INI without trusting it: one section per
// name, one type key per section, and no content outside a section. Anything it
// cannot account for marks the whole file ambiguous instead of choosing a line.
func parseAccountConfig(text string) parsedConfig {
	parsed := parsedConfig{duplicate: map[string]int{}}
	current := -1
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			if !strings.HasSuffix(trimmed, "]") {
				parsed.malformed = true
				current = -1
				continue
			}
			name := strings.TrimSuffix(strings.TrimPrefix(trimmed, "["), "]")
			if !configSectionName(name) {
				parsed.malformed = true
				current = -1
				continue
			}
			parsed.duplicate[name]++
			parsed.sections = append(parsed.sections, configSection{name: name})
			current = len(parsed.sections) - 1
			continue
		}
		if current < 0 {
			parsed.malformed = true
			continue
		}
		key, value, ok := strings.Cut(trimmed, "=")
		if !ok {
			parsed.malformed = true
			continue
		}
		section := &parsed.sections[current]
		if strings.Contains(trimmed, encryptedValueMark) {
			section.encrypted = true
		}
		if strings.TrimSpace(key) == "type" {
			if section.typed {
				section.repeatedType = true
			}
			section.typed = true
			if token := strings.TrimSpace(value); configTypeToken(token) {
				section.kind = token
			}
		}
		section.lines = append(section.lines, trimmed)
	}
	return parsed
}

// readAccountConfig opens the fixed path without waiting for a writer, then
// judges the descriptor it actually got: only a regular file is read (a symlink
// resolves to its target), so a FIFO or device cannot stall this read even if it
// replaced a regular file just before the open. The byte limit bounds what a
// regular file may contribute.
func readAccountConfig(path string) ([]byte, error) {
	file, err := openConfigFile(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() || opened.Size() > accountConfigLimit {
		return nil, ErrConfigInvalid
	}
	raw, err := io.ReadAll(io.LimitReader(file, accountConfigLimit+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > accountConfigLimit || !utf8.Valid(raw) {
		return nil, ErrConfigInvalid
	}
	return raw, nil
}

func configSectionName(name string) bool {
	if len(name) < 1 || len(name) > configSectionMaxLen {
		return false
	}
	for _, r := range name {
		if !(r == '-' || r == '_' || r == '.' || unicode.IsLetter(r) || unicode.IsDigit(r)) {
			return false
		}
	}
	return true
}

func configTypeToken(kind string) bool {
	if len(kind) < 1 || len(kind) > 32 {
		return false
	}
	for _, r := range kind {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}
