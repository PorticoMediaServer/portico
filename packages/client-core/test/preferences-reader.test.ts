import assert from 'node:assert/strict';
import test from 'node:test';
import {PreferencesReader,type PreferencesApi} from '../src/preferences-reader.ts';

const fence = 'a'.repeat(64);
const envelope = (data: unknown, serverId = 'server-one', viewerFence = fence) => ({scope: {serverId, viewerFence}, data});
const digest = 'a'.repeat(64);
const fields = [
  {key: 'search.rememberHistory', type: 'boolean', default: true, scopes: ['profile-server'], group: 'search', labelKey: 'preferences.search.rememberHistory'},
  {key: 'appearance.reduceMotion', type: 'boolean', default: false, scopes: ['profile-device-class'], group: 'appearance', labelKey: 'preferences.appearance.reduceMotion'},
];
const snapshot = (revision = 1) => ({
  deviceClass: 'web', registryRevision: 'p39.A1', registry: {revision: 'p39.A1', fields},
  documents: [
    {scope: 'profile-server', revision, digest, activeRevision: revision, values: {'search.rememberHistory': false}},
    {scope: 'profile-device-class', revision: 1, digest, activeRevision: 1, values: {}},
  ],
  effective: {'search.rememberHistory': false, 'appearance.reduceMotion': false},
  effectiveSource: {'search.rememberHistory': 'profile-server', 'appearance.reduceMotion': 'default'},
  clampedFields: [],
});

function reader(handler: (path: string, method: string, body: unknown, signal: AbortSignal) => Promise<unknown>, viaConsole = true) {
  const api: PreferencesApi = viaConsole
    ? {async request<T>(): Promise<T> { throw new Error('unbounded fallback must not be used'); }, async requestConsole<T>(path: string, method: string, body: unknown, signal: AbortSignal) { return await handler(path, method, body, signal) as T; }}
    : {async request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) { return await handler(path, method ?? 'GET', body, signal ?? AbortSignal.timeout(1000)) as T; }};
  return new PreferencesReader(api, {serverId: 'server-one', viewerId: 'local:one:profile'});
}

test('preferences read parses the published snapshot without the console client', async () => {
  const seen: {path: string; method: string}[] = [];
  const r = reader(async (path, method) => { seen.push({path, method}); return envelope(snapshot()); });
  const parsed = await r.preferences('web');
  assert.equal(parsed.effective['search.rememberHistory'], false);
  assert.deepEqual(seen, [{path: '/v1/preferences?deviceClass=web', method: 'GET'}]);
  r.dispose();
});

test('the plain request transport works when requestConsole is absent', async () => {
  const r = reader(async () => envelope(snapshot()), false);
  assert.equal((await r.preferences('web')).deviceClass, 'web');
  r.dispose();
});

test('a changed server or fence ends the scope instead of mixing viewers', async () => {
  let foreign = false;
  const r = reader(async () => envelope(snapshot(), foreign ? 'server-two' : 'server-one'));
  await r.preferences('web');
  foreign = true;
  await assert.rejects(r.preferences('web'), /selected viewer changed/);
  r.dispose();
  let changed = false;
  const fenced = reader(async () => envelope(snapshot(), 'server-one', changed ? 'b'.repeat(64) : fence));
  await fenced.preferences('web');
  changed = true;
  await assert.rejects(fenced.preferences('web'), /selected viewer changed/);
  fenced.dispose();
});

test('a patch carries scope, revision, values and an idempotency key', async () => {
  let sent: Record<string, unknown> = {};
  const r = reader(async (path, method, body) => {
    assert.equal(path, '/v1/preferences');
    assert.equal(method, 'PATCH');
    sent = body as Record<string, unknown>;
    return envelope(snapshot(2));
  });
  const next = await r.applyPreferences('profile-server', 'web', 1, {'search.rememberHistory': true});
  assert.equal(sent.scope, 'profile-server');
  assert.equal(sent.deviceClass, 'web');
  assert.equal(sent.expectedRevision, 1);
  assert.deepEqual(sent.values, {'search.rememberHistory': true});
  assert.ok(typeof sent.idempotencyKey === 'string' && sent.idempotencyKey.length > 0);
  assert.equal(next.documents[0]!.revision, 2);
  r.dispose();
});

test('disposal ends in-flight work and refuses new reads', async () => {
  let resolve!: (v: unknown) => void;
  const r = reader(() => new Promise(res => { resolve = res; }));
  const pending = r.preferences('web');
  r.dispose();
  resolve(envelope(snapshot()));
  await assert.rejects(pending);
  await assert.rejects(r.preferences('web'), /closed/);
});

test('a selected viewer is required', () => {
  assert.throws(() => new PreferencesReader({request: async () => { throw new Error('unused'); }}, {serverId: '', viewerId: 'v'}));
});


test('confirmed snapshots feed live consumers; late reads cannot undo an applied patch',async()=>{
 let defer=false,finish!:(value:unknown)=>void;
 const r=reader(async(_path,method)=>method==='PATCH'?envelope(snapshot(3)):defer?await new Promise(resolve=>{finish=resolve;}):envelope(snapshot(1)));
 let publications=0;const unsubscribe=r.subscribe(()=>publications++);
 await r.preferences('web');assert.equal(r.getSnapshot()?.documents[0]?.revision,1);
 defer=true;const late=r.preferences('web');await new Promise(resolve=>setImmediate(resolve));
 await r.applyPreferences('profile-server','web',1,{'search.rememberHistory':true});
 finish(envelope(snapshot(2)));await late;
 assert.equal(r.getSnapshot()?.documents[0]?.revision,3);assert.equal(publications,2);
 unsubscribe();r.dispose();assert.equal(r.getSnapshot(),undefined);
});

test('device-class caches remain separate inside one fenced viewer',async()=>{
 const r=reader(async(path)=>envelope({...snapshot(),deviceClass:path.endsWith('tablet')?'tablet':'web'}));
 await r.preferences('web');await r.preferences('tablet');
 assert.equal(r.getSnapshot('web')?.deviceClass,'web');assert.equal(r.getSnapshot('tablet')?.deviceClass,'tablet');r.dispose();
});

test('typed observation distinguishes validated scope changes from malformed foreign proof', async () => {
  let body: unknown = envelope(snapshot());
  const r = reader(async () => body);
  assert.equal((await r.observePreferences('web')).kind, 'ok');
  body = envelope({bad: 'data'}, 'server-two');
  const malformed = await r.observePreferences('web');
  assert.equal(malformed.kind, 'unreadable');
  body = envelope(snapshot(), 'server-two');
  assert.deepEqual(await r.observePreferences('web'), {kind: 'scope-changed'});
  body = envelope(snapshot(), 'server-one', 'b'.repeat(64));
  assert.deepEqual(await r.observePreferences('web'), {kind: 'scope-changed'});
  assert.equal(r.getSnapshot()?.documents[0]?.revision, 1); r.dispose();
});

test('typed observations forward cancellation, refuse pre-abort and preserve original transport errors', async () => {
  let calls = 0, signal!: AbortSignal;
  const failure = Object.assign(new Error('busy'), {retryAfterSeconds: Infinity});
  const r = reader(async (_p, _m, _b, owned) => { calls++; signal = owned; throw failure; });
  const pre = new AbortController(); pre.abort();
  assert.equal((await r.observePreferences('web', pre.signal)).kind, 'unreadable'); assert.equal(calls, 0);
  const failed = await r.observePreferences('web'); assert.equal(failed.kind, 'unreadable');
  if (failed.kind === 'unreadable') assert.equal(failed.error, failure);
  assert.equal(calls, 1); assert.equal(signal.aborted, false); r.dispose();

  let finish!: (raw: unknown) => void, owned!: AbortSignal;
  const late = reader(async (_p, _m, _b, requestSignal) => { owned = requestSignal; return new Promise(resolve => { finish = resolve; }); });
  const controller = new AbortController(), result = late.observePreferences('web', controller.signal);
  controller.abort(); assert.equal(owned.aborted, true);
  finish(envelope(snapshot())); assert.equal((await result).kind, 'unreadable'); assert.equal(late.getSnapshot(), undefined); late.dispose();
});

test('observation removes caller abort listeners after both success and failure', async () => {
  const controller = new AbortController(); let added = 0, removed = 0, fail = false;
  const signal = controller.signal;
  const add = signal.addEventListener.bind(signal), remove = signal.removeEventListener.bind(signal);
  signal.addEventListener = (...args: Parameters<typeof signal.addEventListener>) => { added++; return add(...args); };
  signal.removeEventListener = (...args: Parameters<typeof signal.removeEventListener>) => { removed++; return remove(...args); };
  const r = reader(async () => { if (fail) throw new Error('offline'); return envelope(snapshot()); });
  await r.observePreferences('web', signal); fail = true; await r.observePreferences('web', signal);
  assert.equal(added, 2); assert.equal(removed, 2); r.dispose();
});

test('observations retain revision-ordered adoption without retrofitting default single-flight', async () => {
  const pending: ((raw: unknown) => void)[] = [];
  const r = reader(async (_p, method) => method === 'PATCH' ? envelope(snapshot(5)) : new Promise(resolve => pending.push(resolve)));
  const observed = r.observePreferences('web'), ordinary = r.preferences('web');
  assert.equal(pending.length, 2, 'opt-in and default reads keep independent transport ownership');
  await r.applyPreferences('profile-server', 'web', 1, {'search.rememberHistory': true});
  pending[0]!(envelope(snapshot(2))); pending[1]!(envelope(snapshot(3)));
  const result = await observed; await ordinary;
  assert.equal(result.kind, 'ok'); if (result.kind === 'ok') assert.equal(result.value.documents[0]?.revision, 5);
  assert.equal(r.getSnapshot()?.documents[0]?.revision, 5); r.dispose();
});

test('disposal prevents further observation adoption', async () => {
  let finish!: (raw: unknown) => void;
  const r = reader(async () => new Promise(resolve => { finish = resolve; }));
  const pending = r.observePreferences('web'); r.dispose(); finish(envelope(snapshot()));
  assert.equal((await pending).kind, 'unreadable'); assert.equal(r.getSnapshot(), undefined);
  assert.equal((await r.observePreferences('web')).kind, 'unreadable');
});

test('a successful envelope for a different device class is unreadable to the observing owner', async () => {
  const r = reader(async () => envelope({...snapshot(), deviceClass: 'tablet'}));
  assert.equal((await r.observePreferences('web')).kind, 'unreadable');
  assert.equal(r.getSnapshot('tablet'), undefined); r.dispose();
});
