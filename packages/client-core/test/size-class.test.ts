import {strict as assert} from 'node:assert';
import {test} from 'node:test';
import {sizeClassForWidth, type SizeClass} from '../src/index.ts';

test('current logical width has exact shared size boundaries', () => {
  const cases: [number, SizeClass][] = [
    [0, 'compact'], [390, 'compact'], [599, 'compact'], [599.99, 'compact'],
    [600, 'medium'], [1023, 'medium'], [1023.99, 'medium'],
    [1024, 'expanded'], [1440, 'expanded'], [Number.MAX_VALUE, 'expanded'],
  ];
  for (const [width, expected] of cases) assert.equal(sizeClassForWidth(width), expected, String(width));
});

test('unmeasured and invalid startup widths use compact', () => {
  for (const width of [-1, -600, NaN, Infinity, -Infinity]) assert.equal(sizeClassForWidth(width), 'compact');
});

test('folding and resizing classify each measurement without sticky device state', () => {
  assert.deepEqual([599, 600, 1023, 1024, 1023, 600, 599].map(sizeClassForWidth),
    ['compact', 'medium', 'medium', 'expanded', 'medium', 'medium', 'compact']);
});
