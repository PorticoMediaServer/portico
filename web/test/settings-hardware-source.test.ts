import test from 'node:test';
import assert from 'node:assert/strict';
import {SettingsHardwareSource} from '../src/screens/settings/hardware/hardware-source.ts';
import {hardwareRowState} from '../src/screens/settings/hardware/hardware-rows.ts';

// Streaming › Hardware on the rebuilt server (W2 F1, 16 Nov walk: the page asked the old server's
// /v1/admin/playback/hardware and /v1/admin/transcode/capacity). Definition 12 rules 31 to 34.
const device = {id: 'intel-0', name: 'Intel UHD 630', method: 'quick_sync', inUse: true, formats: [{format: 'hevc', decode: true, encode: true}],
  toneMappingWithMasteringData: true, toneMappingWithoutMasteringData: true};
const report = (checking: boolean, lastError?: object) => ({checking, devices: [device], formats: [{format: 'hevc', decode: true, encode: true}, {format: 'av1', decode: true, encode: false}],
  toneMappingWithMasteringData: true, toneMappingWithoutMasteringData: true, lastTest: '2026-11-16T12:00:00Z', ...(lastError ? {lastError} : {})});

test('Hardware reads the rebuilt report and draws its devices, formats and tone mapping', async () => {
  const paths: string[] = [];
  const source = new SettingsHardwareSource({request: async <T>(path: string) => {paths.push(path);return report(false) as T;}}, () => true, () => {});
  try {
    await source.load();
    assert.deepEqual(paths, ['/v1/admin/hardware']);
    const state = source.getSnapshot();
    assert.equal(hardwareRowState('streaming.hwDevices', state)?.present, true);
    assert.deepEqual(hardwareRowState('streaming.hwFormats', state)?.gridRows?.map(row => row.label), ['HEVC', 'AV1']);
    assert.equal(hardwareRowState('streaming.hwToneMapping', state)?.statusTone, 'ok');
    assert.deepEqual(hardwareRowState('streaming.hwError', state), {present: false});
  } finally {source.dispose();}
});

test('Test hardware waits until the server has finished checking, then says what it found', async () => {
  const answers = [report(true), report(true), report(false, {reason: 'probe_encode_failed', remedy: 'Pass /dev/dri to the container.'})];
  const calls: string[] = [];
  const source = new SettingsHardwareSource({request: async <T>(path: string, method?: string) => {
    calls.push(`${method ?? 'GET'} ${path}`);
    return (calls.length === 1 ? report(false) : answers.shift()) as T;
  }}, () => true, () => {}, async () => {});
  try {
    await source.load();
    await source.test();
    assert.deepEqual(calls, ['GET /v1/admin/hardware', 'POST /v1/admin/hardware/test', 'GET /v1/admin/hardware', 'GET /v1/admin/hardware']);
    const state = source.getSnapshot();
    assert.equal(state.testing, false);
    assert.equal(state.tested, 'problem');
    assert.match(hardwareRowState('streaming.hwError', state)?.displayValue ?? '', /Pass \/dev\/dri to the container\./);
  } finally {source.dispose();}
});
