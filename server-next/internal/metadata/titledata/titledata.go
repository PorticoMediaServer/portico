// Package titledata keeps Portico's title data on this server: the published file of mood,
// theme and similar-title facts about films and shows, built from Wikidata and Wikipedia and
// keyed by the ids providers give titles. Every server downloads the same whole file and sends
// nothing about its library.
//
// What it checks before a file is used, in order (the data set's Security and Provenance
// contract): the signed list (manifest.json) is signed with Ed25519 by the key compiled into
// this server, over its exact bytes, in the detached manifest.json.sig; the list names that
// same key; it is no older than the newest list this server accepted, so an old signed list
// cannot be replayed; the file is on the list's own host; its download has exactly the size
// and SHA-256 the list gives, and it expands to exactly the size the list gives; and the
// expanded file passes SQLite's check and is a full file of the schema, domain and version the
// list names. Anything that fails is refused and the copy in use stays in use.
//
// The file in use gives films and shows their mood tags (db.ApplyTitleData), matched by the ids
// providers gave them and never by name, after each new file and daily for titles matched since.
// A mood tag adds to a title and replaces nothing it has, so titles in a library set to local
// metadata only get them too, from the ids their files carry (definition area 4, rule 58).
//
// Owners and people know the feature as Enhanced Recommendations; "title data" is the code's
// name for its file. The owner's switch, Enhanced Recommendations, is on unless switched off.
// Off, the file and every mood tag go and no request is made.
package titledata

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/diskspace"
	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
)

// DefaultAddress is where the signed list is published. Each format of the data has a folder of
// its own, so a server reading a new format asks in the new folder. The owner can point the
// server at another copy (a mirror); whatever the address, the list must be signed by
// PublishedKey.
var DefaultAddress = fmt.Sprintf("https://data.getportico.tv/v%d/manifest.json", db.TitleDataSchema)

// PublishedKey is the Ed25519 public key, standard base64, that signs the list. It is empty
// until the first release is signed, and until then nothing is downloaded.
const PublishedKey = ""

const (
	// domain is the one part of the data set this server uses: films and shows.
	domain = "screen"
	// MaxFileBytes caps a file, downloaded or expanded, whatever the list says. The data set's
	// own budget is under 1 GB.
	MaxFileBytes      = 2 << 30
	maxManifestBytes  = 1 << 20
	maxSignatureBytes = 4 << 10

	// A list is fetched weekly, at a random point in a two-day spread so servers do not all ask
	// at once. A failed fetch is tried again after an hour, doubling to at most a day.
	checkEvery = 7 * 24 * time.Hour
	applyEvery = 24 * time.Hour
	spread     = 48 * time.Hour
	firstRetry = time.Hour
	lastRetry  = 24 * time.Hour
)

// Setting is the owner's choice, the settings document "title_data": only the owner reads or
// saves it, at /v1/admin/settings/title_data.
type Setting struct {
	// Download is the switch named Enhanced Recommendations, on by default. The line under it:
	// "When Enhanced Recommendations are enabled, Portico regularly downloads additional
	// metadata to supplement existing data. This database is provided by Portico."
	// Off, the file and its tags are removed at once and nothing is downloaded.
	Download bool `json:"download"`
	// Address is where the signed list is, for a copy of it on the owner's own host: https, or
	// http on this machine. Empty means DefaultAddress. Whatever the address, the list must be
	// signed by Portico's key.
	Address string `json:"address,omitempty"`
}

// Document is the owner's setting.
var Document = settings.Define("title_data", Setting{Download: true}, func(s *Setting) error {
	s.Address = strings.TrimSpace(s.Address)
	if s.Address == "" {
		return nil
	}
	if u, err := url.Parse(s.Address); err != nil || !usable(u) {
		return &settings.Invalid{Field: "address", Message: "Give the address as https://, or http:// on this machine."}
	}
	return nil
})

// stateKey names what this server must remember across a restart: the newest list it accepted,
// so an older one is refused, and the file in use.
const stateKey = "title_data_state"

type state struct {
	// Schema is the format the list and file are in. A new format is published in its own
	// folder with release numbers from 1, so the newest release accepted counts only within one
	// format.
	Schema   int    `json:"schema,omitempty"`
	Sequence int64  `json:"sequence"`
	Version  string `json:"version,omitempty"`
	File     string `json:"file,omitempty"` // a name in Dir
}

// formatBeforeRecorded is the format of a state saved before the format was recorded: every
// server then read format 3.
const formatBeforeRecorded = 3

// ErrRefused is a list or file that failed a check. The copy in use stays in use.
var ErrRefused = errors.New("Enhanced Recommendations data refused")

func refused(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrRefused, fmt.Sprintf(format, args...))
}

// Source fetches one address. The server uses HTTPS; a test hands it files instead.
type Source interface {
	Open(ctx context.Context, address string) (io.ReadCloser, error)
}

// Updater keeps the title data file current.
type Updater struct {
	DB     *db.DB
	Dir    string            // where the file is kept: a folder of its own in the data folder
	Source Source            // nil fetches over HTTPS
	Key    ed25519.PublicKey // nil uses PublishedKey
	// Address is a test publication's list, which wins over the owner's setting. A release
	// leaves it empty.
	Address string
	// Settings holds the owner's setting; nil is its default (on).
	Settings *settings.Settings
	Log      *slog.Logger
}

// ParseKey reads an Ed25519 public key written in standard base64, as PublishedKey is.
func ParseKey(s string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("not an Ed25519 public key in base64")
	}
	return raw, nil
}

func (u *Updater) key() ed25519.PublicKey {
	if u.Key != nil {
		return u.Key
	}
	key, _ := ParseKey(PublishedKey)
	return key
}

func (u *Updater) source() Source {
	if u.Source != nil {
		return u.Source
	}
	return HTTPS{}
}

func (u *Updater) log() *slog.Logger {
	if u.Log != nil {
		return u.Log
	}
	return slog.Default()
}

// KeyID names a public key in the list: the first 8 bytes of SHA-256 over the raw key, in hex.
func KeyID(key ed25519.PublicKey) string {
	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:8])
}

// Manifest is the signed list of published files.
type Manifest struct {
	SchemaVersion int                       `json:"schema_version"`
	Sequence      int64                     `json:"sequence"` // one more per release
	IssuedAt      string                    `json:"issued_at"`
	KeyID         string                    `json:"key_id"`
	Domains       map[string]ManifestDomain `json:"domains"`
}

// ManifestDomain is one domain's current version and its full file. (The list also offers
// deltas; this server always takes the full file.)
type ManifestDomain struct {
	DatasetVersion string       `json:"dataset_version"`
	Full           ManifestFile `json:"full"`
}

// ManifestFile is one published file. URL is relative to the list.
type ManifestFile struct {
	URL               string `json:"url"`
	SHA256            string `json:"sha256"`
	Bytes             int64  `json:"bytes"`
	UncompressedBytes int64  `json:"uncompressed_bytes"`
}

func (u *Updater) setting() Setting { return Document.Get(u.Settings) }

func (s Setting) on() bool { return s.Download }

func (s Setting) address() string {
	if s.Address == "" {
		return DefaultAddress
	}
	return s.Address
}

func (u *Updater) state(ctx context.Context) (state, error) {
	var st state
	raw, ok, err := u.DB.Setting(ctx, stateKey)
	if err != nil || !ok {
		return st, err
	}
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return st, err
	}
	if st.Schema == 0 {
		st.Schema = formatBeforeRecorded
	}
	if st.Schema != db.TitleDataSchema {
		// Another format's release numbers and file do not count: its file is not opened.
		return state{}, nil
	}
	return st, nil
}

func (u *Updater) saveState(ctx context.Context, st state) error {
	st.Schema = db.TitleDataSchema
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return u.DB.SaveSetting(ctx, stateKey, string(raw), time.Now())
}

// Open puts the file kept from before a restart back in use, unless the owner switched title
// data off. A file kept in another format is deleted; the next check downloads this format's.
func (u *Updater) Open(ctx context.Context) error {
	if !u.setting().on() {
		return nil
	}
	st, err := u.state(ctx)
	if err != nil {
		return err
	}
	if st.File == "" {
		u.removeOthers("")
		return nil
	}
	path := filepath.Join(u.Dir, st.File)
	if _, err := os.Stat(path); err != nil {
		return nil // gone from the disk: the next check downloads it again
	}
	return u.DB.UseTitleData(path)
}

// Run keeps the file current until ctx ends: it fetches the list a minute after start and then
// weekly. A save of the owner's setting is acted on at once: switched off, the file and its tags
// go; switched on, or given a new address, the list is fetched.
func (u *Updater) Run(ctx context.Context) {
	if err := u.Open(ctx); err != nil {
		u.log().Warn("the Enhanced Recommendations data kept from before could not be opened", "error", err)
	}
	kick := make(chan struct{}, 1)
	Document.Watch(u.Settings, func(Setting) {
		select {
		case kick <- struct{}{}:
		default:
		}
	})
	var due, applied time.Time
	failures := 0
	next := time.Minute
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(next):
		case <-kick:
			due = time.Time{} // switched on or moved: fetch now
		}
		next = time.Hour
		if !u.setting().on() {
			if err := u.remove(ctx); err != nil && ctx.Err() == nil {
				u.log().Warn("removing the Enhanced Recommendations data failed", "error", err)
			}
			due = time.Time{}
			continue
		}
		if !time.Now().Before(due) {
			if err := u.Check(ctx); err != nil {
				if ctx.Err() != nil {
					return
				}
				failures++
				due = time.Now().Add(min(firstRetry<<min(failures-1, 5), lastRetry))
				u.log().Warn("checking for new Enhanced Recommendations data failed; the data in use stays", "error", err)
			} else {
				failures = 0
				due = time.Now().Add(checkEvery + rand.N(spread))
			}
		}
		if time.Since(applied) > applyEvery {
			if err := u.Apply(ctx); err != nil && ctx.Err() == nil {
				u.log().Warn("applying Enhanced Recommendations to titles failed; it is tried again tomorrow", "error", err)
			}
			applied = time.Now()
		}
		next = min(time.Hour, time.Until(due))
	}
}

// Check does what the owner's switch says now: off, it removes the file; on, it fetches the
// signed list and, if it names a version this server does not have, downloads, checks and
// installs that file. A refusal or failure leaves the copy in use as it was.
func (u *Updater) Check(ctx context.Context) error {
	s := u.setting()
	if !s.on() {
		return u.remove(ctx)
	}
	key := u.key()
	if key == nil {
		return nil // no release is signed yet
	}
	st, err := u.state(ctx)
	if err != nil {
		return err
	}
	address := s.address()
	if u.Address != "" {
		address = u.Address
	}
	m, err := u.fetchList(ctx, address, key)
	if err != nil {
		return err
	}
	if m.Sequence < st.Sequence {
		return refused("the list is release %d, older than release %d already accepted", m.Sequence, st.Sequence)
	}
	entry, ok := m.Domains[domain]
	if !ok {
		return nil // nothing published for films and shows: keep what is here
	}
	if entry.DatasetVersion == st.Version && st.File != "" {
		if _, err := os.Stat(filepath.Join(u.Dir, st.File)); err == nil {
			if m.Sequence > st.Sequence {
				st.Sequence = m.Sequence
				return u.saveState(ctx, st)
			}
			return nil
		}
	}
	if !safeVersion.MatchString(entry.DatasetVersion) {
		return refused("the version %q is not a usable name", entry.DatasetVersion)
	}
	if err := os.MkdirAll(u.Dir, 0o700); err != nil {
		return err
	}
	downloaded, err := u.download(ctx, address, entry.Full)
	if err != nil {
		return err
	}
	defer os.Remove(downloaded)
	if err := db.PrepareTitleData(ctx, downloaded, domain, entry.DatasetVersion); err != nil {
		if errors.Is(err, db.ErrTitleDataShape) {
			return fmt.Errorf("%w: %v", ErrRefused, err)
		}
		return err
	}
	name := domain + "-" + entry.DatasetVersion + ".sqlite"
	if err := os.Rename(downloaded, filepath.Join(u.Dir, name)); err != nil {
		return err
	}
	if err := u.DB.UseTitleData(filepath.Join(u.Dir, name)); err != nil {
		return err
	}
	if err := u.saveState(ctx, state{Sequence: m.Sequence, Version: entry.DatasetVersion, File: name}); err != nil {
		return err
	}
	u.log().Info("new Enhanced Recommendations data is in use", "version", entry.DatasetVersion, "release", m.Sequence)
	u.removeOthers(name)
	return u.Apply(ctx)
}

// Apply gives every film and show the mood tags the file in use has for it, and takes them off
// titles it no longer has them for. A batch of titles is one Background write.
func (u *Updater) Apply(ctx context.Context) error {
	for after := int64(0); ; {
		next, err := u.DB.ApplyTitleData(ctx, after, 100)
		if err != nil {
			return err
		}
		if next == 0 {
			return u.DB.RemoveUnusedTitleDataTags(ctx)
		}
		after = next
	}
}

// safeVersion is a version that can be part of a file name.
var safeVersion = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._-]{0,63}$`)

// remove stops using the file and deletes it. The newest accepted release is kept, so turning
// the switch on again still refuses an older list.
func (u *Updater) remove(ctx context.Context) error {
	st, err := u.state(ctx)
	if err != nil {
		return err
	}
	if err := u.DB.UseTitleData(""); err != nil {
		return err
	}
	u.removeOthers("")
	for left := true; left; {
		if left, err = u.DB.RemoveTitleDataTags(ctx, 500); err != nil {
			return err
		}
	}
	if err := u.DB.RemoveUnusedTitleDataTags(ctx); err != nil {
		return err
	}
	if st.File != "" || st.Version != "" {
		return u.saveState(ctx, state{Sequence: st.Sequence})
	}
	return nil
}

// removeOthers deletes every file in Dir but keep: older versions and abandoned downloads.
func (u *Updater) removeOthers(keep string) {
	entries, _ := os.ReadDir(u.Dir)
	for _, e := range entries {
		if e.Name() != keep && !e.IsDir() {
			os.Remove(filepath.Join(u.Dir, e.Name()))
		}
	}
}

// fetchList downloads the list and its detached signature and checks the signature over the
// list's exact bytes before reading anything in it.
func (u *Updater) fetchList(ctx context.Context, address string, key ed25519.PublicKey) (Manifest, error) {
	var m Manifest
	body, err := u.get(ctx, address, maxManifestBytes)
	if err != nil {
		return m, err
	}
	signatureAddress, err := url.Parse(address)
	if err != nil {
		return m, err
	}
	signatureAddress.Path += ".sig"
	if signatureAddress.RawPath != "" {
		signatureAddress.RawPath += ".sig"
	}
	sig, err := u.get(ctx, signatureAddress.String(), maxSignatureBytes)
	if err != nil {
		return m, err
	}
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || !ed25519.Verify(key, body, signature) {
		return m, refused("the list's signature does not verify")
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return m, refused("the signed list is not readable: %v", err)
	}
	// The key id is inside the signed bytes: a list naming any other key is refused even with a
	// good signature.
	if m.KeyID != KeyID(key) {
		return m, refused("the list names key %q, not this server's", m.KeyID)
	}
	if m.SchemaVersion != db.TitleDataSchema {
		return m, refused("the list is schema %d, this server reads %d", m.SchemaVersion, db.TitleDataSchema)
	}
	if m.Sequence < 1 {
		return m, refused("the list has no release number")
	}
	return m, nil
}

// get reads a whole small answer, refusing one longer than limit.
func (u *Updater) get(ctx context.Context, address string, limit int64) ([]byte, error) {
	body, err := u.source().Open(ctx, address)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	raw, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, refused("an answer was longer than %d bytes", limit)
	}
	return raw, nil
}

// download fetches a published file, checks its size and SHA-256, and expands it to exactly the
// size the list gives. It answers the path of the expanded file, in Dir.
func (u *Updater) download(ctx context.Context, listAddress string, f ManifestFile) (string, error) {
	address, err := fileAddress(listAddress, f.URL)
	if err != nil {
		return "", err
	}
	if f.Bytes < 1 || f.Bytes > MaxFileBytes || f.UncompressedBytes < 1 || f.UncompressedBytes > MaxFileBytes {
		return "", refused("the file's sizes are out of range")
	}
	want, err := hex.DecodeString(f.SHA256)
	if err != nil || len(want) != sha256.Size {
		return "", refused("the file's SHA-256 is not readable")
	}
	// Room for the download, the expanded file and the floor that protects the database.
	if !diskspace.Room(u.Dir, f.Bytes+f.UncompressedBytes+diskspace.ProducerFloor) {
		return "", errors.New("not enough free space for the Enhanced Recommendations data")
	}
	body, err := u.source().Open(ctx, address)
	if err != nil {
		return "", err
	}
	defer body.Close()
	compressed, err := os.CreateTemp(u.Dir, "download-*.gz")
	if err != nil {
		return "", err
	}
	defer os.Remove(compressed.Name())
	defer compressed.Close()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(compressed, hash), io.LimitReader(body, f.Bytes+1))
	if err != nil {
		return "", err
	}
	if n != f.Bytes || !bytes.Equal(hash.Sum(nil), want) {
		return "", refused("the file is not the one the list names (size or SHA-256 differs)")
	}
	if _, err := compressed.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	zr, err := gzip.NewReader(compressed)
	if err != nil {
		return "", refused("the file is not gzip: %v", err)
	}
	expanded, err := os.CreateTemp(u.Dir, "download-*.sqlite")
	if err != nil {
		return "", err
	}
	written, err := io.Copy(expanded, io.LimitReader(zr, f.UncompressedBytes+1))
	if err == nil && written != f.UncompressedBytes {
		err = refused("the file expands to %d bytes, the list says %d", written, f.UncompressedBytes)
	}
	if err == nil {
		err = expanded.Sync()
	}
	if closeErr := expanded.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(expanded.Name())
		return "", err
	}
	return expanded.Name(), nil
}

// fileAddress resolves a file's address against the list's. Files are named relative to the
// list and must be on its host: a list cannot send the server anywhere else.
func fileAddress(listAddress, ref string) (string, error) {
	base, err := url.Parse(listAddress)
	if err != nil {
		return "", err
	}
	r, err := url.Parse(ref)
	if err != nil || ref == "" || r.IsAbs() || r.Host != "" || r.User != nil || strings.HasPrefix(ref, "//") ||
		strings.ContainsAny(ref, "\\\x00\r\n") {
		return "", refused("the file's address %q is not relative to the list", ref)
	}
	target := base.ResolveReference(r)
	if target.Scheme != base.Scheme || target.Host != base.Host {
		return "", refused("the file's address leaves the list's host")
	}
	return target.String(), nil
}

// HTTPS fetches over HTTPS only, or over plain HTTP from this machine's own loopback address,
// where a test publication is served. A redirect may move within the same host and nowhere else.
type HTTPS struct{}

var httpsClient = &http.Client{
	Timeout: 30 * time.Minute,
	CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 || r.URL.Scheme != via[0].URL.Scheme || r.URL.Host != via[0].URL.Host {
			return refused("a redirect left the list's host")
		}
		return nil
	},
}

// usable is an https address, or an http one on a loopback IP address.
func usable(u *url.URL) bool {
	if u.Host == "" || u.User != nil {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	ip, err := netip.ParseAddr(u.Hostname())
	return u.Scheme == "http" && err == nil && ip.IsLoopback()
}

func (HTTPS) Open(ctx context.Context, address string) (io.ReadCloser, error) {
	u, err := url.Parse(address)
	if err != nil || !usable(u) {
		return nil, refused("the Enhanced Recommendations address must be https")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	res, err := httpsClient.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		return nil, fmt.Errorf("Enhanced Recommendations: %s answered %s", u.Host, res.Status)
	}
	return res.Body, nil
}
