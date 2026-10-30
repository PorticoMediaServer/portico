// Package configfile supplies the single-file settings mechanism. The settings
// specification supplies keys through Binding; this package defines no product
// settings or environment-variable catalog.
package configfile

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/pelletier/go-toml/v2"
	"io"
	"os"
	"github.com/PorticoMediaServer/portico/server/internal/atomicfile"
	"github.com/PorticoMediaServer/portico/server/internal/authorityfiles"
	"reflect"
	"strings"
	"sync"
	"time"
)

const MaxBytes = 1 << 20

type Source string

const (
	Default     Source = "default"
	File        Source = "file"
	Environment Source = "environment"
)

type Binding struct {
	Key, Env string
	Default  any
	ParseEnv func(string) (any, error)
	Validate func(any) error
}
type Value struct {
	Value  any
	Source Source
	Locked bool
}
type Loader struct {
	path   string
	lookup func(string) (string, bool)
	mu     sync.RWMutex
	values map[string]any
	digest [32]byte
	loaded bool
}

func New(path string) *Loader {
	return &Loader{path: path, lookup: os.LookupEnv, values: map[string]any{}}
}
func (l *Loader) read() ([]byte, error) {
	info, err := os.Lstat(l.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > MaxBytes {
		return nil, errors.New("config must be a regular file of at most 1 MiB")
	}
	f, err := os.Open(l.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	actual, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, actual) {
		return nil, errors.New("config changed while opening")
	}
	raw, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if len(raw) > MaxBytes {
		return nil, errors.New("config exceeds 1 MiB")
	}
	return raw, err
}

// Reload keeps the last valid document on a partial write or parse error.
func (l *Loader) Reload() (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	raw, err := l.read()
	if err != nil {
		return false, err
	}
	digest := sha256.Sum256(raw)
	if l.loaded && digest == l.digest {
		return false, nil
	}
	values := map[string]any{}
	if err = toml.Unmarshal(raw, &values); err != nil {
		return false, errors.New("portico.toml is invalid; the previous config remains active")
	}
	l.values, l.digest, l.loaded = values, digest, true
	return true, nil
}
func valueAt(values map[string]any, key string) (any, bool) {
	// Keys are literal top-level keys or supplied dotted paths; no implicit
	// PORTICO_ name conversion creates a product setting.
	if v, ok := values[key]; ok {
		return v, true
	}
	parts := strings.Split(key, ".")
	var v any = values
	for _, part := range parts {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		v, ok = m[part]
		if !ok {
			return nil, false
		}
	}
	return v, true
}
func (l *Loader) Resolve(b Binding) (Value, error) {
	l.mu.RLock()
	v, ok := valueAt(l.values, b.Key)
	l.mu.RUnlock()
	out := Value{Value: b.Default, Source: Default}
	if ok {
		out.Value, out.Source = v, File
	}
	if b.Env != "" {
		if raw, exists := l.lookup(b.Env); exists {
			if b.ParseEnv == nil {
				return Value{}, errors.New("binding requires an environment parser")
			}
			parsed, err := b.ParseEnv(raw)
			if err != nil {
				return Value{}, fmt.Errorf("invalid %s: %w", b.Env, err)
			}
			out = Value{parsed, Environment, true}
		}
	}
	if b.Validate != nil {
		if err := b.Validate(out.Value); err != nil {
			return Value{}, err
		}
	}
	return out, nil
}

// Decode provides typed decoding without exposing a guessed key catalog.
func (l *Loader) Decode(target any) error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	raw, err := toml.Marshal(l.values)
	if err != nil {
		return err
	}
	return toml.Unmarshal(raw, target)
}

// Write atomically replaces the file and rereads it. Environment bindings remain
// authoritative. Callers provide the full typed document from the settings spec.
func (l *Loader) Write(document any) error {
	defer authorityfiles.Process.Admit()()
	if document == nil || reflect.ValueOf(document).Kind() == reflect.Pointer && reflect.ValueOf(document).IsNil() {
		return errors.New("config document is required")
	}
	raw, err := toml.Marshal(document)
	if err != nil {
		return err
	}
	if len(raw) > MaxBytes {
		return errors.New("config exceeds 1 MiB")
	}
	var check map[string]any
	if err = toml.NewDecoder(bytes.NewReader(raw)).Decode(&check); err != nil {
		return err
	}
	l.mu.Lock()
	err = atomicfile.Write(l.path, raw, 0600)
	if err == nil {
		l.values, l.digest, l.loaded = check, sha256.Sum256(raw), true
	}
	l.mu.Unlock()
	return err
}

// Watch rereads content, rather than relying on modtime/size, so atomic renames
// and equal-size rewrites are detected. The caller owns and joins this loop.
func (l *Loader) Watch(ctx context.Context, interval time.Duration, onChange func(), onError func(error)) {
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			changed, err := l.Reload()
			if err != nil {
				if onError != nil {
					onError(err)
				}
			} else if changed && onChange != nil {
				onChange()
			}
		}
	}
}

// Patch keeps other registered domains when one settings authority publishes its
// values. Dotted keys are written as TOML tables, without parallel literal keys.
func (l *Loader) Patch(patch map[string]any) error {
	defer authorityfiles.Process.Admit()()
	l.mu.Lock()
	defer l.mu.Unlock()
	// Read the file again while holding the writer lock: another editor may have
	// changed an unrelated domain since the watcher's last poll.
	raw, err := l.read()
	if err != nil {
		return err
	}
	values := map[string]any{}
	if err = toml.Unmarshal(raw, &values); err != nil {
		return err
	}
	for key, value := range patch {
		delete(values, key)
		parts := strings.Split(key, ".")
		at := values
		for _, part := range parts[:len(parts)-1] {
			child, ok := at[part].(map[string]any)
			if !ok {
				child = map[string]any{}
				at[part] = child
			}
			at = child
		}
		at[parts[len(parts)-1]] = value
	}
	raw, err = toml.Marshal(values)
	if err != nil {
		return err
	}
	if len(raw) > MaxBytes {
		return errors.New("config exceeds 1 MiB")
	}
	if err = atomicfile.Write(l.path, raw, 0600); err != nil {
		return err
	}
	l.values, l.digest, l.loaded = values, sha256.Sum256(raw), true
	return nil
}
