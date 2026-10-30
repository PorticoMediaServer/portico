import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../packages/i18n/src/index.ts';
import {setInstallationIdentity} from '@core/installation.ts';
import {settingsRowIndex} from '@core/presentation/settings-spec.ts';
import {deviceHelpAddress, deviceHelpURL, deviceRowState, isDeviceSyntheticRow} from '../src/screens/settings/device-rows-model.ts';
import {settingsBinding} from '@core/presentation/settings-bindings.ts';
import {readDeviceSettingsBinding} from '../src/screens/settings/device-bindings.ts';
import {webSettingsPlatform} from '../src/platform/web-settings-platform.ts';

const row = (id: string) => {
  const location = settingsRowIndex.get(id);
  assert.ok(location, id);
  return location.row;
};

test('the Device section owns only its route and address rows beyond the value rows', () => {
  assert.equal(isDeviceSyntheticRow('device.linkTv'), true);
  assert.equal(isDeviceSyntheticRow('device.server'), true);
  assert.equal(isDeviceSyntheticRow('device.serverVersion'), true);
  assert.equal(isDeviceSyntheticRow('device.help'), true);
  // The merged About row opens from the app version, so that row is a route row now.
  assert.equal(isDeviceSyntheticRow('device.appVersion'), true);
  for (const id of ['device.name', 'device.lossNotices', 'playback.window', 'general.name'])
    assert.equal(isDeviceSyntheticRow(id), false, id);
});

test('the Server row names the connected server and keeps the drawn link', () => {
  const state = deviceRowState(row('device.server'), {serverName: 'Portico demo'});
  assert.equal(state?.present, true);
  assert.equal(state?.help, defaultI18n.t('next.settingsSpec.account.device.groups.3.rows.0.help', {server: 'Portico demo'}));
});

test('the Server version row shows the real reported version and vanishes when it is unknown', () => {
  assert.deepEqual(deviceRowState(row('device.serverVersion'), {serverName: 'Portico demo', serverVersion: '0.1.0'}),
    {present: true, value: '0.1.0', displayValue: '0.1.0', readOnly: true});
  assert.equal(deviceRowState(row('device.serverVersion'), {serverName: 'Portico demo'}), undefined);
});

test('the Help row is present and opens the drawn address externally', () => {
  assert.deepEqual(deviceRowState(row('device.help'), {serverName: 'Portico demo'}), {present: true});
  assert.equal(deviceHelpAddress, 'getportico.tv/docs/');
  assert.equal(deviceHelpURL, 'https://getportico.tv/docs/');
});

test('rows outside the Device section are never given a synthetic state', () => {
  assert.equal(deviceRowState(row('device.name'), {serverName: 'Portico demo', serverVersion: '0.1.0'}), undefined);
  assert.deepEqual(deviceRowState(row('device.appVersion'), {serverName: 'Portico demo'}), {present: true});
  assert.equal(deviceRowState(row('general.name'), {serverName: 'Portico demo'}), undefined);
});

test('the web device facade reports the real installation name and version, read-only', () => {
  setInstallationIdentity({installationId: 'a'.repeat(32), name: 'Safari on Mac', platform: 'web', app: 'Portico Web', appVersion: '0.1.0'});
  try {
    const platform = webSettingsPlatform();
    assert.equal(platform.environment.platform, 'web');
    assert.equal(platform.environment.os, 'browser');
    const device = platform.device;
    assert.ok(device);
    assert.deepEqual(device.getSnapshot().facts, {name: {value: 'Safari on Mac', readOnly: true}, appVersion: '0.1.0'});
    // The browser owns only the drawn device settings a web session can keep; the
    // installation name and version stay read-only facts.
    assert.equal(typeof device.apply, 'function');
    assert.deepEqual(device.getSnapshot().document?.defaults, {lossNotices: true});
    assert.deepEqual(device.getSnapshot().document?.values, {lossNotices: true});
  } finally { setInstallationIdentity(undefined); }
});


test('Link a TV is exposed only with its original captured API control', () => {
  assert.deepEqual(deviceRowState(row('device.linkTv'), {serverName: 'Current server'}), {present: false});
  assert.deepEqual(deviceRowState(row('device.linkTv'), {serverName: 'Current server', linkTvAvailable: true}), {present: true});
});


test('unsaved picture and sound notices read their effective default and retain an explicit off choice', async () => {
  const port = webSettingsPlatform().device!, binding = settingsBinding('device.lossNotices')!;
  assert.equal(readDeviceSettingsBinding(binding, port, {}, 'device.lossNotices')?.current, true);
  const original = port.getSnapshot().document!;
  await port.apply!(original.revision, {lossNotices: false}, new AbortController().signal);
  const read = readDeviceSettingsBinding(binding, port, {}, 'device.lossNotices');
  assert.equal(read?.current, false); assert.equal(read?.default, true);
  assert.notEqual(port.getSnapshot().document!.revision, original.revision);
});
