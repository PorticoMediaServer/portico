import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';
import {screenModule} from './helpers/screen-harness.mjs';

const card = {id: 'playlist-1', kind: 'playlist', title: 'Road trip', count: 3, navigation: {view: 'playlist', entityId: 'playlist-1'}};

class FakeSavedService {
  static instances: FakeSavedService[] = [];
  static mutations: string[] = [];
  static failAt = Infinity;
  static syncThrowAt = Infinity;
  static retrySucceeds = true;
  static retryGate: Promise<void> | null = null;
  static mutateGate: Promise<void> | null = null;
  route: any;
  intent: any;
  disposed = false;
  committed: string[] = [];
  snapshot: any = {phase: 'idle', projection: null, playlist: null, error: null, lastFailure: undefined};
  options: any;
  constructor(options: any) {this.options = options; FakeSavedService.instances.push(this);}
  subscribe = () => () => {};
  select = async (route: any) => {
    this.route = route;
    this.snapshot = {phase: 'ready', projection: route.view === 'playlists' ? {sections: [{entries: [card]}]} : null,
      playlist: route.view === 'playlist' ? {id: route.playlistId, revision: 7, actions: ['add'], entryCount: 0, limits: {maxEntries: 1000}} : null,
      error: null, lastFailure: undefined};
  };
  mutate = (intent: any): Promise<void> => {
    this.intent = intent;
    const attempt = FakeSavedService.mutations.length;
    FakeSavedService.mutations.push(intent.itemId);
    if (attempt === FakeSavedService.syncThrowAt) throw new Error('not queued');
    if (attempt === FakeSavedService.failAt) {
      this.snapshot = {...this.snapshot, lastFailure: {intent, operationId: `op-${intent.itemId}`, kind: 'retry-required'}};
      return Promise.reject(Object.assign(new Error('network'), {code: 'network'}));
    }
    const settle = () => {
      this.committed.push(intent.itemId);
      this.snapshot = {...this.snapshot, playlist: {...this.snapshot.playlist, revision: this.snapshot.playlist.revision + 1, entryCount: this.snapshot.playlist.entryCount + 1}};
    };
    if (FakeSavedService.mutateGate) return FakeSavedService.mutateGate.then(settle);
    settle();
    return Promise.resolve();
  };
  retryMutation = async () => {
    const failure = this.snapshot.lastFailure;
    if (!failure) return;
    if (FakeSavedService.retryGate) await FakeSavedService.retryGate;
    if (!FakeSavedService.retrySucceeds) throw new Error('kept failing');
    this.snapshot = {...this.snapshot, lastFailure: undefined};
    this.committed.push(failure.intent.itemId);
  };
  getSnapshot = () => this.snapshot;
  dispose = () => {this.disposed = true;};
}

function reset() {
  FakeSavedService.instances = [];
  FakeSavedService.mutations = [];
  FakeSavedService.failAt = Infinity;
  FakeSavedService.syncThrowAt = Infinity;
  FakeSavedService.retrySucceeds = true;
  FakeSavedService.retryGate = null;
  FakeSavedService.mutateGate = null;
}

async function port(album?: () => Promise<readonly string[]>) {
  const {MusicPlaylistPort} = await screenModule(new URL('../src-next/screens/destinations/music/playlist-port.ts', import.meta.url), {
    react: {default: {}},
    '../../../../../../packages/client-core/src/saved': {default: {}, SavedService: FakeSavedService},
  });
  return new MusicPlaylistPort({api: {request: async () => ({})}, scope: {serverId: 'server', viewerId: 'viewer'},
    current: () => true, requestId: async () => 'request-id', ...(album ? {album} : {})});
}

test('the playlist port lists the profile playlists from the Saved producer', async () => {
  reset();
  const value = await port();
  await value.load();
  const snapshot = value.getSnapshot();
  assert.equal(snapshot.phase, 'ready');
  assert.deepEqual(snapshot.playlists.map((playlist: any) => playlist.id), ['playlist-1']);
  assert.deepEqual(FakeSavedService.instances[0].route, {view: 'playlists'});
  value.dispose();
});

test('adding one leaf selects its real revision and uses SavedService add', async () => {
  reset();
  const value = await port();
  await value.load();
  await value.add('playlist-1', 'song-1');
  const writer = FakeSavedService.instances.at(-1)!;
  assert.deepEqual(writer.route, {view: 'playlist', playlistId: 'playlist-1'});
  assert.deepEqual(writer.intent, {action: 'add', itemId: 'song-1'});
  assert.equal(writer.disposed, true);
  assert.deepEqual(writer.committed, ['song-1']);
  assert.equal(value.getSnapshot().busy, false);
  value.dispose();
});

test('an ambiguous single leaf retries its own operation instead of adding twice', async () => {
  reset();
  FakeSavedService.failAt = 0;
  const value = await port();
  await value.load();
  await assert.rejects(value.add('playlist-1', 'song-1', 'Road trip'));
  const writer = FakeSavedService.instances.at(-1)!;
  assert.equal(value.getSnapshot().leaf?.itemId, 'song-1');
  assert.equal(value.getSnapshot().leaf?.playlistName, 'Road trip');
  assert.deepEqual(FakeSavedService.mutations, ['song-1']);
  await value.add('playlist-1', 'song-1', 'Road trip');
  assert.deepEqual(writer.committed, ['song-1']);
  assert.deepEqual(FakeSavedService.mutations, ['song-1']);
  assert.equal(value.getSnapshot().leaf, undefined);
  value.dispose();
});

test('a single leaf that threw before queueing retries with a fresh mutate', async () => {
  reset();
  FakeSavedService.syncThrowAt = 0;
  const value = await port();
  await value.load();
  await assert.rejects(value.add('playlist-1', 'song-1', 'Road trip'));
  assert.equal(value.getSnapshot().leaf?.itemId, 'song-1');
  await value.add('playlist-1', 'song-1', 'Road trip');
  const writer = FakeSavedService.instances.at(-1)!;
  assert.deepEqual(writer.committed, ['song-1']);
  assert.deepEqual(FakeSavedService.mutations, ['song-1', 'song-1']);
  value.dispose();
});

test('two leaf retries share one fresh mutate after a synchronous throw', async () => {
  reset();
  FakeSavedService.syncThrowAt = 0;
  const value = await port();
  await value.load();
  await assert.rejects(value.add('playlist-1', 'song-1', 'Road trip'));
  let release!: () => void;
  FakeSavedService.mutateGate = new Promise(resolve => {release = resolve;});
  const retryA = value.retryLeaf();
  const retryB = value.retryLeaf();
  assert.equal(retryA, retryB);
  release();
  assert.equal(await retryA, true);
  const writer = FakeSavedService.instances.at(-1)!;
  assert.deepEqual(FakeSavedService.mutations, ['song-1', 'song-1']);
  assert.deepEqual(writer.committed, ['song-1']);
  value.dispose();
});

test('two leaf retries share one canonical retry after an accepted rejection', async () => {
  reset();
  FakeSavedService.failAt = 0;
  const value = await port();
  await value.load();
  await assert.rejects(value.add('playlist-1', 'song-1', 'Road trip'));
  let release!: () => void;
  FakeSavedService.retryGate = new Promise(resolve => {release = resolve;});
  const retryA = value.retryLeaf();
  const retryB = value.retryLeaf();
  assert.equal(retryA, retryB);
  release();
  assert.equal(await retryA, true);
  const writer = FakeSavedService.instances.at(-1)!;
  assert.deepEqual(FakeSavedService.mutations, ['song-1']);
  assert.deepEqual(writer.committed, ['song-1']);
  value.dispose();
});

test('two same-frame leaf adds share one mutate', async () => {
  reset();
  const value = await port();
  await value.load();
  let release!: () => void;
  FakeSavedService.mutateGate = new Promise(resolve => {release = resolve;});
  const addA = value.add('playlist-1', 'song-1', 'Road trip');
  const addB = value.add('playlist-1', 'song-1', 'Road trip');
  assert.equal(addA, addB);
  release();
  await addA;
  const writer = FakeSavedService.instances.at(-1)!;
  assert.deepEqual(FakeSavedService.mutations, ['song-1']);
  assert.deepEqual(writer.committed, ['song-1']);
  value.dispose();
});

test('a retry with nothing pending settles false', async () => {
  reset();
  const value = await port();
  assert.equal(await value.retryLeaf(), false);
  value.dispose();
});

test('a whole album adds every captured song leaf in server order', async () => {
  reset();
  const value = await port(async () => ['song-2', 'song-3', 'song-1']);
  const result = await value.addAlbum({id: 'playlist-1', name: 'Road trip'});
  const writer = FakeSavedService.instances.at(-1)!;
  assert.deepEqual(result, {phase: 'done', added: 3});
  assert.deepEqual(writer.committed, ['song-2', 'song-3', 'song-1']);
  assert.deepEqual(writer.route, {view: 'playlist', playlistId: 'playlist-1'});
  assert.equal(value.getSnapshot().album?.phase, 'done');
  assert.equal(value.getSnapshot().album?.total, 3);
  assert.equal(writer.disposed, true);
  value.dispose();
});

test('a failed write reports partial progress and retry resumes without replaying successes', async () => {
  reset();
  FakeSavedService.failAt = 1;
  const value = await port(async () => ['song-2', 'song-3', 'song-1']);
  const first = await value.addAlbum({id: 'playlist-1', name: 'Road trip'});
  const writer = FakeSavedService.instances.at(-1)!;
  assert.equal(first.phase, 'partial');
  assert.equal(first.added, 1);
  assert.deepEqual(writer.committed, ['song-2']);
  assert.equal(writer.disposed, false);
  assert.deepEqual(value.getSnapshot().album, {playlistId: 'playlist-1', playlistName: 'Road trip', added: 1, phase: 'partial'});
  assert.equal(value.getSnapshot().busy, false);
  const retried = await value.resumeAlbum();
  assert.deepEqual(retried, {phase: 'done', added: 3});
  assert.deepEqual(writer.committed, ['song-2', 'song-3', 'song-1']);
  assert.deepEqual(FakeSavedService.mutations, ['song-2', 'song-3', 'song-1']);
  value.dispose();
});

test('two resume taps share one retry and never double-add the failed leaf', async () => {
  reset();
  FakeSavedService.failAt = 1;
  const value = await port(async () => ['song-2', 'song-3', 'song-1']);
  const first = await value.addAlbum({id: 'playlist-1', name: 'Road trip'});
  assert.equal(first.added, 1);
  let release!: () => void;
  FakeSavedService.retryGate = new Promise(resolve => {release = resolve;});
  const retryA = value.resumeAlbum();
  const retryB = value.resumeAlbum();
  assert.equal(retryA, retryB);
  release();
  const retried = await retryA;
  assert.deepEqual(retried, {phase: 'done', added: 3});
  const writer = FakeSavedService.instances.at(-1)!;
  assert.deepEqual(writer.committed, ['song-2', 'song-3', 'song-1']);
  value.dispose();
});

test('zero confirmed additions use the existing failure state and retry the same action', async () => {
  reset();
  FakeSavedService.failAt = 0;
  const value = await port(async () => ['song-2']);
  const first = await value.addAlbum({id: 'playlist-1', name: 'Road trip'});
  assert.equal(first.phase, 'failed');
  assert.equal(first.added, 0);
  assert.equal(value.getSnapshot().album?.phase, 'failed');
  assert.equal(value.getSnapshot().error instanceof Error, true);
  const retried = await value.resumeAlbum();
  assert.deepEqual(retried, {phase: 'done', added: 1});
  value.dispose();
});

test('a retry that cannot settle stays partial and is never reported as full success', async () => {
  reset();
  FakeSavedService.failAt = 0;
  const value = await port(async () => ['song-2', 'song-3']);
  const first = await value.addAlbum({id: 'playlist-1', name: 'Road trip'});
  assert.equal(first.phase, 'failed');
  FakeSavedService.retrySucceeds = false;
  const retried = await value.resumeAlbum();
  assert.equal(retried.phase, 'failed');
  assert.equal(retried.added, 0);
  assert.equal(value.getSnapshot().album?.phase, 'failed');
  value.dispose();
});

test('a failed membership read adds nothing and can be retried', async () => {
  reset();
  let tries = 0;
  const value = await port(async () => {
    tries++;
    if (tries === 1) throw new Error('membership failed');
    return ['song-2'];
  });
  const first = await value.addAlbum({id: 'playlist-1', name: 'Road trip'});
  assert.equal(first.phase, 'failed');
  assert.equal(tries, 1);
  assert.equal(FakeSavedService.mutations.length, 0);
  const retried = await value.resumeAlbum();
  assert.deepEqual(retried, {phase: 'done', added: 1});
  assert.equal(tries, 2);
  value.dispose();
});

test('the approved partial copy uses normal singular and plural forms', () => {
  assert.equal(defaultI18n.t('next.selection.playlistAlbumPartial', {count: 1}), 'The album wasn’t fully added. 1 song was added.');
  assert.equal(defaultI18n.t('next.selection.playlistAlbumPartial', {count: 3}), 'The album wasn’t fully added. 3 songs were added.');
});

test('the real playlist surface closes after listing without writing anything', async () => {
  reset();
  const value = await port();
  await value.load();
  assert.equal(value.getSnapshot().phase, 'ready');
  assert.ok(value.getSnapshot().playlists.length > 0, 'the real port listed the offered playlists');
  assert.equal(FakeSavedService.mutations.length, 0, 'listing alone performs no mutation');
  value.dispose();
  assert.equal(FakeSavedService.mutations.length, 0, 'closing without choosing writes nothing');
  assert.deepEqual(FakeSavedService.instances.flatMap((writer: any) => writer.committed), [], 'no playlist write was committed');
});

test('the real picker controller closes without a choice and writes nothing', async () => {
  reset();
  const {MusicPlaylistPort} = await screenModule(new URL('../src-next/screens/destinations/music/playlist-port.ts', import.meta.url), {
    react: {default: {}},
    '../../../../../../packages/client-core/src/saved': {default: {}, SavedService: FakeSavedService},
  });
  const {MusicPlaylistPickerController} = await screenModule(new URL('../src-next/screens/destinations/music/MusicPlaylistPicker.tsx', import.meta.url), {
    react: {default: {createElement() {}, useSyncExternalStore() {}}, useSyncExternalStore: () => ({})},
    '../../../../../../packages/i18n/src': {defaultI18n},
    '../../../kit/overlays': {Menu: 'Menu'},
    '../lifetime': {nativeDestinationLifetime: () => ({scope: {serverId: 'server', viewerId: 'viewer'}})},
    '../../native-screen-services': {nativeScreenServices: () => ({requestId: async () => 'request-id'})},
    './playlist-port': {MusicPlaylistPort},
  });
  const controller = new MusicPlaylistPickerController({api: {request: async () => ({})}, current: () => true});
  const lifetime = {signal: new AbortController().signal, isCurrent: () => true};
  controller.openAlbum({albumId: 'album-1', load: async () => ['song-1', 'song-2']}, lifetime);
  await new Promise<void>(resolve => setImmediate(resolve));
  await new Promise<void>(resolve => setImmediate(resolve));
  assert.equal(controller.getSnapshot().open, true, 'the real controller opened the picker');
  assert.equal(controller.getSnapshot().port?.getSnapshot().playlists.length, 1, 'the real port listed the offer');
  controller.close();
  assert.equal(controller.getSnapshot().open, false, 'Cancel closes the actual picker');
  assert.equal(FakeSavedService.mutations.length, 0, 'closing without choosing performs no mutation');
  assert.deepEqual(FakeSavedService.instances.flatMap((writer: any) => writer.committed), [], 'no playlist write was committed');
});
