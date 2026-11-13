package backup

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server-next/internal/atomicfile"
	"github.com/PorticoMediaServer/portico/server-next/internal/dirsync"
)

// Unattended backups retain only the salted derived key, never the passphrase.
// This file is deliberately outside the backup's allowed keyFiles list.
const protectionFile = "backup-passphrase.key"

func (s *Service) Protected() bool {
	_, err := os.Stat(filepath.Join(s.DataDir, protectionFile))
	return !errors.Is(err, fs.ErrNotExist)
}

// SetPassphrase changes future backups only. Old ones still need their old phrase.
// An empty value removes protection; no API returns either the phrase or key.
func (s *Service) SetPassphrase(phrase string) error {
	if len(phrase) > 1024 {
		return errors.New("use a passphrase of at most 1024 bytes")
	}
	path := filepath.Join(s.DataDir, protectionFile)
	if phrase == "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return dirsync.Sync(s.DataDir)
	}
	material, err := newArchiveKeyMaterial(phrase)
	if err != nil {
		return err
	}
	defer material.clear()
	raw := append(material.salt[:], material.key...)
	defer clear(raw)
	return atomicfile.Write(path, raw, 0o600)
}

func (s *Service) archiveProtection() (*archiveKeyMaterial, error) {
	raw, err := os.ReadFile(filepath.Join(s.DataDir, protectionFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("the backup protection key cannot be read; set the passphrase again")
	}
	defer clear(raw)
	if len(raw) != kdfSaltBytes+32 {
		return nil, errors.New("the backup protection key is damaged; set the passphrase again")
	}
	material := &archiveKeyMaterial{key: append([]byte(nil), raw[kdfSaltBytes:]...)}
	copy(material.salt[:], raw[:kdfSaltBytes])
	return material, nil
}

func backupReader(source io.Reader, phrase string) (io.Reader, error) {
	buffered := bufio.NewReader(source)
	prefix, _ := buffered.Peek(len(envelopeMagic))
	if bytes.HasPrefix(prefix, envelopeMagic[:len(envelopeMagic)-1]) {
		return newEncryptedReader(buffered, phrase)
	}
	return buffered, nil
}
