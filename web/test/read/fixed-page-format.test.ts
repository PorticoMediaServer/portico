import assert from 'node:assert/strict';
import test from 'node:test';
import {pageFormat} from '../../src/screens/read/fixed-page.tsx';

// The page strip and Pages panel move to a page in the book's own format: the controller refuses
// a location whose format isn't the book's, so a CBR or CB7 page labelled CBZ never moved (RD-10).
test('a page jump carries the book\'s own format', () => {
  for (const format of ['pdf', 'cbz', 'cbr', 'cb7'] as const) assert.equal(pageFormat(format), format);
});
