import test from 'node:test';
import assert from 'node:assert/strict';
import {schemas} from '../src/schemas.ts';

test('session comparison is optional and explicitly describes the committed plan', () => {
  const session = schemas.GetPlaybackSessionResponse;
  const info = session.properties.playbackInfo;
  assert.equal(new Set<string>(session.required).has('playbackInfo'), false);
  assert.equal(info.type, 'object');
  assert.notEqual('nullable' in info && info.nullable, true);
  assert.deepEqual(info.properties.basis.enum, ['committed_delivery_plan']);
  for (const field of ['source', 'delivered', 'route', 'routes', 'reason', 'losses', 'network', 'basis']) {
    assert.equal(new Set<string>(info.required).has(field), true);
  }
  assert.equal(new Set<string>(info.required).has('estimatedBitrateKbps'), false);
  for (const side of [info.properties.source, info.properties.delivered]) {
    assert.equal(side.properties.video.properties.hdr10Plus.type, 'boolean');
    assert.equal(new Set<string>(side.properties.video.required).has('hdr10Plus'), false);
    for (const field of ['dolbyVisionRpu', 'dolbyVisionBaseLayer'] as const) {
      assert.equal(side.properties.video.properties[field].type, 'boolean');
      assert.equal(new Set<string>(side.properties.video.required).has(field), false);
      assert.equal('nullable' in side.properties.video.properties[field], false);
    }
    for (const field of ['url', 'path', 'token', 'screen', 'pcm', 'device']) assert.equal(field in side.properties, false);
  }
  assert.equal('observedPlayback' in info.properties, false);
  assert.equal('observedPlayback' in session.properties, true);
});
