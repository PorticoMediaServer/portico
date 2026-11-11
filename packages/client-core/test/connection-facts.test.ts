import test from 'node:test';
import assert from 'node:assert/strict';
import {readConnectionFacts} from '../src/query/connection-operations.ts';

test('the home fact is read from the public system answer, without credentials or redirects, and is unknown when the server does not say', async () => {
  const signal = new AbortController().signal;
  let request: unknown[] = [];
  const answer = (body: string): typeof fetch => async (url, options) => {request = [url, options]; return new Response(body, {headers: {'Content-Type': 'application/json'}});};
  assert.deepEqual(await readConnectionFacts('https://server.test', signal, answer('{"name":"Den","atHome":true}')), {remote: false});
  assert.deepEqual(request, ['https://server.test/v1/system', {method: 'GET', redirect: 'error', credentials: 'omit', referrerPolicy: 'no-referrer', signal, headers: {Accept: 'application/json'}}]);
  assert.deepEqual(await readConnectionFacts('https://server.test', signal, answer('{"name":"Den","atHome":false}')), {remote: true});
  assert.deepEqual(await readConnectionFacts('https://server.test', signal, answer('{"name":"Den"}')), {}, 'a server that does not say leaves the device\'s place unknown, the cautious case');
  await assert.rejects(readConnectionFacts('https://server.test', signal, async () => new Response('x'.repeat(8193))), /./);
});
