import test from 'node:test';
import assert from 'node:assert/strict';
import type {HomeSelectionPlaybackItem} from '@core/home-selection/types.ts';
import {selectionSegments, selectionStartSeconds} from '../src/platform/playback/selection.ts';

type CatalogueIdentity = Extract<HomeSelectionPlaybackItem['identity'], {libraryId: string}>;
type EntrySelection = Extract<HomeSelectionPlaybackItem, {entry: unknown}> & {identity: CatalogueIdentity};
const leaf = (id: string): EntrySelection => ({identity: {id, kind: 'movie', libraryId: 'library'}, entry: {id, kind: 'movie', libraryId: 'library', title: id, playback: {itemId: id}}, target: {itemId: id}});
const album: EntrySelection = {identity: {id: 'release', kind: 'album', libraryId: 'library'}, entry: {id: 'release', kind: 'album', libraryId: 'library', title: 'release', playback: {itemId: 'song-two', startSeconds: 12}}, target: {itemId: 'song-two', startSeconds: 12}};

test('selection playback retains displayed aggregate identity and the supplied display order', () => {
  assert.deepEqual(selectionSegments([leaf('first'), album, leaf('last')], true), [
    {source: {items: {ids: ['first']}}}, {source: {container: {kind: 'album', id: 'release'}}}, {source: {items: {ids: ['last']}}},
  ]);
  assert.deepEqual(selectionSegments([album, leaf('last')], true), [{source: {container: {kind: 'album', id: 'release'}}, anchor: {itemId: 'song-two'}}, {source: {items: {ids: ['last']}}}]);
});

test('selection enqueue keeps pick order without moving current to an aggregate anchor', () => {
  assert.deepEqual(selectionSegments([album, leaf('last'), leaf('first')], false), [{source: {container: {kind: 'album', id: 'release'}}}, {source: {items: {ids: ['last', 'first']}}}]);
});

test('selection leaves are chunked at the actual 500-item segment limit without losing picks', () => {
  const items = Array.from({length: 503}, (_, index) => leaf(`item-${index}`)), segments = selectionSegments(items, true);
  assert.equal(segments.length, 2);
  assert.deepEqual(segments.flatMap(s => 'items' in s.source ? s.source.items.ids : []), items.map(i => i.entry.id));
});

test('selection rejects forged displayed identities and playback targets before dispatch', () => {
  assert.throws(() => selectionSegments([], true));
  assert.throws(() => selectionSegments([{...album, identity: {...album.identity, id: 'different'}}], true));
  assert.throws(() => selectionSegments([{...album, target: {itemId: '../different'}}], true));
  assert.throws(() => selectionSegments([{...leaf('first'), entry: {...leaf('first').entry, available: false}}], true));
  assert.throws(() => selectionSegments([{...leaf('first'), target: {itemId: 'different'}}], true));
});

test('selection keeps authoritative resume and explicit start-over distinct from the displayed default', () => {
  assert.deepEqual(selectionSegments([{...album, target: {itemId: 'resume-child', startSeconds: 45}}], true), [{source: {container: {kind: 'album', id: 'release'}}, anchor: {itemId: 'resume-child'}}]);
  const movie = leaf('movie');
  assert.deepEqual(selectionSegments([{...movie, target: {itemId: 'movie', startSeconds: 0}}], true), [{source: {items: {ids: ['movie']}}}]);
});

test('a collection keeps its real selector and never treats its preview members as the playback target', () => {
  const collection: Extract<HomeSelectionPlaybackItem, {collection: unknown}> & {identity: CatalogueIdentity} = {identity: {kind: 'collection', id: 'collection', libraryId: 'library'}, collection: {collection: {id: 'collection', libraryId: 'library', name: 'Actual collection', itemCount: 40, revision: 2}, mediaKind: 'movie', members: [leaf('preview-only').entry]}};
  assert.deepEqual(selectionSegments([collection, album, leaf('last')], true), [
    {source: {container: {kind: 'collection', id: 'collection'}}},
    {source: {container: {kind: 'album', id: 'release'}}},
    {source: {items: {ids: ['last']}}},
  ]);
  assert.deepEqual(selectionSegments([collection], false), [{source: {container: {kind: 'collection', id: 'collection'}}}]);
  assert.equal(selectionStartSeconds([collection, album]), undefined);
  assert.equal(selectionStartSeconds([album, collection]), undefined);
  assert.equal(selectionStartSeconds([{...album, startIntent: 'explicit'}, collection]), 12);
  assert.throws(() => selectionSegments([{...collection, identity: {...collection.identity, id: 'forged'}}], true));
  assert.throws(() => selectionSegments([{...collection, identity: {...collection.identity, libraryId: 'other'}}], true));
});

test('a Play selection over the single-request segment ceiling is refused, while enqueue stays per segment', () => {
  const show = (id: string): EntrySelection => ({identity: {id, kind: 'show', libraryId: 'library'}, entry: {id, kind: 'show', libraryId: 'library', title: id, playback: {itemId: `${id}-resume`}}, target: {itemId: `${id}-resume`}});
  const items = Array.from({length: 17}, (_, index) => index % 2 ? leaf(`item-${index}`) : show(`show-${index}`));
  assert.equal(selectionSegments(items.slice(0, 16), true).length, 16);
  assert.throws(() => selectionSegments(items, true), (error: unknown) => error instanceof Error && error.message === 'queue_too_large');
  assert.equal(selectionSegments(items, false).length, 17);
});

test('the first target start intent decides the queue start, as native does', () => {
  assert.equal(selectionStartSeconds([{...album, startIntent: 'resume'}]), undefined);
  assert.equal(selectionStartSeconds([{...album, startIntent: 'start-over', target: {itemId: 'song-two', startSeconds: 0}}]), 0);
  assert.equal(selectionStartSeconds([{...album, startIntent: 'explicit'}]), 12);
  assert.throws(() => selectionStartSeconds([{...album, startIntent: 'explicit', target: {itemId: 'song-two'}}]));
  assert.throws(() => selectionStartSeconds([{...album, startIntent: 'explicit', target: {itemId: 'song-two', startSeconds: -1}}]));
});
