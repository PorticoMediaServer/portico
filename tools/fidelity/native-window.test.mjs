import test from 'node:test';
import assert from 'node:assert/strict';
import {validateMeasurement, nativeWindowRectToRoot, nativeWindowCoversViewport} from './contract.mjs';
import {compareMeasurements} from './compare-files.mjs';

const rectangle = (x, y, width, height) => ({x, y, width, height});
function measured() {
  const rootWindow = rectangle(0, 0, 320, 640);
  return {version: 1, scope: 'screen', fullViewport: true,
    measurement: {renderer: 'react-native', platform: 'android', coordinateUnit: 'dp', geometrySource: 'native-window'},
    viewport: {width: 320, height: 640, deviceScaleFactor: 3}, root: rootWindow,
    diagnostics: {fontScale: 1, nativeCoordinates: {basis: 'attached-native-window',
      windowAPI: 'Example.NativeView.windowBounds', rootWindow,
      publicRoot: {api: 'bounding-client-rect', rect: rectangle(0, -25.5, 320, 640)},
      publicRootReadback: {api: 'measure-in-window', rect: rectangle(0, -25.5, 320, 640)}}},
    anchorIssues: [], repeatedLists: [], strings: [],
    elements: ['title', 'caption'].map((frame, index) => {
      const windowRect = rectangle(20, 40 + index * 40, 120, 24);
      const runs = [rectangle(20, 40 + index * 40, 80, 24)];
      return {id: String(index), parent: null, frame, tag: 'RN:Text', role: 'text', leaf: true,
        ownText: frame, normalizedText: frame, focused: index === 0,
        box: nativeWindowRectToRoot(windowRect, rootWindow), textRects: runs.map(run => nativeWindowRectToRoot(run, rootWindow)),
        effectiveOpacity: 1, effectiveBackgroundColor: 'rgb(7, 16, 24)',
        style: {fontFamily: 'Example', resolvedFontFamily: 'Example', fontSize: '20px', fontWeight: '700', color: 'rgb(237, 245, 250)'},
        diagnostics: {nativeWindowRect: windowRect, nativeWindowTextRects: runs,
          publicMeasurement: {api: 'bounding-client-rect', rect: rectangle(20, 4.5 + index * 40, 120, 24)}}};
    })};
}
function referenceFor(actual) {
  const reference = structuredClone(actual);
  reference.measurement = {renderer: 'dom', coordinateUnit: 'css-px'};
  reference.viewport.deviceScaleFactor = 1;
  delete reference.diagnostics;
  for (const node of reference.elements) delete node.diagnostics;
  return reference;
}

test('native window geometry retains public API diagnostics without assuming a constant ShadowTree translation', () => {
  const actual = measured(), before = JSON.stringify(actual);
  assert.deepEqual(validateMeasurement(actual), []);
  const result = compareMeasurements(referenceFor(actual), actual);
  assert.equal(result.verdict, 'PASS');
  assert.deepEqual(result.calibration.counts, {self: 0, oneFont: 1, oneOpacity: 1});
  assert.equal(JSON.stringify(actual), before);
  assert.equal(actual.elements[0].box.y, 40);
  assert.equal(actual.elements[0].diagnostics.publicMeasurement.rect.y - actual.diagnostics.nativeCoordinates.publicRoot.rect.y, 30);
});

test('native root eligibility uses its physical window origin and unchanged extent precision rule', () => {
  const screen = measured(), info = screen.measurement;
  assert.equal(nativeWindowCoversViewport(rectangle(0, 0, Math.fround(320 / 3), 640), {width: 320 / 3, height: 640}, info), true);
  for (const root of [rectangle(0, 1, 320, 640), rectangle(-1, 0, 320, 640), rectangle(0, 0, 320.0001, 640), rectangle(0, 0, 960, 1920)])
    assert.equal(nativeWindowCoversViewport(root, screen.viewport, info), false);
  assert.equal(nativeWindowCoversViewport(screen.root, screen.viewport, {renderer: 'dom', coordinateUnit: 'css-px'}), false);
  assert.equal(nativeWindowCoversViewport(screen.root, screen.viewport, {...info, coordinateUnit: 'px'}), false);
});

test('root-relative conversion subtracts only the observed window root and never scales raw data', () => {
  const root = rectangle(7, 11, 320, 640), raw = rectangle(20.25, 40.5, 120.75, 24.5);
  assert.deepEqual(nativeWindowRectToRoot(raw, root), rectangle(13.25, 29.5, 120.75, 24.5));
  assert.deepEqual(raw, rectangle(20.25, 40.5, 120.75, 24.5));
  assert.throws(() => nativeWindowRectToRoot({...raw, width: NaN}, root));
  assert.throws(() => nativeWindowRectToRoot({...raw, privateValue: 'forbidden'}, root));
});

test('mixed coordinate boxes, text runs and non-window root claims fail validation', () => {
  for (const alter of [
    value => {value.elements[0].box.y -= 10;},
    value => {value.elements[0].textRects[0].y += 25.5;},
    value => {delete value.elements[0].diagnostics.nativeWindowRect;},
    value => {delete value.elements[0].diagnostics.nativeWindowTextRects;},
    value => {value.diagnostics.nativeCoordinates.rootWindow.y = 1;},
    value => {value.measurement.geometrySource = 'choose-zero-api';},
    value => {delete value.measurement.geometrySource;},
  ]) {
    const actual = structuredClone(measured()); alter(actual);
    assert.ok(validateMeasurement(actual).length);
  }
});

test('unavailable physical host boxes remain coverage gaps, never fabricated zero boxes or public API fallbacks', () => {
  const actual = measured(), reference = referenceFor(actual), node = actual.elements[0];
  node.box = null; node.ownText = null; node.normalizedText = null;
  delete node.textRects; delete node.diagnostics.nativeWindowRect; delete node.diagnostics.nativeWindowTextRects;
  actual.unavailable = ['box', 'ownText', 'normalizedText', 'textRects'].map(property => ({element: node.id, property, reason: 'current physical native host fact unavailable'}));
  assert.deepEqual(validateMeasurement(actual), []);
  const result = compareMeasurements(reference, actual);
  assert.equal(result.assessment.measuredVerdict, 'WITHHELD');
  assert.equal(result.assessment.coverage.invalidMeasurements, 0);
  assert.equal(result.assessment.coverage.unavailableMeasurements, 4);
  node.box = node.diagnostics.publicMeasurement.rect;
  assert.ok(validateMeasurement(actual).includes('elements.0.nativeWindow.box'));
  const dom = referenceFor(actual); dom.elements[0].box = null;
  assert.ok(validateMeasurement(dom).includes('elements.0.box'));
});

test('raw coordinate diagnostics are curated numeric facts and never input glyphs or arbitrary props', () => {
  for (const alter of [
    value => {value.diagnostics.nativeCoordinates.privateProps = {};},
    value => {value.diagnostics.nativeCoordinates.windowAPI = 'https://example.invalid/private';},
    value => {value.diagnostics.nativeCoordinates.windowAPI = 'data:forbidden';},
    value => {value.diagnostics.nativeCoordinates.publicRoot.rect.value = 'forbidden';},
    value => {value.elements[0].diagnostics.publicMeasurement.api = 'preferred-zero-api';},
    value => {value.elements[0].diagnostics.nativeWindowRect.url = 'forbidden';},
    value => {value.elements[0].input = true;},
  ]) {
    const actual = structuredClone(measured()); alter(actual);
    assert.ok(validateMeasurement(actual).length);
  }
});

test('published false fullscreen flags stay invalid and old files are not repaired by the new source policy', () => {
  const actual = measured(), reference = referenceFor(actual); actual.fullViewport = false;
  assert.ok(validateMeasurement(actual).includes('fullViewport'));
  const result = compareMeasurements(reference, actual);
  assert.equal(result.assessment.measuredVerdict, 'WITHHELD');
  assert.equal(actual.fullViewport, false);
});
