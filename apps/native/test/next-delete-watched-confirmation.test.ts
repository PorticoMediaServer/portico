import test from 'node:test';
import assert from 'node:assert/strict';
import {offlineFixture, viewer, owner, digest, title, settle} from './helpers/offline-fixture.mjs';

test('Delete watched keeps offline files until the server confirms the exact play finished', async t => {
 for (const finished of [false, true]) for (const ended of [false, true]) {
  const f = await offlineFixture();t.after(() => f.offline.dispose());
  f.seed('copy', 'itm_film', {sha256: digest, meta: {downloadId: 'dl_one', owner, title, kind: 'movie', bundle: 4}});
  await f.offline.open();f.offline.adopt(viewer);await f.offline.setSettings({deleteWatched: true});
  f.offline.recordProgress({entryId: 'copy', itemId: 'itm_film', playId: 'play', positionSeconds: ended ? 6000 : 5580, durationSeconds: 6000, state: ended ? 'ended' : 'paused', ended});await settle();
  assert.deepEqual(f.removed, [], 'neither a near-end pause nor an actual end deletes offline');
  let reply!: (value: unknown) => void;
  f.handlers.set('/v1/items/state:batch', () => new Promise(resolve => {reply = resolve;}));
  f.offline.connect(f.api, f.service, viewer);await settle();assert.deepEqual(f.removed, [], 'sending progress is not confirmation');
  reply({items: [], plays: [{itemId: 'itm_film', playId: 'play', finished}]});await settle();
  assert.deepEqual(f.removed, finished ? ['copy'] : []);
 }
});

test('a finished reply cannot remove a download that has resumed while sync was pending', async t => {
 const f = await offlineFixture();t.after(() => f.offline.dispose());
 f.seed('copy', 'itm_film', {sha256: digest, meta: {downloadId: 'dl_one', owner, title, kind: 'movie', bundle: 4}});
 await f.offline.open();f.offline.adopt(viewer);await f.offline.setSettings({deleteWatched: true});
 const progress = {entryId: 'copy', itemId: 'itm_film', playId: 'play', positionSeconds: 5800, durationSeconds: 6000, ended: false};
 f.offline.recordProgress({...progress, state: 'paused'});await settle();
 let reply!: (value: unknown) => void;
 f.handlers.set('/v1/items/state:batch', () => new Promise(resolve => {reply = resolve;}));
 f.offline.connect(f.api, f.service, viewer);await settle();
 f.offline.recordProgress({...progress, state: 'playing'});
 reply({items: [], plays: [{itemId: 'itm_film', playId: 'play', finished: true}]});await settle();
 assert.deepEqual(f.removed, []);
});
