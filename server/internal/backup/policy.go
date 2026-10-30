package backup

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/atomicfile"
	"github.com/PorticoMediaServer/portico/server/internal/authorityfiles"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
	"runtime"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Settings is safe to publish. Passphrases and derived key material never leave
// the domain through a read/projection/config DTO.
type Settings struct {
	BackupFolder        string `json:"backupFolder"`
	IncludeArtwork      bool   `json:"includeArtwork"`
	PassphraseProtected bool   `json:"passphraseProtected"`
	PassphraseSet       bool   `json:"passphraseSet"`
}
type SettingsPatch struct {
	BackupFolder        *string `json:"backupFolder,omitempty"`
	IncludeArtwork      *bool   `json:"includeArtwork,omitempty"`
	PassphraseProtected *bool   `json:"passphraseProtected,omitempty"`
	Passphrase          *string `json:"passphrase,omitempty"`
}

// Snapshot belongs to a single backup run; changes never redirect an in-flight
// copy or switch its encryption halfway through publication.
type Snapshot struct {
	Settings
	material *archiveKeyMaterial
}

// An authenticated versioned blob contains only a derived 32-byte master.
var policyMaterialMagic = []byte("PBAKEY\x00\x02")

func (s Snapshot) clear() { s.material.clear() }

type PolicyStore struct {
	db                     *sql.DB
	state                  string
	ConfigurationCommitted func(context.Context) error
}

func NewPolicyStore(db *sql.DB, state string) *PolicyStore { return &PolicyStore{db: db, state: state} }

type policyReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (p *PolicyStore) read(ctx context.Context, q policyReader) (Settings, []byte, error) {
	var s Settings
	var sealed []byte
	e := q.QueryRowContext(ctx, `SELECT backup_folder,include_artwork,passphrase_protected,passphrase_sealed FROM backup_settings WHERE singleton=1`).Scan(&s.BackupFolder, &s.IncludeArtwork, &s.PassphraseProtected, &sealed)
	s.PassphraseSet = len(sealed) > 0
	if s.BackupFolder == "" {
		s.BackupFolder = filepath.Join(p.state, BackupsDir)
	}
	return s, sealed, e
}
func (p *PolicyStore) ReadTx(ctx context.Context, tx *sql.Tx) (Settings, error) {
	s, _, e := p.read(ctx, tx)
	return s, e
}
func (p *PolicyStore) Settings(ctx context.Context) (Settings, error) {
	s, _, e := p.read(ctx, p.db)
	return s, e
}
func (p *PolicyStore) Snapshot(ctx context.Context) (Snapshot, error) {
	s, sealed, e := p.read(ctx, p.db)
	out := Snapshot{Settings: s}
	if e != nil {
		return out, e
	}
	if e = ValidateDirectory(p.state, s.BackupFolder); e != nil {
		return out, e
	}
	if s.PassphraseProtected {
		key, e := p.secretKey(false)
		if e != nil {
			return out, ErrInvalid
		}
		defer clear(key)
		a, e := sealAEAD(key)
		if e != nil {
			return out, e
		}
		// Reject pre-release sealed-passphrase blobs without opening/converting
		// them. A new write through Change supplies v2 key material.
		prefixBytes := len(policyMaterialMagic) + kdfSaltBytes
		if len(sealed) != prefixBytes+a.NonceSize()+32+a.Overhead() || !bytes.HasPrefix(sealed, policyMaterialMagic) {
			return out, ErrInvalid
		}
		prefix := sealed[:prefixBytes]
		nonce := sealed[prefixBytes : prefixBytes+a.NonceSize()]
		master, e := a.Open(nil, nonce, sealed[prefixBytes+a.NonceSize():], prefix)
		if e != nil {
			return out, ErrInvalid
		}
		out.material = &archiveKeyMaterial{key: master}
		copy(out.material.salt[:], prefix[len(policyMaterialMagic):])
	}
	return out, nil
}
func (p *PolicyStore) Update(ctx context.Context, patch SettingsPatch, authorize ...func(*sql.Tx) error) (Settings, error) {
	w, e := dbwork.Begin(ctx, p.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	if e != nil {
		return Settings{}, e
	}
	defer w.Rollback()
	for _, auth := range authorize {
		if auth == nil {
			return Settings{}, ErrInvalid
		}
		if e = auth(w.Tx()); e != nil {
			return Settings{}, e
		}
	}
	if _, _, protocol := usermutation.RequestFrom(ctx); protocol && len(authorize) == 0 {
		return Settings{}, ErrInvalid
	}
	r, e := usermutation.CheckContext(ctx, w.Tx(), time.Now().UnixMilli())
	if e != nil {
		return Settings{}, e
	}
	if r != nil {
		return p.ReadTx(ctx, w.Tx())
	}

	before, e := p.ReadTx(ctx, w.Tx())
	if e != nil {
		return Settings{}, e
	}
	out, e := p.UpdateTx(ctx, w.Tx(), patch)
	if e != nil {
		return out, e
	}
	if e = (Configuration{Store: p}).queueChange(ctx, w.Tx(), before, out); e != nil {
		return out, e
	}
	if e = usermutation.SaveContext(ctx, w.Tx(), usermutation.Result{Status: "applied", TargetID: "backups"}, time.Now().UnixMilli()); e != nil {
		return out, e
	}

	if e = w.Commit(); e != nil {
		return out, e
	}
	if p.ConfigurationCommitted != nil {
		e = p.ConfigurationCommitted(context.WithoutCancel(ctx))
	}
	return out, e
}

// prepareSettings checks the non-secret configuration without creating paths.
func (p *PolicyStore) prepareSettings(s Settings, patch SettingsPatch) (Settings, error) {
	if patch.BackupFolder != nil {
		s.BackupFolder = *patch.BackupFolder
		if s.BackupFolder == "" {
			s.BackupFolder = filepath.Join(p.state, BackupsDir)
		}
	}
	if e := validateDirectory(p.state, s.BackupFolder, false); e != nil {
		return s, e
	}
	if patch.IncludeArtwork != nil {
		s.IncludeArtwork = *patch.IncludeArtwork
	}
	if patch.PassphraseProtected != nil {
		s.PassphraseProtected = *patch.PassphraseProtected
	}
	return s, nil
}

func (p *PolicyStore) UpdateTx(ctx context.Context, tx *sql.Tx, patch SettingsPatch) (Settings, error) {
	s, sealed, e := p.read(ctx, tx)
	if e != nil {
		return s, e
	}
	s, e = p.prepareSettings(s, patch)
	if e != nil {
		return s, e
	}
	if patch.Passphrase != nil {
		if len(*patch.Passphrase) < 12 || len(*patch.Passphrase) > 1024 {
			return s, ErrInvalid
		}
		key, e := p.secretKey(true)
		if e != nil {
			return s, e
		}
		defer clear(key)
		a, e := sealAEAD(key)
		if e != nil {
			return s, e
		}
		nonce := make([]byte, a.NonceSize())
		if _, e = rand.Read(nonce); e != nil {
			return s, e
		}
		material, e := newArchiveKeyMaterial(*patch.Passphrase)
		if e != nil {
			return s, e
		}
		defer material.clear()
		prefix := append(append([]byte(nil), policyMaterialMagic...), material.salt[:]...)
		sealed = a.Seal(append(prefix, nonce...), nonce, material.key, prefix)
	}
	s.PassphraseSet = len(sealed) > 0
	if e = validateProtection(s); e != nil {
		return s, e
	}
	if !s.PassphraseProtected {
		sealed = nil
	}
	if e = ValidateDirectory(p.state, s.BackupFolder); e != nil {
		return s, e
	}
	folder := s.BackupFolder
	if folder == filepath.Join(p.state, BackupsDir) {
		folder = ""
	} // portable default
	_, e = tx.ExecContext(ctx, `UPDATE backup_settings SET backup_folder=?,include_artwork=?,passphrase_protected=?,passphrase_sealed=? WHERE singleton=1`, folder, s.IncludeArtwork, s.PassphraseProtected, sealed)
	if e == nil {
		_, e = tx.ExecContext(ctx, `INSERT INTO backup_destinations(path) VALUES(?) ON CONFLICT(path) DO NOTHING`, folder)
	}
	s.PassphraseSet = len(sealed) > 0
	return s, e
}
func sealAEAD(key []byte) (cipher.AEAD, error) {
	b, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	return cipher.NewGCM(b)
}
func (p *PolicyStore) secretKey(create bool) ([]byte, error) {
	if create {
		defer authorityfiles.Process.Admit()()
	}
	root := filepath.Join(p.state, "Keys")
	if e := canonicalDirectory(root, create); e != nil {
		return nil, e
	}
	name := filepath.Join(root, "backup-passphrase.key")
	if create {
		if _, e := os.Lstat(name); errors.Is(e, os.ErrNotExist) {
			key := make([]byte, 32)
			if _, e = rand.Read(key); e != nil {
				return nil, e
			}
			// UpdateTx is serialized by the same database write gate. Never replace
			// an installed key; publish a fully flushed new key atomically.
			e = atomicfile.Write(name, key, 0600)
			clear(key)
			if e != nil {
				return nil, e
			}
			if e = syncDirectory(root); e != nil {
				return nil, e
			}
		} else if e != nil {
			return nil, e
		}
	}

	info, e := os.Lstat(name)
	if e != nil || !info.Mode().IsRegular() || info.Size() != 32 || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return nil, ErrInvalid
	}
	f, e := os.Open(name)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	opened, e := f.Stat()
	if e != nil || !os.SameFile(info, opened) {
		return nil, ErrInvalid
	}
	key, e := io.ReadAll(io.LimitReader(f, 33))
	if len(key) != 32 {
		return nil, ErrInvalid
	}
	return key, e
}

// User-picked destinations must already exist and must not be data, metadata,
// cache, keys, a filesystem root, or a link. Only minted backup children are written.
func validateProtection(s Settings) error {
	if s.PassphraseProtected && !s.PassphraseSet {
		return ErrInvalid
	}
	return nil
}

func ValidateDirectory(state, dir string) error {
	return validateDirectory(state, dir, true)
}

func validateDirectory(state, dir string, create bool) error {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || filepath.Dir(dir) == dir {
		return ErrInvalid
	}
	if dir == filepath.Join(state, BackupsDir) {
		return inspectCanonicalDirectory(dir, create, true)
	}
	if e := canonicalDirectory(dir, false); e != nil {
		return ErrInvalid
	}
	rel, e := filepath.Rel(state, dir)
	if e == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))) {
		return ErrInvalid
	}
	rel, e = filepath.Rel(dir, state)
	if e == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))) {
		return ErrInvalid
	}
	return nil
}
func canonicalDirectory(dir string, create bool) error {
	return inspectCanonicalDirectory(dir, create, create)
}

// allowMissing permits the portable default folder before its first backup.
func inspectCanonicalDirectory(dir string, create, allowMissing bool) error {
	for p := dir; ; p = filepath.Dir(p) {
		info, e := os.Lstat(p)
		if e != nil {
			if !allowMissing || !errors.Is(e, os.ErrNotExist) {
				return e
			}
		} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return ErrInvalid
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	if create {
		return os.MkdirAll(dir, 0700)
	}
	return nil
}

// SetPolicyResolver is startup-only. Both manual and scheduled jobs consume it.
func (s *Service) SetPolicyResolver(resolve func(context.Context) (Snapshot, error)) {
	s.policy = resolve
}
func (s *Service) snapshot(ctx context.Context) (Snapshot, error) {
	if s.policy != nil {
		return s.policy(ctx)
	}
	return Snapshot{Settings: Settings{BackupFolder: s.backupRoot()}}, nil
}

// SetPolicyStore installs the normalized owner settings and the live policy.
func (s *Service) SetPolicyStore(p *PolicyStore) {
	s.settingsStore = p
	s.SetPolicyResolver(p.Snapshot)
}
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	if s.settingsStore == nil {
		return Settings{}, ErrInvalid
	}
	return s.settingsStore.Settings(ctx)
}
func (s *Service) UpdateSettings(ctx context.Context, patch SettingsPatch, authorize ...func(*sql.Tx) error) (Settings, error) {
	if s.settingsStore == nil {
		return Settings{}, ErrInvalid
	}
	return s.settingsStore.Update(ctx, patch, authorize...)
}

// CleanupDestinationPartials runs once at startup, before any job can start.
func CleanupDestinationPartials(state, directory string) error {
	if err := ValidateDirectory(state, directory); err != nil {
		return err
	}
	if err := cleanupPartialsDirectory(directory); err != nil {
		return err
	}
	return CleanupScratch(state)
}

// CleanupScratch is startup-only and removes only this domain's temporary names.
func CleanupScratch(state string) error {
	if e := canonicalDirectory(filepath.Join(state, "Temp"), true); e != nil {
		return e
	}
	if e := removeScratchNames(filepath.Join(state, "Temp"), []string{"backup-snapshot-", "backup-decrypt-"}); e != nil {
		return e
	}
	return removeScratchNames(filepath.Join(state, "Keys"), []string{".backup-passphrase.key."})
}
func removeScratchNames(path string, prefixes []string) error {
	root, e := openCanonicalRoot(path)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	defer root.Close()
	f, e := root.Open(".")
	if e != nil {
		return e
	}
	defer f.Close()
	for {
		entries, e := f.ReadDir(128)
		for _, entry := range entries {
			for _, prefix := range prefixes {
				if strings.HasPrefix(entry.Name(), prefix) {
					if err := root.RemoveAll(entry.Name()); err != nil {
						return err
					}
					break
				}
			}
		}
		if errors.Is(e, io.EOF) {
			return nil
		}
		if e != nil {
			return e
		}
	}
}

func (p *PolicyStore) Destinations(ctx context.Context) ([]string, error) {
	rows, e := p.db.QueryContext(ctx, `SELECT path FROM backup_destinations ORDER BY path`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	roots := []string{}
	for rows.Next() {
		var path string
		if e = rows.Scan(&path); e != nil {
			return nil, e
		}
		if path == "" {
			path = filepath.Join(p.state, BackupsDir)
		}
		roots = append(roots, path)
	}
	return roots, rows.Err()
}
func (s *Service) destinations(ctx context.Context) ([]string, error) {
	active := s.backupRoot()
	if s.settingsStore != nil {
		settings, e := s.settingsStore.Settings(ctx)
		if e != nil {
			return nil, e
		}
		active = settings.BackupFolder
	} else if s.policy != nil {
		snapshot, e := s.policy(ctx)
		if e != nil {
			return nil, e
		}
		defer snapshot.clear()
		active = snapshot.BackupFolder
	}
	roots := []string{active}
	extra := []string{filepath.Join(s.state, BackupsDir)}
	if s.settingsStore != nil {
		previous, e := s.settingsStore.Destinations(ctx)
		if e != nil {
			return nil, e
		}
		extra = append(extra, previous...)
	}
	seen := map[string]bool{active: true}
	for _, path := range extra {
		if !seen[path] {
			roots = append(roots, path)
			seen[path] = true
		}
	}
	return roots, nil
}

func openCanonicalRoot(path string) (*os.Root, error) {
	if e := canonicalDirectory(path, false); e != nil {
		return nil, e
	}
	before, e := os.Lstat(path)
	if e != nil || !before.IsDir() {
		return nil, ErrInvalid
	}
	root, e := os.OpenRoot(path)
	if e != nil {
		return nil, e
	}
	after, e := root.Stat(".")
	if e != nil || !os.SameFile(before, after) {
		root.Close()
		return nil, ErrInvalid
	}
	return root, nil
}
