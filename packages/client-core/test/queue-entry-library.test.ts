import test from 'node:test';
import assert from 'node:assert/strict';
import {parseEntry} from '../src/playback-v1/types.ts';

const base = {entryId: 'qe_1', position: 0, itemId: 'itm_song', kind: 'track', title: 'In the Flesh?', available: true, images: {}};

test("a queue entry keeps the library the server names, so Now Playing can read the song's own actions", () => {
  assert.equal(parseEntry({...base, libraryId: 'lib_music'}).libraryId, 'lib_music');
  assert.equal(parseEntry(base).libraryId, undefined);
  assert.equal(parseEntry({...base, libraryId: ''}).libraryId, undefined);
});

test("the player passes an entry's library on only for an available entry in the accepted window", async t => {
  const {PlaybackService} = await import('../src/index.ts');
  const {V1PlaybackApi} = await import('../src/playback-v1/legacy-bridge.ts');
  const {V1QueuePlayer} = await import('../src/playback-v1/v1-queue-player.ts');
  const {QueueClient} = await import('../src/playback-v1/queue.ts');
  const {parseQueue} = await import('../src/playback-v1/types.ts');
  const items = [{...base, entryId: 'qe_1', position: 0, itemId: 'itm_a', libraryId: 'lib_music'},
    {...base, entryId: 'qe_2', position: 1, itemId: 'itm_b', available: false, libraryId: 'lib_music'}];
  const http = {send: async () => ({status: 200, headers: {}, body: {items, start: 0, total: 2, revision: 'r1'}})};
  const api = new V1PlaybackApi(http);
  const playback = new PlaybackService(api, () => 'device');
  const player = new V1QueuePlayer({http, api, playback, viewer: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile'}});
  t.after(() => { player.dispose(); playback.leave(); });
  const window = await new QueueClient(http).window('q1');
  const header = parseQueue({id: 'q1', revision: 'r1', total: 2, medium: 'audio', current: {entryId: 'qe_1', position: 0}, repeat: 'off'});
  (player as unknown as {accept(h: unknown, w: unknown): void}).accept(header, window);
  const view = (player.getSnapshot() as {view?: {items: readonly {entryId: string; libraryId?: string}[]}}).view;
  assert.deepEqual(view?.items.map(item => [item.entryId, item.libraryId]), [['qe_1', 'lib_music'], ['qe_2', undefined]]);
});
