import test from 'node:test';
import assert from 'node:assert/strict';
import {createDownloadAttempts, isDownloadQualityRequired} from '../src-next/screens/downloads/download-attempt.ts';

// #736 pairing (downloads saved as Ask each time). Native's download sheets always send the quality the person chose
// from the drawn sheet (collections 31/32), so Ask never reaches the server as an omission. What the contract adds for
// the client is the operation id: the same choice retried resends the identical body under the same id (the server
// replays it), a changed choice is a new intent with a new id, and an answer that settles the intent retires the id.
const attempts = () => {let n = 0; return createDownloadAttempts(() => `op_${++n}`);};

test('the same choice tried again after a failure keeps its operation id', () => {
  const a = attempts();
  assert.equal(a.operationFor('["item","film","720p-high"]'), 'op_1');
  assert.equal(a.operationFor('["item","film","720p-high"]'), 'op_1', 'identical body, identical id');
});

test('a changed choice gets a new operation id, and going back to the first is a new intent too', () => {
  const a = attempts();
  assert.equal(a.operationFor('["item","film","720p-high"]'), 'op_1');
  assert.equal(a.operationFor('["item","film","original"]'), 'op_2', 'another quality');
  assert.equal(a.operationFor('["item","film","720p-high"]'), 'op_3', 'the old id is never reused for a body it did not carry last');
  const c = attempts();
  assert.equal(c.operationFor(JSON.stringify(['container', 'season', 's1', 'device', 'original', {episodes: 'all'}])), 'op_1');
  assert.equal(c.operationFor(JSON.stringify(['container', 'season', 's1', 'device', 'original', {episodes: 'next', keepNext: 3}])), 'op_2', 'another episodes policy');
});

test('an answer that settles the intent retires the id: the next choice, even the same one, is a new intent', () => {
  const a = attempts();
  assert.equal(a.operationFor('x'), 'op_1');
  a.settle();
  assert.equal(a.operationFor('x'), 'op_2');
});

test('the server asking for a quality is recognised by its code, nothing else', () => {
  assert.equal(isDownloadQualityRequired({code: 'download_quality_required'}), true);
  assert.equal(isDownloadQualityRequired({code: 'idempotency_key_reused'}), false);
  assert.equal(isDownloadQualityRequired(undefined), false);
  assert.equal(isDownloadQualityRequired(new Error('download_quality_required')), false);
});
