import test from 'node:test';
import assert from 'node:assert/strict';
import {nativeMeasurementLaunch} from '../src-next/dev/measurement-launch';

test('measurement launch admits only the public signed-out welcome recipe', () => {
  assert.deepEqual(nativeMeasurementLaunch('portico://native-layout?specimen=signed-out-welcome'), {kind: 'public', specimen: 'signed-out-welcome'});
  assert.equal(nativeMeasurementLaunch('portico://native-layout?specimen=signed-out-welcome'), nativeMeasurementLaunch('portico://native-layout?specimen=signed-out-welcome'));
  for (const url of [null, '', 'portico://kit?gallery=buttons', 'portico://account-proof?server=https://fixture.invalid', 'https://fixture.invalid']) {
    assert.deepEqual(nativeMeasurementLaunch(url), {kind: 'normal'});
  }
});

test('malformed measurement requests stay refused rather than falling through to real auth restoration', () => {
  for (const url of ['portico://native-layout', 'portico://native-layout?specimen=unknown',
    'portico://native-layout?specimen=signed-out-welcome&specimen=signed-out-welcome',
    'portico://native-layout?specimen=signed-out-welcome&server=https://fixture.invalid',
    'portico://native-layout?specimen=signed-out-welcome&password=private',
    'portico://native-layout?specimen=signed-out-welcome&runtime=injected',
    'portico://native-layout?specimen=signed-out-welcome#__session',
    'portico://name:private@native-layout?specimen=signed-out-welcome',
    'portico://native-layout:1?specimen=signed-out-welcome', 'portico://native-layout/path?specimen=signed-out-welcome',
    'portico://native-layout/?specimen=signed-out-welcome', 'https://native-layout?specimen=signed-out-welcome',
    'portico://native-layout-bad?specimen=signed-out-welcome', 'portico://native-layout%zz?specimen=signed-out-welcome']) {
    assert.deepEqual(nativeMeasurementLaunch(url), {kind: 'refused'});
  }
  for (const url of ['portico:native-layout?specimen=signed-out-welcome', 'portico:/native-layout?specimen=signed-out-welcome']) {
    assert.deepEqual(nativeMeasurementLaunch(url), {kind: 'refused'});
  }
});
