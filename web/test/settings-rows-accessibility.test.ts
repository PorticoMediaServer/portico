import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
import {settingsRowIndex, type SettingsRow} from '@core/presentation/settings-spec.ts';
import {transformTsx} from './transform-tsx.mjs';

registerHooks({load(url, context, next) {
  if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
  if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
  return next(url, context);
}});
const {SettingsRows} = await import('../src/kit/settings/SettingsRows.tsx');
const common = {change() {throw new Error('Rendering cannot save settings');}, reset() {throw new Error('Rendering cannot reset settings');},
  renderSpecial() {return null;}, advancedOpen: false, onAdvancedChange() {}};

test('a non-manager restriction value stays in the accessible tree', () => {
  const row: SettingsRow = {id: 'profile.allowDownloads', label: 'next.settings.on', control: {type: 'toggle'}};
  const html = renderToStaticMarkup(createElement(SettingsRows, {...common, holder: {groups: [{rows: [row]}]},
    state: () => ({present: true, value: false, displayValue: 'Downloads are off', label: 'Downloads', readOnly: true, disabled: true})}));
  assert.match(html, /Downloads are off/);
  assert.match(html, /aria-disabled="true"/);
  assert.doesNotMatch(html, /\binert=|aria-hidden="true"/);
  assert.doesNotMatch(html, /<input/);
});

test('saving and disabled matrix rows remain readable while their native controls stay guarded', () => {
  const row = settingsRowIndex.get('playback.skipping')?.row;
  assert.ok(row);
  const html = renderToStaticMarkup(createElement(SettingsRows, {...common, holder: {groups: [{rows: [row]}]},
    state: () => ({present: true, value: {intro: 'off', credits: 'ask', recap: 'ask'}, disabled: true, saving: true})}));
  assert.match(html, /aria-busy="true"/);
  assert.match(html, /<fieldset[^>]*disabled=""/);
  const selected = html.match(/<input[^>]*type="radio"[^>]*value="off"[^>]*>/)?.[0];
  assert.ok(selected);
  assert.match(selected, /checked=""/);
  assert.doesNotMatch(html, /\binert=/);
});

test('the conversion folder stays available without a location-mode choice', () => {
  const row = settingsRowIndex.get('streaming.transcodeFolder')!.row;
  assert.equal(settingsRowIndex.has('streaming.transcodeLocation'), false);
  assert.equal(settingsRowIndex.has('streaming.memoryCap'), false);
  const html = renderToStaticMarkup(createElement(SettingsRows, {...common, holder: {groups: [{rows: [row]}]}, state: () => ({present: true, value: '/srv/transcode'})}));
  assert.match(html, /id="streaming.transcodeFolder"/);
  assert.doesNotMatch(html, /id="streaming.transcodeFolder"[^>]*aria-disabled="true"/);
});
