package cursor

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

const keyFile = "cursor-signing-key.json"

type diskKeyring struct {
	Current       string `json:"current"`
	CreatedAt     int64  `json:"createdAt"`
	Previous      string `json:"previous,omitempty"`
	PreviousUntil int64  `json:"previousUntil,omitempty"`
}

// NewStateSigner keeps cursor signatures stable across server restarts. The
// signing key is rotated by RotateIfDue from background maintenance; an
// earlier key remains usable for its maximum cursor lifetime. The state
// directory must already exist and be private to the server process.
func NewStateSigner(stateDir string, ttl, rotateEvery time.Duration) (*Signer, error) {
	if ttl <= 0 || ttl > 24*time.Hour || rotateEvery < ttl || rotateEvery <= 0 {
		return nil, errors.New("cursor rotation interval must cover cursor lifetime")
	}
	path := filepath.Join(stateDir, keyFile)
	ring, err := readOrCreateKeyring(path)
	if err != nil {
		return nil, err
	}
	key, err := base64.RawURLEncoding.DecodeString(ring.Current)
	if err != nil || len(key) != 32 || ring.CreatedAt <= 0 {
		return nil, errors.New("cursor signing key is invalid")
	}
	s, err := New(key, ttl)
	if err != nil {
		return nil, err
	}
	s.statePath = path
	s.createdAt = time.Unix(0, ring.CreatedAt)
	s.rotateEvery = rotateEvery
	if ring.Previous != "" {
		s.previous, err = base64.RawURLEncoding.DecodeString(ring.Previous)
		if err != nil || len(s.previous) != 32 || ring.PreviousUntil <= 0 {
			return nil, errors.New("previous cursor signing key is invalid")
		}
		s.previousUntil = time.Unix(0, ring.PreviousUntil)
	}
	return s, nil
}

// Rotate invalidates no unexpired cursor signed by the current key. It is
// intended for operator-directed key rotation. It never runs from a request.
func (s *Signer) Rotate() error {
	return s.rotate(false)
}

// RotateIfDue is called by background maintenance. A failed disk write keeps
// the current in-memory key, so paging continues while the operator repairs
// the state directory.
func (s *Signer) RotateIfDue() error {
	return s.rotate(true)
}

func (s *Signer) rotate(onlyIfDue bool) error {
	s.rotateMu.Lock()
	defer s.rotateMu.Unlock()
	s.mu.RLock()
	path, createdAt, rotateEvery := s.statePath, s.createdAt, s.rotateEvery
	current, previousUntil := s.key, s.previousUntil
	previousLive := len(s.previous) != 0 && s.now().Before(previousUntil)
	s.mu.RUnlock()
	if path == "" {
		return errors.New("cursor signer has no durable key source")
	}
	if onlyIfDue && s.now().Before(createdAt.Add(rotateEvery)) {
		return nil
	}
	if previousLive {
		if onlyIfDue {
			return nil
		}
		return errors.New("previous cursor signing key still has live cursors")
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	now := s.now()
	ring := diskKeyring{
		Current:       base64.RawURLEncoding.EncodeToString(key),
		CreatedAt:     now.UnixNano(),
		Previous:      base64.RawURLEncoding.EncodeToString(current),
		PreviousUntil: now.Add(s.ttl).UnixNano(),
	}
	if err := replaceKeyring(path, ring); err != nil {
		return err
	}
	s.mu.Lock()
	s.previous, s.previousUntil = current, time.Unix(0, ring.PreviousUntil)
	s.key, s.createdAt = key, now
	s.mu.Unlock()
	return nil
}

func readOrCreateKeyring(path string) (diskKeyring, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		key := make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return diskKeyring{}, err
		}
		ring := diskKeyring{Current: base64.RawURLEncoding.EncodeToString(key), CreatedAt: time.Now().UnixNano()}
		raw, err := json.Marshal(ring)
		if err != nil {
			return diskKeyring{}, err
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			if _, err = file.Write(raw); err == nil {
				err = file.Sync()
			}
			if closeErr := file.Close(); err == nil {
				err = closeErr
			}
			if err != nil {
				_ = os.Remove(path)
			}
			return ring, err
		}
		if !os.IsExist(err) {
			return diskKeyring{}, err
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return diskKeyring{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return diskKeyring{}, errors.New("cursor signing key is not a private regular file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return diskKeyring{}, err
	}
	var ring diskKeyring
	if err = json.Unmarshal(raw, &ring); err != nil {
		return diskKeyring{}, err
	}
	return ring, nil
}

func replaceKeyring(path string, ring diskKeyring) error {
	raw, err := json.Marshal(ring)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, ".cursor-key-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(raw); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
