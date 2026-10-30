import test from 'node:test';
import assert from 'node:assert/strict';
import type {BrowseSortCapability} from '../../../../packages/client-core/src/browse.ts';
import {nextBrowseSort, nextFacetSelection, trackWindow} from '../../src/kit/browse/model.ts';

test('phone facet activation traverses inclusion, exclusion, and clear without losing exclusion', () => {
  assert.equal(nextFacetSelection(false, false, true), 'include');
  assert.equal(nextFacetSelection(true, false, true), 'exclude');
  assert.equal(nextFacetSelection(false, true, true), 'clear');
  assert.equal(nextFacetSelection(true, false, false), 'clear');
  assert.equal(nextFacetSelection(false, true, false), 'clear');
});

const sort = (id: string, directions: readonly ('asc' | 'desc')[], defaultDirection: 'asc' | 'desc', seeded = false): BrowseSortCapability => ({id, labelKey: id, directions, defaultDirection, seeded, expensive: false, applicableKinds: []});
const sorts = [sort('title', ['asc', 'desc'], 'asc'), sort('year', ['desc', 'asc'], 'desc'), sort('forYou', ['desc'], 'desc'), sort('random', ['asc'], 'asc', true)];

test('sort activation honors capability defaults, allowed flips, and random reselection', () => {
  assert.deepEqual(nextBrowseSort(sorts, {field: 'title', direction: 'asc'}, 'year'), {field: 'year', direction: 'desc'});
  assert.deepEqual(nextBrowseSort(sorts, {field: 'year', direction: 'desc'}, 'year'), {field: 'year', direction: 'asc'});
  assert.deepEqual(nextBrowseSort(sorts, {field: 'forYou', direction: 'desc'}, 'forYou'), {field: 'forYou', direction: 'desc'});
  assert.equal(nextBrowseSort(sorts, {field: 'random', direction: 'asc'}, 'random'), undefined);
  assert.equal(nextBrowseSort(sorts, {field: 'title', direction: 'asc'}, 'unsupported'), undefined);
});

test('a 50,000-song viewport stays bounded and clamps at each end', () => {
  assert.deepEqual(trackWindow(0, 560, 56, 50_000), {start: 0, end: 14});
  assert.deepEqual(trackWindow(28_000, 560, 56, 50_000), {start: 496, end: 514});
  assert.deepEqual(trackWindow(2_799_944, 560, 56, 50_000), {start: 49_995, end: 50_000});
  assert.deepEqual(trackWindow(-20, 560, 56, 0), {start: 0, end: 0});
  assert.throws(() => trackWindow(0, 560, 0, 50_000), /Invalid track viewport/);
  assert.throws(() => trackWindow(NaN, 560, 56, 50_000), /Invalid track viewport/);
});
