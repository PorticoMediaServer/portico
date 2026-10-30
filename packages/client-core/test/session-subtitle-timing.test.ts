import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi} from '../src/index.ts';
import {localV1Http} from '../src/playback-v1/local-http.ts';
import {SessionsClient} from '../src/playback-v1/sessions.ts';
import {parseSession} from '../src/playback-v1/types.ts';

const file = {trackId: 'actual_subtitle', format: 'vobsub', url: '/v1/media/subtitles/grant/actual/1/original.idx',
  companionUrl: '/v1/media/subtitles/grant/actual/1/original.sub'};
const envelope = (files: readonly object[]) => ({id: 'actual_session', revision: '1', itemId: 'actual_item', kind: 'vod', role: 'local', state: 'paused',
  lease: {reportEveryMs: 10000}, presentation: {generation: 2, mode: 'direct', engine: 'mpv', url: '/v1/media/actual/video', startPositionMs: 5000,
    decision: {}, subtitles: files}});

async function read(files: readonly object[]) {
  const api = new HttpLocalApi('https://server.example', '', async (input, init) => {
    assert.equal(new URL(String(input)).pathname, '/v1/playback/sessions/actual_session');assert.equal(init?.method, 'GET');
    return new Response(JSON.stringify(envelope(files)), {status: 200, headers: {'Content-Type': 'application/json'}});
  });
  return (await new SessionsClient(localV1Http(api)).get('actual_session')).presentation.subtitles;
}

test('Session client retains source-relative subtitle files and their complete signed offset through HTTP', async () => {
  const files = await read([-1725, 0, 1425.25].map(offsetMs => ({...file, timeDomain: 'source-relative', offsetMs})));
  assert.deepEqual(files.map(value => value.offsetMs), [-1725, 0, 1425.25]);
  for (const value of files) {
    assert.equal(value.timeDomain, 'source-relative');assert.equal(value.companionUrl, file.companionUrl);
    assert.equal(value.url, file.url);assert.ok(Object.isFrozen(value));assert.equal(Object.hasOwn(value, 'offsetMs'), true);
  }
  assert.ok(Object.isFrozen(files));
});

test('legacy subtitle files keep timing unknown instead of acquiring a zero offset or source-relative default', async () => {
  const [legacy] = await read([file]);
  assert.equal(legacy!.timeDomain, undefined);assert.equal(legacy!.offsetMs, undefined);
  assert.equal(Object.hasOwn(legacy!, 'timeDomain'), false);assert.equal(Object.hasOwn(legacy!, 'offsetMs'), false);
  assert.equal(legacy!.companionUrl, file.companionUrl);
});

test('invalid timing facts are not coerced, and companion URL validation stays in force', () => {
  for (const timing of [{timeDomain: null, offsetMs: null}, {timeDomain: 1, offsetMs: '1200'}, {timeDomain: false, offsetMs: false},
    {timeDomain: 'wall-clock', offsetMs: NaN}, {timeDomain: [], offsetMs: Infinity}, {timeDomain: {}, offsetMs: -Infinity}]) {
    const [value] = parseSession(envelope([{...file, ...timing}])).presentation.subtitles;
    assert.equal(Object.hasOwn(value!, 'timeDomain'), false);assert.equal(Object.hasOwn(value!, 'offsetMs'), false);
  }
  assert.throws(() => parseSession(envelope([{...file, companionUrl: 'file:///private/subtitle.sub', timeDomain: 'source-relative', offsetMs: -1500}])));
});
