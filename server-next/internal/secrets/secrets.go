// Package secrets seals what the server must keep but never show: a remote source's password,
// the owner's provider keys. A sealed value is kept in the database; the key that opens it is a
// file beside the database, not in it, so a copy of the database alone, sent with a support
// request, opens nothing.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// keyFile holds the key, made the first time the server starts, readable by the server alone.
const keyFile = "secrets.key"

// ErrCannotOpen: a sealed value was sealed with another key (the key file was lost or
// replaced) or for something else. Whoever stored it must enter it again.
var ErrCannotOpen = errors.New("this secret cannot be opened with this server's key")

// Box seals and opens values with the server's key.
type Box struct{ aead cipher.AEAD }

// Open opens the box in dataDir, making its key the first time.
func Open(dataDir string) (*Box, error) {
	name := filepath.Join(dataDir, keyFile)
	key, err := os.ReadFile(name)
	if errors.Is(err, fs.ErrNotExist) {
		key = make([]byte, 32)
		rand.Read(key)
		var f *os.File
		if f, err = os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600); err == nil {
			_, err = f.Write(key)
			err = errors.Join(err, f.Sync(), f.Close())
		} else if errors.Is(err, fs.ErrExist) {
			key, err = os.ReadFile(name) // made a moment ago by another part of the server
		}
	}
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("%s is not a key Portico made", name)
	}
	block, _ := aes.NewCipher(key)
	aead, _ := cipher.NewGCM(block)
	return &Box{aead: aead}, nil
}

// Seal seals plain to what it belongs to (a source's public id, a setting's name), so it
// cannot be moved to another and opened there.
func (b *Box) Seal(belongsTo string, plain []byte) []byte {
	nonce := make([]byte, b.aead.NonceSize())
	rand.Read(nonce)
	return b.aead.Seal(nonce, nonce, plain, []byte(belongsTo))
}

// Open opens what Seal sealed for the same owner.
func (b *Box) Open(belongsTo string, sealed []byte) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(sealed) < n {
		return nil, ErrCannotOpen
	}
	plain, err := b.aead.Open(nil, sealed[:n], sealed[n:], []byte(belongsTo))
	if err != nil {
		return nil, ErrCannotOpen
	}
	return plain, nil
}
