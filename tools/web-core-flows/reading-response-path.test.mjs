import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readingResponsePath} from './reading-response-path.mjs';

test('retained reading evidence contains neither session nor page identity', () => {
  assert.equal(readingResponsePath('/v1/reading/sessions/private-session/pages/private-page'), '/v1/reading/sessions/:session/pages/:page');
  assert.equal(readingResponsePath('/v1/reading/sessions/private-session/source'), '/v1/reading/sessions/:session/source');
});
test('unrelated and unexpected resource paths are excluded', () => {
  for (const path of ['/v1/reading/sessions/private-session', '/v1/accounts/private', '/v1/reading/sessions/private-session/source/extra', '/v1/reading/sessions/private-session/pages', '/v1/reading/sessions/private-session/pages/a/extra']) {
    assert.equal(readingResponsePath(path), null);
  }
});
