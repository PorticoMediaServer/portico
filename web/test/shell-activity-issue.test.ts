import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../packages/i18n/src/index.ts';
import {parseActivitySummary} from '@core/admin/activity-summary/index.ts';
import {activityIssueSettings, activityMajorIssue, activityPanelView} from '../src/shell/activity-model.ts';
import {homeFixtures as f} from '../../packages/i18n/src/catalog/en-US/next/dev/home-fixtures.ts';

const environment = {t: defaultI18n.t, locale: 'en-US', timeZone: 'UTC'};
const fence = 'a'.repeat(64);

/** The exact owner document shape the server publishes, one issue at a time. */
function document(issues: readonly unknown[]) {
  return {
    scope: {serverId: 'server', viewerFence: fence}, observedAt: '2026-10-24T12:00:00Z', serverName: f.server,
    streams: {count: 0, convertingCount: 0, overflow: {count: 0, convertingCount: 0}, items: []},
    scans: [], tasks: {count: 0, items: []},
    update: {available: false, state: 'current', current: '0.1.0'},
    health: {degraded: false}, majorIssues: issues, lastScanFinishedAt: '2026-10-24T11:00:00Z',
  };
}
const issue = (code: string, kind: string, severity: string, destination: {kind: string; target: string}, subject = '') => ({kind, code, severity, subject, destination});
function view(issues: readonly unknown[], openIssue: (value: {kind: string; target: string}) => void = () => {}) {
  return activityPanelView({phase: 'ready', document: parseActivitySummary(document(issues))}, f.server, environment, {onDashboard: () => {}, onRetry: () => {}, openUpdate: () => {}, openIssue});
}

test('a parsed qualified library issue becomes one row with approved words and its real destination', () => {
  const calls: {kind: string; target: string}[] = [];
  const issueView = view([issue('library_unreachable', 'library', 'playback_stop', {kind: 'library-source', target: 'source-1'}, 'Archive')], value => calls.push(value)).majorIssue;
  assert.equal(issueView?.title, "Archive can't be reached");
  assert.equal(issueView?.description, defaultI18n.t('next.shell.issue.libraryDetail'));
  assert.equal(issueView?.since, undefined);
  issueView!.onOpen();
  assert.deepEqual(calls, [{kind: 'library-source', target: 'source-1'}]);
});

test('every qualified code uses its approved copy and never a diagnostic guess', () => {
  const cases = [
    ['library_unreachable', 'library', 'playback_stop', {kind: 'library-source', target: 'source-1'}, 'Archive', 'next.shell.issue.libraryTitle', 'next.shell.issue.libraryDetail'],
    ['remote_unreachable', 'remote-source', 'playback_stop', {kind: 'remote-source', target: 'remote-1'}, 'Seedbox', 'next.shell.issue.remoteTitle', 'next.shell.issue.remoteDetail'],
    ['remote_program_unavailable', 'remote-source', 'playback_stop', {kind: 'remote-source', target: 'remote-1'}, '', 'next.shell.issue.remoteProgramTitle', 'next.shell.issue.remoteProgramDetail'],
    ['storage_nearly_full', 'storage', 'data_risk', {kind: 'storage', target: 'state'}, '', 'next.shell.issue.storageTitle', 'next.shell.issue.storageDetail'],
    ['backup_failed', 'backups', 'data_risk', {kind: 'backups', target: ''}, '', 'next.shell.issue.backupTitle', 'next.shell.issue.backupDetail'],
    ['secure_renewal_failed', 'networking', 'data_risk', {kind: 'networking', target: 'certificate'}, '', 'next.shell.issue.renewalTitle', 'next.shell.issue.renewalDetail'],
  ] as const;
  for (const [code, kind, severity, destination, subject, title, detail] of cases) {
    const row = view([issue(code, kind, severity, destination, subject)]).majorIssue;
    assert.equal(row?.title, defaultI18n.t(title, {subject}), code);
    assert.equal(row?.description, defaultI18n.t(detail), code);
  }
});

test('the server order selects the drawn row and later issues stay off the glance', () => {
  const first = issue('storage_nearly_full', 'storage', 'data_risk', {kind: 'storage', target: 'state'});
  const second = issue('library_unreachable', 'library', 'playback_stop', {kind: 'library-source', target: 'source-1'}, 'Archive');
  assert.equal(view([first, second]).majorIssue?.title, defaultI18n.t('next.shell.issue.storageTitle'));
});

test('no issue, an unknown code or an unready read never invents a row or a destination', () => {
  assert.equal(view([]).majorIssue, undefined);
  const unknown = {kind: 'library', code: 'future_issue', severity: 'playback_stop', subject: 'Archive', destination: {kind: 'library-source', target: 'source-1'}};
  assert.equal(activityMajorIssue(unknown, defaultI18n.t, () => {}), undefined);
  for (const phase of ['idle', 'loading', 'error', 'unavailable'] as const) {
    const stale = activityPanelView({phase, document: parseActivitySummary(document([issue('storage_nearly_full', 'storage', 'data_risk', {kind: 'storage', target: 'state'})]))}, f.server, environment,
      {onDashboard: () => {}, onRetry: () => {}, openUpdate: () => {}, openIssue: () => {}});
    assert.equal(stale.majorIssue, undefined);
  }
  assert.equal(activityMajorIssue(undefined, defaultI18n.t, () => {}), undefined);
});

test('a measured degraded state stays attention and is not relabelled as a qualified major issue', () => {
  const parsed = parseActivitySummary({...document([]), health: {degraded: true}});
  const ready = activityPanelView({phase: 'ready', document: parsed}, f.server, environment, {onDashboard: () => {}, onRetry: () => {}, openUpdate: () => {}, openIssue: () => {}});
  assert.equal(ready.health, 'attention');
  assert.equal(ready.majorIssue, undefined);
});

test('issue destinations reuse the existing Settings pages only', () => {
  assert.equal(activityIssueSettings('library-source'), 'libraries');
  assert.equal(activityIssueSettings('remote-source'), 'remote-sources');
  assert.equal(activityIssueSettings('storage'), 'storage');
  assert.equal(activityIssueSettings('backups'), 'storage');
  assert.equal(activityIssueSettings('networking'), 'remote');
  assert.equal(activityIssueSettings('future-destination'), undefined);
});
