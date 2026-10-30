import test from 'node:test';
import assert from 'node:assert/strict';
import {fixedPageWindow} from '../../src/screens/read/fixed-page-window.ts';
import type {FixedLayoutInput} from '@portico/reader-core';

const base: FixedLayoutInput = {format: 'cbz', viewport: {width: 1280, height: 800}, page: 2, pageCount: 13,
  twoPages: true, firstPageAlone: true, direction: 'ltr', fit: 'auto'};

test('the comic footer keeps the cover single and uses the renderer pair containing either selected page', () => {
  for (const page of [1, 2]) {
    const window = fixedPageWindow({...base, page});
    assert.deepEqual(window.groups.map(group => group.pages), [[0], [1, 2], [3, 4], [5, 6], [7, 8]]);
    assert.deepEqual(window.groups.filter(group => group.current).map(group => group.pages), [[1, 2]]);
  }
  assert.deepEqual(fixedPageWindow({...base, page: 0}).current.pages, [0]);
  assert.deepEqual(fixedPageWindow({...base, page: 12}).current.pages, [11, 12]);
});

test('RTL preserves logical advancement while reversing the paired page order for rendering', () => {
  for (const format of ['cbz', 'cbr'] as const) {
    const window = fixedPageWindow({...base, format, direction: 'rtl'});
    assert.deepEqual(window.current.visualPages, [2, 1]);
    assert.deepEqual(window.groups[1].pages, [1, 2]);
    assert.equal(window.current.nextPage, 3);
  }
});

test('upright, continuous and disabled two-page modes keep single thumbnails; landscape manga and book folds retain spreads', () => {
  for (const input of [{...base, viewport: {width: 820, height: 1180}}, {...base, direction: 'vertical' as const}, {...base, twoPages: false}]) {
    assert.ok(fixedPageWindow(input).groups.every(group => group.pages.length === 1));
  }
  assert.deepEqual(fixedPageWindow({...base, viewport: {width: 844, height: 390}, direction: 'rtl'}).current.pages, [1, 2]);
  assert.deepEqual(fixedPageWindow({...base, viewport: {width: 840, height: 1000, crease: {left: 410, right: 430}}}).current.pages, [1, 2]);
  assert.deepEqual(fixedPageWindow({...base, firstPageAlone: false}).current.pages, [2, 3]);
});

test('a large document mounts a bounded window and an odd final page never invents a companion', () => {
  const spread = fixedPageWindow({...base, page: 9000, pageCount: 10000});
  assert.equal(spread.groups.length, 5);
  assert.equal(spread.groups.flatMap(group => group.pages).length, 10);
  const single = fixedPageWindow({...base, page: 9000, pageCount: 10000, twoPages: false});
  assert.equal(single.groups.length, 11);
  assert.deepEqual(fixedPageWindow({...base, page: 11, pageCount: 12}).current.pages, [11]);
  assert.deepEqual(fixedPageWindow({...base, page: 0, pageCount: 1}).groups.map(group => group.pages), [[0]]);
});
