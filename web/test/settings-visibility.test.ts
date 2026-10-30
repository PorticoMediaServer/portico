import test from 'node:test';
import assert from 'node:assert/strict';
import {settingsRowIndex, settingsSpec} from '../../packages/client-core/src/presentation/settings-spec.ts';
import {settingsRowVisible, settingsSectionVisible, type SettingsEnvironment} from '../../packages/client-core/src/presentation/settings-visibility.ts';
import {searchSettings, settingsRoute} from '../src/kit/settings/search.ts';
import {settingsIconName} from '../src/kit/settings/icons.ts';
import {defaultI18n} from '../../packages/i18n/src/index.ts';

const visible = (id: string, environment: SettingsEnvironment) => settingsRowVisible(settingsRowIndex.get(id)!.row, environment);
test('native OS variants expose one actual row without recreating removed hardware controls', () => {
  const desktop = {platform: 'desktop', role: 'owner', signIn: 'local'} as const;
  assert.equal(visible('device.player', {...desktop, os: 'windows'}), false);
  assert.equal(visible('device.player', {platform: 'phone', os: 'android'}), true);
  assert.equal(visible('device.player', {platform: 'phone', os: 'ios'}), false);
  assert.equal(visible('device.matchFrameRateDesktop', {...desktop, os: 'windows'}), true);
  assert.equal(visible('device.matchFrameRateDesktop', {...desktop, os: 'linux'}), false);
  assert.equal(visible('device.sampleRateMatchingMac', {...desktop, os: 'macos'}), true);
  assert.equal(visible('device.sampleRateMatching', {...desktop, os: 'macos'}), false);
  assert.equal(visible('device.passthroughFormatsMac', {...desktop, os: 'macos'}), true);
  assert.equal(visible('device.passthroughFormats', {...desktop, os: 'macos'}), false);
  for (const id of ['quality.playerEngine', 'audioSubtitles.burnIn', 'audioSubtitles.passthroughFormats', 'streaming.hardwareDevice']) assert.equal(settingsRowIndex.has(id), false);
});
test('desktop inherits web sections and parent platform restrictions reach child controls', () => {
  const environment = {platform: 'desktop', os: 'windows'} as const;
  assert.equal(settingsSectionVisible(settingsSpec.find(section => section.id === 'reading')!, environment), true);
  assert.equal(visible('profile.name', environment), true);
  assert.equal(visible('device.audioOutput', {platform: 'web', os: 'browser'}), false);
  assert.equal(visible('device.audioOutput', environment), true);
});
test('Local identity is absent with unknown/Portico kind and cannot leak through search or a deep link', () => {
  const row = settingsRowIndex.get('remote.identityCode')!.row;
  assert.equal(settingsRowVisible(row, {platform: 'web', os: 'browser', role: 'owner'}), false);
  assert.equal(settingsRowVisible(row, {platform: 'web', os: 'browser', role: 'owner', signIn: 'portico'}), false);
  assert.equal(settingsRowVisible(row, {platform: 'web', os: 'browser', role: 'owner', signIn: 'local'}), true);
  const environment = {platform: 'web', os: 'browser', role: 'owner', signIn: 'portico'} as const;
  const eligible = (row: Parameters<typeof settingsRowVisible>[0]) => settingsRowVisible(row, environment);
  assert.equal(searchSettings('identity code', defaultI18n.t, eligible).some(hit => hit.rowId === row.id), false);
  assert.deepEqual(settingsRoute({scope: 'server', section: 'remote', row: row.id}, () => true, eligible), {scope: 'server', section: 'remote'});
});
test('Settings icon lookup strips exactly one prefix and uses the drawn fallback', () => {
  assert.equal(settingsIconName('server'), 'i-server');
  assert.equal(settingsIconName('i-server'), 'i-server');
  assert.equal(settingsIconName('i-i-server'), 'i-sliders');
  assert.equal(settingsIconName('chip'), 'i-chip');
  assert.equal(settingsIconName('unknown'), 'i-sliders');
});
