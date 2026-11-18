import test from 'node:test';
import assert from 'node:assert/strict';
import {captureQueueSource, type WebPlaybackQueueSource} from '../src/platform/playback/queue-source.ts';

test('queue capture preserves the Home row, library, filters and order after the source changes', () => {
  const input = {homeRow: {id: 'recently_added', library: 'films', genre: 'drama', year: '2020..2029', favorite: false, sort: 'title', order: 'asc'}};
  const expected = structuredClone(input), captured = captureQueueSource(input);
  input.homeRow.genre = 'horror'; input.homeRow.order = 'desc';
  assert.deepEqual(captured, expected);
  assert.ok(Object.isFrozen(captured) && 'homeRow' in captured && Object.isFrozen(captured.homeRow));
  assert.deepEqual(captureQueueSource({saved: {id: 'actual_view'}}), {saved: {id: 'actual_view'}});
  assert.deepEqual(captureQueueSource({container: {kind: 'collection', id: 'actual_collection'}}), {container: {kind: 'collection', id: 'actual_collection'}});
});

test('queue capture refuses visible item aliases, mixed sources, unknown query fields and altered sort semantics', () => {
  for (const source of [
    {items: {ids: ['first_visible']}}, {saved: {id: 'view'}, container: {kind: 'collection', id: 'collection'}},
    {container: {kind: 'person', id: 'person'}}, {saved: {id: ''}},
    {query: {libraryId: 'library', limit: 80}},
    {query: {libraryId: 'library', sort: [{field: 'title', direction: 'desc', seed: 1}]}},
    {query: {libraryId: 'library', query: {variant:'predicate',field: 'title', operator: 'invented', value: 'x'}}},
  ]) assert.throws(() => captureQueueSource(source as WebPlaybackQueueSource));
});


test('season capture snapshots only the complete resource and refuses page/filter/order additions',()=>{
 const input={container:{kind:'season' as const,id:'season-1'}},captured=captureQueueSource(input);input.container.id='season-2';
 assert.deepEqual(captured,{container:{kind:'season',id:'season-1'}});assert.ok(Object.isFrozen(captured)&&'container' in captured&&Object.isFrozen(captured.container));
 for(const extra of [{ids:['visible']},{playState:'unplayed'},{direction:'desc'},{shuffle:true}])assert.throws(()=>captureQueueSource({container:{kind:'season',id:'season-1',...extra}} as WebPlaybackQueueSource));
});
