import test from 'node:test';
import assert from 'node:assert/strict';
import {RemoteFormSource} from '../src/screens/settings/server-remote/remote-form-source.ts';
import {settingsRowIndex} from '@core/presentation/settings-spec.ts';
import type {RemoteAccess, RemoteConfiguration} from '@core/remote-access.ts';
const config = (): RemoteConfiguration => ({revision: '3', enabled: true, mapping: true, pcp: true, natpmp: false, upnp: true,
  publicPort: 32500, gateway: '192.0.2.1', lanSharing: true, ipv6Open: false});
const status = (stored = config()): RemoteAccess => ({authorityId: 'original-authority', generation: '2', config: stored, state: 'reachable',
  topology: {lan: [], public: [], bind: '0.0.0.0'}, candidates: [], mappings: []});
const row = (id: string) => settingsRowIndex.get(id)!.row;
const held = <T>() => {let resolve!: (value: T) => void;const promise = new Promise<T>(done => {resolve = done;});return {promise, resolve};};
const turn = () => new Promise<void>(resolve => setImmediate(resolve));
function fixture(initial = config()) {
  const calls: {path: string; method: string; body: any; signal?: AbortSignal}[] = [];
  let stored = initial, live = true, failRead = false, failSave = false;
  const source = new RemoteFormSource({async request<T>(path: string, method: string, body?: unknown, signal?: AbortSignal): Promise<T> {
    calls.push({path, method, body, signal});
    if (method === 'POST') {
      if (failSave) throw {status: 503};
      const next = body as {authorityId: string; config: RemoteConfiguration};
      assert.equal(next.authorityId, 'original-authority');assert.equal(next.config.revision, stored.revision);
      stored = {...next.config, revision: String(Number(stored.revision) + 1)};
    } else if (failRead) throw {status: 503};
    return status(stored) as T;
  }}, () => live, true);
  return {source, calls, live: (value: boolean) => {live = value;}, failRead: (value: boolean) => {failRead = value;}, failSave: (value: boolean) => {failSave = value;}};
}

test('all five approved rows use one original form and save real revision and authority', async () => {
  const f = fixture(), s = f.source;
  try {
    assert.equal(f.calls.length, 0);await s.load();
    assert.deepEqual(['remote.mapping', 'remote.mappingProtocols', 'remote.gateway', 'remote.publicPort', 'remote.ipv6'].map(id => s.rowState(row(id)).value),
      ['auto', {pcp: 'try', natpmp: 'skip', upnp: 'try'}, '192.0.2.1', 32500, false]);
    s.change('remote.mappingProtocols', {pcp: 'skip', natpmp: 'try', upnp: 'skip'});
    s.change('remote.gateway', '192.0.2.2');s.change('remote.publicPort', 44444);s.change('remote.ipv6', true);
    s.change('remote.mapping', 'off');
    await turn();
    assert.equal(f.calls.length, 2);
    assert.deepEqual(f.calls[1].body.config, {...config(), mapping: false, pcp: false, natpmp: true, upnp: false, gateway: '192.0.2.2', publicPort: 44444, ipv6Open: true});
    assert.equal(s.getSnapshot().saved!.revision, '4');assert.equal(s.getSnapshot().dirty, false);
    await s.load();assert.equal(s.rowState(row('remote.mapping')).value, 'off');
    assert.equal(s.has('remote.enabled'), false, 'removed master switch is never offered');
    assert.equal(s.has('remote.allow'), false, 'Who can connect remains on its own canonical policy');
  } finally {s.dispose();}
});

test('unknown optional IPv6 round-trips absent; mapping off preserves unrelated fields', async () => {
  const stored = config();delete stored.ipv6Open;const f = fixture(stored), s = f.source;
  try {
    await s.load();const state = s.rowState(row('remote.ipv6'));
    assert.equal(state.value, undefined);assert.equal(state.readOnly, true);
    s.change('remote.ipv6', true);s.change('remote.mapping', 'off');
    s.change('remote.mappingProtocols', {pcp: 'skip', natpmp: 'skip', upnp: 'skip'});
    s.change('remote.gateway', 'ignored');await s.save();
    assert.deepEqual(f.calls.at(-1)!.body.config, {...stored, mapping: false});
    assert.equal(Object.hasOwn(f.calls.at(-1)!.body.config, 'ipv6Open'), false);
  } finally {s.dispose();}
});

test('a refused automatic save retains the draft and waits for deliberate Retry', async () => {
  const f = fixture(), s = f.source;
  try {
    f.failRead(true);await s.load();assert.ok(s.getSnapshot().loadError);assert.equal(s.getSnapshot().draft, undefined);
    f.failRead(false);await s.load();assert.equal(s.getSnapshot().loadError, undefined);
    f.failSave(true);s.change('remote.publicPort', 40000);await turn();
    assert.ok(s.getSnapshot().saveError);assert.equal(s.getSnapshot().dirty, true);assert.equal(s.getSnapshot().draft!.publicPort, 40000);
    await turn();assert.equal(f.calls.filter(call => call.method === 'POST').length, 1);
    f.failSave(false);await s.save();assert.equal(s.getSnapshot().saveError, undefined);assert.equal(s.getSnapshot().saved!.publicPort, 40000);
    assert.equal(f.calls.filter(call => call.method === 'POST').length, 2);
  } finally {s.dispose();}
});

test('uncommitted owner and non-owner readers never load or write', async () => {
  let reads = 0;const api = {request: async <T>() => {reads++;return status() as T;}};
  for (const [live, owner] of [[false, true], [true, false]]) {
    const source = new RemoteFormSource(api, () => live, owner);
    await source.load();source.change('remote.mapping', 'off');await assert.rejects(source.save(), {name: 'AbortError'});
    assert.equal(source.rowState(row('remote.mapping')).present, false);source.dispose();
  }
  assert.equal(reads, 0);
});

test('retired load and save refuse ignored-abort late results and no successor HTTP', async () => {
  const pending = held<RemoteAccess>();let reads = 0, live = true;
  const s = new RemoteFormSource({async request<T>() {reads++;return await (reads === 1 ? status() : pending.promise) as T;}}, () => live, true);
  await s.load();s.change('remote.publicPort', 40000);
  const saving = s.save();s.dispose();live = false;
  await assert.rejects(saving, {name: 'AbortError'});pending.resolve(status({...config(), publicPort: 40000, revision: '4'}));
  await Promise.resolve();assert.equal(s.getSnapshot().saved!.publicPort, 32500);
  await s.load();assert.equal(reads, 2);
});

test('a non-answering API has a bounded read and a bounded explicit retry', async t => {
  t.mock.timers.enable({apis: ['setTimeout']});let calls = 0;
  const s = new RemoteFormSource({request: async <T>() => {calls++;return await new Promise<T>(() => {});}}, () => true, true);
  try {
    const one = s.load(), duplicate = s.load();assert.equal(one, duplicate);await Promise.resolve();assert.equal(calls, 1);
    t.mock.timers.tick(15000);await one;assert.ok(s.getSnapshot().loadError);assert.equal(s.getSnapshot().loading, false);
    const retry = s.load();await Promise.resolve();assert.equal(calls, 2);t.mock.timers.tick(15000);await retry;assert.ok(s.getSnapshot().loadError);
  } finally {s.dispose();t.mock.timers.reset();}
});

test('invalid values and stale save/discard callbacks cannot write a replacement draft', async () => {
  const f = fixture(), s = f.source;
  try {
    await s.load();const old = s.getSnapshot();
    s.change('remote.publicPort', 1023);s.change('remote.publicPort', 2000.5);s.change('remote.gateway', 'x'.repeat(65));
    s.change('remote.mappingProtocols', {pcp: 'try', natpmp: 'skip', upnp: 'malformed'});
    assert.equal(s.getSnapshot(), old);
    s.change('remote.publicPort', 44000);const ignored = s.save(old);s.discard(old);
    assert.equal(f.calls.length, 1);assert.equal(s.getSnapshot().dirty, true);
    await ignored;await turn();assert.equal(f.calls.length, 2);assert.equal(s.getSnapshot().saved!.publicPort, 44000);
    assert.equal(s.rowState(row('remote.publicPort')).changed, true);
    s.reset('remote.publicPort');await turn();assert.equal(s.getSnapshot().saved!.publicPort, 32500);
    assert.equal(s.rowState(row('remote.publicPort')).changed, false);assert.equal(f.calls.length, 3);
  } finally {s.dispose();}
});


test('a synchronous loading subscriber coalesces into the already reserved form load', async () => {
  const f = fixture();let nested: Promise<void> | undefined;
  const stop = f.source.subscribe(() => {if (f.source.getSnapshot().loading) nested = f.source.load();});
  try {const original = f.source.load();await original;assert.equal(nested, original);assert.equal(f.calls.length, 1);}
  finally {stop();f.source.dispose();}
});

test('a current predicate returning true after retiring its source cannot dispatch a read', async () => {
  let source: RemoteFormSource, retire = false, calls = 0;
  source = new RemoteFormSource({request: async <T>() => {calls++;return status() as T;}}, () => {if (retire) source.dispose();return true;}, true);
  retire = true;await source.load();assert.equal(calls, 0);
});


test('retirement before the reserved read starts settles quietly without any HTTP', async () => {
  const f = fixture(), loading = f.source.load();f.source.dispose();await loading;
  assert.equal(f.calls.length, 0);
});

test('Check now coalesces and locks edits through the delayed status refresh', async () => {
  const check = held<RemoteAccess>(), refreshed = held<RemoteAccess>();const calls: string[] = [];
  const s = new RemoteFormSource({async request<T>(path: string, method = 'GET') {
    calls.push(`${method} ${path}`);
    return await (path.endsWith('/check') ? check.promise : calls.length === 1 ? status() : refreshed.promise) as T;
  }}, () => true, true);
  try {
    await s.load();let notifications = 0;const stop = s.subscribe(() => notifications++);
    const pending = s.check();assert.equal(s.check(), pending);assert.equal(s.getSnapshot().checking, true);
    assert.equal(s.rowState(row('remote.publicPort')).disabled, true);assert.equal(notifications, 1);
    s.change('remote.publicPort', 40000);await turn();assert.equal(s.getSnapshot().dirty, false);
    check.resolve(status());await turn();s.change('remote.publicPort', 41000);
    assert.equal(s.getSnapshot().checking, true);assert.equal(s.getSnapshot().draft!.publicPort, 32500);
    refreshed.resolve(status());await pending;
    assert.equal(s.getSnapshot().checking, false);assert.equal(s.rowState(row('remote.publicPort')).disabled, false);
    assert.deepEqual(calls, ['GET /v1/networking/remote', 'POST /v1/networking/remote/check', 'GET /v1/networking/remote']);stop();
  } finally {s.dispose();}
});

test('an edit made by a save-completion subscriber is not lost behind the save reservation', async () => {
  const f = fixture();let edited = false;
  try {
    await f.source.load();const stop = f.source.subscribe(() => {
      const state = f.source.getSnapshot();
      if (!edited && !state.saving && state.saved?.publicPort === 40000) {edited = true;f.source.change('remote.publicPort', 41000);}
    });
    f.source.change('remote.publicPort', 40000);await turn();
    assert.equal(f.source.getSnapshot().saved!.publicPort, 41000);assert.equal(f.calls.filter(call => call.method === 'POST').length, 2);stop();
  } finally {f.source.dispose();}
});
