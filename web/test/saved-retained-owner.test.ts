import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi} from '@portico/client-core';
import {createRetainedSavedOwner} from '../src/screens/saved/retained-owner.ts';
import {installSavedDownloadsBinder, type SavedDownloadsBinderInput} from '../src/screens/saved/native-binding.ts';
import type {SavedDownloadsPort, SavedDownloadsScope, SavedDownloadsSnapshot} from '../src/screens/saved/downloads-port.ts';

const scope: SavedDownloadsScope = {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', sessionFamilyId: 'family'};
const tick = () => new Promise<void>(resolve => setImmediate(resolve));
async function settle(done: () => boolean) {for (let i = 0; i < 30 && !done(); i++) await tick();assert.ok(done());}
const prepared = {id: 'preparation', itemId: 'item', libraryId: 'library', profileId: 'profile', quality: 'original', origin: 'item',
  title: {kind: 'movie', title: 'Stored title', onServer: true}, batchId: '', state: 'ready', reason: '',
  artifact: {kind: 'prepared', ref: 'version', sha256: 'a'.repeat(64), bytes: 4096, estimated: false, container: 'mp4', contentType: 'video/mp4', fileName: 'film.mp4'},
  progress: {bytesDone: 4096, bytesTotal: 4096, bytesTotalEstimated: false, percent: 100, etaMs: null}, actions: ['remove'], revision: 1,
  createdAt: '2026-10-23T10:00:00Z', updatedAt: '2026-10-23T10:01:00Z', readyAt: '2026-10-23T10:01:00Z', expiresAt: '2026-10-30T10:01:00Z'};
function source() {
  const requests: string[] = [], errors: unknown[] = [];let current = true;
  const api = new HttpLocalApi('http://127.0.0.1:39117', 'unit-token', async input => {
    const path = new URL(String(input)).pathname;requests.push(path);
    const value = path === '/v1/downloads/preparations' ? {items: [prepared], nextCursor: ''}
      : path === '/v1/downloads/usage' ? {profileId: 'profile', profileBytes: 0, profileCount: 0, serverBytes: 0, serverCount: 0, distinctArtifactBytes: 0, maxPreparedBytes: 0, remainingBytes: null, retentionDays: 7, profiles: []}
        : undefined;
    assert.notEqual(value, undefined, path);return new Response(JSON.stringify(value), {headers: {'Content-Type': 'application/json'}});
  });
  const owner = createRetainedSavedOwner({api, scope, isCurrent: () => current, reportError: error => errors.push(error)});
  return {owner, api, requests, errors, retire() {current = false;}};
}
function port(portScope = scope) {
  const listeners = new Set<() => void>();let disposed = 0;
  let state: SavedDownloadsSnapshot = {available: false, phase: 'ready', items: []};
  const unused = async (): Promise<never> => {throw new Error('Unused native command');};
  const value: SavedDownloadsPort = {scope: portScope, getSnapshot: () => state, subscribe(listener) {listeners.add(listener);return () => {listeners.delete(listener);};},
    refresh: unused, loadMore: unused, action: unused, play: unused, getArtwork: unused, actionGroup: unused, setRules: unused,
    chooseFolder: unused, openFolder: unused, previewOtherProfiles: unused, removeOtherProfiles: unused, dispose() {disposed++;listeners.clear();}};
  return {value, disposed: () => disposed, ready() {state = {...state, available: true};for (const listener of [...listeners]) listener();}};
}

test('one retained Saved runtime binds lazily and hands READY bytes off once across repeated route borrowing', async () => {
  const f = source(), native = port();let input: SavedDownloadsBinderInput | undefined, handoffs = 0, active: AbortSignal | undefined;
  let finish!: () => void;
  assert.equal(f.requests.length, 0);f.owner.start();assert.equal(f.requests.length, 0);
  const remove = installSavedDownloadsBinder(async captured => {
    input = captured;return {localDownloads: native.value, dispose: native.value.dispose,
      saveReadyPreparation: async (_preparation, signal) => {handoffs++;active = signal;await new Promise<void>(resolve => {finish = resolve;});}};
  });
  try {
    await settle(() => !!f.owner.getSnapshot().localDownloads);
    assert.equal(input?.api, f.api);assert.equal(input?.runtime, f.owner.runtime);assert.deepEqual(input?.runtime.scope, scope);
    assert.equal(f.requests.length, 0, 'a port without real local availability starts no READY observer');
    const borrowed = f.owner.runtime;native.ready();await settle(() => handoffs === 1);
    f.owner.start();assert.equal(f.owner.runtime, borrowed);
    await f.owner.runtime.select({view: 'downloads'});await f.owner.runtime.downloads.refresh();
    assert.equal(handoffs, 1);assert.equal(active?.aborted, false);
    assert.equal(f.owner.getSnapshot().localDownloads?.getSnapshot().items.length, 0, 'native ACK does not manufacture a completed local row');
    assert.equal(f.owner.runtime.downloads.getSnapshot().items[0]?.state, 'ready');
    f.retire();assert.deepEqual(f.owner.getSnapshot(), {});assert.equal(input?.isCurrent(), false);
    f.owner.dispose();assert.equal(active?.aborted, true);assert.equal(native.disposed(), 1);assert.equal(input?.signal.aborted, true);
    finish();await tick();assert.deepEqual(f.errors, []);
  } finally {finish?.();f.owner.dispose();remove();}
});

test('a native bind completing after viewer retirement is disposed without installing an observer or exposing a port', async () => {
  const f = source(), native = port();let complete!: (value: {localDownloads: SavedDownloadsPort; saveReadyPreparation(): Promise<void>; dispose(): void}) => void;
  const remove = installSavedDownloadsBinder(() => new Promise(resolve => {complete = resolve;}));
  try {
    f.owner.start();f.retire();f.owner.dispose();
    complete({localDownloads: native.value, saveReadyPreparation: async () => {assert.fail('No late native command');}, dispose: native.value.dispose});
    await tick();assert.equal(native.disposed(), 1);assert.deepEqual(f.owner.getSnapshot(), {});assert.equal(f.requests.length, 0);assert.deepEqual(f.errors, []);
  } finally {f.owner.dispose();remove();}
});

test('a native port for another captured profile is rejected and its lease closed before any preparation read', async () => {
  const f = source(), native = port({...scope, profileId: 'other'});
  const remove = installSavedDownloadsBinder(async () => ({localDownloads: native.value, saveReadyPreparation: async () => {assert.fail('Wrong profile');}, dispose: native.value.dispose}));
  try {
    f.owner.start();await settle(() => f.errors.length === 1);
    assert.equal(native.disposed(), 1);assert.deepEqual(f.owner.getSnapshot(), {});assert.equal(f.requests.length, 0);
    assert.match(String(f.errors[0]), /saved_download_scope_mismatch/);
  } finally {f.owner.dispose();remove();}
});
