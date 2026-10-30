import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';

// Runtime pass (O4): a music library's tabs read "Discover, Artists, Browse, Browse" because the
// albums and songs views fell through to the "Browse" fallback. Every view has its own name.
test('each library view has its own tab name', () => {
  const t = (view: string) => defaultI18n.t('lib.tab', {view});
  assert.deepEqual(['discover', 'collections', 'categories', 'releases', 'songs', 'authors', 'series'].map(t), ['Discover', 'Collections', 'Categories', 'Albums', 'Songs', 'Authors', 'Series']);
  assert.equal(t('something-new'), 'Browse');
});
