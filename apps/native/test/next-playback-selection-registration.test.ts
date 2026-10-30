import test from 'node:test';
import assert from 'node:assert/strict';
import {FakePlaybackServer, fakeHttp} from '../../../packages/client-core/src/playback-v1/testing/fake-server.ts';
import {SessionsClient} from '../../../packages/client-core/src/playback-v1/sessions.ts';
import {NativePlayback, type PlaybackSession} from '../src-next/platform/playback/native-playback.ts';

// Source regressions only: campaign item work does not run tests/builds/devices.
test('native adoption receipt is captured before a synchronous listener re-registers the same session', async () => {
  const http = fakeHttp(new FakePlaybackServer()), timers = {setTimer: () => 0, clearTimer: () => {}};
  const path = new NativePlayback({http, timers});
  const session = await new SessionsClient(http, timers).start({itemId: 'movie'}, {state: 'playing'}, 'native-registration-000000');
  let replacement: PlaybackSession | undefined, reentered = false;
  const off = path.subscribeSessions(() => {
    if (!reentered) {reentered = true; replacement = path.transport.adopt(session, 120);}
  });
  try {
    const original = path.transport.adopt(session, 120);
    const first = path.transport.selectionRegistration!(session.id, original)!;
    const second = path.transport.selectionRegistration!(session.id, replacement!)!;
    assert.equal(first.current(), false);
    assert.equal(first.forget(() => true), false);
    assert.equal(second.current(), true);
    assert.equal(path.transport.session(session.id), session);
    assert.equal(path.transport.selectionRegistration!(session.id), undefined, 'a bare id cannot claim an existing owner');
  } finally {off(); path.dispose();}
});
