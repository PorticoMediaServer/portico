import test from 'node:test';
import assert from 'node:assert/strict';
import {decode} from '../src/runtime.ts';

test('backup policy responses expose protection facts without secret material', () => {
  const settings = {backupFolder: '/backups', includeArtwork: false, passphraseProtected: true, passphraseSet: true};
  for (const name of ['GetAdminBackupSettingsResponse', 'PutAdminBackupSettingsResponse'] as const) {
    assert.doesNotThrow(() => decode(name, settings));
    for (const field of ['passphrase', 'passphraseSealed', 'masterKey']) {
      const result = decode(name, {...settings, [field]: 'private'}) as Record<string, unknown>;
      assert.equal(Object.hasOwn(result, field), false);
    }
    assert.throws(() => decode(name, {...settings, passphraseSet: null}));
  }
});

test('backup inspection publishes only an actual non-null encryption fact', () => {
  for (const encrypted of [true, false]) {
    assert.doesNotThrow(() => decode('PostAdminBackupsInspectResponse', {encrypted}));
  }
  for (const input of [{}, {encrypted: null}, {encrypted: 'false'}]) {
    assert.throws(() => decode('PostAdminBackupsInspectResponse', input));
  }
  const result = decode('PostAdminBackupsInspectResponse', {encrypted: false, path: '/private', passphrase: 'private'}) as Record<string, unknown>;
  assert.deepEqual(Object.keys(result), ['encrypted']);
});

test('restore keeps passphrase input at the declared top level', () => {
  const request = {operationId: 'restore-operation', source: {backupId: 'backup'}};
  assert.doesNotThrow(() => decode('PostAdminBackupsRestoreRequest', request));
  assert.doesNotThrow(() => decode('PostAdminBackupsRestoreRequest', {...request, passphrase: 'private'}));
  assert.throws(() => decode('PostAdminBackupsRestoreRequest', {...request, passphrase: 1}));
  const absent = decode('PostAdminBackupsRestoreRequest', {...request, passphrase: null}) as Record<string, unknown>;
  assert.equal(Object.hasOwn(absent, 'passphrase'), false);
  const nested = decode('PostAdminBackupsRestoreRequest', {...request, source: {...request.source, passphrase: 'private'}}) as {source: Record<string, unknown>};
  assert.equal(Object.hasOwn(nested, 'passphrase'), false);
  assert.equal(Object.hasOwn(nested.source, 'passphrase'), false);
});
