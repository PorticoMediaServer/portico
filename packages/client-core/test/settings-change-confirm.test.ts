import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../i18n/src/index.ts';
import {settingsChangeConfirmation} from '../src/presentation/settings-change-confirm.ts';
import {settingsRowIndex} from '../src/presentation/settings-spec.ts';

test('O-151: only remote-access edits that can cut people off ask before saving', () => {
  const ask = 'settings.confirm.remoteAccess';
  assert.equal(settingsChangeConfirmation('remote.who', 'none', 'anyone'), ask);
  assert.equal(settingsChangeConfirmation('remote.who', 'owner', 'anyone'), ask);
  assert.equal(settingsChangeConfirmation('remote.who', 'anyone', 'none'), undefined, 'opening up never asks');
  assert.equal(settingsChangeConfirmation('remote.mapping', 'off', 'auto'), ask);
  assert.equal(settingsChangeConfirmation('remote.mapping', 'auto', 'off'), undefined);
  assert.equal(settingsChangeConfirmation('remote.secure', false, true), ask);
  assert.equal(settingsChangeConfirmation('remote.secure', true, false), undefined);
  assert.equal(settingsChangeConfirmation('remote.publicPort', 32600, 32500), ask);
  assert.equal(settingsChangeConfirmation('remote.publicPort', 32500, 32500), undefined, 'an unchanged value saves at once');
  assert.equal(settingsChangeConfirmation('remote.homeNets', ['192.168.1.0/24', '10.0.0.0/8'], ['192.168.1.0/24']), ask);
  assert.equal(settingsChangeConfirmation('remote.trustedProxies', ['10.0.0.2'], []), ask);
  assert.equal(settingsChangeConfirmation('remote.discovery', false, true), undefined, 'every other row keeps auto-save');
  assert.equal(settingsChangeConfirmation('playback.quality', 'auto', 'original'), undefined);
});

test('every confirmed row exists in the settings spec and its question is in the catalogue', () => {
  for (const id of ['remote.who', 'remote.mapping', 'remote.secure', 'remote.publicPort', 'remote.interface', 'remote.homeNets', 'remote.trustedProxies']) {
    assert.ok(settingsRowIndex.get(id), id);
  }
  assert.notEqual(defaultI18n.t('settings.confirm.remoteAccess'), 'settings.confirm.remoteAccess');
  assert.notEqual(defaultI18n.t('settings.confirm.change'), 'settings.confirm.change');
});
