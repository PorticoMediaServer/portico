import test from 'node:test';
import assert from 'node:assert/strict';
import {DownloadsService} from '@core/downloads-service.ts';
import {SavedOperations} from '../src/screens/saved/operations.ts';
import {SavedReadyDownloads, readyArtifactKey} from '../src/screens/saved/ready-downloads.ts';
import type {SavedDownloadsScope} from '../src/screens/saved/downloads-port.ts';

const scope: SavedDownloadsScope = {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', sessionFamilyId: 'family'};
const artifact = {kind: 'prepared', ref: 'version', sha256: 'a'.repeat(64), bytes: 4096, estimated: false, container: 'mp4', contentType: 'video/mp4', fileName: 'film.mp4'};
const preparation = (overrides: Record<string, unknown> = {}) => ({
  id: 'preparation', itemId: 'item', libraryId: 'library', profileId: 'profile', quality: 'original', origin: 'item',
  title: {kind: 'movie', title: 'Film', onServer: true}, batchId: '', state: 'ready', reason: '', artifact,
  progress: {bytesDone: 4096, bytesTotal: 4096, bytesTotalEstimated: false, percent: 100, etaMs: null},
  actions: ['remove'], revision: 1, createdAt: '2026-10-23T10:00:00Z', updatedAt: '2026-10-23T10:01:00Z', readyAt: '2026-10-23T10:01:00Z', expiresAt: '2026-10-30T10:01:00Z', ...overrides,
});
function source() {
  let items: unknown[] = [];
  const service = new DownloadsService({async request<T>(path: string): Promise<T> {
    if (path.startsWith('/v1/downloads/preparations?')) return {items, nextCursor: ''} as T;
    if (path === '/v1/downloads/usage') return {profileId: 'profile', profileBytes: 0, profileCount: 0, serverBytes: 0, serverCount: 0, distinctArtifactBytes: 0, maxPreparedBytes: 0, remainingBytes: null, retentionDays: 7, profiles: []} as T;
    throw new Error(`Unexpected producer request: ${path}`);
  }});
  return {service, async publish(rows: unknown[]) { items = rows; await service.refresh(); }};
}

test('one real service hands each exact READY artifact off once, independent of preparation revision', async () => {
  const server = source(), parent = new SavedOperations(() => true), keys: string[] = [];
  const observer = new SavedReadyDownloads(server.service, scope, parent, async value => { keys.push(readyArtifactKey(value)!); }, assert.fail);
  try {
    await server.publish([preparation({state: 'running'})]);
    assert.equal(keys.length, 0);
    await server.publish([preparation()]);
    await server.publish([preparation({revision: 2, updatedAt: '2026-10-23T10:02:00Z'})]);
    await server.publish([preparation({artifact: {...artifact, fileName: 'renamed.mp4', contentType: 'video/other'}})]);
    assert.equal(keys.length, 1, 'grant/progress revision and display fields do not requeue bytes');
    for (const changed of [{bytes: 8192}, {sha256: 'b'.repeat(64)}, {kind: 'source'}, {ref: 'other-version'}]) await server.publish([preparation({artifact: {...artifact, ...changed}})]);
    await server.publish([preparation({id: 'different-preparation'})]);
    assert.equal(keys.length, 6, 'each actual identity field distinguishes the native handoff');
  } finally { observer.dispose(); parent.dispose(); server.service.dispose(); }
});

test('native queue acknowledgement does not fabricate local transfer state or rewrite the preparation', async () => {
  const server = source(), parent = new SavedOperations(() => true);
  let handoffs = 0;
  const observer = new SavedReadyDownloads(server.service, scope, parent, async () => { handoffs++; }, assert.fail);
  try {
    await server.publish([preparation()]);
    const snapshot = server.service.getSnapshot();
    await server.publish([preparation({revision: 3})]);
    assert.equal(handoffs, 1);
    assert.equal(snapshot.items[0].state, 'ready');
    assert.equal('localDownloads' in snapshot, false);
    assert.equal(Object.isFrozen(snapshot.items[0]), true);
  } finally { observer.dispose(); parent.dispose(); server.service.dispose(); }
});

test('captured profile and retired family cannot start or report an old handoff', async () => {
  const server = source(); let current = true, signal: AbortSignal | undefined, finish!: () => void, calls = 0, errors = 0;
  const parent = new SavedOperations(() => current);
  const pending = new SavedReadyDownloads(server.service, scope, parent, (_preparation, value) => {
    calls++; signal = value;
    return new Promise<void>(resolve => { finish = resolve; });
  }, () => { errors++; });
  try {
    await server.publish([preparation({profileId: 'another-profile'})]); assert.equal(calls, 0);
    await server.publish([preparation()]); assert.equal(calls, 1);
    current = false; pending.dispose(); parent.dispose();
    assert.equal(signal?.aborted, true);
    finish(); await Promise.resolve();
    await server.publish([preparation({id: 'late-preparation'})]);
    assert.equal(calls, 1); assert.equal(errors, 0);
  } finally { pending.dispose(); parent.dispose(); server.service.dispose(); }
});

test('a failed artifact handoff is reported once without a refresh retry loop', async () => {
  const server = source(), parent = new SavedOperations(() => true), failure = new Error('Native queue refused');
  let calls = 0; const errors: unknown[] = [];
  const observer = new SavedReadyDownloads(server.service, scope, parent, async () => { calls++; throw failure; }, error => { errors.push(error); });
  try {
    await server.publish([preparation()]); await Promise.resolve();
    await server.publish([preparation({revision: 10})]); await Promise.resolve();
    assert.equal(calls, 1); assert.deepEqual(errors, [failure]);
  } finally { observer.dispose(); parent.dispose(); server.service.dispose(); }
});
