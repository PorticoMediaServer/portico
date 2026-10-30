import test from 'node:test';
import assert from 'node:assert/strict';

test('both platform HTTP facts share retryable and permanent delivery policy', async () => {
  const {classifyPlaybackFailure} = await import('../src-next/platform/playback/playback-failure.ts');
  for (const httpStatus of [403, 404, 410, 408, 429, 500, 503]) {
    const expectedNetwork = httpStatus >= 500 || httpStatus === 408 || httpStatus === 429;
    for (const networkError of [true, false]) {
      const result = classifyPlaybackFailure({httpStatus, networkError, engineCode: 'decode_error', hasPlayed: false});
      assert.equal(result.network, expectedNetwork, String(httpStatus));
      assert.equal(result.engine, null, 'HTTP never teaches a decoder failure');
    }
  }
});

test('startup engine failures retain fallback while healthy and delivery failures do not learn', async () => {
  const {classifyPlaybackFailure} = await import('../src-next/platform/playback/playback-failure.ts');
  assert.equal(classifyPlaybackFailure({hasPlayed: false}).engine, 'source_not_supported');
  assert.equal(classifyPlaybackFailure({hasPlayed: true}).engine, null);
  for (const hasPlayed of [false, true]) {
    assert.equal(classifyPlaybackFailure({hasPlayed, networkError: true, engineCode: 'decode_error'}).engine, null);
    assert.equal(classifyPlaybackFailure({hasPlayed, deliveryError: true, engineCode: 'decode_error'}).engine, null);
    assert.equal(classifyPlaybackFailure({hasPlayed, engineCode: 'decode_error'}).engine, 'decode_error');
    assert.equal(classifyPlaybackFailure({hasPlayed, engineCode: 'source_not_supported'}).engine, 'source_not_supported');
  }
  assert.equal(classifyPlaybackFailure({engineCode: 'engine_error'}).engine, null);
});

test('a start-up failure whose cause is uncertain teaches nothing; a named decoder or parser fault still does', async () => {
  const {classifyPlaybackFailure} = await import('../src-next/platform/playback/playback-failure.ts');
  // An interruption, a time-out, or a transport that was reporting trouble: not the media's fault.
  assert.deepEqual(classifyPlaybackFailure({hasPlayed: false, uncertain: true}), {network: false, engine: null});
  assert.deepEqual(classifyPlaybackFailure({hasPlayed: true, uncertain: true}), {network: false, engine: null});
  assert.equal(classifyPlaybackFailure({hasPlayed: false, uncertain: false}).engine, 'source_not_supported');
  assert.equal(classifyPlaybackFailure({hasPlayed: false}).engine, 'source_not_supported', 'a runtime that does not say is as before');
  for (const engineCode of ['decode_error', 'source_not_supported'] as const)
    assert.equal(classifyPlaybackFailure({hasPlayed: false, uncertain: true, engineCode}).engine, engineCode);
  assert.deepEqual(classifyPlaybackFailure({hasPlayed: false, uncertain: true, networkError: true}), {network: true, engine: null});
});
