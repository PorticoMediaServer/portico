import test from 'node:test';
import assert from 'node:assert/strict';
import {markerTime, markerWindowParts, moveMarkerEdge, parseMarkerTime, validMarker} from '../src-next/kit/shell/segment-geometry.ts';

test('marker editing preserves microseconds and accepts long film times', () => {
  for (const value of ['0', '83500000', '6000000000', '3600000001']) assert.equal(parseMarkerTime(markerTime(value)), value);
  assert.equal(parseMarkerTime('1:23.5'), '83500000');
  assert.equal(parseMarkerTime('1:99'), undefined);
  assert.equal(parseMarkerTime('1:60:00'), undefined);
  assert.equal(parseMarkerTime('-1:00'), undefined);
});
test('marker handles cannot cross or leave the real file duration', () => {
  const bounds = {startUS: '38000000', endUS: '108000000'};
  assert.equal(validMarker(bounds, '3240000000'), true);
  assert.equal(validMarker({startUS: '108000000', endUS: '38000000'}, '3240000000'), false);
  assert.equal(validMarker(bounds, ''), false);
  assert.equal(moveMarkerEdge(bounds, 'start', 200000000, 3240000000).startUS, '107999999');
  assert.equal(moveMarkerEdge(bounds, 'end', 9000000000, 3240000000).endUS, '3240000000');
  assert.equal(moveMarkerEdge(bounds, 'start', -1, 3240000000).startUS, '0');
  assert.equal(markerWindowParts(bounds, {startUS: '3000000000', endUS: '3240000000'}), undefined);
});
