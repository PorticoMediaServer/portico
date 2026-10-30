import test from 'node:test';
import assert from 'node:assert/strict';
import {withLibraryScope} from '../src/screens/browse/controller.ts';
import type {BrowseResult} from '@core/browse.ts';

const entry = (id: string, libraryId?: string) => ({id, kind: 'show', title: id, ...(libraryId ? {libraryId} : {})}) as never;
const page = (entries: never[]) => Object.freeze({pivot: 'shows', applied: {}, entries: Object.freeze(entries), pageInfo: {}, positionIndex: []}) as unknown as BrowseResult;

test('browse entries without a library inherit the page library, never overwrite one', () => {
  const source = page([entry('show-a'), entry('show-b', 'other')]);
  const scoped = withLibraryScope(source, 'library');
  assert.equal(scoped.entries[0]?.libraryId, 'library');
  assert.equal(scoped.entries[1]?.libraryId, 'other');
  assert.equal(source.entries[0]?.libraryId, undefined);
});

test('a fully scoped page keeps its identity', () => {
  const source = page([entry('show-a', 'library')]);
  assert.equal(withLibraryScope(source, 'library'), source);
  assert.equal(withLibraryScope(source, ''), source);
});
