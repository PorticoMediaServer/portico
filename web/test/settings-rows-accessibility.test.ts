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

test('memory preference remains selectable with an unavailable note and an independent disk folder', async () => {
  const {hardwareRowState} = await import('../src/screens/settings/hardware/hardware-rows.ts');
  const row = settingsRowIndex.get('streaming.transcodeLocation')!.row;
  assert.equal(row.control.default, 'memory');
  assert.deepEqual(row.control.options?.map(option => option.value), ['memory', 'disk']);
  assert.equal(row.children?.[0].enableWhen, undefined);
  for (const memoryAvailable of [false, true]) for (const stored of ['memory', 'disk', undefined]) {
    const snapshot = {phase: 'ready', capacity: {hardware: {devices: []}, temporaryDirectory: {
      path: '/srv/transcode', ready: true, freeBytes: null, fallback: false, memoryPath: '/dev/shm', memoryAvailable,
    }}} as any;
    const state = hardwareRowState(row.id, snapshot, stored)!;
    assert.equal(state.value, stored ?? 'memory');
    assert.equal(state.defaultValue, 'memory');
    assert.equal(state.help, memoryAvailable ? undefined : 'Memory is unavailable on this server.');
    assert.ok(state.choices?.every(option => !option.locked));
    const changed: unknown[] = [];
    const props = {...common, holder: {groups: [{rows: [row]}]}, state: (entry: SettingsRow) => entry.id === row.id ? state : {present: true, value: '/srv/transcode'},
      change: (_row: SettingsRow, value: unknown) => {changed.push(value);}};
    const controls = (node: any): any[] => Array.isArray(node) ? node.flatMap(controls)
      : !node?.props ? [] : typeof node.type === 'function' && ['Group', 'Row'].includes(node.type.name)
        ? controls(node.type(node.props)) : [node, ...controls(node.props.children)];
    const select = controls(SettingsRows(props)).find(node => node.props.options?.some((option: any) => option.value === 'memory'));
    assert.ok(select);
    assert.equal(select.props.value, stored ?? 'memory');
    assert.equal(select.props.options.find((option: any) => option.value === 'memory').disabled, false);
    select.props.onValueChange('memory');select.props.onValueChange('disk');
    assert.deepEqual(changed, ['memory', 'disk']);
    const html = renderToStaticMarkup(createElement(SettingsRows, props));
    if (!memoryAvailable) assert.match(html, /Memory is unavailable on this server\./);
    assert.match(html, /id="streaming.transcodeFolder"/);
    assert.doesNotMatch(html, /id="streaming.transcodeFolder"[^>]*aria-disabled="true"/);
  }
});
