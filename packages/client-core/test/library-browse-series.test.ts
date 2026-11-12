import test from 'node:test';
import assert from 'node:assert/strict';
import {libraryBrowseCapabilities, libraryItemQuery} from '../src/library-browse.ts';

test('an audiobook library\'s Series tab lists its series, each opening its volumes', () => {
  const library = {id: 'lib_a', name: 'Audiobooks', kind: 'audiobooks', views: [{kind: 'audiobook'}, {kind: 'series'}], sorts: [{id: 'title', order: 'asc'}], filters: []} as never;
  const caps = libraryBrowseCapabilities(library, 'series') as {pivots: {id: string}[]; resolvedPivot: {entityKinds: string[]}};
  assert.deepEqual(caps.pivots.map(pivot => pivot.id), ['books', 'series']);
  assert.deepEqual(caps.resolvedPivot.entityKinds, ['series']);
  assert.deepEqual(libraryItemQuery(library, 'series', [], [{field: 'title', direction: 'asc'}]), {library: 'lib_a', kind: 'series', sort: 'title', order: 'asc'});
});
