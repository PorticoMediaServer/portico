import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {test} from 'node:test';
import {HttpLocalApi, ReadingError, parsePreferenceSnapshot} from '@portico/client-core';
import {routePath} from '../../../packages/contracts/src/index.ts';
import {createReadBindings} from '../../src/screens/read/bindings.ts';

const preferences = parsePreferenceSnapshot(JSON.parse(readFileSync(new URL('../../../server/testdata/contracts/get-bootstrap.json', import.meta.url), 'utf8')).response.preferences);
function fixture(options: {hold?: Promise<void>; route?: AbortSignal} = {}) {
  let routeCurrent = true;
  let selected = {sessionFamilyId: 'unit-family', viewer: {serverId: 'unit-server', accountId: 'unit-account', profileId: 'unit-profile', authority: 'local', role: 'member'}};
  const calls: {path: string; method: string}[] = [];
  let sequence = 0;
  const api = new HttpLocalApi('http://127.0.0.1:39117', 'unit-captured-token', async (input, init = {}) => {
    const path = new URL(String(input)).pathname; calls.push({path, method: init.method ?? 'GET'});
    if (path === routePath('post_reading_sessions')) { if (options.hold) await options.hold; return new Response(JSON.stringify({itemId: 'unit-book', sourceId: 'unit-source', sessionId: `unit-session-${++sequence}`,
      revision: 'a'.repeat(64), format: 'pdf', size: 1024, metadata: {title: 'Stored unit title'}}), {headers: {'Content-Type': 'application/json', ETag: '"' + 'a'.repeat(64) + '"'}}); }
    if (init.method === 'DELETE') {if (init.keepalive) calls.push({path, method: 'DELETE keepalive'}); return new Response(null, {status: 204});}
    throw new Error('unexpected unit operation');
  });
  const source = {getSnapshot: () => preferences, subscribe: () => () => {}};
  const bound = createReadBindings({api, session: () => selected, isCurrent: () => routeCurrent, preferences: source, facts: {preferences}, ...(options.route ? {libraryRouteSignal: options.route} : {})});
  return {bound, calls, source, api, leaveRoute() {routeCurrent = false;}, switchProfile() {selected = {...selected, viewer: {...selected.viewer, profileId: 'different-profile'}};}};
}

test('captured Read bindings are lazy and explicitly close the previous real facade session before replacement', async () => {
  const f = fixture(); assert.equal(f.calls.length, 0);
  assert.strictEqual(f.bound.preferences.getSnapshot(), preferences);
  const first = await f.bound.open({itemId: 'unit-book', sourceId: 'unit-source'});
  const second = await f.bound.open({itemId: 'unit-book', sourceId: 'unit-source'});
  assert.notEqual(first.manifest.sessionId, second.manifest.sessionId); assert.equal(first.signal.aborted, true);
  assert.deepEqual(f.calls.map(call => call.method), ['POST', 'DELETE', 'POST']);
  await f.bound.dispose(); assert.deepEqual(f.calls.map(call => call.method), ['POST', 'DELETE', 'POST', 'DELETE']);
  assert.equal(f.bound.client.signal.aborted, true); assert.equal(f.bound.authority.isCurrent(), false);
  await f.bound.dispose(); assert.equal(f.calls.length, 4);
});

test('profile replacement retires locally without a cleanup request under another viewer', async () => {
  const f = fixture(), opened = await f.bound.open({itemId: 'unit-book', sourceId: 'unit-source'});
  f.switchProfile(); assert.equal(f.bound.authority.isCurrent(), false);
  await assert.rejects(f.bound.open({itemId: 'unit-book', sourceId: 'unit-source'}), error => error instanceof ReadingError && error.code === 'cancelled');
  await f.bound.dispose(); assert.equal(opened.signal.aborted, true); assert.deepEqual(f.calls.map(call => call.method), ['POST']);
});

test('token retirement uses the captured facade lifetime and notifies the mounted reader', async () => {
  const f = fixture(); let retired = 0;
  const unsubscribe = f.bound.authority.subscribe(() => retired++);
  await f.bound.open({itemId: 'unit-book', sourceId: 'unit-source'});
  f.api.setAccessToken('unit-replacement-token'); assert.equal(f.bound.authority.isCurrent(), false); assert.equal(retired, 1);
  unsubscribe(); await f.bound.dispose(); assert.equal(retired, 1); assert.deepEqual(f.calls.map(call => call.method), ['POST']);
});

test('leaving the reader releases its server session though the route has already gone (E-web-15)', async () => {
  const f = fixture(), opened = await f.bound.open({itemId: 'unit-book', sourceId: 'unit-source'});
  f.leaveRoute();
  await f.bound.dispose();
  assert.equal(opened.signal.aborted, true);
  assert.deepEqual(f.calls.map(call => call.method), ['POST', 'DELETE'], 'the same viewer releases the session after navigation');
});

test('an open still on its way when the reader goes is released when it arrives, not leaked', async () => {
  let arrive!: () => void;
  const f = fixture({hold: new Promise<void>(resolve => {arrive = resolve;})});
  const opening = f.bound.open({itemId: 'unit-book', sourceId: 'unit-source'});
  await new Promise(resolve => setTimeout(resolve, 0));
  f.leaveRoute();
  const disposed = f.bound.dispose();
  arrive();
  await assert.rejects(opening, error => error instanceof ReadingError && error.code === 'cancelled');
  await disposed;
  assert.deepEqual(f.calls.map(call => call.method), ['POST', 'DELETE'], 'the late session is deleted before the binding retires');
});

test('the library route guard aborting before the reader unmounts still releases its sessions (E-web-15, round 4)', async () => {
  const route = new AbortController(), f = fixture({route: route.signal});
  let retired = 0; f.bound.authority.subscribe(() => retired++);
  const opened = await f.bound.open({itemId: 'unit-book', sourceId: 'unit-source'});
  route.abort(); f.leaveRoute();
  assert.equal(f.bound.authority.isCurrent(), false, 'the reader retires at once');
  assert.equal(retired, 1, 'and is told so');
  await f.bound.dispose();
  assert.equal(opened.signal.aborted, true);
  assert.deepEqual(f.calls.map(call => call.method), ['POST', 'DELETE'], 'the session is released before the client retires');
  assert.equal(f.bound.client.signal.aborted, true);
});

test('an open on its way when the route guard aborts is released on arrival', async () => {
  let arrive!: () => void;
  const route = new AbortController(), f = fixture({route: route.signal, hold: new Promise<void>(resolve => {arrive = resolve;})});
  const opening = f.bound.open({itemId: 'unit-book', sourceId: 'unit-source'});
  await new Promise(resolve => setTimeout(resolve, 0));
  route.abort(); f.leaveRoute(); arrive();
  await assert.rejects(opening, error => error instanceof ReadingError && error.code === 'cancelled');
  await f.bound.dispose();
  assert.deepEqual(f.calls.map(call => call.method), ['POST', 'DELETE']);
});

test('a hard page leave (reload, tab close) releases the held session with a keepalive farewell (E-web-33)', async () => {
  const page = new EventTarget(), before = (globalThis as {window?: unknown}).window;
  (globalThis as {window?: unknown}).window = page;
  try {
    const f = fixture(), opened = await f.bound.open({itemId: 'unit-book', sourceId: 'unit-source'});
    page.dispatchEvent(Object.assign(new Event('pagehide'), {persisted: true}));
    assert.equal(f.calls.some(call => call.method === 'DELETE keepalive'), false, 'a page kept for Back keeps its session');
    page.dispatchEvent(new Event('pagehide'));
    await new Promise(resolve => setTimeout(resolve, 0));
    assert.ok(f.calls.some(call => call.method === 'DELETE keepalive' && call.path === routePath('delete_reading_sessions_sessionId', {sessionId: opened.manifest.sessionId})), JSON.stringify(f.calls));
    await f.bound.dispose();
    const count = f.calls.length;
    page.dispatchEvent(new Event('pagehide'));
    await new Promise(resolve => setTimeout(resolve, 0));
    assert.equal(f.calls.length, count, 'a disposed binding sends nothing');
  } finally {(globalThis as {window?: unknown}).window = before;}
});
