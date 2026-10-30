import test from 'node:test';
import assert from 'node:assert/strict';
import {encodedRoutePath, routePath} from '../../../packages/contracts/src/routes.ts';
import {FakePlaybackServer, fakeHttp} from '../../../packages/client-core/src/playback-v1/testing/fake-server.ts';
import {SessionsClient} from '../../../packages/client-core/src/playback-v1/sessions.ts';

test('the shared playback fixture preserves a nested file path without bypassing route validation', async () => {
  const client = new SessionsClient(fakeHttp(new FakePlaybackServer()));
  const session = await client.start({itemId: 'movie-1'}, {state: 'playing'}, 'fixture-route-0000000001');
  const parameters = {grant: 'grant-' + session.id, file: session.presentation.generation + '/master.m3u8'};
  assert.throws(() => encodedRoutePath('get_media_grant_file', parameters), /Invalid encoded route parameter/);
  // The server hands out the address with the file's own path segments, as the fixture does.
  assert.equal(session.presentation.url, `/v1/media/${parameters.grant}/${parameters.file}`);
  assert.equal(decodeURIComponent(routePath('get_media_grant_file', parameters)), session.presentation.url);
});
