import assert from 'node:assert/strict';
import {test} from 'node:test';
import {HttpLocalApi} from '@portico/client-core';
import {createReadingCollection, createReadingContinueCollection, createReadingSeriesCollection} from '../../src/screens/read/library-collection.ts';

const tick = () => new Promise<void>(resolve => setImmediate(resolve));
async function settle(done: () => boolean) {for (let i = 0; i < 100 && !done(); i++) await tick(); assert.ok(done(), 'bounded unit request settled');}
// The rebuilt server's answers: the item list for a library's titles and a series' volumes, and the
// landing or Home row for Continue reading.
const wire = (id: string, libraryId: string, extra: Record<string, unknown> = {}) => ({id, kind: 'book', title: `Unit title ${id}`, libraryId, addedAt: '2026-11-12T00:00:00Z',
  images: {}, state: {watched: false, playCount: 0, favorite: false, inList: false, notInterested: false}, ...extra});
const json = (data: unknown) => new Response(JSON.stringify(data), {headers: {'Content-Type': 'application/json'}});
const viewer = {accountId: 'unit-account', profileId: 'unit-profile', serverId: 'unit-server', authority: 'local', role: 'member'} as const;
function fixture(total = 480, wrongLibrary = false, budget = Infinity) {
  const calls: URL[] = [];
  const api = new HttpLocalApi('http://127.0.0.1:39117', 'unit-token', async input => {
    const url = new URL(String(input)); calls.push(url);
    const start = Number(url.searchParams.get('cursor') ?? 0), limit = Number(url.searchParams.get('limit'));
    // RD-6: the server's byte budget can stop a page early, with a cursor from the last item kept.
    const items = Array.from({length: Math.max(0, Math.min(limit, budget, total - start))}, (_, offset) => wire(`unit-${start + offset}`, wrongLibrary ? 'another-library' : 'unit-library'));
    return json({items, ...(start + items.length < total ? {nextCursor: String(start + items.length)} : {})});
  });
  const client = api.reading({viewer, isCurrent: () => true});
  const rows = createReadingCollection(client, 'unit-library');
  return {client, rows, calls, dispose() {rows.dispose(); client.dispose();}};
}

test('actual cursor pages feed the existing sparse collection without fabricated totals or full-library work', async t => {
  const f = fixture(); t.after(() => f.dispose()); assert.equal(f.calls.length, 0);
  const collection = f.rows.collection;
  collection.ensureRange(0, 12); await settle(() => collection.itemAt(0) !== undefined);
  assert.equal(collection.getSnapshot().totalIsLowerBound, true);
  assert.ok(f.calls.length <= 3); assert.equal(f.calls[0].pathname, '/v1/items'); assert.equal(f.calls[0].searchParams.get('library'), 'unit-library');
  for (let first = 60; first < 480; first += 60) {
    collection.ensureRange(first, first + 12); await settle(() => collection.itemAt(first) !== undefined);
    assert.ok(collection.resident().pages <= 6); assert.ok(collection.resident().items <= 360);
  }
  await settle(() => collection.getSnapshot().totalIsLowerBound === false);
  assert.equal(collection.getSnapshot().total, 480); assert.equal(collection.itemAt(479)?.id, 'unit-479');
  assert.ok(f.calls.length <= 10);
});

test('RD-6: a page the server stopped early is read on from its cursor, so every item has its place and none is skipped', async t => {
  const f = fixture(130, false, 7); t.after(() => f.dispose());
  const collection = f.rows.collection;
  collection.ensureRange(0, 70); await settle(() => collection.itemAt(69) !== undefined);
  assert.notEqual(collection.getSnapshot().status, 'error');
  for (let i = 0; i < 70; i++) assert.equal(collection.itemAt(i)?.id, `unit-${i}`, `slot ${i}`);
  collection.ensureRange(120, 130); await settle(() => collection.getSnapshot().totalIsLowerBound === false);
  assert.equal(collection.getSnapshot().total, 130); assert.equal(collection.itemAt(129)?.id, 'unit-129');
  assert.ok(f.calls.every(call => Number(call.searchParams.get('limit')) >= 1), 'each top-up asks only for the slots left');
});

test('an unexpected library row never becomes a visible or selectable Read item', async t => {
  const f = fixture(1, true); t.after(() => f.dispose());
  f.rows.collection.ensureRange(0, 1); await settle(() => f.rows.collection.getSnapshot().status === 'error');
  assert.equal(f.rows.collection.itemAt(0), undefined); assert.equal(f.rows.collection.resident().items, 0);
});

function continueFixture(total = 3, options: {all?: boolean; wrongLibrary?: boolean; delayed?: boolean} = {}) {
  const calls: URL[] = []; let release: (() => void) | undefined, cancelled = 0;
  const api = new HttpLocalApi('http://127.0.0.1:39143', 'continue-unit-token', async (input, init = {}) => {
    const url = new URL(String(input)); calls.push(url);
    assert.equal(init.method, 'GET');
    const items = Array.from({length: total}, (_, index) => wire(`item-${index}`, options.wrongLibrary || options.all && index % 2 ? 'another-library' : 'unit-library', {
      state: {watched: false, lastPlayedAt: new Date(10000 + index).toISOString(),
        position: JSON.stringify({format: 'pdf', pageIndex: index + 17, progression: .25, fingerprint: '0123456789abcdef'})}}));
    const data = JSON.stringify({rows: [{id: url.pathname === '/v1/home' ? 'continue_reading' : 'continue', title: 'Continue', shape: 'poster', items}]});
    if (options.delayed) return new Promise<Response>(resolve => {release = () => resolve(new Response(new ReadableStream({
      start(controller) {controller.enqueue(new TextEncoder().encode(data));}, cancel() {cancelled++;},
    }), {headers: {'Content-Type': 'application/json'}}));});
    return new Response(data, {headers: {'Content-Type': 'application/json'}});
  });
  const client = api.reading({viewer, isCurrent: () => true});
  const rows = createReadingContinueCollection(client, options.all ? undefined : 'unit-library');
  return {api, client, rows, calls, get cancelled() {return cancelled;}, release() {assert.ok(release); release();}, dispose() {rows.dispose(); client.dispose();}};
}

test('Continue keeps each title\'s place in the library\'s own row, read once, without preparing items', async t => {
  const f = continueFixture(); t.after(() => f.dispose()); assert.equal(f.calls.length, 0);
  const collection = f.rows.collection; collection.ensureRange(0, 12); await settle(() => !!collection.itemAt(0) && collection.getSnapshot().totalIsLowerBound === false);
  const row = collection.itemAt(0)!;
  assert.equal(row.item.id, 'item-0'); assert.equal(row.item.format, 'pdf');
  assert.equal(row.progress.sourceRevision, '0123456789abcdef'); assert.equal(row.progress.observedAt, 10000);
  assert.equal(row.progress.location.format, 'pdf'); assert.equal(row.progress.location.pageIndex, 17); assert.equal(row.progress.location.progression, .25);
  assert.equal(Object.hasOwn(row.item, 'author'), false); assert.equal(Object.isFrozen(row.progress.location), true);
  assert.equal(collection.getSnapshot().total, 3);
  assert.deepEqual(f.calls.map(url => url.pathname), ['/v1/libraries/unit-library/landing']);
});

test('profile-wide Continue is Home\'s row and retains the actual library on each authorized row', async t => {
  const f = continueFixture(2, {all: true}); t.after(() => f.dispose());
  f.rows.collection.ensureRange(0, 1); await settle(() => !!f.rows.collection.itemAt(1));
  assert.equal(f.calls[0].pathname, '/v1/home');
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

test('series collection keeps the volumes in the server\'s order and goes on from its cursor', async t => {
  const calls: URL[] = [];
  const api = new HttpLocalApi('http://127.0.0.1:39117', 'unit-token', async input => {
    const url = new URL(String(input)); calls.push(url);
    if (url.pathname === '/v1/items/itm_series') return json(wire('itm_series', 'unit-library',
      {kind: 'series', title: 'Harbour Tales', counts: {children: 62, leaves: 62, unwatchedLeaves: 61}}));
    const start = url.searchParams.has('cursor') ? 60 : 0;
    return json({items: Array.from({length: start === 0 ? 60 : 2}, (_, index) => wire(`unit-${100 - start - index}`, 'unit-library', {kind: 'issue'})), ...(start === 0 ? {nextCursor: 'more'} : {})});
  });
  const client = api.reading({viewer, isCurrent: () => true}), rows = createReadingSeriesCollection(client, 'unit-library', 'itm_series');
  t.after(() => {rows.dispose(); client.dispose();});
  rows.collection.ensureRange(0, 12);
  await settle(() => rows.collection.itemAt(60) !== undefined && rows.collection.getSnapshot().totalIsLowerBound === false);
  assert.deepEqual([0, 1, 60].map(index => rows.collection.itemAt(index)?.id), ['unit-100', 'unit-99', 'unit-40']);
  assert.equal(rows.collection.getSnapshot().total, 62);
  assert.deepEqual(rows.overview.getSnapshot(), {id: 'itm_series', title: 'Harbour Tales', total: 62, completed: 1});
  const volumes = calls.filter(url => url.pathname === '/v1/items');
  assert.ok(volumes.every(url => url.searchParams.get('series') === 'itm_series' && url.searchParams.get('sort') === 'order'));
  assert.equal(volumes[0].searchParams.has('cursor'), false); assert.equal(volumes[1].searchParams.get('cursor'), 'more');
  assert.equal(calls.filter(url => url.pathname === '/v1/items/itm_series').length, 2);
});
