import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {createSharedReads} from '../src-next/shell/shared-read.ts';

const flush = async () => {for (let pass = 0; pass < 4; pass++) await Promise.resolve();};

test('reads asked while one is in flight share its single request and answer', async () => {
  const shared = createSharedReads();
  let calls = 0, finish!: (value: string) => void;
  const read = () => {calls++; return new Promise<string>(resolve => {finish = resolve;});};
  const first = shared('/v1/home', undefined, read), second = shared('/v1/home', undefined, read), third = shared('/v1/home', new AbortController().signal, read);
  assert.equal(calls, 1);
  finish('home');
  assert.deepEqual(await Promise.all([first, second, third]), ['home', 'home', 'home']);
  await shared('/v1/home', undefined, async () => {calls++; return 'again';});
  assert.equal(calls, 2, 'a read after the first settles is a new request');
});

test('one caller leaving never cancels the others; the request stops only when every caller has gone', async () => {
  const shared = createSharedReads();
  let signal: AbortSignal | undefined, finish!: (value: string) => void;
  const read = (inner: AbortSignal) => {signal = inner; return new Promise<string>(resolve => {finish = resolve;});};
  const leaving = new AbortController(), staying = new AbortController();
  const left = shared('/v1/home', leaving.signal, read), kept = shared('/v1/home', staying.signal, read);
  leaving.abort();
  await assert.rejects(left, {name: 'AbortError'});
  assert.equal(signal?.aborted, false);
  finish('home'); assert.equal(await kept, 'home');

  const alone = new AbortController();
  const only = shared('/v1/home?limit=20', alone.signal, read);
  alone.abort(); await assert.rejects(only, {name: 'AbortError'}); await flush();
  assert.equal(signal?.aborted, true);
});

test('Home document GETs go through the shared read; rows and writes do not', () => {
  const source = readFileSync(new URL('../src-next/shell/bootstrap-services.ts', import.meta.url), 'utf8');
  assert.match(source, /\(method \?\? 'GET'\) === 'GET' && body === undefined && path\.split\('\?'\)\[0\] === routePath\('get_home'\)\s*\? homeReads/);
});
