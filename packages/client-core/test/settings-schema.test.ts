import test from 'node:test';
import assert from 'node:assert/strict';
import {indexSettingsSpec, type SettingsSection, type SettingsRow, type SettingsOperatingSystem} from '../src/presentation/settings-spec.ts';

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
