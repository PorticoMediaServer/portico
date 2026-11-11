// Package updates tells the owner when a newer Portico is out, and how to update this kind of
// install. It never installs anything. It looks once a day (Settings › General: Automatically,
// Never, or at the owner's own address) and when the owner asks.
package updates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
)

// DefaultAddress is where releases are published: the project's latest release on GitHub.
const DefaultAddress = "https://api.github.com/repos/PorticoMediaServer/portico/releases/latest"

const (
	checkEvery = 24 * time.Hour
	// firstCheckAfter keeps the first check away from the start.
	firstCheckAfter = time.Hour
)

// Release is the newest release as last found.
type Release struct {
	Version     string    `json:"version"`
	Name        string    `json:"name,omitempty"`
	Notes       string    `json:"notes,omitempty"`
	URL         string    `json:"url,omitempty"`
	PublishedAt time.Time `json:"publishedAt,omitzero"`
}

// State is what the owner's page shows.
type State struct {
	Version string `json:"version"`
	// Newest is the newest release found, absent before the first check.
	Newest *Release `json:"newest,omitempty"`
	// Newer says Newest is a later version than this one.
	Newer     bool       `json:"newer"`
	CheckedAt *time.Time `json:"checkedAt,omitempty"`
	// Error says why the last check failed.
	Error string `json:"error,omitempty"`
	// Install is this kind of install, for the page to say how to update it: docker,
	// systemd, windows, macos or binary.
	Install string `json:"install"`
}

// Checker finds the newest release.
type Checker struct {
	Settings *settings.Settings
	Version  string
	Client   *http.Client
	Now      func() time.Time

	mu    sync.Mutex
	state State
}

// address is where to look, "" for Never.
func (c *Checker) address() string {
	g := settings.General.Get(c.Settings)
	switch g.UpdateCheck {
	case settings.UpdatesNever:
		return ""
	case settings.UpdatesOwnAddress:
		return g.UpdateAddress
	}
	if origin := os.Getenv("PORTICO_UPDATES_ORIGIN"); origin != "" {
		return origin // a stand-in, for testing a server without reaching the real one
	}
	return DefaultAddress
}

// State is what is known now.
func (c *Checker) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.state
	out.Version, out.Install = c.Version, installKind()
	return out
}

// Check looks for the newest release now, unless checking is Never.
func (c *Checker) Check(ctx context.Context) State {
	address := c.address()
	if address == "" {
		return c.State()
	}
	release, err := c.fetch(ctx, address)
	now := c.Now().UTC()
	c.mu.Lock()
	c.state.CheckedAt, c.state.Error = &now, ""
	if err != nil {
		c.state.Error = err.Error()
	} else {
		c.state.Newest, c.state.Newer = &release, newer(release.Version, c.Version)
	}
	c.mu.Unlock()
	return c.State()
}

func (c *Checker) fetch(ctx context.Context, address string) (Release, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return Release{}, errors.New("the update address is not one Portico can read")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Portico/"+c.Version)
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return Release{}, errors.New("the update address could not be reached")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("the update address answered %d", resp.StatusCode)
	}
	var published struct {
		Tag       string    `json:"tag_name"`
		Name      string    `json:"name"`
		Body      string    `json:"body"`
		URL       string    `json:"html_url"`
		Published time.Time `json:"published_at"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&published); err != nil || published.Tag == "" {
		return Release{}, errors.New("the update address did not answer with a release")
	}
	notes := published.Body
	if len(notes) > 20_000 {
		notes = notes[:20_000]
	}
	return Release{Version: published.Tag, Name: published.Name, Notes: notes, URL: published.URL, PublishedAt: published.Published}, nil
}

// Run checks once a day until ctx ends.
func (c *Checker) Run(ctx context.Context) {
	timer := time.NewTimer(firstCheckAfter)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			c.Check(ctx)
			timer.Reset(checkEvery)
		}
	}
}

// newer says release is a later version than current. A build that is not a release (a
// development build) is never told a release is newer.
func newer(release, current string) bool {
	r, ok1 := parse(release)
	c, ok2 := parse(current)
	if !ok1 || !ok2 {
		return false
	}
	for i := range r {
		if r[i] != c[i] {
			return r[i] > c[i]
		}
	}
	return false
}

func parse(version string) ([3]int, bool) {
	var out [3]int
	version, _, _ = strings.Cut(strings.TrimPrefix(version, "v"), "-")
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// installKind says how this server was installed, for how to update it.
func installKind() string {
	switch {
	case runtime.GOOS == "windows":
		return "windows"
	case runtime.GOOS == "darwin":
		return "macos"
	}
	if _, err := os.Stat("/.dockerenv"); err == nil || os.Getenv("container") != "" {
		return "docker"
	}
	if os.Getenv("INVOCATION_ID") != "" {
		return "systemd"
	}
	return "binary"
}
