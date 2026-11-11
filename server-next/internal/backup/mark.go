package backup

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/PorticoMediaServer/portico/server-next/internal/dirsync"
)

// The mark. Each backup's description carries a mark made over its archive with a key kept in
// this server's data folder (never in a backup). A backup whose mark is missing, or was not
// made by this server's key, or whose archive changed since, is not refused: restoring onto
// a new machine after a loss is what backups are for. The owner is warned plainly instead,
// and must confirm, because whoever could write the backup folder could have put their own
// accounts in it.

// markKeyFile holds the key. It is not a credential of anything else and travels nowhere.
const markKeyFile = "backup-mark.key"

// ErrNotMadeHere: the backup's mark is missing, was made by another server, or the archive
// changed since; restoring it needs the owner's confirmation.
var ErrNotMadeHere = errors.New("this backup wasn't made by this server, or has been changed since")

// markKey reads this server's key, making it the first time. The key is written whole: to a
// file of its own in the data folder, synced, then put in place, so a full disk or a stop
// part-way leaves no half-written key. A key that cannot be read is an error that says so;
// backups are still made without a mark, and a restore goes on once the owner confirms it.
func markKey(dataDir string) ([]byte, error) {
	name := filepath.Join(dataDir, markKeyFile)
	key, err := os.ReadFile(name)
	if errors.Is(err, fs.ErrNotExist) {
		key, err = makeMarkKey(dataDir, name)
	}
	if err == nil && len(key) != 32 {
		err = errors.New("it is not a key Portico made")
	}
	if err != nil {
		return nil, &ErrMarkKey{name, err}
	}
	return key, nil
}

func makeMarkKey(dataDir, name string) ([]byte, error) {
	key := make([]byte, 32)
	rand.Read(key)
	f, err := os.CreateTemp(dataDir, markKeyFile+".new-")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(key)
	if err = errors.Join(err, f.Sync(), f.Close()); err != nil {
		return nil, err
	}
	// Put in place only where no key is yet: two backups starting at once keep the one key.
	if err := os.Link(f.Name(), name); errors.Is(err, fs.ErrExist) {
		return os.ReadFile(name)
	} else if err != nil {
		return nil, err
	}
	return key, dirsync.Sync(dataDir)
}

// ErrMarkKey says this server's mark key cannot be read, and why.
type ErrMarkKey struct {
	Name string
	Err  error
}

func (e *ErrMarkKey) Error() string {
	return "this server's backup key " + e.Name + " can't be read (" + e.Err.Error() + "), so backups are made without a mark " +
		"and a restore can't tell whether a backup was made here"
}

func (e *ErrMarkKey) Unwrap() error { return e.Err }

// MarkKeyProblem says in plain words why this server's mark key cannot be read, "" when it can.
func MarkKeyProblem(dataDir string) string {
	if _, err := markKey(dataDir); err != nil {
		return err.Error()
	}
	return ""
}

// marker hashes an archive's bytes as they pass, to make or check its mark.
type marker struct{ h hash.Hash }

func newMarker(key []byte) *marker { return &marker{hmac.New(sha256.New, key)} }

func (m *marker) Write(p []byte) (int, error) { return m.h.Write(p) }

func (m *marker) mark() string { return hex.EncodeToString(m.h.Sum(nil)) }

// markOf reads a whole archive and answers its mark.
func markOf(key []byte, archivePath string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	m := newMarker(key)
	if _, err := io.Copy(m, f); err != nil {
		return "", err
	}
	return m.mark(), nil
}

// statedMark is the mark a backup's description gives: the .json beside the archive, "" when
// there is none.
func statedMark(archivePath string) string {
	raw, err := os.ReadFile(strings.TrimSuffix(archivePath, ".tar.gz") + ".json")
	if err != nil {
		return ""
	}
	var b Backup
	if json.Unmarshal(raw, &b) != nil {
		return ""
	}
	return b.Mark
}

// MadeHere says whether the archive at archivePath carries this server's mark and is unchanged
// since it was made.
func MadeHere(dataDir, archivePath string) bool {
	key, err := markKey(dataDir)
	if err != nil {
		return false
	}
	stated := statedMark(archivePath)
	if stated == "" {
		return false
	}
	got, err := markOf(key, archivePath)
	return err == nil && hmac.Equal([]byte(got), []byte(stated))
}
