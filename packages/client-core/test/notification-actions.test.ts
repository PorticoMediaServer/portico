import test from 'node:test';
import assert from 'node:assert/strict';
import {notificationIntent} from '../src/notification-actions.ts';

test('the commands that run in place, and a value nobody knows', () => {
  assert.deepEqual(notificationIntent({kind: 'command', label: 'Dismiss', command: 'dismiss-conflict', arguments: {recordingId: 'r'}}), {kind: 'archive-notice'});
  assert.deepEqual(notificationIntent({kind: 'command', label: 'Scan again', command: 'run-scan', arguments: {libraryId: 'lib'}}), {kind: 'scan-library', libraryId: 'lib'});
  assert.deepEqual(notificationIntent({kind: 'command', label: 'Try again', command: 'retry-job', arguments: {downloadId: 'd'}}), {kind: 'downloads'});
  assert.equal(notificationIntent({kind: 'command', label: 'x', command: 'run-scan', arguments: {libraryId: '../other'}}), undefined, 'arguments are opaque ids');
  assert.equal(notificationIntent({kind: 'command', label: 'x', command: 'launch-rocket', arguments: {}}), undefined);
  assert.equal(notificationIntent({kind: 'navigate', label: 'x', target: {view: 'not-a-view'}}), undefined);
});

// "Your report has an update" → Open report: the reporter's own report, read only (no frame draws an editor).
test('Open report finds the reporter\'s own report among theirs and shows the owner\'s reply', async () => {
  const {readOwnFeedbackReport, ownFeedbackReportView} = await import('../src/notification-actions.ts');
  const {defaultI18n} = await import('../../i18n/src/index.ts');
  // The rebuilt server's report, second on the reporter's second page of reports.
  const data = {id: 'report-one', reporter: 'Alex', kind: 'problem', status: 'resolved', message: 'Playback stopped.', context: {about: 'playback/buffering'}, hasLog: false,
    reply: 'Fixed in the new build.', repliedAt: '2026-10-23T10:00:00Z', createdAt: '2026-10-22T18:20:00Z', updatedAt: '2026-10-23T10:00:00Z'};
  const paths: string[] = [];
  const report = await readOwnFeedbackReport({request: async <T,>(path: string) => {paths.push(path);
    return (path.includes('cursor=') ? {items: [{...data, id: 'other'}, data]} : {items: [{...data, id: 'newer'}], nextCursor: 'p2'}) as T;}}, 'report-one');
  assert.deepEqual(paths, ['/v1/reports', '/v1/reports?cursor=p2']);
  const view = ownFeedbackReportView(report, defaultI18n);
  assert.equal(view.title, defaultI18n.t('server.feedback.category.playback'));
  assert.match(view.meta, new RegExp(defaultI18n.t('server.feedback.status.resolved')));
  assert.equal(view.message, 'Playback stopped.');
  assert.deepEqual(view.replies.map(reply => reply.text), ['Fixed in the new build.']);
  await assert.rejects(readOwnFeedbackReport({request: async <T,>() => ({items: []}) as T}, 'gone'), {code: 'not_found'});
});
