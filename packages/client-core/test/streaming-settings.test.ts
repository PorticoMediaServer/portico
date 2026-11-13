import test from 'node:test';
import assert from 'node:assert/strict';
import {OwnerStreamingSettings} from '../src/server-admin/streaming-settings.ts';
import {readOwnerStreamingHardware, testOwnerStreamingHardware} from '../src/server-admin/streaming-hardware.ts';
import {readSettingsBinding, settingsBinding} from '../src/presentation/settings-bindings.ts';

const defaults = {convertWhenNeeded: 'picture-and-sound', awayVideoKbps: 0, awayMusicKbps: 0, uploadSpeedKbps: 0,
  graphicsDevice: 'automatic', hardwareDecoding: true, conversionsAtOnce: 0, backgroundConversionsAtOnce: 0,
  toneMapping: true, conversionFolder: '', ffmpegProgram: ''};
function fixture() {
  const calls: {path: string; method?: string; body?: unknown; headers?: unknown}[] = [];
  const api = {
    request: async <T>(path: string, method?: string, body?: unknown): Promise<T> => {
      calls.push({path, method, body});
      if (path.endsWith('/stream-limits')) return {total: 4, people: {sam: 2}, endPausedVideoAfterMs: 1800000} as T;
      const name = path.endsWith('/downloads') ? 'downloads' : 'streaming';
      return {name, problem: name === 'streaming' ? 'The selected FFmpeg could not be used.' : undefined, etag: name === 'streaming' ? '"8"' : '"3"', defaults: name === 'streaming' ? defaults : {highestQuality: 'original'},
        values: name === 'streaming' ? {...defaults, awayVideoKbps: 8000, awayMusicKbps: 1411, uploadSpeedKbps: 50000} : {highestQuality: '2160p'}} as T;
    },
    requestRaw: async (path: string, method: string, options: any) => {
      calls.push({path, method, body: options.body, headers: options.headers});
      return {status: 200, headers: {}, body: {}};
    },
  };
  return {api, calls};
}

test('Streaming publishes actual supported values/defaults in the existing row vocabulary', async () => {
  const f = fixture(), source = new OwnerStreamingSettings(f.api), configuration = await source.readConfiguration();
  const value = (id: string) => readSettingsBinding(settingsBinding(id)!, {configuration}, id);
  assert.equal(value('streaming.awayQuality')?.current, '1080p-low');
  assert.equal(value('streaming.awayExact')?.current, 8);
  assert.equal(value('streaming.uploadSpeed')?.current, 50);
  assert.equal(value('streaming.uploadSpeed')?.default, null);
  assert.equal(value('streaming.musicAwayQuality')?.current, 'cd');
  assert.equal(value('streaming.downloadQuality')?.current, '4k');
  assert.equal(value('streaming.allowConversion')?.current, 'all');
  assert.equal(value('streaming.hardware')?.current, 'auto');
  assert.equal(value('streaming.maxStreams')?.current, '4');
  assert.equal(value('streaming.pausedTimeout')?.current, '30');
  assert.equal(value('streaming.maxBackgroundConversions')?.current, 'unlimited');
  assert.equal(source.problemFor('streaming.ffmpegPath'), 'The selected FFmpeg could not be used.');
  assert.equal(source.problemFor('streaming.maxStreams'), undefined);
  assert.equal(value('streaming.encoderSpeed'), undefined);
  assert.equal(value('streaming.maxSoftwareConversions'), undefined);
  assert.deepEqual(f.calls.map(c => c.path).sort(), ['/v1/admin/playback/stream-limits', '/v1/admin/settings/downloads', '/v1/admin/settings/streaming']);
});

test('Streaming writes only the changed server field, carrying its document ETag or using the limits merge patch', async () => {
  const f = fixture(), source = new OwnerStreamingSettings(f.api);
  await source.readConfiguration(); f.calls.length = 0;
  await source.save('streaming.allowConversion', 'sound');
  await source.save('streaming.awayQuality', '720p-high');
  await source.save('streaming.uploadSpeed', null);
  await source.save('streaming.hardware', 'software');
  await source.save('streaming.downloadQuality', '1080p-low');
  await source.save('streaming.maxStreams', '2');
  await source.save('streaming.pausedTimeout', '60');
  assert.deepEqual(f.calls.map(c => c.body), [{convertWhenNeeded: 'sound'}, {awayVideoKbps: 4000}, {uploadSpeedKbps: 0},
    {graphicsDevice: 'processor'}, {highestQuality: '1080p-low'}, {total: 2}, {endPausedVideoAfterMs: 3600000}]);
  assert.deepEqual(f.calls[0].headers, {'If-Match': '"8"'});
  assert.deepEqual(f.calls[4].headers, {'If-Match': '"3"'});
  assert.deepEqual(f.calls.slice(5).map(c => [c.method, c.path]), [['PATCH', '/v1/admin/playback/stream-limits'], ['PATCH', '/v1/admin/playback/stream-limits']]);
  const conflict = new OwnerStreamingSettings({...f.api, requestRaw: async () => ({status: 412, headers: {}, body: {error: {code: 'changed_elsewhere'}}})});
  await conflict.readConfiguration();
  await assert.rejects(conflict.save('streaming.hardwareDecode', false), {code: 'changed_elsewhere'});
});

test('Hardware uses rebuilt read/test routes and retains both measured HDR cases without capacity placeholders', async () => {
  const calls: unknown[] = [];
  const format = {format: 'hevc10', decode: true, encode: true};
  const state = {checking: false, devices: [{id: 'intel', name: 'Intel graphics', method: 'qsv', inUse: true,
    formats: [format], toneMappingWithMasteringData: true, toneMappingWithoutMasteringData: false}], formats: [format],
    toneMappingWithMasteringData: true, toneMappingWithoutMasteringData: false, lastTest: '2026-11-13T04:00:00Z'};
  const api = {request: async <T>(path: string, method?: string): Promise<T> => {calls.push([path, method]); return {...state, checking: method === 'POST'} as T;}};
  const read = await readOwnerStreamingHardware(api), testing = await testOwnerStreamingHardware(api);
  assert.equal(read.toneMappingWithMasteringData, true);
  assert.equal(read.toneMappingWithoutMasteringData, false);
  assert.equal(testing.checking, true);
  assert.equal(Object.hasOwn(read, 'sessions'), false);
  assert.deepEqual(calls, [['/v1/admin/hardware', 'GET'], ['/v1/admin/hardware/test', 'POST']]);
  await assert.rejects(readOwnerStreamingHardware({request: async <T>() => ({...state, toneMappingWithoutMasteringData: undefined}) as T}));
});

test('the processor cap appears only when honoured and saves independently of other conversion limits', async () => {
  const f = fixture();
  const source = new OwnerStreamingSettings({...f.api, request: async <T>(path: string, method?: string, body?: unknown): Promise<T> => {
    const doc: any = await f.api.request(path, method, body);
    if (path.endsWith('/settings/streaming')) {
      doc.values = {...doc.values, processorConversionsAtOnce: 2};
      doc.defaults = {...doc.defaults, processorConversionsAtOnce: 0};
    }
    return doc;
  }});
  const configuration = await source.readConfiguration();
  const value = readSettingsBinding(settingsBinding('streaming.maxSoftwareConversions')!, {configuration}, 'streaming.maxSoftwareConversions');
  assert.equal(value?.current, '2');
  assert.equal(value?.default, 'unlimited');
  await source.save('streaming.maxSoftwareConversions', '4');
  assert.deepEqual(f.calls.at(-1)?.body, {processorConversionsAtOnce: 4});
});


test('the per-device graphics cap reads Automatic and saves the owner cap', async () => {
  const f = fixture();
  const source = new OwnerStreamingSettings({...f.api, request: async <T>(path: string, method?: string, body?: unknown): Promise<T> => {
    const doc: any = await f.api.request(path, method, body);
    if (path.endsWith('/settings/streaming')) {
      doc.values = {...doc.values, hardwareConversionsAtOnce: 2};
      doc.defaults = {...doc.defaults, hardwareConversionsAtOnce: 0};
    }
    return doc;
  }});
  const configuration = await source.readConfiguration();
  const value = readSettingsBinding(settingsBinding('streaming.maxHardwareConversions')!, {configuration}, 'streaming.maxHardwareConversions');
  assert.equal(value?.current, '2');
  assert.equal(value?.default, 'auto');
  await source.save('streaming.maxHardwareConversions', 'auto');
  assert.deepEqual(f.calls.at(-1)?.body, {hardwareConversionsAtOnce: 0});
});
