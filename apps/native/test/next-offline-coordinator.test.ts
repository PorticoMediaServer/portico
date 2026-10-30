import test from 'node:test';
import assert from 'node:assert/strict';
import {offlineFixture, viewer, owner, preparation, receipt, deferred, settle} from './helpers/offline-fixture.mjs';

type Fixture = Awaited<ReturnType<typeof offlineFixture>>;
const connected = async (f: Fixture) => {await f.offline.open(); f.offline.connect(f.api, f.service, viewer); await settle();};
const stored = (f: Fixture) => JSON.parse(f.disk.state);
const observe = (f: Fixture, id: string, playId: string, positionSeconds: number) => (f.offline as any).observed({id, playId, positionSeconds, durationSeconds: 1000, ended: false, final: false});
const request = (id: string, items: any[]) => ({requestId: id, total: items.length, totalKnown: true, state: 'complete', ready: items.length, preparing: 0, queued: 0, failed: 0, paused: 0, items, nextCursor: ''});

test('OFF-03: only the accepted preparation can satisfy a title, including after restart', async () => {
  const f = await offlineFixture();
  try {
    await connected(f);
    const wrong = preparation('large', 'film'), chosen = preparation('small', 'film', 'small', 'queued');
    f.snapshot.items = [wrong, chosen];
    await f.offline.want([chosen]); await settle();
    assert.equal(f.started.length, 0);
    assert.equal(stored(f).wanted[owner].small.itemId, 'film');
    f.offline.dispose();
    const restarted = new f.implementation.OfflineDownloads();
    f.offline = restarted;
    f.snapshot.items = [wrong, {...chosen, state: 'ready'}];
    await connected(f);
    assert.deepEqual(f.started.map(value => value.id), ['small']);
    assert.equal(f.started[0].quality, 'small');
  } finally {f.offline.dispose();}
});

test('OFF-03: an accepted copy outside the first server page is collected and resolved by exact id after restart', async () => {
  const f = await offlineFixture();
  try {
    await connected(f);
    await f.offline.want([preparation('older-ready')]); await settle();
    assert.deepEqual(f.started.map(value => value.id), ['older-ready']);
    const queued = preparation('older-queued', 'item', 'original', 'queued');
    await f.offline.want([queued]); await settle();
    f.offline.dispose(); f.offline = new f.implementation.OfflineDownloads();
    f.handlers.set('lookupPreparations', ids => ids.includes('older-queued') ? [{...queued, state: 'ready'}] : []);
    const before = f.calls.length;
    await connected(f); await settle();
    assert.deepEqual(f.started.map(value => value.id), ['older-ready', 'older-queued']);
    // OFF-04: exact ids in one bounded batched read, never per-id or item-id requests.
    assert.deepEqual(f.calls.slice(before).filter(value => value.path === 'lookupPreparations').map(value => value.ids), [['older-queued']]);
    assert.equal(f.calls.some(value => value.path === '/v1/downloads/preparations/older-queued'), false);
  } finally {f.offline.dispose();}
});

test('OFF-06: a removed container member stays removed after a fresh coordinator starts', async () => {
  const f = await offlineFixture();
  try {
    const p = preparation('copy', 'episode');
    f.snapshot.requests = [{request: request('season', [{itemId: p.itemId, preparation: p}]), title: 'Season'}];
    await connected(f); await settle();
    assert.equal(f.started.length, 1);
    await f.offline.remove(f.offline.getSnapshot().entries[0]);
    assert.equal(stored(f).enrolledMembers[owner].season.episode, true);
    f.offline.dispose(); f.offline = new f.implementation.OfflineDownloads();
    await connected(f); await settle();
    assert.equal(f.started.length, 1, 'the still-listed request cannot resurrect a deleted member');
    assert.equal(f.offline.getSnapshot().entries.length, 0);
    f.snapshot.requests = [{request: request('new-season-request', [{itemId: p.itemId, preparation: p}]), title: 'Season'}];
    f.service.publish(); await settle();
    assert.equal(f.started.length, 2, 'a newly accepted request remains a deliberate new intent');
  } finally {f.offline.dispose();}
});

test('container membership without an accepted preparation never borrows another quality', async () => {
  const f = await offlineFixture();
  try {
    f.snapshot.items = [preparation('unrelated', 'episode')];
    f.snapshot.requests = [{request: request('season', [{itemId: 'episode'}]), title: 'Season'}];
    await connected(f);
    assert.equal(f.started.length, 0);
    const p = preparation('accepted', 'episode', 'small');
    f.snapshot.requests = [{request: request('season', [{itemId: 'episode', preparation: p}]), title: 'Season'}];
    f.service.publish(); await settle();
    assert.deepEqual(f.started.map(value => value.id), ['accepted']);
  } finally {f.offline.dispose();}
});

test('OFF-06: an empty loading list keeps member markers; an authenticated missing request prunes them', async () => {
  const f = await offlineFixture({state: JSON.stringify({version: 3, enrolledMembers: {[owner]: {season: {episode: true}}}})});
  try {
    f.handlers.set('/v1/downloads/requests/season?limit=100', () => {throw Object.assign(new Error('offline'), {status: 503});});
    await connected(f);
    assert.equal(stored(f).enrolledMembers[owner].season.episode, true);
    f.offline.dispose(); f.offline = new f.implementation.OfflineDownloads();
    f.handlers.set('/v1/downloads/requests/season?limit=100', () => {throw Object.assign(new Error('gone'), {status: 404});});
    await connected(f);
    assert.equal(stored(f).enrolledMembers[owner].season, undefined);
  } finally {f.offline.dispose();}
});

test('OFF-06: failed enrollment persistence is retried before any file transfer', async () => {
  const f = await offlineFixture();
  try {
    const p = preparation('copy');
    f.snapshot.requests = [{request: request('season', [{itemId: p.itemId, preparation: p}]), title: 'Season'}];
    f.disk.failWrite = true; await connected(f); await settle();
    assert.equal(f.started.length, 0);
    const start = f.native.start;
    f.native.start = async value => {assert.equal(stored(f).enrolledMembers[owner].season.item, true); return start(value);};
    f.disk.failWrite = false; f.service.publish(); await settle();
    assert.equal(f.started.length, 1);
  } finally {f.offline.dispose();}
});

test('OFF-07: removing a row while its retry grant is pending cannot recreate it', async () => {
  const f = await offlineFixture();
  try {
    f.seed('copy', 'film', {state: 'paused', intent: 'hold'}); await connected(f);
    const grant = deferred(), entered = deferred(), original = f.service.grant;
    f.service.grant = async value => {entered.resolve(); await grant.promise; return original(value);};
    const entry = f.offline.getSnapshot().entries[0];
    const resuming = f.offline.resume(entry);
    await entered.promise;
    await f.offline.remove(entry);
    grant.resolve();
    await assert.rejects(resuming, {code: 'cancelled'});
    assert.equal(f.entries.size, 0); assert.equal(f.started.length, 0);
  } finally {f.offline.dispose();}
});

test('OFF-07: pause invalidates a late retry even when the entry still exists', async () => {
  const f = await offlineFixture();
  try {
    f.seed('copy', 'film', {state: 'paused', intent: 'hold'}); await connected(f);
    const grant = deferred(), entered = deferred(), original = f.service.grant;
    f.service.grant = async value => {entered.resolve(); await grant.promise; return original(value);};
    const entry = f.offline.getSnapshot().entries[0], resuming = f.offline.resume(entry);
    await entered.promise; await f.offline.pause(entry); grant.resolve();
    await assert.rejects(resuming, {code: 'cancelled'});
    assert.equal(f.started.length, 0); assert.equal(f.entries.get('copy').intent, 'hold');
  } finally {f.offline.dispose();}
});

test('OFF-07: a replacement with the same id is a different retry incarnation', async () => {
  const f = await offlineFixture();
  try {
    f.seed('copy', 'film', {state: 'paused', intent: 'hold'}); await connected(f);
    const grant = deferred(), entered = deferred(), original = f.service.grant;
    f.service.grant = async value => {entered.resolve(); await grant.promise; return original(value);};
    const entry = f.offline.getSnapshot().entries[0], resuming = f.offline.resume(entry);
    await entered.promise; await f.offline.remove(entry);
    f.emit(f.seed('copy', 'replacement'));
    grant.resolve(); await assert.rejects(resuming, {code: 'cancelled'});
    assert.equal(f.entries.get('copy').itemId, 'replacement'); assert.equal(f.started.length, 0);
  } finally {f.offline.dispose();}
});

test('an accepted native resume keeps its own continuation alive for missing extras', async () => {
  const f = await offlineFixture();
  try {
    f.seed('copy', 'item', {state: 'paused', intent: 'hold', meta: {owner, receipt: receipt('copy'), bundle: 0, viewingSynced: true}});
    await connected(f);
    await f.offline.resume(f.offline.getSnapshot().entries[0]); await settle();
    assert.equal(f.started.length, 1);
    assert.ok(f.calls.some(value => value.path === '/v1/downloads/preparations/copy/bundle'), 'the start-generated native generation must not cancel bundle collection');
  } finally {f.offline.dispose();}
});

test('OFF-08: a storage-blocked accepted copy has a reason and a durable cancel action', async () => {
  const f = await offlineFixture();
  try {
    await connected(f); await f.offline.setSettings({limitBytes: 50});
    const p = preparation('copy'); f.snapshot.items = [p]; await f.offline.want([p]); await settle();
    assert.deepEqual(f.offline.getSnapshot().pendingDownloads?.map((value: {id: string; title: string; reason: string}) => [value.id, value.title, value.reason]), [['copy', 'Title', 'downloads.blocked.limit']]);
    assert.equal(f.started.length, 0);
    await f.offline.cancelPending('copy');
    assert.deepEqual(f.offline.getSnapshot().pendingDownloads, []);
    assert.equal(stored(f).wanted[owner].copy, undefined);
    await f.offline.setSettings({limitBytes: 1000}); await settle();
    assert.equal(f.started.length, 0);
  } finally {f.offline.dispose();}
});

test('OFF-08: raising the storage limit admits the same waiting preparation', async () => {
  const f = await offlineFixture();
  try {
    await connected(f); await f.offline.setSettings({limitBytes: 50});
    const p = preparation('copy'); f.snapshot.items = [p]; await f.offline.want([p]); await settle();
    await f.offline.setSettings({limitBytes: 1000}); await settle();
    assert.deepEqual(f.started.map(value => value.id), ['copy']);
    assert.deepEqual(f.offline.getSnapshot().pendingDownloads, []);
  } finally {f.offline.dispose();}
});

test('OFF-08: a foreground wake remeasures space freed outside the app', async () => {
  const f = await offlineFixture();
  try {
    f.disk.freeBytes = 0; await connected(f);
    const p = preparation('copy'); f.snapshot.items = [p]; await f.offline.want([p]); await settle();
    assert.equal(f.offline.getSnapshot().pendingDownloads?.[0].reason, 'downloads.blocked.space');
    f.disk.freeBytes = 2 ** 40; f.offline.wake(); await settle();
    assert.deepEqual(f.started.map(value => value.id), ['copy']);
  } finally {f.offline.dispose();}
});

test('OFF-08: cancelling accepted work invalidates a grant already in flight', async () => {
  const f = await offlineFixture();
  try {
    await connected(f);
    const p = preparation('copy'), grant = deferred(), entered = deferred(), original = f.service.grant;
    f.snapshot.items = [p]; f.service.grant = async value => {entered.resolve(); await grant.promise; return original(value);};
    await f.offline.want([p]); await entered.promise;
    await f.offline.cancelPending('copy'); grant.resolve(); await settle();
    assert.equal(f.started.length, 0); assert.deepEqual(f.offline.getSnapshot().pendingDownloads, []);
  } finally {f.offline.dispose();}
});

test('OFF-08: admission includes reserved transfers, the device limit and measured zero free bytes', async () => {
  const f = await offlineFixture();
  try {
    await connected(f); await f.offline.setSettings({limitBytes: 150});
    const p = preparation('copy'); f.snapshot.items = [p]; await f.offline.want([p]); await settle();
    assert.equal(f.offline.admission(51), 'downloads.blocked.limit');
    await f.offline.setSettings({limitBytes: 0});
    f.disk.freeBytes = 0; await (f.offline as any).measure();
    assert.equal(f.offline.admission(1), 'downloads.blocked.space');
  } finally {f.offline.dispose();}
});

test('OFF-09: a fresh AV download receives the server bookmark even after an empty renewal pass', async () => {
  const f = await offlineFixture();
  try {
    await connected(f);
    assert.equal(stored(f).renewed?.[owner], undefined, 'no receipts does not consume the daily renewal');
    const p = preparation('copy'); f.snapshot.items = [p];
    f.handlers.set('/v1/downloads/receipts/revalidate', () => ({results: [{receiptId: 'receipt-copy', outcome: 'renewed', code: '', receipt: receipt('copy'), viewing: {positionMs: 75000, watched: false}}]}));
    await f.offline.want([p]); await settle();
    assert.equal(stored(f).resume[owner].item, 75);
    assert.equal(JSON.parse(f.entries.get('copy').meta).viewingSynced, true);
  } finally {f.offline.dispose();}
});

test('OFF-09: a later local play wins over a delayed initial bookmark response', async () => {
  const f = await offlineFixture();
  try {
    await connected(f); const response = deferred(), entered = deferred();
    f.handlers.set('/v1/downloads/receipts/revalidate', async () => {entered.resolve(); return response.promise;});
    const p = preparation('copy'); f.snapshot.items = [p]; await f.offline.want([p]); await entered.promise;
    await observe(f, 'copy', 'new-play', 150);
    response.resolve({results: [{receiptId: 'receipt-copy', outcome: 'renewed', code: '', receipt: receipt('copy'), viewing: {positionMs: 75000, watched: false}}]});
    await settle();
    assert.equal(stored(f).resume[owner].item, 150);
    assert.equal(stored(f).plays[owner]['new-play'].positionSeconds, 150);
  } finally {f.offline.dispose();}
});

test('OFF-09: a recently renewed owner still retries the bookmark of a new copy', async () => {
  const f = await offlineFixture({state: JSON.stringify({version: 3, renewed: {[owner]: Date.now()}})});
  try {
    f.seed('copy');
    f.handlers.set('/v1/downloads/receipts/revalidate', () => ({results: [{receiptId: 'receipt-copy', outcome: 'renewed', code: '', receipt: receipt('copy'), viewing: {positionMs: 45000, watched: false}}]}));
    await connected(f);
    assert.equal(stored(f).resume[owner].item, 45);
  } finally {f.offline.dispose();}
});

test('OFF-14: history compacts by title and retains its latest unsent position', async () => {
  const plays = Object.fromEntries(Array.from({length: 400}, (_, i) => ['play-' + i, {playId: 'play-' + i, itemId: 'item', positionSeconds: i, observedAt: new Date(1_700_000_000_000 + i).toISOString()}]));
  const f = await offlineFixture({state: JSON.stringify({version: 3, plays: {[owner]: plays}})});
  try {
    f.seed('copy'); await f.offline.open(); f.offline.adopt(viewer);
    await observe(f, 'copy', 'latest', 450);
    assert.deepEqual(Object.keys(stored(f).plays[owner]), ['latest']);
    assert.equal(stored(f).plays[owner].latest.positionSeconds, 450);
    assert.equal(stored(f).resume[owner].item, 450);
  } finally {f.offline.dispose();}
});

test('OFF-14: more than 400 distinct titles keep every unsent bookmark through restart and upload', async () => {
  const plays = Object.fromEntries(Array.from({length: 400}, (_, i) => ['play-' + i, {playId: 'play-' + i, itemId: 'item-' + i, positionSeconds: i, observedAt: new Date(1_700_000_000_000 + i).toISOString()}]));
  const f = await offlineFixture({state: JSON.stringify({version: 3, plays: {[owner]: plays}})});
  try {
    f.seed('copy', 'last-item'); await f.offline.open(); f.offline.adopt(viewer);
    await observe(f, 'copy', 'last-play', 500);
    assert.equal(Object.keys(stored(f).plays[owner]).length, 401);
    f.offline.dispose(); f.offline = new f.implementation.OfflineDownloads();
    const sent: string[] = [];
    f.handlers.set('/v1/downloads/progress', body => {sent.push(...body.entries.map((value: any) => value.itemId)); return {applied: body.entries.length, conflicts: 0, entries: body.entries.map((value: any) => ({...value, outcome: 'applied', watched: false}))};});
    await connected(f); await f.offline.syncProgress();
    assert.equal(new Set(sent).size, 401); assert.ok(sent.includes('item-0')); assert.ok(sent.includes('last-item'));
    assert.equal(f.offline.getSnapshot().pendingProgress, 0);
  } finally {f.offline.dispose();}
});

test('OFF-15: unreadable or malformed present state fails closed without replacing its bytes', async () => {
  for (const raw of ['{', 'null', '[]', '{}', '{"version":99}', '{"version":3,"plays":[]}', '{"version":3,"settings":{"wifiOnly":"yes"}}', '{"version":3,"reading":{"owner":{"bad":{}}}}']) {
    const f = await offlineFixture({state: raw});
    try {
      await assert.rejects(f.offline.open());
      await assert.rejects(f.offline.setSettings({wifiOnly: false}));
      assert.equal(f.offline.getSnapshot().ready, false); assert.equal(f.offline.getSnapshot().error, 'downloads.deviceUnreadable');
      assert.equal(f.disk.state, raw); assert.equal(f.writes.length, 0);
    } finally {f.offline.dispose();}
  }
});

test('OFF-15: a failed native read preserves state and a successful retry restores write admission', async () => {
  const original = JSON.stringify({version: 3, settings: {wifiOnly: false}}), f = await offlineFixture({state: original});
  try {
    f.disk.failRead = true;
    await assert.rejects(f.offline.open());
    assert.equal(f.disk.state, original); assert.equal(f.writes.length, 0);
    f.disk.failRead = false; await f.offline.open();
    assert.equal(f.offline.getSnapshot().settings.wifiOnly, false);
    await f.offline.setSettings({deleteWatched: true});
    assert.equal(stored(f).settings.wifiOnly, false); assert.equal(stored(f).settings.deleteWatched, true);
  } finally {f.offline.dispose();}
});

test('OFF-15: a missing state file is an empty device and failed intent persistence is surfaced', async () => {
  const f = await offlineFixture();
  try {
    await connected(f); assert.equal(f.offline.getSnapshot().ready, true);
    f.disk.failWrite = true;
    await assert.rejects(f.offline.want([preparation('copy')]));
    assert.equal(f.offline.getSnapshot().error, 'downloads.deviceUnwritable');
    assert.equal(f.started.length, 0);
  } finally {f.offline.dispose();}
});
