import {strict as assert} from 'node:assert';
import {test} from 'node:test';
import type {WebPlaybackRuntimeSnapshot} from '../src/platform/playback';
import {musicQueueOrigin, musicQueueItem} from '../src/screens/watch/music/queue-facts.ts';

const snapshot = () => ({fence: {kind: 'queue', entryId: 'now', itemId: 'track'}, queueView: {
  queue: {id: 'queue', revision: '4', currentEntryId: 'now'},
  items: [{entryId: 'before', itemId: 'track', libraryId: 'library', kind: 'song'}, {entryId: 'now', itemId: 'track', libraryId: 'library', kind: 'song', artist: 'Track artist', title: 'Track title'}],
  window: {queueId: 'queue', revision: '4', entries: [{entryId: 'before', segmentId: 'album'}, {entryId: 'now', segmentId: 'playlist'}], segments: [{id: 'album', label: 'An album'}, {id: 'playlist', label: 'The chosen playlist'}]},
}} as unknown as WebPlaybackRuntimeSnapshot);

test('Playing from resolves the current occurrence segment even for duplicate item IDs', () => {
  assert.equal(musicQueueOrigin(snapshot()), 'The chosen playlist');
});
test('missing origin and stale queue windows omit source copy rather than use track identity', () => {
  const original = snapshot(), view = original.queueView!;
  for (const window of [undefined, {...view.window!, revision: '3'}, {...view.window!, queueId: 'other'}, {...view.window!, segments: []}, {...view.window!, entries: []}, {...view.window!, segments: [{id: 'playlist', label: '  '}]}]) {
    assert.equal(musicQueueOrigin({...original, queueView: {...view, window}}), undefined);
  }
  assert.equal(musicQueueOrigin({...original, queueView: {...view, queue: {...view.queue, currentEntryId: 'before'}}}), undefined);
});
test('favorite detail uses a real current song/library target; missing or successor targets stay absent', () => {
  const original = snapshot(), view = original.queueView!;
  assert.deepEqual(musicQueueItem(original), {libraryId: 'library', itemId: 'track'});
  assert.equal(musicQueueItem({...original, queueView: {...view, items: view.items.filter(item => item.entryId !== 'now')}}), undefined);
  for (const replacement of [{libraryId: undefined}, {itemId: 'other'}, {kind: 'audiobook_file'}]) {
    assert.equal(musicQueueItem({...original, queueView: {...view, items: [{...view.items[1], ...replacement}]}}), undefined);
  }
  assert.equal(musicQueueItem({...original, queueView: {...view, queue: {...view.queue, currentEntryId: 'before'}}}), undefined);
});
