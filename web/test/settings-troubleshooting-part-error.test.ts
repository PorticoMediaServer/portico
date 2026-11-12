import test from 'node:test';
import assert from 'node:assert/strict';
import {partError} from '../src/screens/settings/server/troubleshooting/index.tsx';

test('a Troubleshooting part keeps its own failure; only a signed-out answer or a lost role reaches the page', () => {
  const page: unknown[] = [];
  const own = partError(error => page.push(error));
  for (const error of [{status: 404, code: 'not_found'}, {status: 500}, new Error('network'), undefined]) own(error);
  assert.deepEqual(page, [], 'a missing route, a server error or a lost connection fails that part only');
  own({status: 401}); own({status: 403, code: 'forbidden'});
  assert.equal(page.length, 2);
});

test('a part that ends itself because its viewer may no longer read it still tells the page', async () => {
  // The reports and feedback parts retire themselves on a 401 or 403; the page must still hear of it (desk A, #3031).
  const {SettingsReportsSource} = await import('../src/screens/settings/server/troubleshooting/reports-source.ts');
  const {SettingsFeedbackSource} = await import('../src/screens/settings/server/troubleshooting/feedback-source.ts');
  const refused = (status: number) => ({request: async () => { throw Object.assign(new Error('no'), {status}); }}) as never;
  const page: unknown[] = [];
  const own = partError(error => page.push(error));
  const reports = new SettingsReportsSource(refused(403), 'server', 'owner', () => true, own);
  const feedback = new SettingsFeedbackSource(refused(401), 'server', 'owner', () => true, own);
  await Promise.all([reports.load(), feedback.load()]);
  assert.deepEqual(page.map(e => [(e as Error).message, (e as {status?: number}).status]),
    [['reports_authority_ended', 403], ['feedback_authority_ended', 401]], 'a refused part must not go blank in silence');
});

test('remembered playback problems keep their own failure; a lost sign-in still reaches the page', async () => {
  const {PlaybackProblemsSource} = await import('../src/screens/settings/server/troubleshooting/playback-problems-source.ts');
  const page: unknown[] = [];
  const failing = (status: number) => new PlaybackProblemsSource({transport: {request: async () => { throw Object.assign(new Error('no'), {status}); }} as never,
    isCurrent: () => true, subscribe: () => () => {}, reportError: error => page.push(error)});
  const missing = failing(404); await missing.load();
  assert.equal(missing.getSnapshot().phase, 'error', 'the part shows its own error');
  assert.equal(page.length, 0);
  await failing(401).load();
  assert.equal(page.length, 1);
});
