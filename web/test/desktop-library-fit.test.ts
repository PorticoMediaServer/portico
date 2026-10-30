import test from 'node:test';
import assert from 'node:assert/strict';
import {desktopLibraryCount} from '../src/shell/desktop-library-fit.ts';

test('the 1024 multi-library budget reserves Home, Live TV, More and gaps before showing whole labels', () => {
  const widths = {available: 539, home: 36, live: 43, more: 55, gap: 16,
    libraries: [44, 59, 39, 37, 73, 79, 79, 79, 79, 79, 79, 79, 79, 79, 79]};
  assert.equal(desktopLibraryCount(widths), 5);
  assert.equal(desktopLibraryCount({...widths, available: 460}), 4);
  assert.equal(desktopLibraryCount({...widths, available: 800}), 6);
});
test('short titles that all fit need no More reservation, including an exact fit', () => {
  assert.equal(desktopLibraryCount({available: 134, home: 36, live: undefined, more: 55, gap: 16, libraries: [20, 10, 20]}), 3);
  assert.equal(desktopLibraryCount({available: 36, home: 36, live: undefined, more: 55, gap: 16, libraries: []}), 0);
});
test('an overlong first library moves to More intact instead of truncating every fixed destination', () => {
  assert.equal(desktopLibraryCount({available: 539, home: 36, live: 43, more: 55, gap: 16, libraries: [900, 20, 20]}), 0);
});
