import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpHostedApi, HttpLocalApi} from '../src/index.ts';
import {ServerListWatcher} from '../src/portico-servers.ts';

let sequence = 0;
for (const bound of [false, true]) {
  for (const hosted of [false, true]) test(`${hosted ? 'Hosted' : 'Local'} API preserves ${bound ? 'explicitly bound' : 'value-call'} injected fetch semantics`, async () => {
    const origin = `https://receiver-${++sequence}.example`, receiver = Object.freeze({transport: 'bound'});
    let calls = 0;
    // Window.fetch accepts Window or undefined, never an API instance. An
    // explicitly bound transport must keep its own receiver instead.
    const fetcher = async function(this: unknown, input: RequestInfo | URL, init?: RequestInit) {
      calls++;
      if (this !== (bound ? receiver : undefined)) throw new TypeError('Unexpected injected fetch receiver');
      assert.equal(String(input), origin + (hosted ? '/v1/servers' : '/v1/system'));
      assert.equal(init?.redirect, 'error'); assert.equal(init?.credentials, 'omit');
      assert.equal(init?.referrerPolicy, 'no-referrer'); assert.equal(init?.method, 'GET');
      assert.equal(init?.body, undefined);
      return new Response(JSON.stringify({items: [], version: '7'}), {headers: {'Content-Type': 'application/json'}});
    };
    const transport = bound ? fetcher.bind(receiver) : fetcher;
    const api = hosted ? new HttpHostedApi(origin, '', transport) : new HttpLocalApi(origin, '', transport);
    const answer = await api.request<{items: unknown[]; version: string}>(hosted ? '/v1/servers' : '/v1/system');
    assert.deepEqual(answer, {items: [], version: '7'}); assert.equal(calls, 1);
  });
  test(`server-list watch preserves ${bound ? 'explicitly bound' : 'value-call'} injected fetch semantics`, async () => {
    const origin = `https://watch-receiver-${++sequence}.example`, receiver = Object.freeze({transport: 'bound'});
    let calls = 0, lists = 0;
    const fetcher = async function(this: unknown, input: RequestInfo | URL, init?: RequestInit) {
      calls++;
      if (this !== (bound ? receiver : undefined)) throw new TypeError('Unexpected injected fetch receiver');
      assert.equal(String(input), origin + '/v1/server-list/d2F0Y2g.mac/7');
      assert.equal(init?.credentials, 'omit'); assert.equal(init?.redirect, 'error'); assert.equal(init?.referrerPolicy, 'no-referrer');
      return new Response(null, {status: 304});
    };
    const answer = {items: [], version: '7', watch: 'd2F0Y2g.mac'};
    const watcher = new ServerListWatcher({hostedOrigin: origin, fetcher: bound ? fetcher.bind(receiver) : fetcher,
      listServers: async () => {lists++; return answer;}, onServers: () => {throw new Error('An unchanged watch must not reload the list');}});
    watcher.adopt(answer);
    assert.equal(await watcher.refresh(), 'unchanged'); assert.equal(calls, 1); assert.equal(lists, 0);
  });
}
