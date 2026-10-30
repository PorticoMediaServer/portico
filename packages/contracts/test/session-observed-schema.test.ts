import test from 'node:test';
import assert from 'node:assert/strict';
import {schemas} from '../src/schemas.ts';

test('session output reuses the observation sparse observed shape without asserting unknown facts', () => {
  const session = schemas.GetPlaybackSessionResponse;
  const observed = session.properties.observedPlayback;
  assert.deepEqual(observed, schemas.PutPlaybackObservationRequest.properties.observedPlayback);
  assert.equal(session.required.includes('observedPlayback'), false);
  assert.equal(observed.type, 'object');
  assert.notEqual('nullable' in observed && observed.nullable, true);
  assert.deepEqual(observed.required, []);
  assert.deepEqual(Object.keys(observed.properties).sort(), ['audio', 'deliveredBitrateKbps', 'screen', 'subtitles', 'video']);
  assert.equal('width' in observed.properties.screen.properties, false);
  for (const key of ['url', 'source', 'plan', 'quality', 'deviceId']) {
    assert.equal(key in observed.properties, false);
  }
});
