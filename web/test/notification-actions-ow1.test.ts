import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {notificationDestination} from '../src/screens/account/notification-destination.ts';

// OW-1: notice actions the server publishes were drawn disabled on web. Each now opens its page or runs in place.
const libraries = [{id: 'lib-1'}];
const nav = (view: string, extra: Record<string, string> = {}) => ({kind: 'navigate' as const, label: view, target: {view, ...extra}});
const cmd = (command: string, args: Record<string, string>) => ({kind: 'command' as const, label: command, command, arguments: args});

test('administrators\' notices open their server pages; a member gets none of them', () => {
  assert.deepEqual(notificationDestination(nav('settings-network'), libraries, true), {view: 'settings', scope: 'server', section: 'remote'});
  assert.deepEqual(notificationDestination(nav('settings-storage'), libraries, true), {view: 'settings', scope: 'server', section: 'storage'});
  assert.deepEqual(notificationDestination(nav('admin-feedback', {entityId: 'rep-1'}), libraries, true), {view: 'settings', scope: 'server', section: 'troubleshooting'});
  for (const view of ['settings-network', 'settings-storage', 'admin-feedback', 'admin-devices']) assert.equal(notificationDestination(nav(view), libraries, false), undefined, view);
});

test('a recording conflict opens Recordings, and Dismiss archives the notice', () => {
  assert.deepEqual(notificationDestination(nav('recordings', {entityId: 'rec-1'}), libraries, true), {view: 'live', tab: 'recordings'});
  assert.deepEqual(notificationDestination(cmd('dismiss-conflict', {recordingId: 'rec-1'}), libraries, true), {view: 'archive'});
});

test('Scan again starts the scan of a library this profile can see', () => {
  assert.deepEqual(notificationDestination(cmd('run-scan', {libraryId: 'lib-1'}), libraries, true), {view: 'scan', libraryId: 'lib-1'});
  assert.equal(notificationDestination(cmd('run-scan', {libraryId: 'lib-9'}), libraries, true), undefined, 'a library this profile can\'t see');
  assert.equal(notificationDestination(cmd('run-scan', {libraryId: 'lib-1'}), libraries, false), undefined, 'not an administrator');
});

test('a failed download\'s Try again and a finished download\'s Open go to Downloads', () => {
  assert.deepEqual(notificationDestination(cmd('retry-job', {downloadId: 'dl-1'}), libraries, false), {view: 'downloads'});
  assert.deepEqual(notificationDestination(cmd('open-download', {downloadId: 'dl-1'}), libraries, false), {view: 'downloads'});
});

test('the page runs Dismiss and Scan again in place, through the notice\'s own service', () => {
  const source = readFileSync(new URL('../src/screens/account/Notifications.route.tsx', import.meta.url), 'utf8');
  assert.match(source, /if \(target\.view === 'archive'\) \{apply\('archive', note\); return;\}/);
  assert.match(source, /startNotificationScan\(context\.api, target\.libraryId\)\.then\(\(\) => service\.apply\('read', \[note\.id\]\)\)/);
});

test('Open report on "Your report has an update" opens the reporter\'s own report, read only, over the notice', () => {
  assert.deepEqual(notificationDestination(cmd('open-feedback', {reportId: 'rep-1'}), libraries, false), {view: 'own-report', reportId: 'rep-1'});
  const source = readFileSync(new URL('../src/screens/account/Notifications.route.tsx', import.meta.url), 'utf8');
  assert.match(source, /if \(target\.view === 'own-report'\) \{opening\.run\(state, \(\) => \{setReport\(target\.reportId\); void service\.apply\('read', \[note\.id\]\);\}\); return;\}/);
});
