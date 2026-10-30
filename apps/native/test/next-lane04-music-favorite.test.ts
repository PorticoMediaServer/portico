import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';
import * as titleRelated from '../src-next/screens/watch/title-related.ts';

async function fixture({ambiguous = false, holdRead = false, denied = false} = {}) {
  let active = true, releases: (() => void)[] = [], mutations = 0, retries = 0, disposals = 0, instances = 0;
  const calls: unknown[] = [];
  class Detail {
    state: any = {phase: 'idle', data: null, pending: [], error: null, mutationError: null};
    intent: any; disposed = false;
    constructor(_options: any) {instances++;}
    getSnapshot = () => this.state;
    async select(target: any) {
      calls.push(target);
      if (holdRead) await new Promise<void>(resolve => releases.push(resolve));
      if (this.disposed) return;
      this.state = {phase: 'ready', data: {personal: {favorite: false}}, pending: [], error: null, mutationError: null};
    }
    async mutate(intent: any) {
      if (denied) throw Object.assign(new Error('Not allowed'), {code: 'forbidden'});
      mutations++; this.intent = intent;
      if (ambiguous) this.state = {...this.state, pending: [{intent, phase: 'retry-required'}], mutationError: {code: 'request_failed'}};
      else this.state = {...this.state, data: {personal: {favorite: intent.value}}};
    }
    async retryMutation() {retries++; this.state = {...this.state, data: {personal: {favorite: this.intent.value}}, pending: [], mutationError: null};}
    dispose() {if (!this.disposed) {this.disposed = true; disposals++;}}
  }
  const empty = () => ({phase: 'idle', items: [], offset: 0, nextCursor: '', canPrevious: false, canNext: false});
  const {NativeTVMusicController} = await screenModule(new URL('../src-next/screens/destinations/music/model.ts', import.meta.url), {
    '../../watch/title-related': titleRelated,
    react: {default: {}}, '../../../../../../packages/client-core/src/detail': {DetailService: Detail},
    '../../../../../../packages/client-core/src/browse': {}, '../../../../../../packages/client-core/src/container-state': {},
    '../../../../../../packages/client-core/src/library-content': {}, '../../../../../../packages/contracts/src/routes': {},
    '../../../../../../packages/client-core/src/server-messages': {unreadableServerResponse: 'unreadable'},
    '../lifetime': {}, './paging': {emptyMusicSection: empty},
  });
  const song = {id: 'song', libraryId: 'music', kind: 'song', title: 'Song', favorite: false};
  const controller = new NativeTVMusicController({entry: {id: 'album', libraryId: 'music', kind: 'album', title: 'Album'},
    api: {request: async () => {throw new Error('unexpected request');}}, similarScope: {},
    lifetime: {scope: {serverId: 'server', viewerId: 'viewer'}, current: () => active}, requestId: async () => 'operation'});
  controller.state = {...controller.getSnapshot(), phase: 'ready', songs: [song], songPage: {...empty(), phase: 'ready', items: [song]}};
  return {controller, song, calls, counts: () => ({mutations, retries, disposals, instances}), retire: () => {active = false;}, release: () => releases.splice(0).forEach(resolve => resolve())};
}

test('tablet favorite accepts only the exact current song and updates from the accepted Detail result', async () => {
  const f = await fixture();
  await f.controller.toggleSongFavorite({...f.song}); assert.equal(f.counts().instances, 0);
  await f.controller.toggleSongFavorite(f.song);
  assert.deepEqual(f.calls, [{itemId: 'song', libraryId: 'music'}]);
  assert.equal(f.controller.songFavorite(f.song), true);
  assert.equal(f.controller.songFavoritePending(f.song), false);
  assert.deepEqual(f.counts(), {mutations: 1, retries: 0, disposals: 1, instances: 1});
  f.controller.dispose();
});

test('an ambiguous favorite receipt keeps the canonical command for an explicit retry', async () => {
  const f = await fixture({ambiguous: true});
  await assert.rejects(f.controller.toggleSongFavorite(f.song), {code: 'request_failed'});
  assert.equal(f.controller.songFavorite(f.song), false);
  assert.equal(f.controller.songFavoritePending(f.song), false, 'retry is available after the request settles');
  assert.deepEqual(f.counts(), {mutations: 1, retries: 0, disposals: 0, instances: 1});
  await f.controller.toggleSongFavorite(f.song);
  assert.equal(f.controller.songFavorite(f.song), true);
  assert.deepEqual(f.counts(), {mutations: 1, retries: 1, disposals: 1, instances: 1}, 'the retry reuses the same service and intent');
  f.controller.dispose();
});

test('duplicate taps while reading and a retired page cannot issue a favorite mutation', async () => {
  const f = await fixture({holdRead: true});
  const pending = f.controller.toggleSongFavorite(f.song);
  assert.equal(f.controller.songFavoritePending(f.song), true);
  await f.controller.toggleSongFavorite(f.song); assert.equal(f.counts().instances, 1);
  f.retire(); f.release(); await pending;
  assert.equal(f.counts().mutations, 0); assert.equal(f.counts().disposals, 1);
  assert.equal(f.controller.songFavorite(f.song), false);
  f.controller.dispose();
});

test('a refused Detail capability surfaces its error and never flips the row', async () => {
  const f = await fixture({denied: true});
  await assert.rejects(f.controller.toggleSongFavorite(f.song), {code: 'forbidden'});
  assert.equal(f.controller.songFavorite(f.song), false);
  assert.equal(f.counts().disposals, 1);
  f.controller.dispose();
});
