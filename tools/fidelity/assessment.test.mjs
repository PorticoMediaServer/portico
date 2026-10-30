import test from 'node:test';
import assert from 'node:assert/strict';
import {assessDifferences} from './assessment.mjs';

const entries = (kind, count) => Array.from({length: count}, () => ({kind}));
test('native coverage gaps stay separate from measured, missing and ambiguous findings', () => {
  const differences = [...entries('unavailable-measurement', 149), ...entries('measured-difference', 15), ...entries('missing-element', 3), {kind: 'ambiguous-match'}];
  const raw = JSON.stringify(differences);
  const result = assessDifferences(differences, {actual: {unavailable: entries('unavailable', 90)}});
  assert.equal(result.coverage.unavailableMeasurements, 149);
  assert.equal(result.coverage.declaredUnavailable.actual, 90);
  assert.deepEqual(result.findings, {measured: 15, missing: 3, extra: 0, ambiguous: 1, cardinality: 0, missingContentSlots: 0});
  assert.equal(result.productFindingsReady, false); assert.equal(result.measuredVerdict, 'WITHHELD');
  assert.equal(JSON.stringify(differences), raw);
  const observed = differences.filter(value => value.kind !== 'unavailable-measurement');
  assert.equal(assessDifferences(observed).measuredVerdict, 'WITHHELD');
  assert.equal(assessDifferences(observed.filter(value => value.kind !== 'ambiguous-match')).measuredVerdict, 'FAIL');
});
test('invalid or wrong-state input cannot look complete before comparison starts', () => {
  const invalid = assessDifferences(entries('invalid-measurement', 2), {actual: {unavailable: entries('unavailable', 120)}});
  assert.equal(invalid.coverage.status, 'INCOMPLETE'); assert.equal(invalid.coverage.invalidMeasurements, 2);
  assert.equal(invalid.coverage.declaredUnavailable.actual, 120); assert.equal(invalid.measuredVerdict, 'WITHHELD');
  for (const kind of ['screen-state', 'calibration-failure', 'anchor-cardinality', 'list-cardinality', 'missing-content-slot', 'invalid-adapter'])
    assert.equal(assessDifferences([{kind}]).measuredVerdict, 'WITHHELD');
  assert.deepEqual(assessDifferences([]).coverage.declaredUnavailable, {reference: 0, actual: 0});
  assert.equal(assessDifferences([]).measuredVerdict, 'PASS');
});

test('an absent observed font or paint value is coverage, not a measured product mismatch', () => {
  const result = assessDifferences([{kind: 'measured-difference', property: 'fontSize', expected: '24px'},
    {kind: 'measured-difference', property: 'color', actual: 'rgb(1, 2, 3)'},
    {kind: 'measured-difference', property: 'fontWeight', expected: '700', actual: '600'}]);
  assert.equal(result.coverage.missingObservedProperties, 2);
  assert.equal(result.findings.measured, 1); assert.equal(result.measuredVerdict, 'WITHHELD');
});

test('unknown image decode is coverage while a known undecoded image remains a measured failure', () => {
  const unknown = assessDifferences([{kind: 'measured-difference', property: 'image-slot.loaded', expected: true}]);
  assert.equal(unknown.coverage.missingObservedProperties, 1); assert.equal(unknown.findings.measured, 0);
  assert.equal(unknown.measuredVerdict, 'WITHHELD');
  const failed = assessDifferences([{kind: 'measured-difference', property: 'image-slot.loaded', expected: true, actual: false}]);
  assert.equal(failed.findings.measured, 1); assert.equal(failed.measuredVerdict, 'FAIL');
});
