import test from 'node:test';
import assert from 'node:assert/strict';
import {createI18n, resolveRegion} from '../../packages/i18n/src/index.ts';
import {messageTime} from '../src/screens/settings/server/troubleshooting/message-time.ts';
import {SettingsMessagesSource, appendMessage, messageRetryMs} from '../src/screens/settings/server/troubleshooting/messages-source.ts';
import type {MessageLine} from '../src/screens/settings/server/troubleshooting/messages-source.ts';
// Lines as the rebuilt server sends them (`GET /v1/admin/logs`, `event: line`), a second apart.
const wire = (n: number, more: Record<string, unknown> = {}) => ({time: new Date(Date.UTC(2026, 10, 12, 19, 0, n)).toISOString(), level: 'info', message: 'Backup finished ' + n, ...more});
const line = (sequence: string): MessageLine => ({sequence, at: '2026-10-22T19:00:00Z', level: 'info', message: 'Backup finished.', detail: ''});
const logsDoc = {name: 'logs', values: {keepDays: 30}, defaults: {keepDays: 30}, etag: '"1"'};
const page = {items: [wire(2), wire(1)]};
const transport = {request: async <T>(path: string) => (path.includes('/settings/') ? logsDoc : page) as T};
test('Messages reads the rebuilt server\'s log oldest first, filtered by level only; the owner\'s source also reads how long messages are kept', async () => {
  const calls: string[] = [];
  const request = async <T>(path: string) => {calls.push(path);return transport.request<T>(path);};
  const admin = new SettingsMessagesSource({request}, undefined, () => true);
  await admin.load();assert.equal(admin.getSnapshot().phase, 'ready');
  assert.deepEqual(admin.getSnapshot().lines.map(x => x.message), ['Backup finished 1', 'Backup finished 2']);
  assert.deepEqual(calls, ['/v1/admin/logs'], 'an administrator is never sent the owner\'s settings read');
  await admin.select('warn');assert.equal(calls.at(-1), '/v1/admin/logs?level=warn');admin.dispose();
  calls.length = 0;
  const owner = new SettingsMessagesSource({request}, undefined, () => true, undefined, {keeping: true});
  await owner.load();assert.deepEqual(calls.sort(), ['/v1/admin/logs', '/v1/admin/settings/logs']);assert.equal(owner.getSnapshot().keepDays, 30);owner.dispose();
});

test('a line shows its values, and a line the app cannot read fails the read', async () => {
  const shown = new SettingsMessagesSource({request: async <T>() => ({items: [wire(1, {level: 'error', message: 'Scan failed', attrs: {library: 'Films', error: 'not mounted'}})], detailUntil: '2026-11-12T20:00:00Z'}) as T}, undefined, () => true);
  await shown.load();
  const [first] = shown.getSnapshot().lines;
  assert.deepEqual([first.level, first.message, first.detail], ['error', 'Scan failed', 'library: Films · error: not mounted']);
  assert.equal(shown.getSnapshot().detailUntil, '2026-11-12T20:00:00Z');shown.dispose();
  const bad = new SettingsMessagesSource({request: async <T>() => ({items: [{time: 'soon', level: 'loud', message: 1}]}) as T}, undefined, () => true);
  await bad.load();assert.equal(bad.getSnapshot().phase, 'error');bad.dispose();
});

test('bounded lines dedupe and keep time order', () => {
  const lines = Array.from({length: 500}, (_, i) => ({...line(String(i)), at: new Date(Date.UTC(2026, 10, 12, 0, 0, i)).toISOString()}));
  assert.equal(appendMessage(lines, lines[499]), lines);
  const late = {...line('late'), at: new Date(Date.UTC(2026, 10, 12, 0, 0, 600)).toISOString()};
  const next = appendMessage(lines, late);assert.equal(next.length, 500);assert.equal(next[0].sequence, '1');assert.equal(next[499].sequence, 'late');
  const earlier = {...line('earlier'), at: new Date(Date.UTC(2026, 10, 12, 0, 0, 250)).toISOString()};
  const inserted = appendMessage(next, earlier);assert.equal(inserted[249].sequence, 'earlier');
});

test('held reads cannot publish into a retired authority', async () => {
  let current = true, release!: (value: unknown) => void;
  const source = new SettingsMessagesSource({request: async <T>() => new Promise<T>(resolve => {release = value => resolve(value as T);})}, undefined, () => current);
  const pending = source.load();current = false;release(page);await pending;assert.equal(source.getSnapshot().phase, 'idle');source.dispose();
});

test('following live adds each line once, after the lines already shown, and opens again when the server ends it', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});
  let calls = 0;
  const source = new SettingsMessagesSource(transport, {open: async () => {
    calls++;
    // The server sends the last lines it holds first: those already on the page are not shown twice.
    const events = calls === 1 ? [wire(1), wire(2), wire(3), wire(3)].map(l => `event: line\ndata: ${JSON.stringify(l)}\n\n`).join('') + ': still here\n\n' : '';
    return new Response(events, {headers: {'Content-Type': 'text/event-stream'}});
  }}, () => true);
  await source.load();source.setLive(true);
  for (let n = 0; n < 20; n++) await Promise.resolve();
  assert.deepEqual(source.getSnapshot().lines.map(x => x.message), ['Backup finished 1', 'Backup finished 2', 'Backup finished 3']);assert.equal(source.getSnapshot().reconnecting, true);
  t.mock.timers.tick(2000);for (let n = 0; n < 20; n++) await Promise.resolve();
  assert.equal(calls, 2);source.pause();const count = calls;t.mock.timers.tick(120000);await Promise.resolve();assert.equal(calls, count);source.dispose();
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
  const source = new SettingsMessagesSource({request: async <T>() => new Promise<T>(resolve => reads.push(value => resolve(value as T)))}, {open: () => new Promise(resolve => opens.push(resolve))}, () => true);
  source.setLive(true);
  const first = source.select('warn'), second = source.select('error');
  reads[1]({items: [wire(20)]});await second;
  reads[0]({items: [wire(10)]});await first;
  assert.deepEqual(source.getSnapshot().lines.map(x => x.message), ['Backup finished 20']);assert.equal(opens.length, 2);
  opens[0](new Response(new ReadableStream({cancel() {cancelled++;}})));await flush();assert.equal(cancelled, 1);
  source.pause();opens[1](new Response(''));await flush();assert.equal(source.getSnapshot().live, false);source.dispose();
});

test('pause fences an ignored-abort read and prevents a delayed select from restarting live', async () => {
  let release!: (value: unknown) => void, opens = 0;
  const source = new SettingsMessagesSource({request: async <T>() => new Promise<T>(resolve => {release = value => resolve(value as T);})}, {open: async () => {opens++;return new Response('');}}, () => true);
  source.setLive(true);const pending = source.select('warn');source.pause();release(page);await pending;await flush();
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

test('Record more detail starts for the chosen minutes and ends with 0; Keep messages saves the owner\'s days', async () => {
  const writes: [string, string | undefined, unknown][] = [];
  const until = '2026-11-12T20:00:00Z';
  const request = async <T>(path: string, method?: string, body?: unknown) => {
    if (method === 'PUT') {writes.push([path, method, body]);return (path.endsWith('/detail') ? ((body as {minutes: number}).minutes ? {detailUntil: until} : {}) : {...logsDoc, values: body}) as T;}
    return transport.request<T>(path);
  };
  const source = new SettingsMessagesSource({request}, undefined, () => true, undefined, {keeping: true});
  await source.load();
  await source.detail(true);assert.equal(source.getSnapshot().detailUntil, until);
  await source.detail(true, 120);
  await source.detail(false);assert.equal(source.getSnapshot().detailUntil, undefined);
  await source.keep(7);assert.equal(source.getSnapshot().keepDays, 7);
  assert.deepEqual(writes, [['/v1/admin/logs/detail', 'PUT', {minutes: 30}], ['/v1/admin/logs/detail', 'PUT', {minutes: 120}], ['/v1/admin/logs/detail', 'PUT', {minutes: 0}], ['/v1/admin/settings/logs', 'PUT', {keepDays: 7}]]);
  assert.equal(source.getSnapshot().changing, false);source.dispose();
  const admin = new SettingsMessagesSource({request}, undefined, () => true);
  await admin.keep(14);assert.equal(writes.length, 4, 'an administrator\'s source never saves the owner\'s setting');admin.dispose();
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
