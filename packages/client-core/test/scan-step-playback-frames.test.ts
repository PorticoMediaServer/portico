import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../i18n/src/index.ts';
import {scanStepName} from '../src/server-admin/console-catalogue.ts';

// #757: the new public scan operation has the one shared owner-facing label (web and native read the same key).
test('the playback_frames scan step reads as "Playback frames" through the shared catalogue key', () => {
  assert.equal(defaultI18n.has('server.scan.step.playback_frames'), true);
  assert.equal(scanStepName(defaultI18n.t, defaultI18n.has, 'playback_frames'), 'Playback frames');
});
