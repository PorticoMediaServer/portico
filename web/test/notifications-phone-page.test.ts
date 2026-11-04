import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';

const route = readFileSync(new URL('../src/screens/account/Notifications.route.tsx', import.meta.url), 'utf8');

test('at phone width /notifications is phone-app 21: the bar with Mark all read, one view row, and a section per audience', () => {
  assert.match(route, /if \(narrow\) return <PhoneNotifications captured=\{captured\}\/>;/);
  assert.match(route, /<PushedNavBar safeArea=\{false\} backLabel=\{t\('next\.shell\.settings'\)\} title=\{t\('notifications\.title'\)\}/);
  assert.match(route, /trailing=\{<Button variant="text" size="small" className=\{styles\.markAll\} onClick=\{readAll\}>\{t\('notifications\.markAllRead'\)\}<\/Button>\}/);
  assert.match(route, /<Tabs phone className=\{styles\.phoneViews\} value=\{view\}/);
  assert.match(route, /section=\{\{audience: 'profile', view, title: t\('notifications\.audience\.profile'\)\}\}/);
  assert.match(route, /section=\{\{audience: 'account-admin', view, title: <>\{t\('notifications\.audience\.admin'\)\}<span>\{t\('next\.shell\.ownerOnly'\)\}<\/span><\/>\}\}/);
});

test('the Server section is its own inbox, for owners only, made on the page and disposed with it; Mark all read reads both', () => {
  assert.match(route, /const owner = context\.facts\.identity\.viewer\.role === 'owner' && sharedState\.audiences\.includes\('account-admin'\);/);
  assert.match(route, /const server = useMemo\(\(\) => owner \? new InboxService\(context\.api, undefined, \{queries: context\.freshness\.queries\}\) : undefined, \[owner, context\]\);/);
  assert.match(route, /useEffect\(\(\) => \(\) => \{server\?\.dispose\(\);\}, \[server\]\);/);
  assert.match(route, /for \(const \[inbox, snapshot\] of \[\[shared, sharedState\], \[server, serverState\]\] as const\)/);
  // A section's feed follows the inbox it was given, so the change hint refreshes the Server inbox too.
  assert.match(route, /service = given \?\? context\.inbox/);
});

test('Mark all read never drops a press on a busy inbox: the inbox holds it until it is free (phone and desktop)', () => {
  const route = readFileSync(new URL('../src/screens/account/Notifications.route.tsx', import.meta.url), 'utf8');
  assert.match(route, /if \(inbox && snapshot && snapshot\.phase !== 'idle'\) void inbox\.readAll\(\);/);
  assert.match(route, /onReadAll=\{\(\) => \{if \(state\.phase === 'ready' && state\.revision > 0\) opening\?\.run\(/);
  assert.doesNotMatch(route, /!snapshot\.busy\) void inbox\.readAll|!state\.busy\) opening\?\.run\(state, \(\) => \{void service\.readAll/);
});
