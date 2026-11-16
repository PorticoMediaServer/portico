import test from 'node:test';
import assert from 'node:assert/strict';
import {DetailService} from '../src/detail.ts';
import {ShowWorkspaceService} from '../src/show-workspace.ts';
import {FreshnessHub} from '../src/freshness-hub.ts';
import {createViewerQueries} from '../src/query/viewer-queries.ts';

// W1 F5 (16 Nov walk): leaving Search for a result disposed the search page's detail services before
// its top result's cleanup cancelled them; that cancel threw, and on a phone the whole app crashed.
test('cancelling a detail or show service after it is disposed is harmless teardown', () => {
  const scope = {serverId: 'server', viewerId: 'viewer'};
  const queries = createViewerQueries({scope, hub: new FreshnessHub(), api: {request: async () => ({})} as never, gcTimeMs: 0});
  const detail = new DetailService({queries, api: {request: async () => ({})} as never, scope: scope as never});
  const show = new ShowWorkspaceService({queries, scope: scope as never});
  detail.dispose(); show.dispose();
  assert.doesNotThrow(() => detail.cancel());
  assert.doesNotThrow(() => show.cancel());
  queries.dispose?.();
});
