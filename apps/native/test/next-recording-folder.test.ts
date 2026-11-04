import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeScreenLifetime} from '../src-next/screens/settings-live/lifetime';
import {NativeLiveOwnerController} from '../src-next/screens/settings-live/live-owner-controller';
import {NativeRecordingRoot} from '../src-next/screens/settings-live/recording-folder-store';
import type {ShellAuthority} from '../src-next/shell/model';

function authority() {
  const value: ShellAuthority = {serverId: 'server', accountId: 'account', profileId: 'main', epoch: 'session1', viewerFence: 'home-fence'};
  let valid = true;
  const listeners = new Set<() => void>();
  return {authority: () => value, current: () => valid, subscribeAuthority: (fn: () => void) => {listeners.add(fn); return () => {listeners.delete(fn);};}, revoke: () => {valid = false; for (const fn of [...listeners]) fn();}};
}
const root = (revision: number, requested: string, effective = requested, more: Partial<{operatorControlled: boolean}> = {}) =>
  ({revision, requested, effective, pendingRestart: !!requested && requested !== effective, operatorControlled: false, ...more});
function deferred<T>() {let resolve!: (value: T) => void, reject!: (error: unknown) => void; const promise = new Promise<T>((a, b) => {resolve = a; reject = b;}); return {promise, resolve, reject};}

test('the controller reads and saves the bare recordings root, and the explorer reads only listings', async () => {
  const owner = authority(), calls: Array<{path: string; method?: string; body?: unknown}> = [];
  const controller = new NativeLiveOwnerController(new NativeScreenLifetime(owner), {request: async <T,>(path: string, method?: string, body?: unknown) => {
    calls.push({path, method, body});
    if (path === '/v1/admin/dvr/recording-root') return (method === 'PATCH' ? root(3, '/srv/new', '/srv/old') : root(2, '', '/srv/old')) as T;
    return {entries: []} as T;
  }}, () => 'op');
  assert.deepEqual(await controller.recordingRoot(), root(2, '', '/srv/old'));
  const saved = await controller.saveRecordingRoot({expectedRevision: 2, operationId: 'w1', path: '/srv/new'});
  assert.equal(saved.pendingRestart, true);
  assert.deepEqual(calls[1], {path: '/v1/admin/dvr/recording-root', method: 'PATCH', body: {expectedRevision: 2, operationId: 'w1', path: '/srv/new'}});
  const opening = new AbortController(), browse = controller.folderBrowser(opening.signal);
  await browse.request('/v1/admin/filesystem?path=%2Fsrv');
  assert.equal(calls.at(-1)?.method, 'GET');
  await assert.rejects(browse.request('/v1/admin/dvr/recording-root', 'PATCH', {}));
  opening.abort();
  await assert.rejects(browse.request('/v1/admin/filesystem?path=%2F'));
  assert.equal(calls.length, 3);
  owner.revoke();
  await assert.rejects(controller.recordingRoot());
  assert.equal(calls.length, 3);
});

test('choosing a folder saves it once; a missing route hides the row; operator control blocks choosing', async () => {
  const writes: unknown[] = [];
  let current = root(4, '/srv/a');
  const store = new NativeRecordingRoot({read: async () => current, save: async intent => {writes.push(intent); current = root(5, intent.path, '/srv/a'); return current;}, current: () => true}, () => 'op1');
  await store.load();
  assert.equal(store.folder(), '/srv/a');
  await store.choose('/srv/a');
  assert.equal(writes.length, 0, 'the folder already in use is no change');
  await store.choose('/srv/b');
  assert.deepEqual(writes, [{expectedRevision: 4, operationId: 'op1', path: '/srv/b'}]);
  assert.equal(store.folder(), '/srv/b');
  assert.equal(store.getSnapshot().status?.pendingRestart, true);

  const gone = new NativeRecordingRoot({read: async () => {throw {status: 404, code: 'not_found'};}, save: async () => {throw Error('no');}, current: () => true});
  await gone.load();
  assert.equal(gone.getSnapshot().unsupported, true);

  const operated = new NativeRecordingRoot({read: async () => root(1, '/srv/x', '/srv/x', {operatorControlled: true}), save: async () => {throw Error('must not write');}, current: () => true});
  await operated.load();
  assert.equal(operated.canChoose(), false);
  await operated.choose('/srv/y');
});

test('an unconfirmed write retries with its own intent; a refused one rereads; a retired key is reread, never replayed', async () => {
  const writes: Array<{operationId: string; path: string}> = [];
  let reads = 0, fail: unknown = Object.assign(Error('offline'), {code: 'network'}), minted = 0;
  const store = new NativeRecordingRoot({read: async () => {reads++; return root(7, '/srv/a');}, save: async intent => {writes.push(intent); if (fail) {const error = fail; fail = undefined; throw error;} return root(8, intent.path);}, current: () => true}, () => `op${++minted}`);
  await store.load();
  await store.choose('/srv/b');
  assert.equal(store.getSnapshot().uncertain, true);
  assert.equal(store.canChoose(), false);
  await store.retry();
  assert.deepEqual(writes.map(write => write.operationId), ['op1', 'op1']);
  assert.equal(store.folder(), '/srv/b');

  fail = {status: 409, code: 'revision_mismatch'};
  await store.choose('/srv/c');
  assert.equal(store.getSnapshot().uncertain, false);
  assert.ok(store.getSnapshot().error);
  const before = reads;
  await store.retry();
  assert.equal(reads, before + 1, 'Try again after a refusal rereads');
  assert.equal(writes.length, 3);

  fail = {status: 410, code: 'console_expired'};
  await store.choose('/srv/d');
  assert.equal(writes.length, 4);
  assert.equal(store.getSnapshot().retired, true);
  assert.equal(reads, before + 2);
  await store.retry();
  assert.equal(writes.length, 4, 'a retired write is never replayed');
});

test('a read or write that outlives its store publishes nothing', async () => {
  const read = deferred<ReturnType<typeof root>>();
  let live = true;
  const store = new NativeRecordingRoot({read: () => read.promise, save: async () => root(1, '/x'), current: () => live});
  let notified = 0;
  store.subscribe(() => {notified++;});
  const loading = store.load();
  assert.equal(notified, 1);
  live = false;
  read.resolve(root(1, '/srv/a'));
  await loading;
  assert.equal(store.getSnapshot().status, undefined);
  assert.equal(notified, 1);

  const second = deferred<ReturnType<typeof root>>();
  const other = new NativeRecordingRoot({read: () => second.promise, save: async () => root(1, '/x'), current: () => true});
  const pending = other.load();
  other.dispose();
  second.resolve(root(1, '/srv/a'));
  await pending;
  assert.equal(other.getSnapshot().status, undefined);
});
