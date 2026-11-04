import assert from 'node:assert/strict';
import test from 'node:test';
import {surfaceSortLabels} from '../src/kit/browse/model.ts';

const sort = (id: string, labelKey: string) => ({id, labelKey, directions: ['asc', 'desc'] as const, defaultDirection: 'asc' as const, expensive: false});
const labels = {field: () => 'field', quick: () => 'quick', sort: (value: {labelKey: string}) => value.labelKey === 'title' ? 'Title' : value.labelKey === 'added' ? 'Date added' : 'Group order'};

test('See all names only the row\'s own order "Best match"; a chosen Title still reads Title (CDATA-A2)', () => {
  const named = surfaceSortLabels(labels, 'Best match');
  assert.equal(named.sort(sort('groupOrder', 'group')), 'Best match');
  assert.equal(named.sort(sort('title', 'title')), 'Title');
  assert.equal(named.sort(sort('added', 'added')), 'Date added');
  assert.equal(surfaceSortLabels(labels, undefined), labels);
});
