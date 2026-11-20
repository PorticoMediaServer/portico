import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';

// Looker, 20 Nov (books on iPhone): after closing the reader with iOS's swipe, Continue reading did nothing until the
// book page was entered again. The page armed its actions from the snapshot it was drawn under and kept that one:
// coming back by the system's Back shows the same route object under a new revision, so the page re-arms from it.
test('a book page sees its own route\'s revision only while that route is on top', async () => {
  const {useRouteRevision} = await screenModule(new URL('../src-next/screens/read/use-route-revision.ts', import.meta.url), {
    react: {...React, useCallback: (fn: unknown) => fn, useSyncExternalStore: (_subscribe: unknown, read: () => unknown) => read()},
  });
  const book = {kind: 'read-book'}, reader = {kind: 'read'};
  let snapshot = {route: book, revision: 4};
  const context = {navigation: {subscribe: () => () => {}, getSnapshot: () => snapshot}};
  assert.equal(useRouteRevision(context, book), 4, 'drawn on top');
  snapshot = {route: reader, revision: 5};
  assert.equal(useRouteRevision(context, book), undefined, 'the reader is over it');
  snapshot = {route: book, revision: 6};
  assert.equal(useRouteRevision(context, book), 6, 'back on top by the system\'s Back: the same route, a new revision');
});
