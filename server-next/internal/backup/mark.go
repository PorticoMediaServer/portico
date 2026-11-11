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

// markKey reads this server's key, making it the first time.
func markKey(dataDir string) ([]byte, error) {
	name := filepath.Join(dataDir, markKeyFile)
	key, err := os.ReadFile(name)
	if errors.Is(err, fs.ErrNotExist) {
		key = make([]byte, 32)
		rand.Read(key)
		var f *os.File
		if f, err = os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600); err == nil {
			_, err = f.Write(key)
			err = errors.Join(err, f.Sync(), f.Close())
		} else if errors.Is(err, fs.ErrExist) {
			key, err = os.ReadFile(name)
		}
	}
	if err == nil && len(key) != 32 {
		err = errors.New(name + " is not a key Portico made")
	}
	return key, err
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
