import test from 'node:test';
import assert from 'node:assert/strict';
import {bulkAccessWithLibraries} from '../src/screens/settings/people-bulk-access.ts';

test('bulk libraries defaults to leaving each target unchanged', () => {
  assert.deepEqual(bulkAccessWithLibraries(undefined, 'keep', []), {});
  assert.deepEqual(bulkAccessWithLibraries({allowRemote: false}, 'keep', ['films']), {allowRemote: false});
});

test('All is an explicit empty allowedLibraries grant and Some sends its selected IDs', () => {
  assert.deepEqual(bulkAccessWithLibraries({maxContentRating: 'PG'}, 'all', ['films']), {maxContentRating: 'PG', allowedLibraries: []});
  assert.deepEqual(bulkAccessWithLibraries({allowRemote: false}, 'some', ['films', 'shows']), {allowRemote: false, allowedLibraries: ['films', 'shows']});
});

test('an incomplete custom selection cannot accidentally grant every library', () => {
  assert.throws(() => bulkAccessWithLibraries({allowRemote: true}, 'some', []), /bulk_library_required/);
});

test('returning to Don’t change omits a prior library patch without mutating it', () => {
  const access = {allowedLibraries: ['films'], allowRemote: false};
  assert.deepEqual(bulkAccessWithLibraries(access, 'keep', []), {allowRemote: false});
  assert.deepEqual(access, {allowedLibraries: ['films'], allowRemote: false});
  const selected = ['films', 'films'];
  const result = bulkAccessWithLibraries(undefined, 'some', selected);
  selected.push('shows');
  assert.deepEqual(result.allowedLibraries, ['films']);
});
