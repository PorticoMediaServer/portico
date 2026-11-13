import assert from 'node:assert/strict';
import {test} from 'node:test';
import {authorPredicate} from '../../../packages/client-core/src/presentation/library-tabs.ts';

// A Read library's Authors are the shared browse grid (its people list is client-core's, tested
// there); an author's card opens the books grid filtered by their person id.
test('an author value filters browse by the author\'s person id', () => {
  assert.deepEqual(authorPredicate('per_1'), {variant: 'predicate', field: 'author', operator: 'equals', value: 'per_1'});
  assert.throws(() => authorPredicate(''), /invalid_request/);
});
