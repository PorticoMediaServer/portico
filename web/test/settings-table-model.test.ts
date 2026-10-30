import test from 'node:test';
import assert from 'node:assert/strict';
import {nextTableSort, selectTableRows, settingsTableWindow, tableFocusFallback} from '../src/kit/settings/table-model.ts';

test('100,000 rows have bounded render work at the beginning, middle and end', () => {
  for (const top of [0, 3000000, 6000000]) {
    const window = settingsTableWindow(100000, top, 560);
    assert.ok(window.last - window.first < 17);
    assert.equal(window.before + (window.last - window.first + 1) * 60 + window.after, 6000000);
    assert.ok(window.first >= 0 && window.last < 100000);
  }
  assert.deepEqual(settingsTableWindow(0, 50, 560), {first: 0, last: -1, before: 0, after: 0});
});
test('sorting changes the server query and selection uses entity IDs across page windows', () => {
  assert.deepEqual(nextTableSort({by: 'name', direction: 'asc'}, 'name'), {by: 'name', direction: 'desc'});
  assert.deepEqual(nextTableSort({by: 'name', direction: 'desc'}, 'active'), {by: 'active', direction: 'asc'});
  const selected = selectTableRows(new Set(['person-previous-page']), ['person-100', 'person-101'], true);
  assert.deepEqual([...selected], ['person-previous-page', 'person-100', 'person-101']);
  assert.deepEqual([...selectTableRows(selected, ['person-100'], false)], ['person-previous-page', 'person-101']);
});
test('removed focused row chooses the next surviving neighbour, previous at the end, search when empty', () => {
  assert.equal(tableFocusFallback(['a', 'b', 'c', 'd'], ['a', 'd'], 'b'), 'd');
  assert.equal(tableFocusFallback(['a', 'b', 'c'], ['a', 'b'], 'c'), 'b');
  assert.equal(tableFocusFallback(['a'], [], 'a'), undefined);
});
