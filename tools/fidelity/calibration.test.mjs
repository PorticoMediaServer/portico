import test from 'node:test';
import assert from 'node:assert/strict';
import {calibrateFrame} from './calibration.mjs';

test('same frame is zero; one font or effective caption opacity observation is exactly one difference', () => {
  const node = (frame, text, opacity) => ({id: frame, frame, tag: 'p', ownText: text, normalizedText: text, leaf: true,
    box: {x: 10, y: 20, width: 100, height: 30}, textRects: [{x: 10, y: 20, width: 50, height: 20}], descendantTextRects: [{x: 10, y: 20, width: 50, height: 20}],
    effectiveOpacity: opacity, effectiveBackgroundColor: 'rgba(7, 16, 24, 1)', style: {fontSize: '32px', fontFamily: 'Example', fontWeight: '800', opacity: String(opacity), color: 'rgb(237, 245, 250)'}});
  const frame = {scope: 'screen', fullViewport: true, root: {width: 200, height: 100}, viewport: {deviceScaleFactor: 1}, anchorIssues: [], elements: [node('title', 'Public heading', 1), node('caption', 'Public caption', .72)]};
  assert.deepEqual(calibrateFrame(frame), {passed: true, counts: {self: 0, oneFont: 1, oneOpacity: 1}, fontProperty: 'fontSize', opacityProperty: 'effectiveOpacity'});
});
test('reference self calibration matches stable observation IDs without waiving app matching ambiguity', async () => {
  const {compareScreens} = await import('./compare.mjs');
  const elements = [
    {id: '0', tag: 'div', ownText: '', style: {backgroundColor: 'rgb(7,16,24)'}},
    {id: '0.0', tag: 'div', ownText: '', style: {backgroundColor: 'rgb(7,16,24)'}},
    {id: '0.1', frame: 'title', tag: 'h1', ownText: 'Public title', style: {fontSize: '32px', fontFamily: 'Example', fontWeight: '800'}},
  ];
  const frame = {scope: 'screen', fullViewport: true, root: {width: 200, height: 100}, viewport: {deviceScaleFactor: 1}, anchorIssues: [], elements};
  assert.deepEqual(calibrateFrame(frame).counts, {self: 0, oneFont: 1, oneOpacity: 1});
  assert.ok(compareScreens(frame, structuredClone(frame)).differences.some(value => value.kind === 'ambiguous-match'));
});
