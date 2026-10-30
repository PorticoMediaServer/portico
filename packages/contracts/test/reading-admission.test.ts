import test from 'node:test';
import assert from 'node:assert/strict';
import {routes} from '../src/routes.ts';
import {schemas} from '../src/schemas.ts';

test('Reading byte inspection uses media admission while DB-only progress stays browsing', () => {
  for (const [id, bodyLimit] of [['post_reading_sessions', 4096], ['put_reading_sessions_sessionId_progress', 8192]] as const) {
    const route = routes.find(route => route.id === id)!;
    assert.equal(route.lane, 'media');
    assert.equal(route.admission, 'media-body');
    assert.equal(route.requires, 'playback');
    assert.equal(route.bodyLimit, bodyLimit);
  }
  const progress = routes.find(route => route.id === 'get_reading_sessions_sessionId_progress')!;
  assert.equal(progress.admission, 'browsing');
  assert.equal(progress.requires, 'read');
});

test('Reading declares factual sequential PDF delivery without adding an archive download route', () => {
  const session = schemas.PostReadingSessionsResponse;
  assert.equal(session.required.includes('sequential'), false);
  assert.equal(session.properties.sequential.type, 'boolean');
  const source = routes.find(route => route.id === 'get_reading_sessions_sessionId_source')!;
  assert.equal(source.status, 206);
  assert.deepEqual(source.responseStatuses, [200, 416]);
  assert.equal(source.responseContentType, 'application/pdf');
  assert.deepEqual(source.headers, ['If-Match', 'Range']);
});
