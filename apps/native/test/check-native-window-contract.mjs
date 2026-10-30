// Public synthetic coupling check. The runner supplies an exact checked server archive.
import assert from 'node:assert/strict';
import {join} from 'node:path';
import {pathToFileURL} from 'node:url';
import {collectNativeScreen} from '../src-next/dev/layout-dump/collect.ts';
import {createNativeMeasurementRegistry} from '../src-next/dev/layout-dump/registry.ts';
import {nativeHost, setNativeObservation, observeNativeWindow, addNativeFixture} from './helpers/native-window-fixture.ts';

const secondary = process.env.PORTICO_RUNNER_SECONDARY_SOURCE;
if (!secondary) throw new Error('An exact checked public server archive is required.');
const {validateMeasurement, nativeWindowRectToRoot, nativeWindowCoversViewport} =
  await import(pathToFileURL(join(secondary, 'tools/fidelity/contract.mjs')).href);
const registry = createNativeMeasurementRegistry();
const title = nativeHost('RN:Text', {x: 10, y: -12, width: 100, height: 30});
const input = nativeHost('RN:TextInput', {x: 10, y: 60, width: 100, height: 30});
for (const key of ['childNodes', 'value', 'textContent', 'props']) {
  Object.defineProperty(input, key, {get() {throw new Error('Input internals were inspected.');}});
}
const root = nativeHost('RN:View', {x: 0, y: -42.666656494140625, width: 390, height: 844}, [title, input]);
setNativeObservation(root, {windowRect: {x: 0, y: 0, width: 390, height: 844}});
setNativeObservation(title, {windowRect: {x: 35, y: 22, width: 102, height: 32},
  style: {resolvedFontFamily: 'Public Test Font', fontSize: '24px', fontWeight: '700', color: 'rgba(1, 2, 3, 1)'},
  effectiveOpacity: 1, effectiveBackgroundColor: 'rgba(0, 0, 0, 1)', focused: false,
  textRects: [{x: 38, y: 23, width: 96, height: 30}], textRectsSource: 'android.Layout.linesToWindowDp'});
setNativeObservation(input, {textRects: [{x: 10, y: 60, width: 100, height: 30}], textRectsSource: 'android.Layout.linesToWindowDp'});
registry.register(title, {kind: 'text'}).recordTextLayout([{text: 'Public fixture', x: 0, y: 0, width: 96, height: 30}]);
registry.register(input, {kind: 'input'}).recordMaskLength(4);
const retireFixture = addNativeFixture(root, registry.register.bind(registry));
const options = {root, registry, observe: observeNativeWindow, isCurrent: () => true,
  platform: 'android', viewport: {width: 390, height: 844, deviceScaleFactor: 3}, diagnostics: {fontScale: 1}};
try {
  const measured = await collectNativeScreen(options);
  assert.deepEqual(validateMeasurement(measured), []);
  assert.equal(measured.fullViewport, true);
  assert.equal(measured.diagnostics.nativeCoordinates.publicRoot.rect.y, -42.666656494140625);
  const text = measured.elements.find(node => node.tag === 'RN:Text');
  assert.deepEqual(text.box, {x: 35, y: 22, width: 102, height: 32});
  assert.deepEqual(text.textRects, [{x: 38, y: 23, width: 96, height: 30}]);
  assert.deepEqual(text.box, nativeWindowRectToRoot(text.diagnostics.nativeWindowRect, measured.diagnostics.nativeCoordinates.rootWindow));
  assert.equal(nativeWindowCoversViewport(measured.diagnostics.nativeCoordinates.rootWindow, measured.viewport, measured.measurement), true);
  const secure = measured.elements.find(node => node.input);
  assert.equal(secure.textRects, undefined);
  assert.equal(secure.diagnostics.nativeWindowTextRects, undefined);
  assert.deepEqual(secure.input, {value: null, redacted: true, maskedLength: 4});

  const altered = structuredClone(measured);
  altered.elements.find(node => node.tag === 'RN:Text').box.x += 0.001;
  assert.ok(validateMeasurement(altered).some(path => path.endsWith('.nativeWindow.box')));
  setNativeObservation(title, {windowRect: undefined});
  const absent = await collectNativeScreen(options);
  assert.deepEqual(validateMeasurement(absent), []);
  assert.equal(absent.elements.find(node => node.tag === 'RN:Text').box, null);
  setNativeObservation(root, {windowRect: {x: 0, y: 24, width: 390, height: 844}});
  const shifted = await collectNativeScreen(options);
  assert.equal(shifted.fullViewport, false);
  assert.ok(validateMeasurement(shifted).includes('nativeWindow.fullViewport'));
  console.log('Native-window producer/shared-validator coupling PASS (public synthetic only).');
} finally {
  retireFixture();
}
