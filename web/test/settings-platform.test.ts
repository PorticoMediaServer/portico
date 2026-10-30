import test from 'node:test';
import assert from 'node:assert/strict';
import {bindSettingsDownloads, installSettingsPlatform, settingsPlatform, type SettingsDevicePort, type SettingsDownloadsPort, type SettingsPlatform, type SettingsViewerScope} from '../src/platform/settings-platform.ts';
import {canWriteDeviceSettings, readDeviceSettingsBinding, writeDeviceSettingsBinding} from '../src/screens/settings/device-bindings.ts';
import {settingsBinding} from '@core/presentation/settings-bindings.ts';

const scope: SettingsViewerScope = {serverId: 'server-a', authority: 'local', accountId: 'person-a', profileId: 'profile-a', sessionFamilyId: 'family-a'};
test('native platform installation is explicit and retired cleanup cannot clear a replacement', () => {
  const first: SettingsPlatform = {environment: {platform: 'desktop', os: 'windows'}};
  const second: SettingsPlatform = {environment: {platform: 'desktop', os: 'linux'}};
  const offFirst = installSettingsPlatform(first), offSecond = installSettingsPlatform(second);
  try { offFirst(); assert.equal(settingsPlatform(), second); } finally { offSecond(); }
  assert.equal(settingsPlatform(), undefined);
});
test('an actual signed-in scope is captured and private storage facts disappear before late notifications', async () => {
  let active = true, notifications = 0, disposed = 0, listener = () => {}, opens = 0;
  const native: SettingsDownloadsPort = {scope, getSnapshot: () => ({available: true, freeBytes: 12000}), subscribe: fn => {listener = fn; return () => {listener = () => {};};}, open: async () => {opens++;}, dispose: () => {disposed++;}};
  const platform: SettingsPlatform = {environment: {platform: 'desktop', os: 'windows'}, bindDownloads: received => {assert.deepEqual(received, scope); return native;}};
  const port = bindSettingsDownloads(platform, scope, () => active)!;
  try {
    port.subscribe(() => notifications++);
    assert.equal(port.getSnapshot().freeBytes, 12000);
    await port.open(new AbortController().signal);
    assert.equal(opens, 1);
    active = false; listener();
    assert.equal(notifications, 0);
    assert.deepEqual(port.getSnapshot(), {available: false});
    await assert.rejects(port.open(new AbortController().signal), /settings_download_scope_closed/);
    assert.equal(opens, 1);
  } finally {port.dispose();port.dispose();}
  assert.equal(disposed, 1);
});
test('cross-viewer adapters close immediately and no server-global fallback is supplied', () => {
  assert.equal(bindSettingsDownloads(undefined, scope, () => true), undefined);
  let disposed = 0;
  const platform: SettingsPlatform = {environment: {platform: 'desktop', os: 'macos'}, bindDownloads: () => ({scope: {...scope, profileId: 'another-profile'}, getSnapshot: () => ({available: true, freeBytes: 999}), subscribe: () => () => {}, open: async () => {}, dispose: () => {disposed++;}})};
  assert.throws(() => bindSettingsDownloads(platform, scope, () => true), /settings_download_scope_mismatch/);
  assert.equal(disposed, 1);
});
test('disposing the shared door aborts an in-flight native open', async () => {
  let signal: AbortSignal | undefined;
  const platform: SettingsPlatform = {environment: {platform: 'desktop', os: 'linux'}, bindDownloads: () => ({scope, getSnapshot: () => ({available: true}), subscribe: () => () => {}, open: async value => {signal = value; await new Promise<void>((_resolve, reject) => value.addEventListener('abort', () => reject(value.reason), {once: true}));}, dispose: () => {}})};
  const port = bindSettingsDownloads(platform, scope, () => true)!;
  const opened = port.open(new AbortController().signal);
  port.dispose();
  await assert.rejects(opened);
  assert.equal(signal?.aborted, true);
});
test('native name/version facts need neither a fabricated document nor a refusing setter', async () => {
  const port: SettingsDevicePort = {subscribe: () => () => {}, getSnapshot: () => ({facts: {name: {value: 'Actual computer', readOnly: true}, appVersion: '1.2.3'}})};
  assert.equal(port.getSnapshot().facts.appVersion, '1.2.3');
  assert.equal(canWriteDeviceSettings(port), false);
  assert.equal(readDeviceSettingsBinding(settingsBinding('device.player')!, port, {}, 'device.player'), undefined);
  await assert.rejects(writeDeviceSettingsBinding(port, {playerEngine: 'native'}, new AbortController().signal, () => true), {name: 'AbortError'});
});
test('an actual readable device document without apply stays read-only', async () => {
  const port: SettingsDevicePort = {subscribe: () => () => {}, getSnapshot: () => ({facts: {}, document: {revision: 'native-revision', values: {playerEngine: 'native'}, defaults: {playerEngine: 'automatic'}}})};
  assert.equal(canWriteDeviceSettings(port), false);
  assert.equal(readDeviceSettingsBinding(settingsBinding('device.player')!, port, {}, 'device.player')?.current, 'native');
  await assert.rejects(writeDeviceSettingsBinding(port, {playerEngine: 'automatic'}, new AbortController().signal, () => true), {name: 'AbortError'});
});
test('device writes use the captured real revision and cannot begin after scope retirement', async () => {
  const calls: unknown[] = []; let current = true;
  const port: SettingsDevicePort = {subscribe: () => () => {}, getSnapshot: () => ({facts: {}, document: {revision: 'native-revision', values: {playerEngine: 'native'}, defaults: {playerEngine: 'automatic'}}}),
    apply: async (revision, patch) => {calls.push([revision, patch]);}};
  assert.equal(canWriteDeviceSettings(port), true);
  await writeDeviceSettingsBinding(port, {playerEngine: 'automatic'}, new AbortController().signal, () => current);
  assert.deepEqual(calls, [['native-revision', {playerEngine: 'automatic'}]]);
  current = false;
  await assert.rejects(writeDeviceSettingsBinding(port, {playerEngine: 'native'}, new AbortController().signal, () => current), {name: 'AbortError'});
  assert.equal(calls.length, 1);
});
