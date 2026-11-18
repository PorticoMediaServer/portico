import test from 'node:test';
import assert from 'node:assert/strict';
import {offlineFixture, viewer, owner, preparation, deferred, settle, digest, title} from './helpers/offline-fixture.mjs';

const permission = Date.now() + 86_400_000;
const state = (fields = {}) => JSON.stringify({version: 3, permissions: {[owner]: permission}, ...fields});

test('restarted downloads keep their native facts and are scoped to the exact saved viewer', async t => {
  const f = await offlineFixture({state: state()}); t.after(() => f.offline.dispose());
  const other = {...viewer, serverId: 'other-server'};
  f.seed('mine', 'film', {dir: 'file:///downloads/mine/', waiting: 'wifi', intent: 'hold',
    meta: {owner, downloadId: 'dl_mine', title, extras: ['poster']}});
  f.seed('other', 'film', {meta: {owner: f.implementation.offlineOwnerKey(other), downloadId: 'dl_other'}});
  f.seed('unknown', 'film', {meta: {downloadId: 'dl_unknown'}});
  f.offline.adopt(viewer);
  await f.offline.open();
  const [entry] = f.offline.getSnapshot().entries;
  assert.deepEqual(f.offline.getSnapshot().entries.map(e => e.id), ['mine']);
  assert.equal(entry.artworkUrl, 'file:///downloads/mine/extras/poster');
  assert.equal(entry.waiting, 'wifi'); assert.equal(entry.held, true);
  assert.equal(entry.info?.durationSeconds, 100);
  assert.equal((await f.offline.localMedia(entry)).entryId, 'mine');
  f.offline.adopt(other);
  assert.deepEqual(f.offline.getSnapshot().entries.map(e => e.id), ['other']);
  assert.equal(f.offline.playabilityReason(entry), 'other_profile');
  f.offline.adopt(undefined);
  assert.deepEqual(f.offline.getSnapshot().entries, []);
  assert.equal(f.entries.size, 3);
});

test('an old bookkeeping document keeps its settings', async t => {
  const f = await offlineFixture({state: JSON.stringify({version: 2,
    settings: {wifiOnly: false, deleteWatched: true, autoNext: true, limitBytes: 1234},
    resume: {item: 90}, keys: {key: 'old'}, nextAfter: {[owner]: {item: {quality: 'original'}}},
  })}); t.after(() => f.offline.dispose());
  await f.offline.open();
  const saved = JSON.parse(f.disk.state);
  assert.equal(saved.version, 3);
  assert.deepEqual(saved.settings, {wifiOnly: false, deleteWatched: true, limitBytes: 1234});
  assert.deepEqual(saved.resume, {});
  assert.equal(saved.keys, undefined);
  assert.equal(saved.nextAfter, undefined);
});

test('the retired next-episode preference leaves rebuilt files and plays intact', async t => {
  const play = {playId: 'pending', itemId: 'item', downloadId: 'dl_copy', positionSeconds: 90, observedAt: new Date().toISOString()};
  const plays = {[owner]: {pending: play, legacy: {playId: 'legacy', itemId: 'item', positionSeconds: 45, observedAt: play.observedAt}}};
  const f = await offlineFixture({state: state({plays, nextAfter: {[owner]: {item: {quality: 'original'}}}})});
  t.after(() => f.offline.dispose());
  f.seed('copy');
  f.offline.adopt(viewer); await f.offline.open();
  const saved = JSON.parse(f.disk.state);
  assert.equal(saved.nextAfter, undefined);
  assert.deepEqual(saved.plays[owner], {pending: play});
  assert.deepEqual(f.removed, []);
  assert.equal(f.offline.getSnapshot().pendingProgress, 1);
});

for (const change of ['server', 'signout']) {
  test(`a grant resolving after ${change} cannot start a transfer`, async t => {
    const f = await offlineFixture(); t.after(() => f.offline.dispose());
    const entered = deferred(), release = deferred(), grant = f.service.grant;
    f.service.grant = async target => {entered.resolve(); await release.promise; return grant(target);};
    f.offline.connect(f.api, f.service, viewer); await f.offline.open(); await settle();
    const pending = f.offline.fetch(preparation('copy'));
    const rejected = assert.rejects(pending, /downloads.connectToStart/);
    await entered.promise;
    if (change === 'server') f.offline.connect(f.api, f.service, {...viewer, serverId: 'other-server'});
    else f.offline.adopt(undefined);
    release.resolve();
    await rejected;
    assert.deepEqual(f.started, []);
  });
}

test('another profile cannot submit a queued play, and the owner can retry it on wake', async t => {
  const f = await offlineFixture({state: state()}); t.after(() => f.offline.dispose());
  f.seed('copy', 'film', {sha256: digest});
  f.offline.adopt(viewer); await f.offline.open();
  const entry = f.offline.getSnapshot().entries[0], media = await f.offline.localMedia(entry);
  f.offline.recordProgress({entryId: entry.id, itemId: entry.itemId, playId: media.playId,
    positionSeconds: 35, durationSeconds: 100, ended: false, state: 'paused'});
  await settle();
  f.offline.connect(f.api, f.service, {...viewer, profileId: 'other-profile'});
  await settle();
  assert.equal(f.offline.getSnapshot().pendingProgress, 0);
  assert.equal(f.calls.some(call => call.path === '/v1/items/state:batch'), false);

  let contact = false;
  f.handlers.set('/v1/items/state:batch', body => {
    if (!contact) throw new Error('offline');
    assert.equal(body.plays[0].positionMs, 35000);
    assert.equal(body.plays[0].playId, media.playId);
    return {plays: [{itemId: 'film', playId: media.playId, finished: false}]};
  });
  f.offline.connect(f.api, f.service, viewer); await settle();
  assert.equal(f.offline.getSnapshot().pendingProgress, 1);
  contact = true;
  f.offline.wake(); await settle();
  assert.equal(f.offline.getSnapshot().pendingProgress, 0);
});

test('an acknowledgement cannot erase a newer offline observation', async t => {
  const f = await offlineFixture({state: state()}); t.after(() => f.offline.dispose());
  const raw = f.seed('copy', 'film', {sha256: digest});
  const observed = (positionSeconds: number) => f.offline.observed({id: raw.id, playId: 'play', positionSeconds,
    durationSeconds: 100, ended: false, final: false});
  f.offline.adopt(viewer); await f.offline.open();
  await observed(15);
  const entered = deferred(), ack = deferred();
  const sent: number[] = [];
  f.handlers.set('/v1/items/state:batch', async body => {
    const play = body.plays[0];
    sent.push(play.positionMs);
    if (sent.length === 1) {entered.resolve(); await ack.promise;}
    return {plays: [{itemId: play.itemId, playId: play.playId, finished: false}]};
  });
  f.offline.connect(f.api, f.service, viewer);
  await entered.promise;
  await observed(30);
  ack.resolve(); await f.offline.syncProgress();
  assert.deepEqual(sent, [15000, 30000]);
  assert.equal(f.offline.getSnapshot().pendingProgress, 0);
});

test('automatic retry continues only transfers that stopped by themselves and can mend', async t => {
  const f = await offlineFixture(); t.after(() => f.offline.dispose());
  f.seed('stopped', 'film', {state: 'failed', error: 'network'});
  f.seed('held', 'held-film', {state: 'paused', intent: 'hold'});
  f.seed('damaged', 'damaged-film', {state: 'failed', error: 'verification_failed'});
  f.offline.connect(f.api, f.service, viewer); await f.offline.open(); await settle();
  assert.deepEqual(f.started.map(request => request.id), ['stopped']);
});

test('cached details and local media reject an owner change during the native read', async t => {
  const f = await offlineFixture({state: state({resume: {[owner]: {film: 25}}})}); t.after(() => f.offline.dispose());
  f.seed('copy', 'film', {sha256: digest, dir: 'file:///downloads/copy/', meta: {owner, downloadId: 'dl_copy', extras: ['backdrop']}});
  f.native.readExtra = async () => JSON.stringify({itemId: 'film', preparationId: 'copy', media: {width: 1920}});
  f.offline.adopt(viewer); await f.offline.open();
  const entry = f.offline.getSnapshot().entries[0];
  assert.deepEqual(await f.offline.displayFacts(entry), {resumePositionSeconds: 25, backdropUrl: 'file:///downloads/copy/extras/backdrop', media: {width: 1920}});
  const release = deferred(), localFile = f.native.localFile;
  f.native.localFile = async id => {await release.promise; return localFile(id);};
  const local = f.offline.localMedia(entry);
  f.offline.adopt({...viewer, profileId: 'other-profile'});
  release.resolve();
  await assert.rejects(local, /downloads.offline.otherProfile/);
});
