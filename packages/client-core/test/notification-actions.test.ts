import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {notificationIntent} from '../src/notification-actions.ts';
import {notificationCommands} from '../src/notifications.ts';

// OW-1 (review sweep, 2 Nov): seven published notice actions did nothing on web or native. Every command and view the
// server's producers publish now has one shared meaning; the producers file is read so a new action can't slip by.

const producers = readFileSync(new URL('../../../server/internal/notify/producers.go', import.meta.url), 'utf8');
const published = {
  commands: [...new Set([...producers.matchAll(/Command: "([a-z-]+)", Arguments: map\[string\]string\{([^}]*)\}/g)].map(m => JSON.stringify([m[1], [...m[2].matchAll(/"([A-Za-z]+)":/g)].map(a => a[1])])))].map(v => JSON.parse(v) as [string, string[]]),
  views: [...new Set([...producers.matchAll(/View: "([a-z-]+)"/g)].map(m => m[1]!))],
};

test('every command a producer publishes has a meaning, given its documented argument', () => {
  assert.ok(published.commands.length >= 5, 'the producers file was read');
  for (const [command, keys] of published.commands) {
    const args = Object.fromEntries(keys.map(key => [key, 'id-1'])), action = {kind: 'command' as const, label: command, command, arguments: args};
    // A preparation notice is read with its notice (Root33: the notice and its action must agree).
    const note = keys.includes('preparationId') ? {source: 'downloads', category: 'download.prepared', arguments: {preparationId: 'id-1'}, actions: [action]} as never : undefined;
    assert.ok(notificationIntent(action, note), command);
    assert.equal(notificationIntent({kind: 'command', label: command, command, arguments: {}}), undefined, `${command} without its argument`);
  }
  for (const command of notificationCommands) assert.ok(published.commands.some(([name]) => name === command) || command === 'open-download', `${command} is published`);
});

test('every view a producer publishes has a meaning', () => {
  assert.ok(published.views.length >= 6, 'the producers file was read');
  for (const view of published.views) assert.ok(notificationIntent({kind: 'navigate', label: view, target: {view, entityId: 'id-1', libraryId: 'lib-1'}}), view);
});

test('the commands that run in place, and a value nobody knows', () => {
  assert.deepEqual(notificationIntent({kind: 'command', label: 'Dismiss', command: 'dismiss-conflict', arguments: {recordingId: 'r'}}), {kind: 'archive-notice'});
  assert.deepEqual(notificationIntent({kind: 'command', label: 'Scan again', command: 'run-scan', arguments: {libraryId: 'lib'}}), {kind: 'scan-library', libraryId: 'lib'});
  assert.deepEqual(notificationIntent({kind: 'command', label: 'Try again', command: 'retry-job', arguments: {downloadId: 'd'}}), {kind: 'downloads'});
  assert.equal(notificationIntent({kind: 'command', label: 'x', command: 'run-scan', arguments: {libraryId: '../other'}}), undefined, 'arguments are opaque ids');
  assert.equal(notificationIntent({kind: 'command', label: 'x', command: 'launch-rocket', arguments: {}}), undefined);
  assert.equal(notificationIntent({kind: 'navigate', label: 'x', target: {view: 'not-a-view'}}), undefined);
});

// "Your report has an update" → Open report: the reporter's own report, read only (no frame draws an editor).
test('Open report reads the reporter\'s own report and shows the owner\'s replies, oldest first', async () => {
  const {readOwnFeedbackReport, ownFeedbackReportView} = await import('../src/notification-actions.ts');
  const {defaultI18n} = await import('../../i18n/src/index.ts');
  const data = {id: 'report-one', cursor: '1', revision: 3, status: 'resolved', kind: 'playback', category: 'buffering', message: 'Playback stopped.', createdAt: '2026-10-22T18:20:00Z', updatedAt: '2026-10-23T10:00:00Z',
    expiresAt: '2027-04-20T18:20:00Z', reporter: {name: 'Alex', authority: 'local', role: 'member', self: true}, diagnostics: {decision: 'not-requested'}, duplicates: 0,
    thread: [{sequence: 2, at: '2026-10-23T10:00:00Z', revision: 3, status: 'resolved', reply: 'Fixed in the new build.', actorClass: 'owner'},
      {sequence: 1, at: '2026-10-22T19:00:00Z', revision: 2, status: 'in-progress', reply: 'Looking into it.', actorClass: 'owner'},
      {sequence: 3, at: '2026-10-23T11:00:00Z', revision: 4, status: 'resolved', reply: '  ', actorClass: 'owner'}]};
  const paths: string[] = [];
  const report = await readOwnFeedbackReport({request: async <T,>(path: string) => {paths.push(path); return {data, scope: {serverId: 's', viewerFence: 'f'.repeat(64)}} as T;}}, 'report-one');
  assert.deepEqual(paths, ['/v1/feedback/reports/report-one']);
  const view = ownFeedbackReportView(report, defaultI18n);
  assert.equal(view.title, defaultI18n.t('server.feedback.category.playback'));
  assert.match(view.meta, new RegExp(defaultI18n.t('server.feedback.status.resolved')));
  assert.equal(view.message, 'Playback stopped.');
  assert.deepEqual(view.replies.map(reply => reply.text), ['Looking into it.', 'Fixed in the new build.'], 'empty status-only steps are not replies');
});
