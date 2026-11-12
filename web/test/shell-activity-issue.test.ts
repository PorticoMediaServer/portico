import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../packages/i18n/src/index.ts';
import {parseActivitySummary} from '@core/admin/activity-summary/index.ts';
import {activityIssueSettings, activityMajorIssue, activityPanelView} from '../src/shell/activity-model.ts';
import {homeFixtures as f} from '../../packages/i18n/src/catalog/en-US/next/dev/home-fixtures.ts';

const environment = {t: defaultI18n.t, locale: 'en-US', timeZone: 'UTC'};
/** The owner summary as the rebuilt server publishes it: its major alerts, most urgent first. */
function document(alerts: readonly unknown[]) {
  return {
    observedAt: '2026-10-24T12:00:00Z', serverName: f.server,
    streams: {count: 0, converting: 0, items: []}, scans: [], tasks: {count: 0, items: []},
    alerts, lastScanAt: '2026-10-24T11:00:00Z',
  };
}
/** A major alert in the Dashboard's shape; the library or source it is about is in its params, as the server sends it. */
const issue = (code: string, destination: {kind: string; target: string}, params: Record<string, string> = {}) => ({code, severity: 'major', major: true, params, action: destination});
function view(issues: readonly unknown[], openIssue: (value: {kind: string; target: string}) => void = () => {}) {
  return activityPanelView({phase: 'ready', document: parseActivitySummary(document(issues))}, f.server, environment, {onDashboard: () => {}, onRetry: () => {}, openUpdate: () => {}, openIssue});
}

test('a parsed qualified library issue becomes one row with approved words and its real destination', () => {
  const calls: {kind: string; target: string}[] = [];
  const issueView = view([issue('library_unreachable', {kind: 'library-source', target: 'source-1'}, {library: 'Archive'})], value => calls.push(value)).majorIssue;
  assert.equal(issueView?.title, "Archive can't be reached");
  assert.equal(issueView?.description, defaultI18n.t('next.shell.issue.libraryDetail'));
  assert.equal(issueView?.since, undefined);
  issueView!.onOpen();
  assert.deepEqual(calls, [{kind: 'library-source', target: 'source-1'}]);
});

test('every qualified code uses its approved copy and never a diagnostic guess', () => {
  const cases = [
    ['library_unreachable', {kind: 'library-source', target: 'source-1'}, {library: 'Archive'}, 'Archive', 'next.shell.issue.libraryTitle', 'next.shell.issue.libraryDetail'],
    ['remote_unreachable', {kind: 'remote-source', target: 'remote-1'}, {source: 'Seedbox', reason: 'unreachable', problem: 'timed out'}, 'Seedbox', 'next.shell.issue.remoteTitle', 'next.shell.issue.remoteDetail'],
    ['remote_program_unavailable', {kind: 'remote-source', target: 'remote-1'}, {}, '', 'next.shell.issue.remoteProgramTitle', 'next.shell.issue.remoteProgramDetail'],
    ['storage_nearly_full', {kind: 'storage', target: ''}, {freeBytes: '1000'}, '', 'next.shell.issue.storageTitle', 'next.shell.issue.storageDetail'],
    ['backup_failed', {kind: 'backups', target: ''}, {reason: 'disk full'}, '', 'next.shell.issue.backupTitle', 'next.shell.issue.backupDetail'],
    ['secure_renewal_failed', {kind: 'networking', target: 'certificate'}, {}, '', 'next.shell.issue.renewalTitle', 'next.shell.issue.renewalDetail'],
  ] as const;
  for (const [code, destination, params, subject, title, detail] of cases) {
    const row = view([issue(code, destination, params)]).majorIssue;
    assert.equal(row?.title, defaultI18n.t(title, {subject}), code);
    assert.equal(row?.description, defaultI18n.t(detail), code);
  }
});

test('the server order selects the drawn row and later issues stay off the glance', () => {
  const first = issue('storage_nearly_full', {kind: 'storage', target: ''});
  const second = issue('library_unreachable', {kind: 'library-source', target: 'source-1'}, {library: 'Archive'});
  assert.equal(view([first, second]).majorIssue?.title, defaultI18n.t('next.shell.issue.storageTitle'));
});

test('no issue or an unready read never invents a row or a destination', () => {
  assert.equal(view([]).majorIssue, undefined);
  for (const phase of ['idle', 'loading', 'error', 'unavailable'] as const) {
    const stale = activityPanelView({phase, document: parseActivitySummary(document([issue('storage_nearly_full', {kind: 'storage', target: ''})]))}, f.server, environment,
      {onDashboard: () => {}, onRetry: () => {}, openUpdate: () => {}, openIssue: () => {}});
    assert.equal(stale.majorIssue, undefined);
  }
  assert.equal(activityMajorIssue(undefined, defaultI18n.t, () => {}), undefined);
});

test('unknown and data-folder alerts stay visible and open the Dashboard without issuing a command', () => {
  const dashboard: unknown[] = [], commands: unknown[] = [];
  for (const code of ['future_issue', 'data_folder_readable']) {
    const parsed = parseActivitySummary(document([issue(code, {kind: 'fix-permissions', target: ''}, {message: 'Move the data folder to another drive.'})]));
    const ready = activityPanelView({phase: 'ready', document: parsed}, f.server, environment,
      {onDashboard: section => dashboard.push(section), onRetry: () => {}, openUpdate: () => {}, openIssue: value => commands.push(value)});
    assert.equal(ready.health, 'attention');
    assert.equal(ready.majorIssue?.title, code === 'future_issue' ? 'Move the data folder to another drive.' : defaultI18n.t('next.dashboard.permissionsAlert'));
    ready.majorIssue!.onOpen();
  }
  assert.deepEqual(dashboard, [undefined, undefined]);
  assert.deepEqual(commands, []);
  assert.equal(view([]).health, 'normal');
});

test('issue destinations reuse the existing Settings pages only', () => {
  assert.equal(activityIssueSettings('library-source'), 'libraries');
  assert.equal(activityIssueSettings('remote-source'), 'remote-sources');
  assert.equal(activityIssueSettings('storage'), 'storage');
  assert.equal(activityIssueSettings('backups'), 'storage');
  assert.equal(activityIssueSettings('networking'), 'remote');
  assert.equal(activityIssueSettings('future-destination'), undefined);
});
