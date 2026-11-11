package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/secrets"
)

// Config is a source's settings, kept as JSON with it. Nothing in it is secret: the owner's
// pages show all of it.
type Config struct {
	Preset string `json:"preset"`
	// Limits are the owner's own; zero values take the preset's.
	Limits Limits `json:"limits"`
	// WebDAV: the address and the user name.
	Address string `json:"address,omitempty"`
	User    string `json:"user,omitempty"`
	// SecretSet says a password or key is stored, which is all the owner is shown of it.
	SecretSet bool `json:"secretSet,omitempty"`
}

// Secret is what a source signs in with. It is stored sealed, never returned by any route,
// never logged, and can only be replaced.
type Secret struct {
	Password string `json:"password,omitempty"`
}

// PresetNamed returns the preset a source names, or the default.
func PresetNamed(name string) (Preset, bool) {
	for _, p := range Presets {
		if p.Name == name {
			return p, true
		}
	}
	return Presets[1], name == ""
}

// limits are the source's limits in force: the owner's where set, the preset's otherwise.
func (c Config) limits() Limits {
	preset, _ := PresetNamed(c.Preset)
	l := preset.Limits
	if c.Limits.Connections > 0 {
		l.Connections = c.Limits.Connections
	}
	if c.Limits.PerSecond > 0 {
		l.PerSecond = c.Limits.PerSecond
	}
	return l
}

// kinds builds each kind of source from its settings and secret.
var kinds = map[string]func(Config, Secret) (Storage, error){
	"webdav": func(c Config, s Secret) (Storage, error) { return NewWebDAV(c.Address, c.User, s.Password) },
}

// Build makes a source's storage, not yet behind a throttle.
func Build(kind string, c Config, s Secret) (Storage, error) {
	build, ok := kinds[kind]
	if !ok {
		return nil, fmt.Errorf("unknown kind of remote source %q", kind)
	}
	return build(c, s)
}

// Test reaches a source that is not saved yet: its top folder is listed once, within the
// source's limits. A slow-down answer is waited for until ctx ends.
func Test(ctx context.Context, kind string, c Config, s Secret) error {
	storage, err := Build(kind, c, s)
	if err != nil {
		return err
	}
	_, err = Throttled(storage, NewThrottle(c.limits())).List(ctx, "")
	return err
}

// ErrNeedsAttention says the source's saved details no longer work as they are: the
// configuration is refused, or a folder is too large to list at once. Its page says "Needs
// attention" and the owner edits the connection or chooses folders inside the large one.
var ErrNeedsAttention = errors.New("the source needs attention")

// Registry opens saved sources for scans and playback. It keeps one throttle per source, so
// everything Portico asks of a source shares its limits.
type Registry struct {
	db  *db.DB
	box *secrets.Box

	mu   sync.Mutex
	open map[int64]*opened
}

type opened struct {
	revision int64
	storage  Storage
	throttle *Throttle
	every    time.Duration
	state    string
}

// NewRegistry opens the registry. Sources' secrets are sealed with the server's key (package
// secrets), made in dataDir the first time.
func NewRegistry(d *db.DB, dataDir string) (*Registry, error) {
	box, err := secrets.Open(dataDir)
	if err != nil {
		return nil, err
	}
	return &Registry{db: d, box: box, open: map[int64]*opened{}}, nil
}

// Seal seals a secret to the source it belongs to.
func (r *Registry) Seal(publicID string, s Secret) ([]byte, error) {
	plain, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	return r.box.Seal(publicID, plain), nil
}

func (r *Registry) unseal(publicID string, sealed []byte) (Secret, error) {
	var s Secret
	if len(sealed) == 0 {
		return s, nil
	}
	plain, err := r.box.Open(publicID, sealed)
	if err != nil {
		// Sealed with another key: the key file was lost or replaced.
		return s, ErrSignIn
	}
	return s, json.Unmarshal(plain, &s)
}

// Secret returns a saved source's secret, to test a change to how the source is reached.
func (r *Registry) Secret(ctx context.Context, id int64) (Secret, error) {
	row, err := r.db.RemoteSourceByID(ctx, id)
	if err != nil {
		return Secret{}, err
	}
	sealed, _, err := r.db.RemoteSourceSecret(ctx, id)
	if err != nil {
		return Secret{}, err
	}
	return r.unseal(row.PublicID, sealed)
}

// Open opens a saved source by its row id: its storage behind its throttle, and how often it
// is scanned by itself. It is Scanner.Remote. A changed source is opened afresh.
func (r *Registry) Open(ctx context.Context, id int64) (Storage, time.Duration, error) {
	row, err := r.db.RemoteSourceByID(ctx, id)
	if err != nil {
		return nil, 0, err
	}
	sealed, revision, err := r.db.RemoteSourceSecret(ctx, id)
	if err != nil {
		return nil, 0, err
	}
	if revision != row.Revision {
		// The source was changed between the two reads. Read both again, so a new password
		// is never used with the address it replaced.
		return r.Open(ctx, id)
	}
	r.mu.Lock()
	o := r.open[id]
	r.mu.Unlock()
	if o != nil && o.revision == revision {
		return watched{o.storage, r, id}, o.every, nil
	}
	var c Config
	if err := json.Unmarshal([]byte(row.Config), &c); err != nil {
		return nil, 0, err
	}
	secret, err := r.unseal(row.PublicID, sealed)
	if err != nil {
		r.found(id, err, false)
		return nil, 0, err
	}
	storage, err := Build(row.Kind, c, secret)
	if err != nil {
		return nil, 0, err
	}
	preset, _ := PresetNamed(c.Preset)
	throttle := NewThrottle(c.limits())
	o = &opened{revision: revision, storage: Throttled(storage, throttle), throttle: throttle, every: preset.RescanEvery, state: row.State}
	r.mu.Lock()
	r.open[id] = o
	r.mu.Unlock()
	return watched{o.storage, r, id}, o.every, nil
}

// Waiting reports whether the source was told to slow down and until when.
func (r *Registry) Waiting(id int64) (time.Time, bool) {
	r.mu.Lock()
	o := r.open[id]
	r.mu.Unlock()
	if o == nil {
		return time.Time{}, false
	}
	return o.throttle.Waiting()
}

// Forget drops what is open for a source that was removed.
func (r *Registry) Forget(id int64) {
	r.mu.Lock()
	delete(r.open, id)
	r.mu.Unlock()
}

// found records how a request to a source went, and keeps it when the state changed: a
// refused sign-in, an address that cannot be reached, or fine again. Other failures (a
// folder that is not there) say nothing about the source.
func (r *Registry) found(id int64, err error, opened bool) {
	state, problem := "", ""
	var unreachable *url.Error
	switch {
	case err == nil:
		state = "ok"
	case errors.Is(err, ErrNoRanges):
		state, problem = "limited", "The source doesn't let Portico read parts of a file."
	case errors.Is(err, ErrNeedsAttention):
		state, problem = "needs_attention", err.Error()
	case errors.Is(err, ErrSignIn):
		state, problem = "sign_in", "The source refused its sign-in."
	case errors.As(err, &unreachable) && !errors.Is(err, context.Canceled):
		state, problem = "unreachable", unreachable.Err.Error()
	default:
		return
	}
	r.mu.Lock()
	o := r.open[id]
	if o != nil && o.state == "limited" && state == "ok" && !opened {
		// A listing that works says nothing about reading parts of files.
		r.mu.Unlock()
		return
	}
	changed := o == nil || o.state != state
	if o != nil {
		o.state = state
	}
	r.mu.Unlock()
	if changed {
		_ = r.db.SetRemoteSourceState(context.Background(), id, state, problem)
	}
}

// watched tells the registry how each request to a source went.
type watched struct {
	Storage
	r  *Registry
	id int64
}

func (w watched) List(ctx context.Context, dir string) ([]Entry, error) {
	entries, err := w.Storage.List(ctx, dir)
	w.r.found(w.id, err, false)
	return entries, err
}

func (w watched) Open(ctx context.Context, path string) (File, error) {
	file, err := w.Storage.Open(ctx, path)
	w.r.found(w.id, err, true)
	return file, err
}
