import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {RecordingsLayout} from '../src/kit/dvr/RecordingsLayout.tsx';

const css = readFileSync(new URL('../src/kit/dvr/rules.module.css', import.meta.url), 'utf8');

test('Recordings carries phone-app 12\'s back bar: "‹ Live TV" and its title', () => {
  const html = renderToStaticMarkup(createElement(RecordingsLayout, {title: 'Live TV', sourceLabel: 'Source', allChannels: 'All channels', sourceName: 'Demo',
    back: {label: 'Live TV', title: 'Recordings', onBack: () => {}}}, 'body'));
  assert.match(html, /class="[^"]*phoneNav[^"]*"><header[^>]*><button type="button">.*Live TV<\/button><span[^>]*>Recordings<\/span>/);
  const without = renderToStaticMarkup(createElement(RecordingsLayout, {title: 'Live TV', sourceLabel: 'Source', allChannels: 'All channels', sourceName: 'Demo'}, 'body'));
  assert.doesNotMatch(without, /phoneNav/);
});

test('on a phone the back bar replaces the Live TV head and the Guide | Recordings tabs; wider, it is hidden', () => {
  assert.match(css, /^\.phoneNav \{ display: none; \}/m);
  const phone = css.slice(css.lastIndexOf('@media (max-width: 599px) {'));
  assert.match(phone, /\.page:has\(> \.phoneNav\) > :is\(\.header, \.tabs\) \{ display: none; \}/);
  assert.match(phone, /\.phoneNav \{ display: block;/);
  for (const file of ['../src/screens/live/RecordingsScreen.tsx', '../src/screens/live/SeriesRules.tsx']) {
    assert.match(readFileSync(new URL(file, import.meta.url), 'utf8'), /back=\{[^}]*label: t\('next\.home\.shell\.live'\), title: t\('recordings\.title'\), onBack: (goGuide|onGuide)\}/, file);
  }
});
