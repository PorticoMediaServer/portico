import test from 'node:test';
import assert from 'node:assert/strict';
import {fixedPageClearance} from '../src/kit/reader/page-clearance.ts';

// desktop-read 10 at 1440x900: 72px top bar, strip above an 88px footer.
const bar = {offsetTop: 0, offsetHeight: 72};
const footer = {offsetTop: 812, offsetHeight: 88};
const strip = {offsetTop: 694, offsetHeight: 118};

test('a page keeps clear of the top bar and the footer', () => {
  assert.deepEqual(fixedPageClearance(900, [bar], [footer]), {top: 72, bottom: 88});
});

test('the page strip raises the bottom clearance above the footer', () => {
  assert.deepEqual(fixedPageClearance(900, [bar], [strip, footer]), {top: 72, bottom: 206});
});

test('the room left for the page is the window less both clearances', () => {
  const {top, bottom} = fixedPageClearance(900, [bar], [strip, footer]);
  assert.equal(900 - top - bottom, 622);
});

test('layers that are not laid out reserve nothing', () => {
  assert.deepEqual(fixedPageClearance(900, [{offsetTop: 0, offsetHeight: 0}], [{offsetTop: 900, offsetHeight: 0}]), {top: 0, bottom: 0});
  assert.deepEqual(fixedPageClearance(900, [], []), {top: 0, bottom: 0});
});

test('a clearance never exceeds the window or goes negative', () => {
  assert.deepEqual(fixedPageClearance(100, [{offsetTop: 0, offsetHeight: 500}], [{offsetTop: -50, offsetHeight: 10}]), {top: 100, bottom: 100});
  assert.deepEqual(fixedPageClearance(100, [{offsetTop: Number.NaN, offsetHeight: 10}], [{offsetTop: 400, offsetHeight: 10}]), {top: 0, bottom: 0});
});
