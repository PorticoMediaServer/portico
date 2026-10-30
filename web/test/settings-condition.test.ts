import test from 'node:test';
import assert from 'node:assert/strict';
import {settingsConditionMet} from '../src/kit/settings/condition.ts';

test('a parent deny-list applies with an allow-list rather than being bypassed by it', () => {
  const rule = {id: 'parent', in: ['automatic', 'manual'], notIn: ['manual']};
  assert.equal(settingsConditionMet(rule, 'automatic'), true);
  assert.equal(settingsConditionMet(rule, 'manual'), false);
  assert.equal(settingsConditionMet(rule, 'unknown'), false);
});
test('an absent real parent cannot satisfy allowed values or a coerced numeric/boolean alias', () => {
  assert.equal(settingsConditionMet({id: 'parent', in: [true]}, undefined), false);
  assert.equal(settingsConditionMet({id: 'parent', in: [true]}, 'true'), false);
  assert.equal(settingsConditionMet({id: 'parent', in: [1]}, '1'), false);
  assert.equal(settingsConditionMet({id: 'parent', in: [1]}, 1), true);
});
test('negative-only and unconstrained rows retain their actual condition semantics', () => {
  assert.equal(settingsConditionMet({id: 'parent', notIn: ['off']}, 'off'), false);
  assert.equal(settingsConditionMet({id: 'parent', notIn: ['off']}, 'on'), true);
  assert.equal(settingsConditionMet(undefined, undefined), true);
});
