import test from 'node:test';
import assert from 'node:assert/strict';
import type {HomeSelectionPlaybackItem} from '../../../packages/client-core/src/home-selection/types.ts';
import {selectionSegments, selectionStartSeconds} from '../src-next/platform/playback/selection.ts';

function entry(id: string, kind: 'movie' | 'show' | 'album' = 'movie', target = id, startIntent: 'resume' | 'start-over' | 'explicit' = 'resume'): HomeSelectionPlaybackItem {
  return {identity: {id, kind, libraryId: 'library'}, entry: {id, kind, libraryId: 'library', title: id, playback: {itemId: target, startSeconds: 41}}, target: {itemId: target, startSeconds: 41}, startIntent};
}
const collection: HomeSelectionPlaybackItem = {identity: {id: 'collection-1', kind: 'collection', libraryId: 'library'}, collection: {
  mediaKind: 'movie',
  collection: {id: 'collection-1', libraryId: 'library', name: 'collection-1', itemCount: 90, revision: 3},
  members: [{id: 'preview-only', kind: 'movie', libraryId: 'library', title: 'preview-only', playback: {itemId: 'preview-only'}}],
}};

test('mixed selectors keep actual aggregate identities and the caller order without preview expansion', () => {
  assert.deepEqual(selectionSegments([entry('movie-1'), entry('movie-2'), entry('show-1', 'show', 'episode-9'), collection, entry('movie-3')], true), [
    {source: {items: {ids: ['movie-1', 'movie-2']}}},
    {source: {container: {kind: 'show', id: 'show-1'}}},
    {source: {container: {kind: 'collection', id: 'collection-1'}}},
    {source: {items: {ids: ['movie-3']}}},
  ]);
});

test('the first real show resume target is an anchor, while enqueue keeps the whole show', () => {
  const show = entry('show-1', 'show', 'episode-9');
  assert.deepEqual(selectionSegments([show], true), [{source: {container: {kind: 'show', id: 'show-1'}}, anchor: {itemId: 'episode-9'}}]);
  assert.deepEqual(selectionSegments([show], false), [{source: {container: {kind: 'show', id: 'show-1'}}}]);
  // RN-08: an ordinary Resume leaves the position to the server; a card's retained one is display data.
  assert.equal(selectionStartSeconds([show]), undefined);
  assert.equal(selectionStartSeconds([entry('show-1', 'show', 'episode-9', 'explicit')]), 41);
  assert.equal(selectionStartSeconds([collection]), undefined);
});

test('real resume and start-over targets need not equal the display entry playback default', () => {
  const show = entry('show-1', 'show', 'episode-default');
  assert.ok('entry' in show);
  const resumed = {...show, target: {itemId: 'episode-resume', startSeconds: 72}};
  assert.equal(selectionStartSeconds([resumed]), undefined, 'Resume asks the server for its current position');
  assert.equal(selectionStartSeconds([{...resumed, startIntent: 'explicit' as const}]), 72);
  assert.deepEqual(selectionSegments([resumed], true)[0]?.anchor, {itemId: 'episode-resume'});
  const movie = entry('movie-1'); assert.ok('entry' in movie);
  const fromStart = {...movie, target: {itemId: 'movie-1', startSeconds: 41}, startIntent: 'start-over' as const};
  assert.equal(selectionStartSeconds([fromStart]), 0);
  assert.deepEqual(selectionSegments([fromStart], true), [{source: {items: {ids: ['movie-1']}}}]);
});

test('a banner-only collection needs no fake playable child and retains its actual container', () => {
  assert.ok('collection' in collection);
  const noPreview = {...collection, collection: {...collection.collection, members: []}};
  assert.deepEqual(selectionSegments([noPreview], true), [{source: {container: {kind: 'collection', id: 'collection-1'}}}]);
});

test('mismatched library, displayed identity, leaf target and unavailable entries are refused', () => {
  const movie = entry('movie-1'); assert.ok('entry' in movie);
  for (const item of [
    {...movie, identity: {id: 'other', kind: 'movie' as const, libraryId: 'library'}},
    {...movie, identity: {id: 'movie-1', kind: 'movie' as const, libraryId: 'other-library'}},
    {...movie, target: {itemId: 'another-movie'}},
    {...movie, entry: {...movie.entry, available: false}},
    {...movie, target: {itemId: 'movie-1', startSeconds: NaN}},
  ]) assert.throws(() => selectionSegments([item], true));
  assert.ok('collection' in collection);
  assert.throws(() => selectionSegments([{...collection, collection: {...collection.collection, collection: {...collection.collection.collection, id: 'another'}}}], false));
});

test('leaves are capped at 500 per segment and exceeding real segment limits never truncates', () => {
  const leaves = Array.from({length: 501}, (_, i) => entry(`movie-${i}`));
  const segments = selectionSegments(leaves, true);
  assert.equal(segments.length, 2);
  assert.equal('items' in segments[0]!.source && segments[0]!.source.items.ids.length, 500);
  assert.deepEqual(segments[1], {source: {items: {ids: ['movie-500']}}});
  const shows = Array.from({length: 17}, (_, i) => entry(`show-${i}`, 'show', `episode-${i}`));
  // SIM-04, as on the web: Play replaces the queue and is capped; adding to it is not.
  assert.throws(() => selectionSegments(shows, true), /queue_too_large/);
  assert.equal(selectionSegments(shows, false).length, 17);
});
