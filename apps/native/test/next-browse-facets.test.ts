import test from 'node:test';
import assert from 'node:assert/strict';
import {nextFacetState, changeFacetSelection} from '../src-next/kit/browse/facet-selection.ts';

test('phone facet taps include, exclude, then clear without losing other selections', () => {
  const originalIncluded = Object.freeze(['Family']);
  const originalExcluded = Object.freeze(['Horror']);
  let included: readonly string[] = originalIncluded, excluded: readonly string[] = originalExcluded;
  for (const expected of ['include', 'exclude', 'clear'] as const) {
    const state = nextFacetState('Animation', included, excluded, true);
    assert.equal(state, expected);
    ({included, excluded} = changeFacetSelection('Animation', state, included, excluded));
    assert.equal(included.includes('Animation'), expected === 'include');
    assert.equal(excluded.includes('Animation'), expected === 'exclude');
    assert.ok(included.includes('Family'));
    assert.ok(excluded.includes('Horror'));
  }
  assert.deepEqual(originalIncluded, ['Family']);
  assert.deepEqual(originalExcluded, ['Horror']);
});

test('TV and desktop normal facet toggles do not silently become exclusions', () => {
  const first = nextFacetState('1980', [], [], false);
  assert.equal(first, 'include');
  const selected = changeFacetSelection('1980', first, [], []);
  assert.equal(nextFacetState('1980', selected.included, selected.excluded, false), 'clear');
});

test('changing polarity removes a value from its previous set and repeated selection stays unique', () => {
  const first = changeFacetSelection('Drama', 'include', ['Drama', 'Comedy'], ['Drama', 'Horror']);
  assert.deepEqual(first, {included: ['Comedy', 'Drama'], excluded: ['Horror']});
  const excluded = changeFacetSelection('Drama', 'exclude', first.included, first.excluded);
  assert.deepEqual(excluded, {included: ['Comedy'], excluded: ['Horror', 'Drama']});
});
