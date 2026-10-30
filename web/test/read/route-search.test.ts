import assert from 'node:assert/strict';
import {test} from 'node:test';
import {readSearch, retainedReadTarget} from '../../src/screens/read/route-search.ts';

test('the Read search accepts only the actual opaque source identity and never a source URL', () => {
  assert.deepEqual(readSearch({sourceId: 'actual_source-1', url: 'https://untrusted.invalid/book.epub'}), {sourceId: 'actual_source-1'});
  assert.deepEqual(readSearch({sourceId: 'https://untrusted.invalid/book.epub'}), {});
  assert.deepEqual(readSearch({sourceId: ['unit-source']}), {});
});
test('Reading Settings and return keep the same target, while leaving or changing book retires it', () => {
  const book = retainedReadTarget(undefined, {itemId: 'actual-item', sourceId: 'actual-source'}, false)!;
  assert.strictEqual(retainedReadTarget(book, undefined, true), book);
  assert.strictEqual(retainedReadTarget(book, {...book}, false), book);
  assert.equal(retainedReadTarget(book, undefined, false), undefined);
  const replacement = retainedReadTarget(book, {...book, sourceId: 'actual-other-version'}, false);
  assert.notStrictEqual(replacement, book); assert.equal(replacement?.sourceId, 'actual-other-version');
});
