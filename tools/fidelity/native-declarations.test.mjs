import test from 'node:test';
import assert from 'node:assert/strict';
import {normalizeDeclaredColor, normalizeNativeDeclaredStyle, normalizeNativeDeclaredAncestry, prepareNativeDeclarations} from './native-declarations.mjs';
import {validateMeasurement} from './contract.mjs';
import {compareMeasurements} from './compare-files.mjs';
import {assessDifferences} from './assessment.mjs';
import {compareScreens} from './compare.mjs';

const node = (id, parent, flattenedStyle = {}) => ({id, parent, tag: 'View', role: 'view', leaf: true, ownText: '', normalizedText: '', focused: false,
  box: {x: 0, y: 0, width: 390, height: 844}, effectiveOpacity: null, effectiveBackgroundColor: null, style: {},
  diagnostics: {flattenedStyle, declaredStyle: {source: 'theme-resolved-stylesheet', complete: true}}});
const screen = elements => ({version: 1, scope: 'screen', fullViewport: true, root: {x: 0, y: 0, width: 390, height: 844},
  viewport: {width: 390, height: 844, deviceScaleFactor: 3}, measurement: {renderer: 'react-native', platform: 'android', coordinateUnit: 'dp'},
  elements, anchorIssues: [], repeatedLists: [], strings: [], unavailable: elements.flatMap(value => ['effectiveOpacity', 'effectiveBackgroundColor'].map(property => ({element: value.id, property, reason: 'Not native-observed'})))});

test('declared color normalizes CSS hex and RN ARGB without treating numeric colour as geometry', () => {
  assert.equal(normalizeDeclaredColor('#1234'), 'rgba(17, 34, 51, 0.26666666666666666)');
  assert.equal(normalizeDeclaredColor(0xff123456), 'rgba(18, 52, 86, 1)');
  assert.equal(normalizeDeclaredColor(-1), 'rgba(255, 255, 255, 1)');
  for (const value of ['url(private.png)', 'var(--theme)', 1.5, '#fffff']) assert.equal(normalizeDeclaredColor(value), undefined);
});
test('complete current own-view defaults are explicit policy, partial input gets none', () => {
  assert.deepEqual(normalizeNativeDeclaredStyle({}).style, {});
  const value = normalizeNativeDeclaredStyle({}, {complete: true}).style;
  assert.equal(value.opacity, '1'); assert.equal(value.backgroundColor, 'rgba(0, 0, 0, 0)');
  assert.equal(value.borderTopLeftRadius, '0px'); assert.equal(value.boxShadow, 'none');
  assert.equal(value.fontFamily, undefined); assert.equal(value.color, undefined);
});
test('declarations normalize exact scalar borders and shadows, not unsupported native mechanisms', () => {
  const {style} = normalizeNativeDeclaredStyle({fontFamily: 'Example', fontSize: 17, fontWeight: 800, letterSpacing: -.2,
    borderRadius: 10, borderBottomLeftRadius: 3, borderWidth: 1, borderColor: '#fff', opacity: .6,
    textShadowColor: 'rgba(7, 16, 24, 0.5)', textShadowRadius: 30, textShadowOffsetX: 0, textShadowOffsetY: 2,
    shadowColor: '#000', shadowOpacity: .2, shadowRadius: 8, shadowOffsetX: 1, shadowOffsetY: 4}, {complete: true});
  assert.equal(style.fontSize, '17px'); assert.equal(style.fontWeight, '800'); assert.equal(style.letterSpacing, '-0.2px');
  assert.equal(style.borderBottomLeftRadius, '3px'); assert.equal(style.borderTopWidth, '1px');
  assert.equal(style.textShadow, 'rgba(7, 16, 24, 0.5) 0px 2px 30px');
  assert.equal(style.boxShadow, 'rgba(0, 0, 0, 0.2) 1px 4px 8px 0px');
  assert.equal(normalizeNativeDeclaredStyle({elevation: 4}, {complete: true}).style.boxShadow, undefined);
});
test('ancestor opacity and backgrounds compose only through complete current declarations', () => {
  const parent = node('root', null, {backgroundColor: '#000', opacity: .5});
  const child = node('child', 'root', {backgroundColor: 'rgba(100, 50, 20, 0.5)', opacity: .4});
  const map = new Map([[parent.id, parent], [child.id, child]]);
  assert.deepEqual(normalizeNativeDeclaredAncestry(child, map), {effectiveOpacity: .2, effectiveBackgroundColor: 'rgba(50, 25, 10, 1)'});
  delete parent.diagnostics.declaredStyle;
  const missing = normalizeNativeDeclaredAncestry(child, map);
  assert.equal(missing.effectiveOpacity, undefined); assert.equal(missing.effectiveBackgroundColor, undefined);
  child.diagnostics.flattenedStyle.backgroundColor = '#fff';
  assert.equal(normalizeNativeDeclaredAncestry(child, map).effectiveBackgroundColor, 'rgba(255, 255, 255, 1)');
});
test('cycles and absent ancestry never qualify opacity', () => {
  const value = node('root', 'missing'); assert.equal(normalizeNativeDeclaredAncestry(value, new Map()).effectiveOpacity, undefined);
  value.parent = value.id; assert.equal(normalizeNativeDeclaredAncestry(value, new Map([[value.id, value]])).effectiveOpacity, undefined);
});
test('projection is native-only, immutable, and never fills runtime geometry/text/focus', () => {
  const raw = screen([node('root', null, {backgroundColor: '#123456'})]), before = JSON.stringify(raw);
  const projected = prepareNativeDeclarations(raw);
  assert.equal(JSON.stringify(raw), before); assert.equal(projected.elements[0].effectiveOpacity, 1);
  assert.equal(projected.elements[0].effectiveBackgroundColor, 'rgba(18, 52, 86, 1)'); assert.equal(projected.unavailable.length, 0);
  assert.equal(projected.elements[0].box, raw.elements[0].box); assert.equal(projected.elements[0].focused, false);
  assert.equal(projected.elements[0].textRects, undefined);
  const legacy = structuredClone(raw); delete legacy.elements[0].diagnostics.declaredStyle;
  assert.equal(prepareNativeDeclarations(legacy), legacy);
  const dom = {...raw, measurement: {renderer: 'dom', coordinateUnit: 'css-px'}};
  assert.equal(prepareNativeDeclarations(dom), dom);
});
test('strict provenance rejects arbitrary diagnostics or incomplete markers', () => {
  const raw = screen([node('root', null)]); assert.deepEqual(validateMeasurement(raw), []);
  raw.elements[0].diagnostics.declaredStyle.complete = false;
  assert.ok(validateMeasurement(raw).includes('elements.0.diagnostics.declaredStyle'));
  raw.elements[0].diagnostics.declaredStyle = {source: 'theme-resolved-stylesheet', complete: true, guessed: true};
  assert.ok(validateMeasurement(raw).includes('elements.0.diagnostics.declaredStyle'));
});
test('declared family is accepted without inventing resolved identity or changing a raw file', () => {
  const raw = screen([node('root', null, {fontFamily: 'Example', fontSize: 24, fontWeight: '700', color: '#fff'})]);
  const value = raw.elements[0]; value.ownText = value.normalizedText = 'Title'; value.textRects = [{x: 0, y: 0, width: 20, height: 20}];
  raw.unavailable.push({element: 'root', property: 'resolvedFontFamily', reason: 'No shaped-resource association'});
  assert.deepEqual(validateMeasurement(raw), []);
  const prepared = prepareNativeDeclarations(raw); assert.equal(prepared.elements[0].style.resolvedFontFamily, undefined);
  assert.equal(raw.unavailable.length, 3); assert.equal(prepared.unavailable.length, 0);
});
test('coverage does not hide independently known differences; complete PASS still requires all facts', () => {
  const result = assessDifferences([{kind: 'unavailable-measurement', property: 'image.loaded'},
    {kind: 'measured-difference', property: 'box.x', expected: 0, actual: 8}]);
  assert.equal(result.coverage.status, 'INCOMPLETE'); assert.equal(result.actionableFindingsReady, true);
  assert.equal(result.actionableVerdict, 'FAIL'); assert.equal(result.measuredVerdict, 'WITHHELD');
  const invalid = assessDifferences([{kind: 'invalid-measurement'}, {kind: 'measured-difference', property: 'box.x', expected: 0, actual: 8}]);
  assert.equal(invalid.actionableFindingsReady, false);
});
test('a mapped child supplies only explicitly selected paint while host geometry and radius stay scored', () => {
  const reference = screen([node('r', null)]), actual = screen([node('a', null), node('fill', 'a')]);
  reference.elements[0].frame = actual.elements[0].frame = 'action'; actual.elements[1].frame = 'face';
  reference.viewport.deviceScaleFactor = 1;
  for (const value of [reference, actual]) {value.unavailable = []; for (const el of value.elements) {
    delete el.diagnostics; el.effectiveOpacity = 1; el.style = {borderTopLeftRadius: '10px'};
    el.effectiveBackgroundColor = 'rgb(255, 255, 255)';
  }}
  actual.elements[0].effectiveBackgroundColor = 'transparent'; actual.elements[1].style.borderTopLeftRadius = '0px';
  const recipe = {actualContentOnly: ['face'], paintSlots: [{reference: 'action', actual: 'face', properties: ['effectiveBackgroundColor']}]};
  assert.equal(compareScreens(reference, actual, recipe).differences.length, 0);
  actual.elements[0].box.x = 8;
  assert.ok(compareScreens(reference, actual, recipe).differences.some(value => value.property === 'box.x'));
  actual.elements.pop();
  assert.ok(compareScreens(reference, actual, recipe).differences.some(value => value.kind === 'missing-paint-slot'));
});
