import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {SettingsWorkspace} from '../src/kit/settings/SettingsWorkspace.tsx';
import {rememberSettingsReturn, settingsReturnAuthority, settingsReturnHref, trackSettingsReturn} from '../src/shell/settings-return.ts';

test('a phone settings page is a full page: "‹ Settings", its title and Done (owner-libraries 24a)', () => {
  const html = renderToStaticMarkup(createElement(SettingsWorkspace, {scope: 'server', navigation: null, compactPage: true, onBack: () => {}, title: 'Libraries', onDone: () => {}}, 'body'));
  assert.match(html, />Settings</);
  assert.match(html, /Libraries/);
  assert.match(html, /<button type="button" class="[^"]*compactDone[^"]*">Done<\/button>/);
  const without = renderToStaticMarkup(createElement(SettingsWorkspace, {scope: 'server', navigation: null, onBack: () => {}}, 'body'));
  assert.doesNotMatch(without, />Done</);
});

test('Done returns to the page Settings was opened from, remembered by the router, or Home', () => {
  const router = readFileSync(new URL('../src/router.tsx', import.meta.url), 'utf8');
  assert.match(router, /trackSettingsReturn\(\(\) => router\.state\.location\);/);
  assert.match(router, /router\.subscribe\('onResolved', rememberSettingsReturn\);/);
  const bootstrap = readFileSync(new URL('../src/shell/bootstrap.ts', import.meta.url), 'utf8');
  assert.match(bootstrap, /settingsReturnAuthority\(viewer && JSON\.stringify\(\[viewer\.serverId, viewer\.authority, viewer\.accountId, viewer\.profileId\]\), next\.phase === 'signed-out'\);/);
  const settings = readFileSync(new URL('../src/screens/settings/Settings.tsx', import.meta.url), 'utf8');
  assert.match(settings, /onDone=\{\(\) => \{void router\.navigate\(\{href: settingsReturnHref\(\)\}\);\}\}/);
  // The bar names the page on a phone; the heading stays for assistive technology.
  const css = readFileSync(new URL('../src/kit/settings/settings.module.css', import.meta.url), 'utf8');
  assert.match(css, /\.pageHeading h1 \{ position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset\(50%\);/);
});

test('the remembered page belongs to its authority: a server or profile switch, or a sign-out, sends Done Home', () => {
  let at = {pathname: '/movies', href: '/movies?view=grid'};
  trackSettingsReturn(() => at);
  const go = (pathname: string, href = pathname) => {at = {pathname, href}; rememberSettingsReturn();};
  const serverA = JSON.stringify(['server-a', 'local', 'account', 'main']), serverB = JSON.stringify(['server-b', 'local', 'account', 'main']);
  const kids = JSON.stringify(['server-a', 'local', 'account', 'kids']);

  // The page the app was opened on is remembered once the shell is running; Settings itself never is.
  settingsReturnAuthority(serverA);
  assert.equal(settingsReturnHref(), '/movies?view=grid');
  go('/settings', '/settings?scope=server&section=libraries');
  assert.equal(settingsReturnHref(), '/movies?view=grid');
  go('/dashboard');
  assert.equal(settingsReturnHref(), '/movies?view=grid');

  // A reconnect under the same authority keeps it; pages seen while restoring are not remembered.
  settingsReturnAuthority(undefined);
  go('/');
  settingsReturnAuthority(serverA);
  assert.equal(settingsReturnHref(), '/movies?view=grid');

  // A server switch forgets it: the old server's page is not this server's.
  go('/library/old-server-library'); go('/settings');
  settingsReturnAuthority(undefined); settingsReturnAuthority(serverB);
  assert.equal(settingsReturnHref(), '/');

  // A profile switch forgets it.
  go('/library/server-b-library'); go('/settings');
  assert.equal(settingsReturnHref(), '/library/server-b-library');
  settingsReturnAuthority(undefined); settingsReturnAuthority(kids);
  assert.equal(settingsReturnHref(), '/');

  // A sign-out forgets it, even when the same account and profile sign in again.
  go('/live'); go('/settings');
  assert.equal(settingsReturnHref(), '/live');
  settingsReturnAuthority(undefined, true);
  assert.equal(settingsReturnHref(), '/');
  settingsReturnAuthority(kids);
  assert.equal(settingsReturnHref(), '/');
});
