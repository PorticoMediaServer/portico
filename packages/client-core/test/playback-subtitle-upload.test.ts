import assert from 'node:assert/strict';
import test from 'node:test';
import {ApiError, HttpLocalApi} from '../src/index.ts';
import type {V1Http, V1Request, V1Response} from '../src/playback-v1/http.ts';
import {uploadPlaybackSubtitle} from '../src/playback-v1/subtitle-upload.ts';

test('playback multipart adaptation retains the real upload wire and receipt for create and replace', async () => {
  for (const method of ['POST', 'PUT'] as const) {
    const path = '/v1/items/item_1/subtitles' + (method === 'PUT' ? '/subtitle_1' : '');
    const body = new FormData(); body.append('metadata', '{"operationId":"same-operation"}');
    body.append('file', new Blob(['subtitle']), 'english.srt');
    const caller = new AbortController(), scoped = new AbortController();
    const wire: {url: string; init: RequestInit}[] = [];
    const api = new HttpLocalApi('https://original.test', 'original-token', (async (url: string, init: RequestInit) => {
      wire.push({url, init});
      return new Response('{"resourceId":"subtitle_1","revision":4}', {status: 201});
    }) as typeof fetch);
    let original: V1Request | undefined, adapted: V1Response | undefined;
    const http = {async send(request: V1Request, transport: V1Http) {
      original = request;
      adapted = await transport.send({...request, signal: scoped.signal});
      return adapted;
    }};
    const receipt = await uploadPlaybackSubtitle(http, api, path, method, body, caller.signal);
    assert.deepEqual(original, {path, method, body, signal: caller.signal});
    assert.deepEqual(adapted, {status: 200, headers: {}, body: receipt});
    assert.equal(adapted.body, receipt);
    assert.deepEqual(receipt, {resourceId: 'subtitle_1', revision: 4});
    assert.equal(wire.length, 1);
    assert.equal(wire[0]!.url, 'https://original.test' + path);
    const init = wire[0]!.init;
    assert.equal(init.method, method); assert.equal(init.body, body);
    assert.deepEqual(init.headers, {Accept: 'application/json', Authorization: 'Bearer original-token', 'Portico-Playback': '2'});
    assert.equal(init.redirect, 'error'); assert.equal(init.credentials, 'omit'); assert.equal(init.referrerPolicy, 'no-referrer');
    scoped.abort(); assert.equal(init.signal?.aborted, false, 'a settled upload has released its deadline cancellation link');
  }
});

test('multipart validation still belongs to the real upload API and rejects before any wire request', async () => {
  const api = new HttpLocalApi('https://original.test', 'token', (async () => {assert.fail('invalid upload must not fetch');}) as typeof fetch);
  const http = {send: (request: V1Request, transport: V1Http) => transport.send(request)};
  for (const [path, method] of [
    ['/v1/items/../subtitles', 'POST'], ['/v1/items/item/subtitles/..', 'PUT'],
    ['/v1/items/item/art', 'POST'], ['/v1/items/item/subtitles', 'PUT'],
  ] as const) await assert.rejects(uploadPlaybackSubtitle(http, api, path, method, new FormData()), /Invalid subtitle upload path/);
});

test('multipart error responses are never synthesized into success or resent by the adaptation', async () => {
  for (const status of [401, 429, 503]) {
    let calls = 0;
    const api = new HttpLocalApi('https://original.test', 'token', (async () => {
      calls++;
      return new Response('{"error":{"code":"writer_capacity","retryable":true}}', {status, headers: {'Retry-After': '0'}});
    }) as typeof fetch);
    const http = {send: (request: V1Request, transport: V1Http) => transport.send(request)};
    await assert.rejects(uploadPlaybackSubtitle(http, api, '/v1/items/item/subtitles', 'POST', new FormData()),
      (error: unknown) => error instanceof ApiError && error.status === status && error.code === 'writer_capacity');
    assert.equal(calls, 1);
  }
});
