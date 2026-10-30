import assert from 'node:assert/strict';
import test from 'node:test';
import {createNativeWindowGeometry} from '../src-next/dev/layout-dump/native-window-geometry.ts';
import type {NativeObservedHost} from '../src-next/dev/layout-dump/native-observer.ts';

const source = 'android.View.transformMatrixToGlobal+getLocationInWindow.boundsDp';
const observed = (extra: Partial<NativeObservedHost>): NativeObservedHost => ({style: {}, unavailable: {}, ...extra});

test('one captured native-window root projects boxes and runs without density, rounding or reference offsets', () => {
  const root = {}, child = {};
  const basis = createNativeWindowGeometry(root, new Map([[root, observed({windowRect: {x: 17.5, y: -2.25, width: 390, height: 844}, windowRectSource: source})],
    [child, observed({windowRect: {x: -3, y: 40.5, width: 30.25, height: 10}, windowRectSource: source,
      textRects: [{x: 20, y: 45, width: 18.125, height: 8}], textRectsSource: 'android.Layout.linesToWindowDp'})]]), () => true);
  assert.deepEqual(basis.root()?.windowRect, {x: 17.5, y: -2.25, width: 390, height: 844});
  assert.deepEqual(basis.host(root).box, {x: 0, y: 0, width: 390, height: 844});
  assert.deepEqual(basis.host(child).box, {x: -20.5, y: 42.75, width: 30.25, height: 10});
  assert.deepEqual(basis.host(child).textRects, [{x: 2.5, y: 47.25, width: 18.125, height: 8}]);
  assert.deepEqual(basis.host(child).unavailable, {});
});

test('missing native bounds or sources stay unavailable without diagnostic or host-property fallback', () => {
  const root = {}, missing = {get getBoundingClientRect() {throw new Error('host inspected');}};
  const input = new Map([[root, observed({windowRect: {x: 0, y: 0, width: 390, height: 844}, windowRectSource: source})],
    [missing, observed({style: {fontSize: '99px'}, textRects: [], windowRect: {x: 0, y: 0, width: 1, height: 1}})]]);
  const basis = createNativeWindowGeometry(root, input, () => true);
  assert.equal(basis.host(missing).box, undefined);
  assert.equal(basis.host(missing).textRects, undefined);
  assert.ok(basis.host(missing).unavailable.box);
  assert.equal(basis.host({}).box, undefined);
  input.set(root, observed({}));
  assert.equal(createNativeWindowGeometry(root, input, () => true).root(), undefined);
});

test('native geometry is copied from one batch and retires with its captured scope', () => {
  const root = {}, child = {};
  let live = true;
  const rootRect = {x: 0, y: 0, width: 390, height: 844}, childRect = {x: 5, y: 10, width: 30, height: 20};
  const input = new Map([[root, observed({windowRect: rootRect, windowRectSource: source})],
    [child, observed({windowRect: childRect, windowRectSource: source, textRects: [], textRectsSource: 'android.Layout.linesToWindowDp'})]]);
  const basis = createNativeWindowGeometry(root, input, () => live);
  rootRect.x = 100; childRect.x = 999; input.clear();
  assert.equal(basis.host(child).box?.x, 5);
  assert.deepEqual(basis.host(child).textRects, []);
  live = false;
  assert.equal(basis.root(), undefined);
  assert.equal(basis.host(child).box, undefined);
  assert.equal(basis.host(child).textRects, undefined);
});

test('nonfinite or overflowing geometry never becomes a canonical box or run', () => {
  const root = {}, child = {};
  const basis = createNativeWindowGeometry(root, new Map([[root, observed({windowRect: {x: 1e308, y: 0, width: 390, height: 844}, windowRectSource: source})],
    [child, observed({windowRect: {x: -1e308, y: 0, width: 30, height: 20}, windowRectSource: source,
      textRects: [{x: -1e308, y: 1, width: 5, height: 5}], textRectsSource: 'android.Layout.linesToWindowDp'})]]), () => true);
  assert.equal(basis.host(child).box, undefined);
  assert.equal(basis.host(child).textRects, undefined);
});
