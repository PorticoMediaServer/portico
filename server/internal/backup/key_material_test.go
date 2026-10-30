package backup

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestPolicySealsOnlyDerivedMasterAndAuthenticatesPublicSalt(t *testing.T) {
	s, p, _ := policyFixture(t)
	password := "derived material private fixture phrase"
	ctx := context.Background()
	if _, err := p.Update(ctx, SettingsPatch{PassphraseProtected: ptr(true), Passphrase: &password}); err != nil {
		t.Fatal(err)
	}
	var blob []byte
	if err := s.db.QueryRow(`SELECT passphrase_sealed FROM backup_settings`).Scan(&blob); err != nil {
		t.Fatal(err)
	}
	key, err := p.secretKey(false)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(key)
	a, err := sealAEAD(key)
	if err != nil {
		t.Fatal(err)
	}
	prefixSize := len(policyMaterialMagic) + kdfSaltBytes
	if !bytes.HasPrefix(blob, policyMaterialMagic) || len(blob) != prefixSize+a.NonceSize()+32+a.Overhead() {
		t.Fatal("wrong derived material storage format")
	}
	master, err := a.Open(nil, blob[prefixSize:prefixSize+a.NonceSize()], blob[prefixSize+a.NonceSize():], blob[:prefixSize])
	if err != nil {
		t.Fatal(err)
	}
	defer clear(master)
	expected := deriveMaster(password, blob[len(policyMaterialMagic):prefixSize])
	defer clear(expected)
	if len(master) != 32 || !bytes.Equal(master, expected) || bytes.Contains(master, []byte(password)) {
		t.Fatal("policy did not seal only its derived master")
	}
	// The public KDF salt is authenticated; corruption must not silently change
	// how subsequent archives derive their restore key.
	damaged := bytes.Clone(blob)
	damaged[len(policyMaterialMagic)] ^= 1
	if _, err = dbwork.ExecWrite(ctx, s.db, dbwork.ClassMaintenance, `UPDATE backup_settings SET passphrase_sealed=?`, damaged); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Snapshot(ctx); !errors.Is(err, ErrInvalid) {
		t.Fatal("unauthenticated policy salt accepted", err)
	}
}

func TestPreReleaseSealedPassphraseFailsClosedUntilExplicitChange(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, p, state := policyFixture(t)
	ctx := context.Background()
	password := "old prerelease fixture phrase"
	key, err := p.secretKey(true)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(key)
	a, err := sealAEAD(key)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, a.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	old := a.Seal(nonce, nonce, []byte(password), []byte("portico backup passphrase v1"))
	if _, err = dbwork.ExecWrite(ctx, s.db, dbwork.ClassMaintenance, `UPDATE backup_settings SET passphrase_protected=1,passphrase_sealed=?`, old); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Snapshot(ctx); !errors.Is(err, ErrInvalid) {
		t.Fatal("old policy was opened/converted", err)
	}
	id, err := s.Start(ctx, KindScheduled, "old-policy-refusal")
	if err != nil {
		t.Fatal(err)
	}
	job, ok := s.Job(id)
	if !ok {
		t.Fatal("missing scheduled job")
	}
	select {
	case <-job.done:
	case <-time.After(10 * time.Second):
		t.Fatal("invalid old policy did not stop scheduled backup")
	}
	deadline := time.Now().Add(10 * time.Second)
	for s.LastBackup() == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if result := s.LastBackup(); result == nil || result.Outcome != "failed" || result.ErrorCode == "" {
		t.Fatal("old policy did not use existing failure result")
	}
	var unchanged []byte
	if err = s.db.QueryRow(`SELECT passphrase_sealed FROM backup_settings`).Scan(&unchanged); err != nil || !bytes.Equal(old, unchanged) {
		t.Fatal("old fixture policy migrated", err)
	}
	if listed, err := s.List(); err != nil || len(listed) != 0 {
		t.Fatal("failed policy published archive", err)
	}
	assertNoScratch(t, state, filepath.Join(state, BackupsDir))
	replacement := "explicit Change replacement fixture phrase"
	if _, err = p.Update(ctx, SettingsPatch{Passphrase: &replacement}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Create(ctx, KindScheduled); err != nil {
		t.Fatal("explicit Change did not repair protected backups", err)
	}
}

func TestEnvelopeV2IndependentKeysSaltAuthenticationAndV1Refusal(t *testing.T) {
	password := "v2 envelope private fixture phrase"
	material, err := newArchiveKeyMaterial(password)
	if err != nil {
		t.Fatal(err)
	}
	defer material.clear()
	payload := []byte("private authenticated fixture bytes")
	encode := func() []byte {
		var b bytes.Buffer
		w, err := newEncryptedWriter(&b, material)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write(payload); err != nil {
			t.Fatal(err)
		}
		if err = w.Close(); err != nil {
			t.Fatal(err)
		}
		return b.Bytes()
	}
	first, second := encode(), encode()
	publicEnd := len(envelopeMagic) + kdfSaltBytes
	headerEnd := publicEnd + archiveSaltBytes
	if !bytes.Equal(first[:publicEnd], second[:publicEnd]) || bytes.Equal(first[publicEnd:headerEnd], second[publicEnd:headerEnd]) || bytes.Equal(first[headerEnd:], second[headerEnd:]) {
		t.Fatal("archives did not derive independent encryption keys")
	}
	for _, offset := range []int{len(envelopeMagic), publicEnd} {
		damaged := bytes.Clone(first)
		damaged[offset] ^= 1
		r, err := newEncryptedReader(bytes.NewReader(damaged), password)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.ReadAll(r); !errors.Is(err, ErrIntegrity) {
			t.Fatal("modified public salt authenticated", err)
		}
	}
	// A genuinely authenticated old envelope is still unsupported: no legacy
	// key derivation or compatibility reader exists in production.
	salt := bytes.Repeat([]byte{7}, 16)
	oldKey := deriveMaster(password, salt)
	defer clear(oldKey)
	oldAEAD, err := sealAEAD(oldKey)
	if err != nil {
		t.Fatal(err)
	}
	oldHeader := append([]byte("PORTICO-BACKUP-ENC\x00\x01"), salt...)
	var old bytes.Buffer
	old.Write(oldHeader)
	oldWriter := encryptedWriter{out: &old, aead: oldAEAD, header: oldHeader}
	if _, err = oldWriter.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err = oldWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = newEncryptedReader(bytes.NewReader(old.Bytes()), password); !errors.Is(err, ErrSourceInvalid) {
		t.Fatal("v1 envelope restore accepted", err)
	}
	s, _, state := policyFixture(t)
	file := filepath.Join(t.TempDir(), "old.pba")
	if err = os.WriteFile(file, old.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	before := fileHashOrEmpty(t, filepath.Join(state, DatabaseName))
	if _, err = s.InspectSource(context.Background(), Source{Path: file}); !errors.Is(err, ErrSourceInvalid) {
		t.Fatal("v1 inspection accepted", err)
	}
	if _, err = s.StageWithPassphrase(context.Background(), Source{Path: file}, password); err == nil {
		t.Fatal("v1 source staged")
	}
	if before != fileHashOrEmpty(t, filepath.Join(state, DatabaseName)) {
		t.Fatal("v1 refusal touched live database")
	}
	if _, err = os.Stat(filepath.Join(state, RestoreStagedDir)); !os.IsNotExist(err) {
		t.Fatal("v1 refusal touched staging", err)
	}
	assertNoScratch(t, state, filepath.Join(state, BackupsDir))
}

func TestChangingAndDisablingProtectionPreservesExistingArchives(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, p, state := policyFixture(t)
	ctx := context.Background()
	firstPhrase := "first immutable archive fixture phrase"
	secondPhrase := "second immutable archive fixture phrase"
	if _, err := p.Update(ctx, SettingsPatch{PassphraseProtected: ptr(true), Passphrase: &firstPhrase}); err != nil {
		t.Fatal(err)
	}
	first, err := s.Create(ctx, KindScheduled)
	if err != nil {
		t.Fatal(err)
	}
	firstFile := filepath.Join(state, BackupsDir, first, encryptedPayload)
	firstHash := fileHashOrEmpty(t, firstFile)
	if _, err = p.Update(ctx, SettingsPatch{Passphrase: &secondPhrase}); err != nil {
		t.Fatal(err)
	}
	second, err := s.Create(ctx, KindManual)
	if err != nil {
		t.Fatal(err)
	}
	secondFile := filepath.Join(state, BackupsDir, second, encryptedPayload)
	secondHash := fileHashOrEmpty(t, secondFile)
	if _, err = p.Update(ctx, SettingsPatch{PassphraseProtected: ptr(false)}); err != nil {
		t.Fatal(err)
	}
	third, err := s.Create(ctx, KindScheduled)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.InspectSource(ctx, Source{BackupID: third}); err != nil || got.Encrypted {
		t.Fatal("new backup did not become unprotected", err)
	}
	for _, test := range []struct{ path, phrase, wrong string }{{firstFile, firstPhrase, secondPhrase}, {secondFile, secondPhrase, firstPhrase}} {
		raw, err := os.ReadFile(test.path)
		if err != nil {
			t.Fatal(err)
		}
		r, err := newEncryptedReader(bytes.NewReader(raw), test.phrase)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.Copy(io.Discard, r); err != nil {
			t.Fatal("existing archive lost its original protection", err)
		}
		r, err = newEncryptedReader(bytes.NewReader(raw), test.wrong)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.Copy(io.Discard, r); !errors.Is(err, ErrIntegrity) {
			t.Fatal("replacement phrase opened prior archive", err)
		}
	}
	if firstHash != fileHashOrEmpty(t, firstFile) || secondHash != fileHashOrEmpty(t, secondFile) {
		t.Fatal("Change/off rewrote an existing archive")
	}
	var sealed []byte
	if err = s.db.QueryRow(`SELECT passphrase_sealed FROM backup_settings`).Scan(&sealed); err != nil || len(sealed) != 0 {
		t.Fatal("off retained derived key material", err)
	}
	assertNoScratch(t, state, filepath.Join(state, BackupsDir))
}
