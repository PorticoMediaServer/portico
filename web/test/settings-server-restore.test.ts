import test from 'node:test';
import assert from 'node:assert/strict';
import type {BackupTransport} from '@core/backups.ts';
import {SettingsBackupSource} from '../src/screens/settings/backup-source.ts';
import {protectedRestoreResult} from '../src/screens/settings/server/restore.ts';

const serverId = 'captured-server';
const settings = {backupFolder: '/actual/backups', includeArtwork: true, passphraseProtected: true, passphraseSet: true};
const backup = {id: 'sealed', createdAt: '2026-10-23T12:00:00Z', kind: 'manual', schemaVersion: 1, serverVersion: '1', bytes: 100, path: '/actual/backups/sealed', encrypted: true};
const staged = {staged: true, restartRequired: true, validation: {schemaVersion: 1, integrity: 'ok'}};
const envelope = (result: unknown) => ({protocolVersion: '1.0', serverId, result});
/** A protected archive that answers the restore with its own refusal, as the server does. */
function fixture(refusal: 'wrong-passphrase' | 'damaged' | undefined) {
  let posted = false, stagedRead = false;
  const api: BackupTransport = {request: async <T>(path: string) => {
    if (path.endsWith('/settings')) return envelope(settings) as T;
    if (path.endsWith('/inspect')) return envelope({encrypted: true}) as T;
    if (path.endsWith('/restore')) {posted = true;return envelope({jobId: 'restore-job'}) as T;}
    if (!posted) return envelope({backups: [backup]}) as T;
    if (refusal !== undefined) return envelope({backups: [backup], restore: {jobId: 'restore-job', phase: 'failed', bytesDone: 0, bytesTotal: 0, errorCode: 'restore_integrity_failed'}}) as T;
    // Preserve both real milestones: staged job, then matching post-restart result.
    if (!stagedRead) {stagedRead = true;return envelope({backups: [backup], restore: {jobId: 'restore-job', phase: 'staged', bytesDone: 1, bytesTotal: 1, result: staged}}) as T;}
    return envelope({backups: [backup], lastRestore: {jobId: 'restore-job', at: '2026-10-23T12:01:00Z', outcome: 'restored', reason: '', schemaVersion: 1}}) as T;
  }};
  return new SettingsBackupSource(api, serverId, () => true, () => {});
}

async function protectedRestore(refusal: 'wrong-passphrase' | 'damaged' | undefined, passphrase: string) {
  const source = fixture(refusal);
  try {
    await source.load();
    const selection = await source.prepareRestore({backupId: 'sealed'}, new AbortController().signal);
    try {const result = await source.restore(selection, passphrase, new AbortController().signal);return {result, error: undefined, source};}
    catch (error) {return {result: undefined, error, source};}
  } catch (error) {source.dispose();throw error;}
}

test('a wrong passphrase and a damaged protected archive reach the same honest state', async () => {
  const wrong = await protectedRestore('wrong-passphrase', 'not the phrase');
  const damaged = await protectedRestore('damaged', 'the right phrase');
  try {
    // The server refuses both with its one authenticated-format code; neither is named as the cause.
    assert.equal((wrong.error as {code?: string}).code, 'restore_integrity_failed');
    assert.equal((damaged.error as {code?: string}).code, 'restore_integrity_failed');
    assert.equal(protectedRestoreResult(wrong.error), 'unopened');
    assert.equal(protectedRestoreResult(damaged.error), 'unopened');
    // Nothing was accepted and no live state moved.
    for (const run of [wrong, damaged]) {
      assert.equal(run.source.getSnapshot().staged, undefined);
      assert.equal(run.source.getSnapshot().busy, false);
      assert.doesNotMatch(JSON.stringify(run.source.getSnapshot()), /not the phrase|the right phrase/);
    }
  } finally {wrong.source.dispose();damaged.source.dispose();}
});

test('the right passphrase on an intact archive is accepted, and unrelated refusals keep their own path', async () => {
  const intact = await protectedRestore(undefined, 'the right phrase');
  try {
    assert.deepEqual(intact.result, staged);
    assert.equal(intact.source.getSnapshot().document?.lastRestore?.jobId, 'restore-job');
    assert.equal(intact.source.getSnapshot().document?.lastRestore?.outcome, 'restored');
    assert.equal(intact.source.getSnapshot().staged, undefined);assert.equal(intact.source.getSnapshot().restorePhase, undefined);
    // Anything that is not the shared authentication refusal is not passphrase wording.
    assert.equal(protectedRestoreResult({code: 'restore_pre_release'}), 'failed');
    assert.equal(protectedRestoreResult({code: 'insufficient_disk'}), 'failed');
    assert.equal(protectedRestoreResult(new Error('no code')), 'failed');
  } finally {intact.source.dispose();}
});
