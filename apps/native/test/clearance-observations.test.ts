import assert from 'node:assert/strict';
import test from 'node:test';
import {clearanceReceipt, compareClearance, readClearance, type ClearanceSnapshot} from './fixtures/superseded/clearance-observations';

/** Authored SOURCE vectors for FX-075's numeric clearance processor. Same coordinate
 * space, explicit scale and safe inset; no declared height is ever a measurement and
 * no vector is a device reading. */
const base: ClearanceSnapshot = {
  schema: 1, units: 'pt', scale: 3, window: {id: 'w1', width: 390, height: 844}, safeBottom: 34,
  toast: {x: 16, y: 700, width: 358, height: 60},
};

test('a dock below the toast reports the signed vertical edge gap it really has', () => {
  const observation = readClearance({...base, dock: {x: 0, y: 780, width: 390, height: 64}});
  assert.equal(observation.observed, true);
  assert.equal(observation.source, 'measured');
  assert.equal(observation.readings.dock?.verticalGap, 20);
  assert.equal(observation.readings.dock?.intersects, false);
  assert.equal(observation.units, 'pt');
  assert.equal(observation.scale, 3);
  assert.equal(observation.safeBottom, 34);
  assert.equal(observation.windowIdentity, 'w1');
});

test('a dock covering the toast bottom intersects physically, while a negative gap alone does not prove it', () => {
  const covering = readClearance({...base, dock: {x: 0, y: 720, width: 390, height: 50}});
  assert.equal(covering.readings.dock?.verticalGap, -40);
  assert.equal(covering.readings.dock?.intersects, true);
  // Starts above the toast bottom but is horizontally disjoint: a negative gap, no intersection.
  const beside = readClearance({...base, tabBar: {x: 0, y: 720, width: 10, height: 50}});
  assert.equal(beside.readings.tabBar?.verticalGap, -40);
  assert.equal(beside.readings.tabBar?.intersects, false);
  // Entirely above the toast: again a negative gap with no physical intersection.
  const above = readClearance({...base, tabBar: {x: 16, y: 600, width: 358, height: 40}});
  assert.equal(above.readings.tabBar?.verticalGap, 600 - (base.toast!.y + base.toast!.height));
  assert.equal(above.readings.tabBar?.intersects, false);
});

test('a role the window did not draw stays unknown instead of false evidence', () => {
  const dockOnly = readClearance({...base, dock: {x: 0, y: 780, width: 390, height: 64}, declared: {tabBarHeight: 62, dockHeight: 66}});
  assert.equal(dockOnly.readings.tabBar, undefined);
  assert.equal(dockOnly.observed, true);
  const declaredOnly = readClearance({...base, declared: {dockHeight: 66, tabBarHeight: 62}});
  assert.equal(declaredOnly.observed, false);
  assert.equal(declaredOnly.source, 'declared-only');
  assert.deepEqual(declaredOnly.readings, {});
  assert.equal(declaredOnly.reason, 'missing-measurement');
});

test('supplied but impossible geometry is invalid, not the same as a missing measurement', () => {
  assert.equal(readClearance({...base, dock: {x: 0, y: Number.POSITIVE_INFINITY, width: 390, height: 64}}).reason, 'invalid-geometry');
  assert.equal(readClearance({...base, tabBar: {x: 0, y: 780, width: -390, height: 64}}).reason, 'invalid-geometry');
  // An invalid toast is not silently rescued by a valid dock.
  assert.equal(readClearance({...base, toast: {x: Number.NaN, y: 700, width: 358, height: 60}, dock: {x: 0, y: 780, width: 390, height: 64}}).reason, 'invalid-geometry');
  assert.equal(readClearance({...base, toast: undefined, dock: {x: 0, y: 780, width: 390, height: 64}}).reason, 'missing-measurement');
  assert.equal(readClearance({...base, scale: 0}).reason, 'invalid-geometry');
  assert.equal(readClearance({...base, safeBottom: Number.NaN}).reason, 'invalid-geometry');
  assert.equal(readClearance({...base, window: {id: '', width: 390, height: 844}}).reason, 'invalid-geometry');
});

test('a resize comparison refuses different units, scales, windows or unobserved sides', () => {
  const portrait = readClearance({...base, dock: {x: 0, y: 780, width: 390, height: 64}});
  const tablet = readClearance({...base, window: {id: 'w1', width: 744, height: 1133}, safeBottom: 20,
    toast: {x: 16, y: 900, width: 712, height: 60}, dock: {x: 0, y: 1000, width: 744, height: 64}});
  const comparison = compareClearance(portrait, tablet);
  assert.equal(comparison.observed, true);
  assert.equal(comparison.deltas?.dock, 20); // tablet 40 - portrait 20 in the same units/scale
  assert.equal(comparison.deltas?.safeBottom, -14);
  assert.equal(compareClearance(portrait, {...portrait, units: 'dp'}).reason, 'incompatible-units');
  assert.equal(compareClearance(portrait, {...portrait, scale: 2}).reason, 'incompatible-scale');
  // Both read with the default call: only the actual window id decides replacement.
  const otherWindow = readClearance({...base, window: {id: 'w2', width: 390, height: 844}, dock: {x: 0, y: 780, width: 390, height: 64}});
  assert.equal(compareClearance(portrait, otherWindow).reason, 'replaced-window');
  assert.equal(compareClearance(readClearance({...base, dock: {x: 0, y: 780, width: 390, height: 64}}),
    readClearance({...base, window: {id: 'w2', width: 390, height: 844}, dock: {x: 0, y: 780, width: 390, height: 64}})).reason, 'replaced-window');
  // The same actual window across a resize stays observed.
  assert.equal(compareClearance(portrait, tablet).observed, true);
  assert.equal(compareClearance(portrait, readClearance({...base, declared: {dockHeight: 66}})).reason, 'unobserved');
});

test('the receipt labels windows by ordinal and keeps each window own units and scale', () => {
  const first = readClearance({...base, dock: {x: 0, y: 780, width: 390, height: 64}});
  const second = readClearance({...base, units: 'dp', scale: 2, window: {id: 'w9', width: 744, height: 1133},
    toast: {x: 16, y: 900, width: 712, height: 60}, dock: {x: 0, y: 1000, width: 744, height: 64}});
  const receipt = clearanceReceipt([first, second]);
  assert.equal(receipt.claim, 'source-only');
  assert.equal(receipt.units, 'mixed');
  assert.equal(receipt.observed, true);
  assert.equal(receipt.windows[0]?.label, 'window-1');
  assert.equal(receipt.windows[1]?.label, 'window-2');
  assert.equal(receipt.windows[1]?.units, 'dp');
  assert.equal(receipt.windows[1]?.scale, 2);
  assert.deepEqual(receipt.redaction, {windowIdentity: 'ordinal-labels'});
  // No raw caller id survives into the receipt.
  assert.equal(JSON.stringify(receipt).includes('w9'), false);
  // Repeat observations of one actual window reuse its ordinal.
  const repeat = clearanceReceipt([first, readClearance({...base, toast: {x: 16, y: 690, width: 358, height: 60}, dock: {x: 0, y: 780, width: 390, height: 64}})]);
  assert.deepEqual(repeat.windows.map(window => window.label), ['window-1', 'window-1']);
  assert.equal(JSON.stringify(repeat).includes('"w1"'), false);
  const empty = clearanceReceipt([]);
  assert.equal(empty.units, 'missing');
  assert.deepEqual(empty.windows, []);
  assert.equal(empty.observed, false);
});
