import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../i18n/src/index.ts';
import {indexSettingsSpec, settingsRowIndex, type SettingsSection, type SettingsRow, type SettingsOperatingSystem} from '../src/presentation/settings-spec.ts';

const label = 'action.save' as const;
const child: SettingsRow = {id: 'child', label, control: {type: 'text'}, os: ['android'], shapes: ['phone']};
const parent: SettingsRow = {id: 'parent', label, control: {type: 'toggle'}, children: [child]};
const section: SettingsSection = {
  id: 'schema-test', scope: 'account', title: label, navGroup: 'test', icon: 'user', kind: 'settings', configOnly: [],
  groups: [{rows: [parent]}],
  panels: {edit: {tabs: [{id: 'advanced', title: label, groups: [{rows: [{id: 'panel', label, control: {type: 'table'}}]}]}]}},
};

test('Settings schema retains each ancestor and panel location without leaking page ancestry', () => {
  const locations = indexSettingsSpec([section]);
  assert.deepEqual(locations.get('parent')?.ancestors, []);
  assert.deepEqual(locations.get('child')?.ancestors, [parent]);
  assert.equal(locations.get('child')?.row, child);
  const panel = locations.get('panel');
  assert.deepEqual(panel?.ancestors, []);
  assert.equal(panel?.panelId, 'edit');
  assert.equal(panel?.tabId, 'advanced');
  assert.equal(panel?.advanced, true);
  const os: SettingsOperatingSystem = 'android';
  assert.deepEqual(child.os, [os]);
});

test('Settings schema rejects ambiguous row identity across page and panel locations', () => {
  assert.throws(() => indexSettingsSpec([{...section, panels: {edit: {groups: [{rows: [child]}]}}}]), /Duplicate Settings row: child/);
});


test('Sign-in offers the sign-in lifetime as the default alongside the existing PIN choices', () => {
  const control = settingsRowIndex.get('signin.pinRemember')?.row.control;
  assert.equal(control?.default, '-1');
  assert.deepEqual(control?.options?.map(option => option.value), ['0', '7', '30', '90', '-1']);
  assert.equal(defaultI18n.t(control!.options!.at(-1)!.label), 'As long as this device stays signed in');
});
