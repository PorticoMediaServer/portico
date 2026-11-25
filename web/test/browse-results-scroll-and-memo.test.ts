import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {browseScrollKey, clearBrowseScrollPositions, getBrowseScrollIndex, setBrowseScrollIndex} from '../src/screens/browse/scroll-position.ts';

test('browseScrollKey distinguishes scope, predicates, sort, and view', () => {
  const base = {libraryId: 'lib-1', pivot: 'movies'};
  const k1 = browseScrollKey(base, [], {field: 'title', direction: 'asc'}, 'grid');
  const k2 = browseScrollKey(base, [], {field: 'title', direction: 'asc'}, 'list');
  const k3 = browseScrollKey(base, [], {field: 'year', direction: 'desc'}, 'grid');
  const k4 = browseScrollKey({...base, libraryId: 'lib-2'}, [], {field: 'title', direction: 'asc'}, 'grid');
  assert.equal(new Set([k1, k2, k3, k4]).size, 4);
});

test('scroll position store persists indices with bounded LRU eviction', () => {
  clearBrowseScrollPositions();
  setBrowseScrollIndex('view-a', 140);
  assert.equal(getBrowseScrollIndex('view-a'), 140);
  setBrowseScrollIndex('view-bad', -5);
  assert.equal(getBrowseScrollIndex('view-bad'), undefined);

  // Fill beyond 64 entries and verify oldest untouched entry is evicted while recently read stays.
  for (let i = 0; i < 63; i++) setBrowseScrollIndex(`k-${i}`, i);
  // Touch 'view-a' so it becomes most recently used.
  assert.equal(getBrowseScrollIndex('view-a'), 140);
  setBrowseScrollIndex('k-63', 63);
  assert.equal(getBrowseScrollIndex('view-a'), 140, 'recently accessed key survives eviction');
  assert.equal(getBrowseScrollIndex('k-0'), undefined, 'least recently used key is evicted at cap');
  clearBrowseScrollPositions();
});

test('BrowseResults caches Intl.DateTimeFormat, memoizes grid renderers, and passes initialIndex', () => {
  const source = readFileSync(new URL('../src/screens/browse/BrowseResults.tsx', import.meta.url), 'utf8');
  assert.match(source, /const addedDateFormatters = new Map<string, Intl\.DateTimeFormat>\(\);/);
  assert.match(source, /const renderPlaceholder = useCallback\(/);
  assert.match(source, /const renderItem = useCallback\(/);
  assert.match(source, /initialIndex=\{initialIndex\}/);
});
