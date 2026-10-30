import test from 'node:test';
import assert from 'node:assert/strict';
import {authoredReferenceScreen} from './reference-screen.mjs';
import {validateMeasurement} from './contract.mjs';
import {calibrateFrame} from './calibration.mjs';
import {compareMeasurements} from './compare-files.mjs';

test('complete authored phone retains contact-sheet provenance without scaling or shifting measured content', () => {
  const input = {version: 1, scope: 'screen', measurement: {renderer: 'dom', coordinateUnit: 'css-px'},
    root: {x: 80, y: 72, width: 390, height: 844}, viewport: {width: 1440, height: 1010, deviceScaleFactor: 1}, fullViewport: false,
    elements: [], strings: [], anchorIssues: [], repeatedLists: []};
  const projected = authoredReferenceScreen(input, {width: 390, height: 844, deviceScaleFactor: 1});
  assert.deepEqual(projected.elements, input.elements); assert.deepEqual(projected.root, {x: 0, y: 0, width: 390, height: 844});
  assert.deepEqual(projected.referencePresentation.windowBounds, input.root); assert.equal(projected.fullViewport, false);
  assert.deepEqual(validateMeasurement(projected, {reference: true}), []);
  assert.ok(validateMeasurement(projected).includes('measurement.sourceRole'), 'actual app cannot use reference presentation projection');
  assert.deepEqual(input.root, {x: 80, y: 72, width: 390, height: 844});
});
test('a scaled contact-sheet screen or wrong viewport is rejected rather than normalized', () => {
  const input = {measurement: {renderer: 'dom'}, viewport: {deviceScaleFactor: 1}, root: {width: 195, height: 422}};
  assert.throws(() => authoredReferenceScreen(input, {width: 390, height: 844, deviceScaleFactor: 1}));
});
test('reference self calibration uses its authored canvas while actual apps cannot claim that canvas', () => {
  const input = {version: 1, scope: 'screen', measurement: {renderer: 'dom', coordinateUnit: 'css-px'},
    root: {x: 80, y: 72, width: 390, height: 844}, viewport: {width: 1440, height: 1010, deviceScaleFactor: 1}, fullViewport: false,
    elements: [{id: 'title', frame: 'title', tag: 'h1', leaf: true, ownText: 'Public title', normalizedText: 'Public title', focused: false,
      box: {x: 16, y: 64, width: 200, height: 40}, effectiveOpacity: 1, style: {fontFamily: 'Example', fontSize: '32px', fontWeight: '800'}}],
    strings: [], anchorIssues: [], repeatedLists: []};
  const frame = authoredReferenceScreen(input, {width: 390, height: 844, deviceScaleFactor: 1});
  assert.deepEqual(calibrateFrame(frame).counts, {self: 0, oneFont: 1, oneOpacity: 1});
  assert.equal(compareMeasurements(frame, structuredClone(frame)).verdict, 'FAIL', 'no reference-as-application pass');
});
