import test from 'node:test';
import assert from 'node:assert/strict';
import {SettingsSupportSource} from '../src/screens/settings/server/troubleshooting/support-source.ts';
import {DEFAULT_SUPPORT_COMPONENTS} from '../src/screens/settings/server/troubleshooting/support-options.ts';

const serverId = 'captured-server', fence = 'a'.repeat(64);
const envelope = (data: unknown) => ({scope: {serverId, viewerFence: fence}, data});
const capabilities = {items: [
  {capability: 'live_tv', available: true, checkedAt: '2026-10-22T18:00:00Z'},
  {capability: 'hardware_encoding', available: false, code: 'hardware_unavailable', detail: 'No usable encoder.', checkedAt: '2026-10-22T18:00:00Z'},
]};
const alert = {id: 'permissions', code: 'state-permissions', status: 'open', severity: 'warning', firstAt: '2026-10-22T17:00:00Z', lastAt: '2026-10-22T18:00:00Z', occurrences: 3, revision: 1};
const ready = {id: 'export-1', expiresAt: new Date(Date.now() + 60 * 60 * 1000).toISOString(), manifest: {version: '1', from: '2026-10-21T18:00:00Z', to: '2026-10-22T18:00:00Z',
  included: ['runtime', 'client'], excluded: ['media'], redaction: 'on', maxBytes: 1048576}};

function fixture(expectedComponents: readonly string[] = DEFAULT_SUPPORT_COMPONENTS) {
  const calls: {path: string; method: string; body: unknown}[] = [];
  let permissions = true;
  let receipt = ready;
  const transport = {request: async <T>(path: string, method = 'GET', body?: unknown) => {
    calls.push({path, method, body});
    if (path.includes('/diagnostics/capabilities')) return capabilities as T;
    if (path.includes('/state-permissions:fix')) {permissions = false;return undefined as T;}
    if (path.includes('/console/registry')) return envelope({fields: []}) as T;
    if (path.includes('/console/alerts')) return envelope(permissions ? [alert] : []) as T;
    if (path.includes('/console/exports/')) return envelope({...receipt, snapshot: {anything: true}}) as T;
    if (path.includes('/console/exports')) {
      const sent = body as {from: string; to: string; components: string[]};
      assert.deepEqual(sent.components, expectedComponents);
      receipt = {...ready, manifest: {...ready.manifest, from: sent.from, to: sent.to, included: [...sent.components]}};
      return envelope(receipt) as T;
    }
    return envelope({items: [], nextCursor: ''}) as T;
  }};
  return {source: new SettingsSupportSource(transport, serverId, 'captured-viewer', () => true), calls};
}

test('Support reads what the server can do and the live permissions warning, in one read', async () => {
  const f = fixture();
  try {
    await f.source.load();
    const state = f.source.getSnapshot();
    assert.equal(state.phase, 'ready');
    assert.deepEqual(state.capabilities.map(row => row.capability), ['live_tv', 'hardware_encoding']);
    assert.equal(state.capabilities[1].code, 'hardware_unavailable');
    assert.equal(state.permissions, true);
    assert.ok(Array.isArray(state.skipped));
  } finally {f.source.dispose();}
});

test('Prepare export defaults to the last day without app logs and keeps the server receipt', async () => {
  const f = fixture();
  try {
    await f.source.load();
    await f.source.prepare(24 * 60 * 60 * 1000, DEFAULT_SUPPORT_COMPONENTS);
    const created = f.calls.find(call => call.method === 'POST' && call.path.endsWith('/console/exports'));
    const sent = created?.body as {from: string; to: string};
    assert.equal(Date.parse(sent.to) - Date.parse(sent.from), 24 * 60 * 60 * 1000);
    assert.equal(f.source.getSnapshot().export?.id, 'export-1');
    const bundle = await f.source.download();
    assert.deepEqual(bundle.snapshot, {anything: true});
  } finally {f.source.dispose();}
});

test('app logs remain available through an explicit component opt-in', async () => {
  const parts = ['runtime', 'client', 'audit', 'snapshot'] as const, f = fixture(parts);
  try {
    await f.source.load();await f.source.prepare(3600000, parts);
    assert.deepEqual(f.source.getSnapshot().export?.manifest.included, parts);
  } finally {f.source.dispose();}
});

test('Fixing permissions clears the warning only after the server accepts the fix', async () => {
  const f = fixture();
  try {
    await f.source.load();
    assert.equal(await f.source.fixPermissions(), true);
    assert.equal(f.source.getSnapshot().permissions, false);
    assert.equal(f.source.getSnapshot().fixed, true);
  } finally {f.source.dispose();}
});

test('a retired scope reads, exports and fixes nothing more', async () => {
  const f = fixture();
  try {
    await f.source.load();
    f.source.dispose();
    const count = f.calls.length;
    await assert.rejects(f.source.download());
    assert.equal(await f.source.fixPermissions(), false);
    await f.source.load();
    assert.equal(f.calls.length, count);
  } finally {f.source.dispose();}
});

test('retiring between load and its queued start performs no requests', async () => {
  let current = true, calls = 0;
  const source = new SettingsSupportSource({request: async <T>() => {calls++;return capabilities as T;}}, serverId, 'viewer', () => current);
  const load = source.load();current = false;await load;
  assert.equal(calls, 0);source.dispose();
});

for (const failure of [Object.assign(new Error('Unauthorized'), {status: 401}), Object.assign(new Error('Forbidden'), {status: 403})]) {
  test(`unreadable alert ${failure.status} remains an error, never an absent warning`, async () => {
    const source = new SettingsSupportSource({request: async <T>(path: string) => {
      if (path.includes('capabilities')) return capabilities as T;
      throw failure;
    }}, serverId, 'viewer', () => true);
    await source.load();assert.equal(source.getSnapshot().phase, 'error');assert.equal(source.getSnapshot().error, failure);source.dispose();
  });
}

test('malformed alert scope fails the page', async () => {
  const source = new SettingsSupportSource({request: async <T>(path: string) => (path.includes('capabilities') ? capabilities : {scope: {serverId: 'other', viewerFence: fence}, data: []}) as T}, serverId, 'viewer', () => true);
  await source.load();assert.equal(source.getSnapshot().phase, 'error');source.dispose();
});

test('held requestConsole download cannot reach a download sink after parent retirement', async () => {
  let current = true, release!: (value: unknown) => void, started!: () => void, sinks = 0;
  const entered = new Promise<void>(resolve => {started = resolve;});
  const transport = {
    request: async <T>() => capabilities as T,
    requestConsole: async <T>(path: string, _method: string, _body: unknown, signal: AbortSignal) => {
      assert.ok(signal);
      if (path.includes('/exports/')) {started();return new Promise<T>(resolve => {release = value => resolve(value as T);});}
      const sent = _body as {from:string;to:string;components:string[]};
      const receipt = {...ready, manifest: {...ready.manifest, from: sent.from, to: sent.to, included: [...sent.components]}};
      return envelope(receipt) as T;
    },
  };
  const source = new SettingsSupportSource(transport, serverId, 'viewer', () => current);
  await source.prepare(3600000, ['runtime']);
  const download = source.download().then(() => {sinks++;});
  await entered;current = false;release(envelope({...ready, snapshot: {}}));
  await assert.rejects(download, {name: 'AbortError'});assert.equal(sinks, 0);assert.equal(source.canDownload(ready.id), false);source.dispose();
});

test('permissions fix is single flight and uncertain retries retain operation identity', async () => {
  let calls = 0, release!: () => void;const ids: unknown[] = [];
  const source = new SettingsSupportSource({request: async <T>(_path: string, _method?: string, body?: unknown) => {
    calls++;ids.push((body as {operationId: string}).operationId);
    if (calls === 1) {await new Promise<void>(resolve => {release = resolve;});throw new Error('Lost response');}
    return undefined as T;
  }}, serverId, 'viewer', () => true);
  const first = source.fixPermissions(), second = source.fixPermissions();assert.equal(first, second);
  await Promise.resolve();assert.equal(calls, 1);release();await assert.rejects(first);
  assert.equal(await source.fixPermissions(), true);assert.equal(ids[0], ids[1]);source.dispose();
});
