import test from 'node:test';
import assert from 'node:assert/strict';
import type {DetailProjection, DetailSnapshot} from '@core/detail.ts';
import type {ShowWorkspaceData} from '@core/show-workspace.ts';
import type {ContentEntry, ContentProjection, ContentSection} from '@core/library-content.ts';
import {showPrimary} from '../src/screens/detail/show-primary.ts';
import {albumSortRoute, albumTrackGroups} from '../src/screens/detail/album-model.ts';

const next = {id: 'episode', kind: 'episode', libraryId: 'tv', title: 'Next episode', seasonNumber: 2, episodeNumber: 4, playback: {itemId: 'episode'}} as const;
const workspace = {scope: {serverId: 'server', libraryId: 'tv', viewerFence: 'viewer'}, nextUp: next} as unknown as ShowWorkspaceData;
const detail = {scope: {serverId: 'server', libraryId: 'tv', itemId: 'episode', viewerFence: 'viewer'}, item: {id: 'episode', kind: 'episode', duration: 100, progressSeconds: 25},
  actions: [{id: 'server-action', enabled: true, playback: {itemId: 'episode', startSeconds: 25}}]} as unknown as DetailProjection;
const ready = (data: DetailProjection = detail): Pick<DetailSnapshot, 'phase' | 'data'> => ({phase: 'ready', data});

test('show primary keeps the next episode facts and uses its actual offered playback command', () => {
  const offer = showPrimary(workspace, ready());
  assert.ok(offer);
  assert.strictEqual(offer.next, next);
  assert.strictEqual(offer.detail, detail);
  assert.equal(offer.action.id, 'server-action', 'action ids need not be play or resume');
  assert.equal(offer.resume, true);
  assert.equal(offer.progress, .25);
  assert.equal(showPrimary(workspace, ready({...detail, actions: [{...detail.actions[0], playback: {itemId: 'episode', startSeconds: 0}}]}))?.resume, false);
  assert.equal(showPrimary(workspace, ready({...detail, item: {...detail.item, progressSeconds: 0}}))?.resume, false);
});

test('show primary refuses stale, unavailable and cross-scope episode offers', () => {
  for (const phase of ['idle', 'loading', 'error'] as const) assert.equal(showPrimary(workspace, {phase, data: detail}), undefined);
  assert.equal(showPrimary({...workspace, nextUp: undefined}, ready()), undefined);
  assert.equal(showPrimary(workspace, {phase: 'ready', data: null}), undefined);
  assert.equal(showPrimary(workspace, ready({...detail, item: {...detail.item, id: 'old-episode'}})), undefined);
  assert.equal(showPrimary(workspace, ready({...detail, item: {...detail.item, kind: 'movie'}})), undefined);
  for (const scope of [{...detail.scope, libraryId: 'other'}, {...detail.scope, serverId: 'other'}, {...detail.scope, viewerFence: 'other'}, {...detail.scope, itemId: 'other'}]) {
    assert.equal(showPrimary(workspace, ready({...detail, scope})), undefined);
  }
  assert.equal(showPrimary(workspace, ready({...detail, actions: [{...detail.actions[0], enabled: false}]})), undefined);
  assert.equal(showPrimary(workspace, ready({...detail, actions: [{...detail.actions[0], id: 'start_over'}]})), undefined);
  assert.equal(showPrimary(workspace, ready({...detail, actions: [{...detail.actions[0], playback: {itemId: 'other', startSeconds: 0}}]})), undefined);
});

test('show progress is finite for a zero duration and bounded at the ends', () => {
  assert.equal(showPrimary(workspace, ready({...detail, item: {...detail.item, duration: 0}}))?.progress, 0);
  assert.equal(showPrimary(workspace, ready({...detail, item: {...detail.item, progressSeconds: -1}}))?.progress, 0);
  assert.equal(showPrimary(workspace, ready({...detail, item: {...detail.item, progressSeconds: 500}}))?.progress, 1);
});

const song = (id: string, discNumber?: number): ContentEntry => ({id, kind: 'song', libraryId: 'music', title: id, ...(discNumber === undefined ? {} : {discNumber})});
const section = (id: string, entries: readonly ContentEntry[], nextCursor = ''): ContentSection => ({id, type: 'list', heading: {key: id, fallback: id}, entries, nextCursor, totalCount: entries.length});

test('album tracks preserve every server section and split real disc boundaries without reordering', () => {
  const one = song('one', 1), two = song('two', 1), three = song('three', 2), extra = song('extra', 2);
  const first = section('items', [one, two, three], 'page-two'), second = section('bonus', [extra]);
  const groups = albumTrackGroups([first, second]);
  assert.deepEqual(groups.map(group => [group.section.id, group.discNumber, group.entries.map(entry => entry.id)]), [
    ['items', 1, ['one', 'two']], ['items', 2, ['three']], ['bonus', 2, ['extra']],
  ]);
  assert.strictEqual(groups[0].entries[0], one);
  assert.equal(groups[0].section.nextCursor, 'page-two');
  assert.deepEqual(first.entries, [one, two, three], 'the server page is not mutated');
  assert.equal(new Set(groups.map(group => group.key)).size, groups.length);
});

test('album grouping does not invent missing discs or undo a server sort across discs', () => {
  const entries = [song('z', 2), song('a', 1), {id: 'cover', kind: 'album', title: 'Album'} as ContentEntry, song('unknown'), song('b', 2)];
  const groups = albumTrackGroups([section('items', entries)]);
  assert.deepEqual(groups.map(group => group.discNumber), [2, 1, undefined, 2]);
  assert.deepEqual(groups.flatMap(group => group.entries.map(entry => entry.id)), ['z', 'a', 'unknown', 'b']);
  assert.deepEqual(albumTrackGroups([section('empty', [])]), []);
});

test('album sort requests stay album-scoped and admit only offered fields and directions', () => {
  const projection = {scope: {libraryId: 'music', view: 'album', entityId: 'album'}, sorts: [
    {id: 'track', labelKey: 'sort.track', directions: ['asc']}, {id: 'title', labelKey: 'sort.title', directions: ['asc', 'desc']},
  ]} as unknown as ContentProjection;
  assert.deepEqual(albumSortRoute(projection, {field: 'title', direction: 'desc'}), {libraryId: 'music', view: 'album', entityId: 'album', sort: 'title', direction: 'desc'});
  assert.equal(albumSortRoute(projection, {field: 'track', direction: 'desc'}), undefined);
  assert.equal(albumSortRoute(projection, {field: 'unknown', direction: 'asc'}), undefined);
  assert.equal(albumSortRoute({...projection, scope: {...projection.scope, view: 'artist'}}, {field: 'title', direction: 'asc'}), undefined);
  assert.equal(albumSortRoute({...projection, scope: {...projection.scope, entityId: ''}}, {field: 'title', direction: 'asc'}), undefined);
});
