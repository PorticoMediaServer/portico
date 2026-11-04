import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {SettingsWorkspace} from '../src/kit/settings/SettingsWorkspace.tsx';

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
  assert.match(router, /const settingsPaths = new Set\(\['\/settings', '\/dashboard'\]\);/);
  assert.match(router, /router\.subscribe\('onResolved', rememberOutsideSettings\);/);
  assert.match(router, /return outsideSettings \?\? '\/';/);
  const settings = readFileSync(new URL('../src/screens/settings/Settings.tsx', import.meta.url), 'utf8');
  assert.match(settings, /onDone=\{\(\) => \{void router\.navigate\(\{href: settingsReturnHref\(\)\}\);\}\}/);
  // The bar names the page on a phone; the heading stays for assistive technology.
  const css = readFileSync(new URL('../src/kit/settings/settings.module.css', import.meta.url), 'utf8');
  assert.match(css, /\.pageHeading h1 \{ position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset\(50%\);/);
});
