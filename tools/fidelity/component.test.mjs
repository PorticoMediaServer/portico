import test from 'node:test';
import assert from 'node:assert/strict';
import {componentMeasurement} from './component.mjs';
import {validateMeasurement} from './contract.mjs';
import {compareMeasurements} from './compare-files.mjs';

function canvas({wrapped = false, x = 20, y = 30, width = 400, height = 200, density = 1} = {}) {
  const node = (id, parent, tag, ownText, box) => ({id, parent, tag, ownText, normalizedText: ownText, focused: false, leaf: true,
    box, textRects: ownText ? [{x: box.x, y: box.y, width: 30, height: 20}] : [], effectiveOpacity: 1,
    style: {fontFamily: 'Example', fontSize: '20px', fontWeight: '700', color: 'rgb(7, 16, 24)'}});
  const root = node('0', null, 'main', '', {x: 0, y: 0, width, height}); root.leaf = false;
  const control = node('0.0', '0', 'button', wrapped ? '' : 'Play', {x, y, width: 100, height: 40});
  control.normalizedText = 'Play'; control.leaf = !wrapped;
  control.textRects = wrapped ? [] : [{x: x + 10, y: y + 10, width: 30, height: 20}];
  control.descendantTextRects = [{x: x + 10, y: y + 10, width: 30, height: 20}];
  return {version: 1, scope: 'screen', measurement: {renderer: 'dom', coordinateUnit: 'css-px'}, fullViewport: true,
    viewport: {width, height, deviceScaleFactor: density}, root: {x: 0, y: 0, width, height}, anchorIssues: [], repeatedLists: [],
    elements: [root, control, ...(wrapped ? [node('0.0.0', '0.0', 'span', 'Play', {x: x + 10, y: y + 10, width: 30, height: 20})] : []), node('0.1', '0', 'h1', 'Outside', {x: 0, y: 0, width: 100, height: 20})],
    strings: [{id: 'play', owner: wrapped ? '0.0.0' : '0.0', kind: 'text', text: 'Play', rects: [{x: x + 10, y: y + 10, width: 30, height: 20}]}, {id: 'outside', owner: '0.1', kind: 'text', text: 'Outside'}]};
}
test('component scope preserves raw facts and density while projecting only the measured subtree', () => {
  const raw = canvas({wrapped: true, density: 2}), before = JSON.stringify(raw);
  const actual = componentMeasurement(raw, {elementId: '0.0', frames: {'0.0.0': 'label'}});
  assert.equal(JSON.stringify(raw), before); assert.equal(actual.scope, 'component'); assert.equal(actual.fullViewport, false);
  assert.equal(actual.viewport.deviceScaleFactor, 2); assert.deepEqual(actual.root, {x: 0, y: 0, width: 100, height: 40});
  assert.deepEqual(actual.componentPresentation.windowBounds, {x: 20, y: 30, width: 100, height: 40});
  assert.deepEqual(actual.elements.map(node => node.id), ['0.0', '0.0.0']);
  assert.deepEqual(actual.elements[1].box, {x: 10, y: 10, width: 30, height: 20});
  assert.deepEqual(actual.strings[0].rects, [{x: 10, y: 10, width: 30, height: 20}]);
  assert.deepEqual(actual.elements[0].style, raw.elements[1].style); assert.deepEqual(validateMeasurement(actual), []);
});
test('one comparator scores components at different window positions without promoting them to screens', () => {
  const reference = componentMeasurement(canvas({x: 200, y: 300, width: 800, height: 600}), {elementId: '0.0', reference: true});
  const actual = componentMeasurement(canvas({wrapped: true, density: 2}), {elementId: '0.0', frames: {'0.0.0': 'label'}});
  const adapter = {textHandledBySlots: ['component-root'], actualContentOnly: ['label'], contentSlots: [{reference: 'component-root', actual: 'label'}]};
  const result = compareMeasurements(reference, actual, adapter);
  assert.equal(result.scope, 'component'); assert.equal(result.verdict, 'PASS');
  assert.deepEqual(result.calibration.counts, {self: 0, oneFont: 1, oneOpacity: 1});
  assert.equal(compareMeasurements(reference, reference, adapter).verdict, 'FAIL', 'a reference cannot become the actual app');
  assert.equal(compareMeasurements(canvas(), actual).verdict, 'FAIL', 'component coverage cannot certify a whole screen');
  assert.ok(validateMeasurement({...actual, scope: 'screen'}).includes('component.scope'));
  actual.elements[0].box.width += 2;
  assert.equal(compareMeasurements(reference, actual, adapter).verdict, 'PASS');
  actual.elements[0].box.width += .01;
  assert.ok(compareMeasurements(reference, actual, adapter).differences.some(value => value.property === 'box.width'));
});
test('clipped, zero-size, ambiguous, cross-tree and native projections fail closed', () => {
  const clipped = componentMeasurement(canvas({x: 350}), {elementId: '0.0'});
  assert.ok(validateMeasurement(clipped).includes('component.visibility'));
  const zero = canvas(); zero.elements[1].box.height = 0; assert.throws(() => componentMeasurement(zero, {elementId: '0.0'}));
  assert.throws(() => componentMeasurement({...canvas(), anchorIssues: [{name: 'ambiguous', matches: 2}]}, {elementId: '0.0'}));
  assert.throws(() => componentMeasurement(canvas(), {elementId: '0.0', frames: {'0.1': 'outside'}}));
  assert.throws(() => componentMeasurement({...canvas(), measurement: {renderer: 'react-native', platform: 'android', coordinateUnit: 'dp'}}));
  assert.throws(() => componentMeasurement(canvas({density: 2}), {elementId: '0.0', reference: true}));
});
