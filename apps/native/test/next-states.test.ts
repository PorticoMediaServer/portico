import test from 'node:test';
import assert from 'node:assert/strict';
import {gridWindow, isSlowLoading, ownerDetails, retryProgress, ringProgress, showSkeleton} from '../src-next/kit/states/model.ts';
import {stateSentenceParts} from '../src-next/kit/states/sentence-parts.ts';

test('unknown and invalid progress remains empty and truthful progress is bounded', () => {
  for (const value of [null, undefined, NaN, Infinity, -Infinity]) assert.equal(ringProgress(value), null);
  assert.equal(ringProgress(-.4), 0);
  assert.equal(ringProgress(1.8), 1);
  assert.equal(retryProgress(8, 15), 8 / 15);
  assert.equal(retryProgress(0, 15), 0);
  for (const interval of [undefined, null, 0, -1, NaN, Infinity]) assert.equal(retryProgress(8, interval), null);
  assert.equal(retryProgress(NaN, 15), null);
});

test('the supplied elapsed clock cannot flash skeletons on a fast response', () => {
  for (const elapsed of [-1, 0, 299, NaN, Infinity]) assert.equal(showSkeleton(elapsed), false);
  assert.equal(showSkeleton(300), true);
  assert.equal(showSkeleton(8000), true);
  for (const elapsed of [0, 7999, NaN, Infinity]) assert.equal(isSlowLoading(elapsed), false);
  assert.equal(isSlowLoading(8000), true);
});

test('a 50,000-title window allocates only visible rows and preserves absolute item indices', () => {
  const first = gridWindow({columns: 7, rowHeight: 414, viewportHeight: 818, scrollOffset: 0, totalCount: 50000});
  assert.equal(first.first, 0);
  assert.equal(first.count, 21);
  const distant = gridWindow({columns: 7, rowHeight: 414, viewportHeight: 818, scrollOffset: 414 * 1000 + 10, totalCount: 50000});
  assert.equal(distant.first, 6993);
  assert.equal(distant.count, 28);
  assert.equal(distant.height, Math.ceil(50000 / 7) * 414);
  assert.equal(distant.top, 999 * 414);
});

test('partial final rows, empty responses and unknown counts remain bounded', () => {
  assert.equal(gridWindow({columns: 3, rowHeight: 220, viewportHeight: 600, scrollOffset: 0, totalCount: 8}).count, 8);
  assert.equal(gridWindow({columns: 3, rowHeight: 220, viewportHeight: 600, scrollOffset: 0, totalCount: 0}).count, 0);
  assert.equal(gridWindow({columns: 3, rowHeight: 220, viewportHeight: 600, scrollOffset: 0}).count, 12);
  assert.equal(gridWindow({columns: 0, rowHeight: 220, viewportHeight: 600, scrollOffset: 0}).count, 0);
  assert.equal(gridWindow({columns: 3, rowHeight: 0, viewportHeight: 600, scrollOffset: 0}).count, 0);
  assert.equal(gridWindow({columns: 3, rowHeight: 220, viewportHeight: Infinity, scrollOffset: 0}).count, 0);
  assert.equal(gridWindow({columns: 3, rowHeight: 220, viewportHeight: 600, scrollOffset: 99999, totalCount: 8}).count, 0);
  assert.equal(gridWindow({columns: 3, rowHeight: 220, viewportHeight: 600, scrollOffset: 0, totalCount: 50000, overscanRows: 50000}).count, 15);
});

test('a viewer never receives owner diagnostics even when an untyped caller supplies them', () => {
  const diagnostic = {codes: ['lease_expired'], description: 'Owner diagnostic'};
  assert.equal(ownerDetails({audience: 'viewer', owner: diagnostic}), undefined);
  assert.equal(ownerDetails({audience: 'owner', owner: diagnostic}), diagnostic);
  assert.equal(ownerDetails({audience: 'owner'}), undefined);
});

test('catalogue argument boundaries keep emphasis separate even when values repeat sentence text', () => {
  const parts = stateSentenceParts('The title {title} is kept at {position}.', {title: 'The title', position: '12:04'}, ['title']);
  assert.deepEqual(parts, [{text: 'The title ', strong: false}, {text: 'The title', strong: true}, {text: ' is kept at ', strong: false}, {text: '12:04', strong: false}, {text: '.', strong: false}]);
  assert.equal(stateSentenceParts('{title}', {title: '<b>data</b>'}, ['title'])[0]!.text, '<b>data</b>');
  assert.throws(() => stateSentenceParts('{missing}', {}, []), /named text arguments/);
  assert.throws(() => stateSentenceParts('{count, number}', {count: '3'}, []), /named text arguments/);
});
