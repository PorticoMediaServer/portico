import test from 'node:test';
import assert from 'node:assert/strict';
import {transcodeLocationChoice} from '../src/presentation/transcode-location.ts';
import {readSettingsBinding, settingsBinding, settingsBindingPatch} from '../src/presentation/settings-bindings.ts';

test('the explicit memory or disk preference stays valid regardless of host availability', () => {
  for (const memoryAvailable of [true, false]) {
    const directory = {memoryAvailable};
    assert.deepEqual(transcodeLocationChoice(directory), {value: 'memory', memoryAvailable});
    assert.deepEqual(transcodeLocationChoice(directory, 'memory'), {value: 'memory', memoryAvailable});
    assert.deepEqual(transcodeLocationChoice(directory, 'disk'), {value: 'disk', memoryAvailable});
  }
  assert.deepEqual(transcodeLocationChoice(undefined), {value: 'memory', memoryAvailable: false});
});

test('the preference binding reads and writes its own runtime field independently of the disk folder', () => {
  const binding = settingsBinding('streaming.transcodeLocation')!;
  const runtime = {transcodeLocation: 'memory', temporaryDirectory: '/mnt/transcode'};
  assert.equal(readSettingsBinding(binding, {forms: {runtime}})?.current, 'memory');
  for (const value of ['memory', 'disk']) {
    assert.deepEqual(settingsBindingPatch(binding, value), {form: 'runtime', values: {transcodeLocation: value}});
    assert.equal(runtime.temporaryDirectory, '/mnt/transcode');
  }
});
