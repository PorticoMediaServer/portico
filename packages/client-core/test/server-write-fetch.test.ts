import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi} from '../src/http.ts';
import {jsonEmptyWrites} from '../src/server-write-fetch.ts';
import {callServer} from '../src/query/server-api.ts';

test('bodyless media-server writes, including the Portico challenge, are JSON on the wire', async () => {
  const sent: RequestInit[] = [];
  const api = new HttpLocalApi('https://server.example', '', async (_url, init) => {
    sent.push(init!);
    assert.equal(new Headers(init?.headers).get('Content-Type'), 'application/json');
    assert.equal(init?.body, '{}');
    return Response.json({});
  });
  await callServer(api, 'POST /v1/auth/portico/challenge');
  for (const method of ['POST', 'PUT', 'PATCH', 'DELETE']) await api.request('/v1/example', method);
  await api.requestConsole('/v1/example', 'POST', undefined, new AbortController().signal);
  assert.equal(sent.length, 6);
});

test('empty raw writes retain authorization and require JSON for every write method', async () => {
  const fetcher = jsonEmptyWrites(async (_url, init) => {
    const headers = new Headers(init?.headers);
    assert.equal(headers.get('Content-Type'), 'application/json');
    assert.equal(headers.get('Authorization'), 'Bearer held-session');
    assert.equal(init?.body, '{}');
    return Response.json({});
  });
  for (const method of ['POST', 'PUT', 'PATCH', 'DELETE']) {
    for (const body of [undefined, null, '']) await fetcher('https://server.example', {method, body, headers: {Authorization: 'Bearer held-session'}});
  }
});

test('the routed transport receives the same JSON default', async () => {
  let called = false;
  const api = new HttpLocalApi('https://server.example');
  api.useRoutes({logicalOrigin: api.baseUrl, fetch: async (_url, init) => {
    called = true;
    assert.equal(new Headers(init?.headers).get('Content-Type'), 'application/json');
    assert.equal(init?.body, '{}');
    return Response.json({});
  }} as any);
  await callServer(api, 'POST /v1/auth/portico/challenge');
  assert.equal(called, true);
});

test('reads, JSON payloads, multipart uploads and explicit byte encodings keep their bodies and headers', async () => {
  const sent: RequestInit[] = [];
  const fetcher = jsonEmptyWrites(async (_url, init) => {sent.push(init!); return Response.json({});});
  for (const method of ['GET', 'HEAD']) {
    await fetcher('https://server.example', {method});
    assert.equal(sent.at(-1)?.body, undefined);
    assert.equal(new Headers(sent.at(-1)?.headers).has('Content-Type'), false);
  }
  const form = new FormData(); form.set('file', new Blob(['image']), 'image.png');
  for (const init of [
    {method: 'PATCH', headers: {'Content-Type': 'application/merge-patch+json'}, body: '{"name":"Ada"}'},
    {method: 'POST', body: form},
    {method: 'PUT', headers: {'Content-Type': 'image/png'}, body: new Uint8Array([1, 2])},
    {method: 'POST', headers: {'Content-Type': 'application/octet-stream'}, body: ''},
  ]) {
    await fetcher('https://server.example', init);
    assert.equal(sent.at(-1), init);
  }
  await fetcher(new Request('https://server.example', {method: 'POST', body: 'already encoded'}));
  assert.equal(sent.at(-1), undefined, 'a Request carrying a body is not replaced');
});
