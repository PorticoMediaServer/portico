import test from 'node:test';
import assert from 'node:assert/strict';
import {createI18n, resolveRegion} from '../../packages/i18n/src/index.ts';
import {messageTime} from '../src/screens/settings/server/troubleshooting/message-time.ts';
import {SettingsMessagesSource, appendMessage, messageRetryMs} from '../src/screens/settings/server/troubleshooting/messages-source.ts';
import type {LogRecord} from '../../packages/client-core/src/server-administration.ts';
const line = (sequence: string): LogRecord => ({sequence, at: '2026-10-22T19:00:00Z', level: 'info', category: 'server', message: 'Backup finished.'});
const settings = {revision: 1, logLevel: 'info', effectiveLevel: 'info', retention: [], levels: ['error','warn','info','debug'], categories: ['server','playback','scan','network','client']};
const page = {items: [line('2'), line('1')], limit: 200, nextCursor: '', level: 'info', effectiveLevel: 'info'};
const transport = {request: async <T>(path: string) => (path.includes('/settings') ? settings : page) as T};

test('list is chronological and filter requests use server filters', async () => {
  const calls: string[] = [];
  const source = new SettingsMessagesSource({request: async <T>(path: string) => {calls.push(path);return transport.request<T>(path);}}, undefined, () => true);
  await source.load();assert.equal(source.getSnapshot().phase, 'ready');assert.deepEqual(source.getSnapshot().lines.map(x => x.sequence), ['1','2']);
  await source.select('warn', 'network');assert.ok(calls.some(path => path.includes('level=warn') && path.includes('category=network')));source.dispose();
});

test('bounded records dedupe sequence strings without numeric precision loss', () => {
  const lines = Array.from({length: 500}, (_, i) => line(String(i)));
  assert.equal(appendMessage(lines, line('499')), lines);
  const next = appendMessage(lines, line('9007199254740993'));assert.equal(next.length, 500);assert.equal(next[0].sequence, '1');assert.equal(next[499].sequence, '9007199254740993');
});

test('held reads cannot publish into a retired authority', async () => {
  let current = true, release!: (value: unknown) => void;
  const source = new SettingsMessagesSource({request: async <T>(path: string) => path.includes('/settings') ? settings as T : new Promise<T>(resolve => {release = value => resolve(value as T);})}, undefined, () => current);
  const pending = source.load();current = false;release(page);await pending;assert.equal(source.getSnapshot().phase, 'idle');source.dispose();
});

test('live tail resets, dedupes, and resumes with opaque Last-Event-ID after normal end', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  const headers: Record<string,string>[] = [];let calls = 0;
  const source = new SettingsMessagesSource(transport, {open: async h => {
    headers.push(h);calls++;
    const events = calls === 1 ? `event: reset\nid: nonce:8\ndata: {}\n\nevent: log\nid: nonce:9\ndata: ${JSON.stringify(line('9'))}\n\nevent: log\nid: nonce:9\ndata: ${JSON.stringify(line('9'))}\n\n` : '';
    return new Response(events, {headers: {'Content-Type': 'text/event-stream'}});
  }}, () => true);
  await source.load();source.setLive(true);
  for (let n = 0; n < 20; n++) await Promise.resolve();
  assert.deepEqual(source.getSnapshot().lines.map(x => x.sequence), ['9']);assert.equal(source.getSnapshot().reconnecting, true);
  t.mock.timers.tick(2000);for (let n = 0; n < 20; n++) await Promise.resolve();
  assert.equal(headers[1]['Last-Event-ID'], 'nonce:9');source.pause();const count = calls;t.mock.timers.tick(120000);await Promise.resolve();assert.equal(calls, count);source.dispose();
});

test('429 Retry-After wins, exponential delay is bounded', () => {
  assert.equal(messageRetryMs(1, '90'), 90000);assert.equal(messageRetryMs(1, 'Thu, 01 Jan 1970 00:00:10 GMT', 0), 10000);assert.equal(messageRetryMs(50, null), 64000);
});

test('permission refusal stops live retries and clears retired lines', async () => {
  const source = new SettingsMessagesSource(transport, {open: async () => new Response('', {status: 403})}, () => true);
  await source.load();source.setLive(true);for (let n = 0; n < 15; n++) await Promise.resolve();
  assert.equal(source.getSnapshot().live, false);assert.equal(source.getSnapshot().phase, 'idle');assert.equal(source.getSnapshot().lines.length, 0);source.dispose();
});

const flush = async () => {for (let i = 0; i < 30; i++) await Promise.resolve();};
test('late filter reads and opens cannot replace the newest filter or duplicate a tail', async () => {
  const reads: ((value: unknown) => void)[] = [], opens: ((value: Response) => void)[] = [];
  let cancelled = 0;
  const source = new SettingsMessagesSource({request: async <T>(path: string) => path.includes('/settings') ? settings as T : new Promise<T>(resolve => reads.push(value => resolve(value as T)))}, {open: () => new Promise(resolve => opens.push(resolve))}, () => true);
  source.setLive(true);
  const first = source.select('warn', 'network'), second = source.select('error', 'server');
  reads[1]({...page, items: [line('20')]});await second;
  reads[0]({...page, items: [line('10')]});await first;
  assert.deepEqual(source.getSnapshot().lines.map(x => x.sequence), ['20']);assert.equal(opens.length, 2);
  opens[0](new Response(new ReadableStream({cancel() {cancelled++;}})));await flush();assert.equal(cancelled, 1);
  source.pause();opens[1](new Response(''));await flush();assert.equal(source.getSnapshot().live, false);source.dispose();
});

test('pause fences an ignored-abort read and prevents a delayed select from restarting live', async () => {
  let release!: (value: unknown) => void, opens = 0;
  const source = new SettingsMessagesSource({request: async <T>(path: string) => path.includes('/settings') ? settings as T : new Promise<T>(resolve => {release = value => resolve(value as T);})}, {open: async () => {opens++;return new Response('');}}, () => true);
  source.setLive(true);const pending = source.select('', 'network');source.pause();release(page);await pending;await flush();
  assert.equal(opens, 1);assert.equal(source.getSnapshot().lines.length, 0);source.dispose();
});

test('terminal list and thrown-open errors retire authority and report once', async () => {
  for (const lane of ['list', 'open']) {
    const errors: unknown[] = [];let opens = 0;
    const failure = Object.assign(new Error('Permission ended'), {code: 'not_permitted', status: 403});
    const source = new SettingsMessagesSource({request: async <T>(path: string) => {if (lane === 'list') throw failure;return transport.request<T>(path);}}, {open: async () => {opens++;throw failure;}}, () => true, error => errors.push(error));
    await source.load();source.setLive(true);await flush();await source.load();source.setLive(true);await flush();
    assert.equal(errors.length, 1);assert.equal(source.getSnapshot().lines.length, 0);assert.equal(opens, lane === 'list' ? 0 : 1);source.dispose();
  }
});

test('oversized streams cancel the underlying body before reconnect', async () => {
  let cancelled = false;
  const source = new SettingsMessagesSource(transport, {open: async () => new Response(new ReadableStream({start(controller) {controller.enqueue(new TextEncoder().encode('data: ' + 'x'.repeat(262145)));}, cancel() {cancelled = true;}}))}, () => true);
  source.setLive(true);await flush();assert.equal(cancelled, true);assert.equal(source.getSnapshot().reconnecting, true);source.dispose();
});

test('uncertain debug write reconciles the immutable intent before accepting a changed toggle', async () => {
  const writes: unknown[] = [];let reconcileFails = true;
  const source = new SettingsMessagesSource({request: async <T>(path: string, method?: string, body?: unknown) => {
    if (method === 'POST') {writes.push(body);throw new Error('Response lost');}
    if (reconcileFails) throw new Error('Read unavailable');
    return {...settings, debugWindowUntil: new Date(Date.now() + 60000).toISOString()} as T;
  }}, undefined, () => true);
  await assert.rejects(source.detail(true));await assert.rejects(source.detail(false));assert.equal(writes.length, 1);
  reconcileFails = false;await source.detail(false);assert.equal(writes.length, 1);assert.equal(source.getSnapshot().changing, false);source.dispose();
});


test('opposite debug action reconciles without replaying the old nonmatching intent', async () => {
  const writes: {operationId: string; minutes: number}[] = [];
  const source = new SettingsMessagesSource({request: async <T>(_path: string, method?: string, body?: unknown) => {
    if (method === 'POST') {writes.push(body as {operationId: string; minutes: number});throw new Error('Response lost');}
    return settings as T;
  }}, undefined, () => true);
  await assert.rejects(source.detail(true));await source.detail(false);
  assert.equal(writes.length, 1);assert.equal(writes[0].minutes, 30);
  assert.equal(source.getSnapshot().changing, false);source.dispose();
});

test('a queued old reconnect callback cannot open after pause or replacement', async t => {
  const callbacks: (() => void)[] = [];
  t.mock.method(globalThis, 'setTimeout', ((callback: () => void) => {callbacks.push(callback);return 1;}) as typeof setTimeout);
  t.mock.method(globalThis, 'clearTimeout', () => {});
  let opens = 0;
  const source = new SettingsMessagesSource(transport, {open: async () => {opens++;return new Response('');}}, () => true);
  source.setLive(true);await flush();assert.equal(opens, 1);assert.equal(callbacks.length, 1);
  source.pause();callbacks[0]();await flush();assert.equal(opens, 1);
  source.setLive(true);await flush();assert.equal(opens, 2);
  source.setLive(true);await flush();assert.equal(opens, 3);
  callbacks[1]();await flush();assert.equal(opens, 3);source.dispose();
});


test('message timestamps keep drawn seconds without a year and follow the viewer clock and zone', () => {
  const device = {locales: ['en-US'], timeZone: 'America/Halifax', hour12: true};
  const twelve = createI18n(resolveRegion({hourCycle: 'h12'}, device));
  const twentyFour = createI18n(resolveRegion({hourCycle: 'h23'}, device));
  assert.equal(messageTime(twelve, '2026-10-22T18:58:02Z'), 'Oct 22, 3:58:02 PM');
  assert.equal(messageTime(twentyFour, '2026-10-22T18:58:02Z'), 'Oct 22, 15:58:02');
});
