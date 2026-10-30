package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/authorityfiles"
	"github.com/PorticoMediaServer/portico/server/internal/configfile"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
)

func TestManifestStreamTraversesWithoutRetainingAllEntries(t *testing.T) {
	dir := t.TempDir()
	w, err := newStreamManifestWriter(filepath.Join(dir, ManifestName), Manifest{Format: Format, Version: StreamVersion})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(nil)
	for n := 0; n < 1025; n++ {
		if err = w.add(FileEntry{Name: fmt.Sprintf("Keys/key-%04d", n), SHA256: hex.EncodeToString(sum[:])}); err != nil {
			t.Fatal(err)
		}
	}
	if err = w.finish(); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Files) != 0 {
		t.Fatal("stream accumulated a file registry")
	}
	count := 0
	var damage []ManifestDamage
	if err = scanStream(t.Context(), m, func(FileEntry, streamPosition) error { count++; return nil }, func(d ManifestDamage) { damage = append(damage, d) }); err != nil {
		t.Fatal(err)
	}
	if count != 1025 || len(damage) != 0 {
		t.Fatalf("entries=%d damage=%+v", count, damage)
	}
}

func TestStreamRestoreReportsExactDamagedLineAndRecoversLaterFiles(t *testing.T) {
	s, state, _ := testService(t)
	for _, name := range []string{"Metadata/first.txt", "Metadata/second.txt", "Keys/intact.key"} {
		if err := writeTestFile(filepath.Join(state, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	id, err := s.Create(t.Context(), KindManual)
	if err != nil {
		t.Fatal(err)
	}
	folder := storagepaths.Path(state, BackupsDir, id)
	raw, err := os.ReadFile(filepath.Join(folder, ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.SplitAfter(raw, []byte("\n"))
	var offset, start, end, line int64
	for n, b := range lines {
		if bytes.Contains(b, []byte("Metadata/first.txt")) {
			start = offset
			end = offset + int64(len(b))
			line = int64(n + 1)
			lines[n] = append(bytes.Repeat([]byte{'!'}, len(b)-1), '\n')
			break
		}
		offset += int64(len(b))
	}
	if line < 1 {
		t.Fatal("fixture entry missing")
	}
	if err = os.WriteFile(filepath.Join(folder, ManifestName), bytes.Join(lines, nil), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := s.Stage(t.Context(), Source{BackupID: id})
	if err != nil {
		t.Fatal(err)
	}
	if result.Validation.Recovery == nil || result.Validation.Recovery.ManifestDamage == nil {
		t.Fatal("damage was not reported")
	}
	report := result.Validation.Recovery.ManifestDamage
	found := false
	for _, d := range report.Regions {
		if d.FirstLine == line && d.StartByte == start && d.EndByte == end && d.Reason == "unreadable_record" {
			found = true
		}
	}
	if !found {
		t.Fatalf("wrong precise report: %+v", report)
	}
	staged := storagepaths.Path(state, RestoreStagedDir)
	if _, err = os.Stat(filepath.Join(staged, "Metadata/first.txt")); !os.IsNotExist(err) {
		t.Fatal("unverifiable file installed", err)
	}
	for _, name := range []string{"Metadata/second.txt", "Keys/intact.key"} {
		got, err := os.ReadFile(filepath.Join(staged, name))
		if err != nil || string(got) != name {
			t.Fatal("readable file was lost", name, err)
		}
	}
}

func TestStreamRestoreNeverBlessesMismatchedKeys(t *testing.T) {
	s, state, _ := testService(t)
	name := "Keys/private.key"
	if err := writeTestFile(filepath.Join(state, name), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	id, err := s.Create(t.Context(), KindManual)
	if err != nil {
		t.Fatal(err)
	}
	folder := storagepaths.Path(state, BackupsDir, id)
	if err = os.WriteFile(filepath.Join(folder, name), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := s.Stage(t.Context(), Source{BackupID: id})
	if err != nil {
		t.Fatal(err)
	}
	r := out.Validation.Recovery.ManifestDamage
	if r.MissingFiles != 1 || r.Count != 1 || r.Regions[0].Reason != "payload_hash_mismatch" {
		t.Fatalf("wrong missing-key report: %+v", r)
	}
	if _, err = os.Stat(storagepaths.Path(state, RestoreStagedDir, name)); !os.IsNotExist(err) {
		t.Fatal("changed key was blessed", err)
	}
}

func TestStreamRestoreRecoversReadableDatabaseWithExplicitHashDamage(t *testing.T) {
	s, state, db := testService(t)
	if _, err := db.Exec(`INSERT INTO configuration(key,value) VALUES('stream_damage','ORIGINAL_STREAM_VALUE')`); err != nil {
		t.Fatal(err)
	}
	id, err := s.Create(t.Context(), KindManual)
	if err != nil {
		t.Fatal(err)
	}
	folder := storagepaths.Path(state, BackupsDir, id)
	raw, err := os.ReadFile(filepath.Join(folder, DatabaseName))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(raw, []byte("ORIGINAL_STREAM_VALUE")) != 1 {
		t.Fatal("fixture value missing")
	}
	raw = bytes.Replace(raw, []byte("ORIGINAL_STREAM_VALUE"), []byte("MODIFIED_STREAM_VALUE"), 1)
	if err = os.WriteFile(filepath.Join(folder, DatabaseName), raw, 0600); err != nil {
		t.Fatal(err)
	}
	out, err := s.Stage(t.Context(), Source{BackupID: id})
	if err != nil {
		t.Fatal(err)
	}
	r := out.Validation.Recovery.ManifestDamage
	if !r.UnverifiedDatabase || r.Regions[0].Reason != "database_hash_mismatch" {
		t.Fatalf("unreported database integrity recovery: %+v", r)
	}
}

func TestStreamRestoreCursorResumesAfterDurableFileMove(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := stageAfterWrite(t)
	previous := applyStep
	defer func() { applyStep = previous }()
	applyStep = func(step string) error {
		if step == "stream:switch:"+DatabaseName {
			return errCrash
		}
		return nil
	}
	if err := ApplyStaged(t.Context(), f.state, nil); !errors.Is(err, errCrash) {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(storagepaths.Path(f.state, RestoreStagedDir, RestoreMarkerFile))
	if err != nil {
		t.Fatal(err)
	}
	var marker restoreMarker
	if err = json.Unmarshal(raw, &marker); err != nil {
		t.Fatal(err)
	}
	if marker.ManifestOffset <= 0 || marker.ManifestLine < 2 {
		t.Fatalf("no durable cursor: %+v", marker)
	}
	if err = restart(t, f.state, nil); err != nil {
		t.Fatal(err)
	}
	if probe(t, storagepaths.Path(f.state, DatabaseName)) != "backup" {
		t.Fatal("resumed wrong database")
	}
}

func TestStreamManifestDamageSummaryBoundAndEOFConvention(t *testing.T) {
	dir := t.TempDir()
	w, err := newStreamManifestWriter(filepath.Join(dir, ManifestName), Manifest{Format: Format, Version: StreamVersion})
	if err != nil {
		t.Fatal(err)
	}
	if err = w.finish(); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, ManifestName), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 25; n++ {
		if _, err = f.WriteString("broken\n"); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	report := &persistence.ManifestRecovery{}
	if err = scanStream(context.Background(), m, func(FileEntry, streamPosition) error { return nil }, func(d ManifestDamage) { addManifestDamage(report, d) }); err != nil {
		t.Fatal(err)
	}
	if report.Count != 25 || len(report.Regions) != 20 || !report.RegionsTruncated {
		t.Fatalf("wrong bounded summary: %+v", report)
	}
}

func TestStreamRestoreKeepsReadableKeyWhenOnlyItsChainChecksumIsDamaged(t *testing.T) {
	s, state, _ := testService(t)
	name := "Keys/chain-damage.key"
	if err := writeTestFile(filepath.Join(state, name), []byte("intact key bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	id, err := s.Create(t.Context(), KindManual)
	if err != nil {
		t.Fatal(err)
	}
	folder := storagepaths.Path(state, BackupsDir, id)
	raw, err := os.ReadFile(filepath.Join(folder, ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.SplitAfter(raw, []byte("\n"))
	changed := false
	for n, line := range lines {
		var record streamRecord
		if json.Unmarshal(line, &record) != nil || record.File == nil || record.File.Name != name {
			continue
		}
		if record.SHA256[0] == '0' {
			record.SHA256 = "1" + record.SHA256[1:]
		} else {
			record.SHA256 = "0" + record.SHA256[1:]
		}
		encoded, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		lines[n] = append(encoded, '\n')
		changed = true
		break
	}
	if !changed {
		t.Fatal("fixture key entry missing")
	}
	if err = os.WriteFile(filepath.Join(folder, ManifestName), bytes.Join(lines, nil), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := s.Stage(t.Context(), Source{BackupID: id})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(storagepaths.Path(state, RestoreStagedDir, name))
	if err != nil || string(got) != "intact key bytes" {
		t.Fatal("readable verified key was lost", err)
	}
	report := out.Validation.Recovery.ManifestDamage
	found := false
	for _, region := range report.Regions {
		found = found || region.Reason == "entry_hash_mismatch"
	}
	if !found || report.MissingFiles != 0 {
		t.Fatalf("chain damage not reported honestly: %+v", report)
	}
}

func TestStreamRestoreFindsReadableRecordsAfterDamagedHeaderAndFirstEntry(t *testing.T) {
	s, state, _ := testService(t)
	name := "Keys/after-damaged-header.key"
	if err := writeTestFile(filepath.Join(state, name), []byte("verified later key"), 0600); err != nil {
		t.Fatal(err)
	}
	id, err := s.Create(t.Context(), KindManual)
	if err != nil {
		t.Fatal(err)
	}
	folder := storagepaths.Path(state, BackupsDir, id)
	raw, err := os.ReadFile(filepath.Join(folder, ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.SplitAfter(raw, []byte("\n"))
	if len(lines) < 4 {
		t.Fatal("fixture manifest too short")
	}
	for n := 0; n < 2; n++ {
		lines[n] = append(bytes.Repeat([]byte{'!'}, len(lines[n])-1), '\n')
	}
	if err = os.WriteFile(filepath.Join(folder, ManifestName), bytes.Join(lines, nil), 0600); err != nil {
		t.Fatal(err)
	}
	if info, err := s.InspectSource(t.Context(), Source{BackupID: id}); err != nil || info.Encrypted {
		t.Fatal("readable damaged plaintext source was not inspectable", err)
	}
	out, err := s.Stage(t.Context(), Source{BackupID: id})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(storagepaths.Path(state, RestoreStagedDir, name)); err != nil || string(got) != "verified later key" {
		t.Fatal("later intact record was discarded", err)
	}
	if out.Validation.Recovery == nil || !out.Validation.Recovery.ManifestDamage.UnverifiedDatabase {
		t.Fatal("missing database expectation was not disclosed")
	}
}

func TestStreamManifestMissingEndReportsNextLineAndExactEOFByte(t *testing.T) {
	folder := t.TempDir()
	writer, err := newStreamManifestWriter(filepath.Join(folder, ManifestName), Manifest{Format: Format, Version: StreamVersion})
	if err != nil {
		t.Fatal(err)
	}
	if err = writer.finish(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(folder, ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	raw = raw[:bytes.IndexByte(raw, '\n')+1]
	if err = os.WriteFile(filepath.Join(folder, ManifestName), raw, 0600); err != nil {
		t.Fatal(err)
	}
	manifest, err := readManifest(folder)
	if err != nil {
		t.Fatal(err)
	}
	var regions []ManifestDamage
	if err = scanStream(t.Context(), manifest, func(FileEntry, streamPosition) error { return nil }, func(region ManifestDamage) { regions = append(regions, region) }); err != nil {
		t.Fatal(err)
	}
	if len(regions) != 1 || regions[0].Reason != "missing_end_record" || regions[0].FirstLine != 2 || regions[0].LastLine != 2 || regions[0].StartByte != int64(len(raw)) || regions[0].EndByte != int64(len(raw)) {
		t.Fatalf("incorrect EOF convention: %+v", regions)
	}
}

func TestManifestStreamAuthoritySetAndPayloadEquality(t *testing.T) {
	live, carried := t.TempDir(), t.TempDir()
	for n := 0; n < 257; n++ {
		name := fmt.Sprintf("Keys/key-%04d", n)
		for _, root := range []string{live, carried} {
			if err := writeTestFile(filepath.Join(root, name), []byte("same bytes"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := sameCarriedAuthority(t.Context(), live, carried); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(live, "Keys/key-0000")
	if err := os.Rename(path, path+"-renamed"); err != nil {
		t.Fatal(err)
	}
	if err := sameCarriedAuthority(t.Context(), live, carried); err == nil {
		t.Fatal("different live names accepted")
	}
	if err := os.Rename(path+"-renamed", path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("new bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := sameCarriedAuthority(t.Context(), live, carried); err == nil {
		t.Fatal("different bytes accepted")
	}
	if err := os.WriteFile(path, []byte("same bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeTestFile(filepath.Join(carried, "Keys/stale"), []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := sameCarriedAuthority(t.Context(), live, carried); err == nil {
		t.Fatal("extra carried name accepted")
	}
}

func TestManifestStreamManagedStorageCarriesOnlyConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, state, _ := testService(t)
	for _, name := range []string{"Keys/managed-storage/active.conf", "Keys/managed-storage/allocation.lock", "Keys/managed-storage/save.tmp", "Keys/ordinary.key"} {
		if err := writeTestFile(filepath.Join(state, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	id, err := s.Create(t.Context(), KindManual)
	if err != nil {
		t.Fatal(err)
	}
	folder := storagepaths.Path(state, BackupsDir, id)
	for _, name := range []string{"Keys/managed-storage/active.conf", "Keys/ordinary.key"} {
		if _, err := os.Stat(filepath.Join(folder, name)); err != nil {
			t.Fatal("authority file omitted", name, err)
		}
	}
	for _, name := range []string{"allocation.lock", "save.tmp"} {
		if _, err := os.Stat(filepath.Join(folder, "Keys/managed-storage", name)); !os.IsNotExist(err) {
			t.Fatal("transient file carried", name, err)
		}
	}
}

func TestManifestStreamRejectsApplicationABAAfterDatabaseCopy(t *testing.T) {
	s, state, _ := testService(t)
	loader := configfile.New(filepath.Join(state, "portico.toml"))
	if err := loader.Write(map[string]any{"name": "A"}); err != nil {
		t.Fatal(err)
	}
	changed := false
	s.SetCopyScrubber(func(ctx context.Context, _ *dbwork.Candidate) error {
		changed = true
		if err := loader.Write(map[string]any{"name": "B"}); err != nil {
			return err
		}
		return loader.Write(map[string]any{"name": "A"})
	})
	if _, err := s.Create(t.Context(), KindManual); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("ABA mutation published: %v", err)
	}
	if !changed {
		t.Fatal("mutation path never exercised")
	}
	assertNoScratch(t, state, s.backupRoot())
}

func TestManifestStreamRejectsInFlightAuthorityWriter(t *testing.T) {
	s, _, _ := testService(t)
	release := authorityfiles.Process.Admit()
	defer release()
	if _, err := s.Create(t.Context(), KindManual); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("in-flight mutation published: %v", err)
	}
}

func TestStreamRestoreFindsReadableRecordsAfterOversizedOpeningDamage(t *testing.T) {
	s, state, _ := testService(t)
	name := "Keys/readable.key"
	if err := writeTestFile(filepath.Join(state, name), []byte("readable"), 0600); err != nil {
		t.Fatal(err)
	}
	id, err := s.Create(t.Context(), KindManual)
	if err != nil {
		t.Fatal(err)
	}
	folder := storagepaths.Path(state, BackupsDir, id)
	path := filepath.Join(folder, ManifestName)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	first := bytes.IndexByte(raw, '\n')
	damaged := append(bytes.Repeat([]byte{'!'}, manifestLineBytes*2), '\n')
	damaged = append(damaged, raw[first+1:]...)
	if err := os.WriteFile(path, damaged, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InspectSource(t.Context(), Source{Path: folder}); err != nil {
		t.Fatal("inspection hid readable records", err)
	}
	out, err := s.Stage(t.Context(), Source{Path: folder})
	if err != nil {
		t.Fatal(err)
	}
	report := out.Validation.Recovery.ManifestDamage
	if report.Regions[0].StartByte != 0 || report.Regions[0].EndByte != int64(manifestLineBytes*2+1) {
		t.Fatalf("opening damage offsets: %+v", report)
	}
	if got, err := os.ReadFile(storagepaths.Path(state, RestoreStagedDir, name)); err != nil || string(got) != "readable" {
		t.Fatal("later readable key lost", err)
	}
}
