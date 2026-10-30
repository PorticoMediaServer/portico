import test from 'node:test';
import assert from 'node:assert/strict';
import {liveRailValueText} from '../src/screens/live/live-chrome-model.ts';
import type {LiveChromeWindow, LiveChromePort} from '../src/screens/live/live-chrome-types.ts';

const window: LiveChromeWindow = {mediaOriginMs: 0, seekStartMs: 0, seekEndMs: 1800000, liveEdgeMs: 1200000,
  positionMs: 600000, programmeStartMs: 0, programmeEndMs: 1800000, behindSeconds: 600, atLive: false};
const programme = {startMs: 0, endMs: 1800000} as LiveChromePort['programme'];
const format = (ms: number) => `${ms / 60000}m`;
test('live slider speaks position, full programme span and actual delay at the displayed scrub position', () => {
  assert.equal(liveRailValueText(window, programme, format), '10m, 0m to 30m, 10 min behind live');
  assert.equal(liveRailValueText({...window, positionMs: 900000}, programme, format), '15m, 0m to 30m, 5 min behind live');
});
test('live edge and missing programme have useful value text without invented programme times', () => {
  assert.equal(liveRailValueText({...window, positionMs: window.liveEdgeMs}, undefined, format), '20m, Live');
});
