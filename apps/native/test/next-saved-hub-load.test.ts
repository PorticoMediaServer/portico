import test from 'node:test';
import assert from 'node:assert/strict';
import {NativeSavedDestination} from '../src-next/screens/saved/model.ts';

const projection = {scope: {serverId: 'server', libraryId: '', libraryKind: 'mixed', view: 'watchlist', entityId: '', viewerFence: 'fence-1'},
  revision: {catalog: 1, viewer: 1}, heading: {key: 'saved.title', fallback: ''}, navigation: [],
  query: {sort: 'title', direction: 'asc', category: 'all', q: '', limit: 40, searchMode: 'none'}, sorts: [], filters: [],
  sections: [{id: 's', type: 'grid', heading: {key: 'saved.title', fallback: ''}, entries: [], totalCount: 0, nextCursor: ''}]};

function destination(paths: string[], savedResponse: (path: string) => unknown) {
  const api = {request: async (path: string) => {paths.push(path);
    if (path.includes('/v1/content')) return savedResponse(path);
    if (path.includes('/saved/summary')) throw Object.assign(new Error('summary unavailable'), {code: 'invalid_response'});
    if (path.includes('/saved-pins/order')) throw Object.assign(new Error('pins unavailable'), {code: 'invalid_response'});
    throw new Error(`unexpected ${path}`);
  }};
  return new NativeSavedDestination({api: api as never, scope: {serverId: 'server', viewerId: 'viewer-1'},
    requestId: async () => 'request-id', current: () => true});
}

test('a live My List draws the hub even when counts and pins are unavailable', async () => {
  const paths: string[] = [], source = destination(paths, () => projection);
  try {
    await source.load();
    const state = source.getSnapshot();
    assert.equal(state.phase, 'ready');
    assert.equal(state.summary, undefined);
    assert.deepEqual(state.pins, []);
    assert.equal(state.myList.phase, 'ready');
    assert.ok(paths.some(path => path.includes('/v1/content')));
  } finally {source.dispose();}
});

test('a refused My List is the Saved page error, not a Home-shaped page over it', async () => {
  const paths: string[] = [], source = destination(paths, () => ({error: 'unreadable'}));
  try {
    await source.load();
    const state = source.getSnapshot();
    assert.equal(state.phase, 'error');
    assert.ok(state.error);
    assert.ok(paths.some(path => path.includes('/v1/content')));
  } finally {source.dispose();}
});
