import test from 'node:test';
import assert from 'node:assert/strict';
import {compareMeasurements} from './compare-files.mjs';
import {validateMeasurement, sameLogicalExtent} from './contract.mjs';

const fixture = () => ({version: 1, scope: 'screen', fullViewport: true,
  root: {x: 0, y: 0, width: 390, height: 844}, viewport: {width: 390, height: 844, deviceScaleFactor: 1}, anchorIssues: [], repeatedLists: [], strings: [],
  elements: ['title', 'caption'].map((frame, index) => ({id: String(index), parent: null, frame, tag: 'Text', role: 'text', leaf: true, ownText: frame, normalizedText: frame,
    box: {x: 16, y: 20 + index * 40, width: 200, height: 30}, textRects: [{x: 16, y: 20 + index * 40, width: 100, height: 25}], focused: index === 0,
    effectiveOpacity: 1, effectiveBackgroundColor: 'rgb(7, 16, 24)', style: {fontFamily: 'Example', resolvedFontFamily: 'Example', fontSize: '24px', fontWeight: '700', color: 'rgb(237, 245, 250)'}}))});

test('native font identity uses observed active families, never a forged CSS fallback chain', () => {
  const reference = fixture(), actual = fixture();
  actual.measurement = {renderer: 'react-native', platform: 'android', coordinateUnit: 'dp'};
  reference.elements[0].style.fontFamily = 'Requested, serif'; delete actual.elements[0].style.fontFamily;
  assert.equal(compareMeasurements(reference, actual).verdict, 'PASS');
  actual.elements[0].style.resolvedFontFamily = 'Different observed face';
  actual.elements[0].style.fontFamily = reference.elements[0].style.fontFamily;
  assert.ok(compareMeasurements(reference, actual).differences.some(value => value.property === 'resolvedFontFamily' && value.kind === 'measured-difference'));
  delete actual.elements[0].style.resolvedFontFamily;
  assert.ok(validateMeasurement(actual).includes('elements.0.style.resolvedFontFamily'));
  actual.unavailable = [{element: '0', property: 'resolvedFontFamily', reason: 'Native active family is not exposed'}];
  const result = compareMeasurements(reference, actual);
  assert.equal(result.assessment.measuredVerdict, 'WITHHELD');
  assert.ok(result.differences.some(value => value.kind === 'unavailable-measurement' && value.property === 'resolvedFontFamily'));
});

test('native viewport consistency accepts exact float32 representation without changing raw bounds or geometry policy', () => {
  const reference = fixture(), actual = fixture();
  for (const screen of [reference, actual]) {
    screen.root.width = screen.viewport.width = 411.42857142857144;
    screen.root.height = screen.viewport.height = 914.2857142857143;
  }
  actual.measurement = {renderer: 'react-native', platform: 'android', coordinateUnit: 'dp'};
  actual.viewport.deviceScaleFactor = 2.625;
  actual.root.width = Math.fround(actual.viewport.width); actual.root.height = Math.fround(actual.viewport.height);
  const raw = JSON.stringify(actual);
  assert.deepEqual(validateMeasurement(actual), []);
  assert.equal(compareMeasurements(reference, actual).verdict, 'PASS');
  assert.equal(JSON.stringify(actual), raw);
  actual.fullViewport = false;
  assert.deepEqual(validateMeasurement(actual), ['fullViewport']);
  actual.fullViewport = true;
  assert.ok(compareMeasurements(fixture(), actual).differences.some(value => value.kind === 'screen-state'));
  actual.elements[0].box.x += 2.01;
  assert.ok(compareMeasurements(reference, actual).differences.some(value => value.property === 'box.x'));
  actual.root.width += .001;
  assert.ok(validateMeasurement(actual).includes('root.viewport'));
  assert.equal(sameLogicalExtent(411.42856, 411.42857, actual.measurement), false);
  assert.equal(sameLogicalExtent(411.42857142857144, Math.fround(411.42857142857144), {renderer: 'dom', coordinateUnit: 'css-px'}), false);
});

test('one shared contract compares native logical geometry without falsifying physical pixel ratio', () => {
  const reference = fixture(), actual = fixture(); actual.measurement = {renderer: 'react-native', platform: 'android', coordinateUnit: 'dp'}; actual.viewport.deviceScaleFactor = 3;
  assert.deepEqual(validateMeasurement(actual), []);
  const result = compareMeasurements(reference, actual, {focus: {frame: 'title'}});
  assert.equal(result.verdict, 'PASS'); assert.deepEqual(result.calibration.counts, {self: 0, oneFont: 1, oneOpacity: 1});
  delete actual.measurement;
  assert.ok(compareMeasurements(reference, actual).differences.some(value => value.kind === 'screen-state'));
});
test('actual unique focused target is scored only for an explicitly drawn focus state', () => {
  const reference = fixture(), actual = fixture(); actual.elements[0].focused = false;
  assert.equal(compareMeasurements(reference, actual).verdict, 'PASS');
  assert.equal(compareMeasurements(reference, actual, {focus: {frame: 'title'}}).differences.filter(value => value.kind === 'focus-state').length, 1);
  actual.elements.forEach(node => node.focused = true);
  assert.equal(compareMeasurements(reference, actual, {focus: {frame: 'title'}}).differences.filter(value => value.kind === 'focus-state').length, 1);
});
test('native unavailable text geometry stays explicit and prevents a zero-difference pass', () => {
  const reference = fixture(), actual = fixture(); actual.measurement = {renderer: 'react-native', platform: 'tvos', coordinateUnit: 'pt'};
  delete actual.elements[1].textRects;
  assert.ok(validateMeasurement(actual).includes('elements.1.textRects'));
  actual.unavailable = [{element: '1', property: 'textRects', reason: 'Actual native line callback unavailable'}];
  assert.deepEqual(validateMeasurement(actual), []);
  assert.equal(compareMeasurements(reference, actual).differences.filter(value => value.kind === 'unavailable-measurement').length, 1);
});
test('invalid measured root or duplicate identities fails before comparison', () => {
  const reference = fixture(), actual = fixture(); actual.root.x = 4; actual.elements[1].id = '0';
  const result = compareMeasurements(reference, actual);
  assert.equal(result.verdict, 'FAIL'); assert.ok(result.differences.some(value => value.label === 'actual.root.viewport'));
  assert.ok(result.differences.some(value => value.label === 'actual.elements.1.id'));
});
test('curated flattened styles are retained as unscored diagnostics and never replace measured facts', () => {
  const reference = fixture(), actual = fixture();
  actual.elements[0].diagnostics = {flattenedStyle: {fontSize: 99, opacity: .1, backgroundColor: 'red'}};
  assert.deepEqual(validateMeasurement(actual), []);
  const report = compareMeasurements(reference, actual);
  assert.equal(report.verdict, 'PASS');
  assert.ok(report.comparison.diagnostics.some(value => value.property === 'diagnostics' && value.scored === false));
  actual.elements[0].style.fontSize = '25px';
  assert.equal(compareMeasurements(reference, actual).differences.filter(value => value.property === 'fontSize').length, 1);
  actual.elements[0].diagnostics.flattenedStyle.source = 'private-prop';
  assert.ok(validateMeasurement(actual).includes('elements.0.diagnostics.flattenedStyle'));
  delete actual.elements[0].diagnostics.flattenedStyle.source;
  actual.elements[0].diagnostics.flattenedStyle.backgroundColor = 'url(data:private)';
  assert.ok(validateMeasurement(actual).includes('elements.0.diagnostics.flattenedStyle'));
});
test('unobserved native focus and opacity remain unavailable rather than becoming false or one', () => {
  const reference = fixture(), actual = fixture(); actual.measurement = {renderer: 'react-native', platform: 'android', coordinateUnit: 'dp'};
  actual.elements[0].focused = null; actual.elements[0].effectiveOpacity = null;
  actual.unavailable = ['focused', 'effectiveOpacity'].map(property => ({element: '0', property, reason: 'Native observation not available'}));
  assert.deepEqual(validateMeasurement(actual), []);
  const result = compareMeasurements(reference, actual, {focus: {frame: 'title'}});
  assert.equal(result.verdict, 'FAIL'); assert.equal(result.differenceCount, 2);
  assert.ok(result.differences.every(value => value.kind === 'unavailable-measurement'));
});
test('actual DOM DPR2/3 preserves density while comparing measured CSS coordinates unchanged', () => {
  for (const density of [2, 3]) {
    const reference = fixture(), actual = fixture();
    actual.measurement = {renderer: 'dom', coordinateUnit: 'css-px'}; actual.viewport.deviceScaleFactor = density;
    assert.deepEqual(validateMeasurement(actual), []);
    const result = compareMeasurements(reference, actual);
    assert.equal(result.verdict, 'PASS'); assert.equal(actual.viewport.deviceScaleFactor, density);
    assert.deepEqual(result.calibration.counts, {self: 0, oneFont: 1, oneOpacity: 1});
    actual.elements[0].box.x += 3;
    assert.ok(compareMeasurements(reference, actual).differences.some(value => value.property === 'box.x'));
  }
});
test('physical bounds and incorrect coordinate-unit declarations cannot masquerade as logical matches', () => {
  const reference = fixture(), actual = fixture(); actual.measurement = {renderer: 'dom', coordinateUnit: 'css-px'}; actual.viewport.deviceScaleFactor = 2;
  actual.root.width *= 2; actual.viewport.width *= 2;
  assert.ok(compareMeasurements(reference, actual).differences.some(value => value.kind === 'screen-state'));
  actual.measurement.coordinateUnit = 'dp';
  assert.ok(validateMeasurement(actual).includes('measurement.coordinateMapping'));
  actual.measurement = {renderer: 'react-native', platform: 'android', coordinateUnit: 'pt'};
  assert.ok(validateMeasurement(actual).includes('measurement.coordinateMapping'));
});
test('unavailable native paint is one measurement failure rather than an invented material difference', () => {
  const reference = fixture(), actual = fixture(); reference.elements[0].style.backdropFilter = 'blur(20px)';
  actual.unavailable = [{element: '0', property: 'backdropFilter', reason: 'Native material observation unavailable'}];
  const report = compareMeasurements(reference, actual);
  assert.equal(report.differenceCount, 1); assert.equal(report.differences[0].kind, 'unavailable-measurement');
});
test('declared unavailable native text is not replaced with empty strings or a copy defect', () => {
  const reference = fixture(), actual = fixture(); actual.elements[0].ownText = null; actual.elements[0].normalizedText = null;
  actual.unavailable = ['ownText', 'normalizedText'].map(property => ({element: '0', property, reason: 'Visible native text has not been observed'}));
  assert.deepEqual(validateMeasurement(actual), []);
  const result = compareMeasurements(reference, actual);
  assert.equal(result.differenceCount, 2); assert.ok(result.differences.every(value => value.kind === 'unavailable-measurement'));
});
test('actual font scale is unscored metadata and cannot cancel a measured font difference', () => {
  const reference = fixture(), actual = fixture(); actual.diagnostics = {fontScale: 1.25};
  assert.deepEqual(validateMeasurement(actual), []); assert.equal(compareMeasurements(reference, actual).verdict, 'PASS');
  actual.elements[0].style.fontSize = '30px';
  assert.ok(compareMeasurements(reference, actual).differences.some(value => value.property === 'fontSize'));
  actual.diagnostics.fontScale = 0; assert.ok(validateMeasurement(actual).includes('diagnostics.fontScale'));
});
