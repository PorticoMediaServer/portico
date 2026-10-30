import test from 'node:test';
import assert from 'node:assert/strict';
import {profileDeviceIcon} from '../src/screens/settings/account-profile/device-icon.ts';

test('device platforms select phone, tablet, television, browser and Cast glyphs', () => {
  for (const platform of ['ios', 'android', 'fireos']) assert.equal(profileDeviceIcon(platform), 'i-phone');
  assert.equal(profileDeviceIcon('ipados'), 'i-tablet');
  for (const platform of ['tvos', 'androidtv', 'firetv']) assert.equal(profileDeviceIcon(platform), 'i-tv');
  for (const platform of ['web', 'macos', 'windows', 'linux']) assert.equal(profileDeviceIcon(platform), 'i-laptop');
  assert.equal(profileDeviceIcon('google-cast'), 'i-cast');
});

test('unrecognised and missing platform facts do not misidentify a device as a person', () => {
  assert.equal(profileDeviceIcon(''), 'i-device');
  assert.equal(profileDeviceIcon('unrecognised'), 'i-device');
});
