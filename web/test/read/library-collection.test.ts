import assert from 'node:assert/strict';
import {test} from 'node:test';
import {HttpLocalApi} from '@portico/client-core';
import {routePath} from '../../../packages/contracts/src/index.ts';
import {createReadingCollection, createReadingContinueCollection, createReadingSeriesCollection} from '../../src/screens/read/library-collection.ts';
import {readItemEligibility} from '../../src/screens/read/navigation.ts';

const tick = () => new Promise<void>(resolve => setImmediate(resolve));
async function settle(done: () => boolean) {for (let i = 0; i < 100 && !done(); i++) await tick(); assert.ok(done(), 'bounded unit request settled');}
function fixture(total = 480, wrongLibrary = false) {
  const calls: URL[] = [];
  const api = new HttpLocalApi('http://127.0.0.1:39117', 'unit-token', async input => {
    const url = new URL(String(input)); calls.push(url);
    if (url.pathname.endsWith('/reading/versions')) return url.pathname.includes('/unit-reading/')
      ? new Response(JSON.stringify({chosenSourceId: 'unit-source', versions: [{sourceId: 'unit-source', format: 'epub', size: 500, inventoryAvailable: false}]}), {headers: {'Content-Type': 'application/json'}})
      : new Response('{}', {status: 404, headers: {'Content-Type': 'application/json'}});
    const start = Number(url.searchParams.get('cursor') ?? 0), limit = Number(url.searchParams.get('limit'));
    const items = Array.from({length: Math.max(0, Math.min(limit, total - start))}, (_, offset) => ({id: `unit-${start + offset}`, sourceId: `source-${start + offset}`,
      libraryId: wrongLibrary ? 'another-library' : 'unit-library', title: `Unit title ${start + offset}`, format: 'epub', inventoryAvailable: true}));
    return new Response(JSON.stringify({items, ...(start + items.length < total ? {nextCursor: String(start + items.length)} : {})}), {headers: {'Content-Type': 'application/json'}});
  });
  const client = api.reading({viewer: {accountId: 'unit-account', profileId: 'unit-profile', serverId: 'unit-server', authority: 'local', role: 'member'}, isCurrent: () => true});
  const rows = createReadingCollection(client, 'unit-library');
  return {client, rows, calls, dispose() {rows.dispose(); client.dispose();}};
}

test('actual cursor pages feed the existing sparse collection without fabricated totals or full-library work', async t => {
  const f = fixture(); t.after(() => f.dispose()); assert.equal(f.calls.length, 0);
  const collection = f.rows.collection;
  collection.ensureRange(0, 12); await settle(() => collection.itemAt(0) !== undefined);
  assert.equal(collection.getSnapshot().totalIsLowerBound, true);
  assert.ok(f.calls.length <= 3); assert.equal(f.calls[0].searchParams.get('libraryId'), 'unit-library');
  for (let first = 60; first < 480; first += 60) {
    collection.ensureRange(first, first + 12); await settle(() => collection.itemAt(first) !== undefined);
    assert.ok(collection.resident().pages <= 6); assert.ok(collection.resident().items <= 360);
  }
  await settle(() => collection.getSnapshot().totalIsLowerBound === false);
  assert.equal(collection.getSnapshot().total, 480); assert.equal(collection.itemAt(479)?.id, 'unit-479');
  assert.ok(f.calls.length <= 10);
});

test('an unexpected library row never becomes a visible or selectable Read item', async t => {
  const f = fixture(1, true); t.after(() => f.dispose());
  f.rows.collection.ensureRange(0, 1); await settle(() => f.rows.collection.getSnapshot().status === 'error');
  assert.equal(f.rows.collection.itemAt(0), undefined); assert.equal(f.rows.collection.resident().items, 0);
});

test('only actual versions classify Read; unavailable real versions remain Read and ordinary item 404 stays ordinary', async t => {
  const f = fixture(); t.after(() => f.dispose());
  const reading = await readItemEligibility(f.client, 'unit-reading');
  assert.equal(reading?.itemId, 'unit-reading'); assert.equal(reading?.versions[0].inventoryAvailable, false);
  assert.equal(await readItemEligibility(f.client, 'unit-movie'), null);
});

function continueFixture(total = 480, options: {all?: boolean; wrongLibrary?: boolean; delayed?: boolean} = {}) {
  const calls: URL[] = []; let release: (() => void) | undefined, cancelled = 0;
  const api = new HttpLocalApi('http://127.0.0.1:39143', 'continue-unit-token', async (input, init = {}) => {
    const url = new URL(String(input)); calls.push(url);
    assert.equal(init.method, 'GET'); assert.equal(url.pathname, routePath('get_reading_continue'));
    const cursor = url.searchParams.get('cursor'); if (cursor) assert.match(cursor, /^after_\d+$/);
    const start = cursor ? Number(cursor.slice(6)) : 0, limit = Number(url.searchParams.get('limit'));
    const items = Array.from({length: Math.max(0, Math.min(limit, total - start))}, (_, offset) => {
      const index = start + offset, sourceId = `source-${index}`;
      return {item: {id: `item-${index}`, sourceId, libraryId: options.wrongLibrary || options.all && index % 2 ? 'another-library' : 'unit-library',
        title: `Stored title ${index}`, format: index === 0 ? 'epub' : 'pdf', inventoryAvailable: index !== 1},
        progress: {sourceId, sourceRevision: 'b'.repeat(64), revision: index + 7, observedAt: index + 10000, stale: index === 0,
          location: {format: 'pdf', pageIndex: index + 17, progression: .25}}};
    });
    const data = JSON.stringify({items, ...(start + items.length < total ? {nextCursor: `after_${start + items.length}`} : {})});
    if (options.delayed) return new Promise<Response>(resolve => {release = () => resolve(new Response(new ReadableStream({
      start(controller) {controller.enqueue(new TextEncoder().encode(data));}, cancel() {cancelled++;},
    }), {headers: {'Content-Type': 'application/json'}}));});
    return new Response(data, {headers: {'Content-Type': 'application/json'}});
  });
  const client = api.reading({viewer: {accountId: 'unit-account', profileId: 'unit-profile', serverId: 'unit-server', authority: 'local', role: 'member'}, isCurrent: () => true});
  const rows = createReadingContinueCollection(client, options.all ? undefined : 'unit-library');
  return {api, client, rows, calls, get cancelled() {return cancelled;}, release() {assert.ok(release); release();}, dispose() {rows.dispose(); client.dispose();}};
}

test('canonical Continue keeps real source/location/CAS/stale facts in a bounded sparse collection without preparing items', async t => {
  const f = continueFixture(); t.after(() => f.dispose()); assert.equal(f.calls.length, 0);
  const collection = f.rows.collection; collection.ensureRange(0, 12); await settle(() => !!collection.itemAt(0));
  const row = collection.itemAt(0)!;
  assert.equal(row.item.sourceId, 'source-0'); assert.equal(row.item.format, 'epub'); assert.equal(row.progress.stale, true);
  assert.equal(row.progress.sourceRevision, 'b'.repeat(64)); assert.equal(row.progress.revision, 7); assert.equal(row.progress.observedAt, 10000);
  assert.equal(row.progress.location.format, 'pdf'); assert.equal(row.progress.location.pageIndex, 17); assert.equal(row.progress.location.progression, .25);
  assert.equal(Object.hasOwn(row.item, 'author'), false); assert.equal(Object.isFrozen(row.progress.location), true);
  assert.equal(collection.itemAt(1)?.item.inventoryAvailable, false); assert.equal(collection.getSnapshot().totalIsLowerBound, true);
  for (let first = 60; first < 480; first += 60) {
    collection.ensureRange(first, first + 12); await settle(() => collection.itemAt(first) !== undefined);
    assert.ok(collection.resident().pages <= 6); assert.ok(collection.resident().items <= 360);
  }
  await settle(() => collection.getSnapshot().totalIsLowerBound === false);
  assert.equal(collection.getSnapshot().total, 480); assert.equal(collection.itemAt(479)?.item.id, 'item-479');
  assert.ok(f.calls.length <= 10); assert.equal(f.calls[0].searchParams.get('libraryId'), 'unit-library');
  assert.ok(f.calls.slice(1).some(url => url.searchParams.get('cursor') === 'after_60'));
});

test('profile-wide Continue omits a library filter and retains the actual library on each authorized row', async t => {
  const f = continueFixture(2, {all: true}); t.after(() => f.dispose());
  f.rows.collection.ensureRange(0, 1); await settle(() => !!f.rows.collection.itemAt(1));
  assert.equal(f.calls[0].searchParams.has('libraryId'), false);
  assert.equal(f.rows.collection.itemAt(0)?.item.libraryId, 'unit-library'); assert.equal(f.rows.collection.itemAt(1)?.item.libraryId, 'another-library');
});

test('a scoped Continue row from another library never becomes visible or selectable', async t => {
  const f = continueFixture(1, {wrongLibrary: true}); t.after(() => f.dispose());
  f.rows.collection.ensureRange(0, 0); await settle(() => f.rows.collection.getSnapshot().status === 'error');
  assert.equal(f.rows.collection.itemAt(0), undefined); assert.equal(f.rows.collection.resident().items, 0);
});

test('a retired Continue capture aborts and discards a late response without publishing rows or issuing new-viewer work', async t => {
  const f = continueFixture(1, {delayed: true}); t.after(() => f.dispose());
  f.rows.collection.ensureRange(0, 0); await settle(() => f.calls.length === 1);
  f.api.setAccessToken('replacement-unit-token'); assert.equal(f.client.signal.aborted, true); f.rows.dispose(); f.release();
  await settle(() => f.cancelled === 1);
  assert.equal(f.rows.collection.resident().items, 0); assert.equal(f.rows.collection.itemAt(0), undefined); assert.equal(f.calls.length, 1);
});

function seriesFixture(wrongSeries = false) {
  const seriesId = 's'.repeat(43), signedCursor = `${'a'.repeat(171)}.${'b'.repeat(43)}`;
  const calls: URL[] = [];
  const api = new HttpLocalApi('http://127.0.0.1:39117', 'unit-token', async input => {
    const url = new URL(String(input)); calls.push(url);
    const start = url.searchParams.has('cursor') ? 60 : 0;
    const items = Array.from({length: start === 0 ? 60 : 2}, (_, index) => ({
      id: `unit-${100 - start - index}`, sourceId: `source-${start + index}`, libraryId: 'unit-library',
      title: `Title ${100 - start - index}`, format: 'epub', inventoryAvailable: true,
      series: {id: wrongSeries ? 'x'.repeat(43) : seriesId, name: 'Accepted series', source: 'owner'},
    }));
    return new Response(JSON.stringify({items, ...(start === 0 ? {nextCursor: signedCursor} : {})}),
      {headers: {'Content-Type': 'application/json'}});
  });
  const client = api.reading({viewer: {accountId: 'unit-account', profileId: 'unit-profile', serverId: 'unit-server', authority: 'local', role: 'member'}, isCurrent: () => true});
  const rows = createReadingSeriesCollection(client, 'unit-library', seriesId);
  return {rows, calls, signedCursor, seriesId, dispose() {rows.dispose(); client.dispose();}};
}

test('series collection preserves producer order and forwards the signed cursor without generic browse', async t => {
  const f = seriesFixture(); t.after(() => f.dispose()); assert.equal(f.calls.length, 0);
  f.rows.collection.ensureRange(0, 12);
  await settle(() => f.rows.collection.itemAt(60) !== undefined && f.rows.collection.getSnapshot().totalIsLowerBound === false);
  assert.equal(f.rows.collection.itemAt(0)?.id, 'unit-100');
  assert.equal(f.rows.collection.itemAt(1)?.id, 'unit-99');
  assert.equal(f.rows.collection.itemAt(60)?.id, 'unit-40');
  assert.equal(f.rows.collection.getSnapshot().total, 62);
  assert.equal(f.calls.length, 2);
  assert.ok(f.calls.every(url => url.pathname === routePath('get_reading_series_items', {libraryId: 'unit-library', seriesId: f.seriesId})));
  assert.equal(f.calls[0].searchParams.has('cursor'), false);
  assert.equal(f.calls[1].searchParams.get('cursor'), f.signedCursor);
  assert.equal(Object.hasOwn(f.rows.collection.itemAt(0)!, 'progress'), false);
});

test('a mismatched series member never enters the resident collection', async t => {
  const f = seriesFixture(true); t.after(() => f.dispose());
  f.rows.collection.ensureRange(0, 1);
  await settle(() => f.rows.collection.getSnapshot().status === 'error');
  assert.equal(f.rows.collection.itemAt(0), undefined);
  assert.equal(f.rows.collection.resident().items, 0);
});
