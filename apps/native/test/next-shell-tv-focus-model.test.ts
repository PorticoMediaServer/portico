import assert from 'node:assert/strict';
import test from 'node:test';
import type {HomeRow} from '../../../packages/client-core/src/home';
import type {ContentEntry} from '../../../packages/client-core/src/library-content';
import {tvFocusIds, sameDestination, tvHomeRegionMap} from '../src-next/shell/tv-focus';
import {tvHomeRows} from '../src-next/screens/home/tv-presentation';

const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'fence'};
const entry = (id: string, extra = {}): ContentEntry => ({id, title: id, kind: 'movie', libraryId: 'movies', ...extra});
const row = (id: string, entries: readonly ContentEntry[]): HomeRow => ({id, title: id, kind: 'recent', artworkShape: 'poster', entries, start: 0,
  viewerFence: 'fence', endpoint: '', privacySensitivity: '', policyState: '', priority: 1, required: false, hideable: true, reorderable: true,
  critical: false, cursorCapable: true, total: entries.length, limit: 20, hasMore: false, nextCursor: '', revision: {catalog: 1, viewer: 1}});

test('TV semantic addresses preserve entity identity across reorder and isolate every authority component', () => {
  const ids = tvFocusIds(authority), item = entry('actual/id');
  assert.equal(ids.entry(item), JSON.stringify(['movie', 'movies', 'actual/id']));
  assert.deepEqual(ids.start, {regionId: ids.spotlight, childId: 'play'});
  assert.notEqual(ids.row('continue'), ids.row('continue/other'));
  for (const field of Object.keys(authority) as (keyof typeof authority)[]) {
    assert.notEqual(tvFocusIds({...authority, [field]: 'other'}).rail, ids.rail);
  }
  assert.equal(ids.destination({kind: 'library', libraryId: 'real-library'}), 'library:real-library');
  assert.equal(sameDestination({kind: 'library', libraryId: 'a'}, {kind: 'library', libraryId: 'b'}), false);
});

test('Home declarative native guides connect spotlight, each remembered row and the last stop without resetting memory', () => {
  const ids = tvFocusIds(authority), map = tvHomeRegionMap(authority, ['continue', 'recent']);
  assert.equal(map.spotlight.entry, 'play');
  assert.equal(map.spotlight.edges.down, ids.row('continue'));
  assert.deepEqual(map.rows.get('continue')?.edges, {left: ids.rail, up: ids.spotlight, down: ids.row('recent')});
  assert.deepEqual(map.rows.get('recent')?.neighbors, {previous: ids.row('continue'), next: ids.customize});
  assert.equal(map.customize.edges.up, ids.row('recent'));
  assert.equal(map.customize.edges.down, undefined);
  assert.equal(tvHomeRegionMap(authority, []).spotlight.edges.down, ids.customize);
  assert.equal(ids.page('recent', 'cursor/1'), JSON.stringify(['page', 'recent', 'cursor/1']));
});

test('focused and retained rows defer reranking, preserve actual updated objects, and allow deletion fallback', () => {
  const a = entry('a'), b = entry('b'), c = entry('c'), d = entry('d');
  const before = [row('continue', [a, b, c]), row('recent', [d])];
  const latestB = entry('b', {title: 'Updated title'});
  const incoming = [row('new-row', [d]), row('recent', [d]), row('continue', [latestB, c, a, d])];
  const held = tvHomeRows(before, incoming, id => id === 'continue', true);
  assert.deepEqual(held.map(value => value.id), ['continue', 'recent', 'new-row']);
  assert.deepEqual(held[0].entries.map(value => value.id), ['a', 'b', 'c', 'd']);
  assert.equal(held[0].entries[1], latestB, 'actions receive current server capabilities, never a stale cloned entry');
  const removed = tvHomeRows(held, [row('continue', [c, a]), row('recent', [d])], () => true, true);
  assert.deepEqual(removed[0].entries.map(value => value.id), ['a', 'c'], 'removed focus target is released for the shared neighbour fallback');
  assert.deepEqual(tvHomeRows(held, incoming, () => false, false), incoming, 'leaving releases the deferred server order');
  assert.deepEqual(tvHomeRows(before, [row('recent', [d])], () => true, true).map(value => value.id), ['recent']);
});
