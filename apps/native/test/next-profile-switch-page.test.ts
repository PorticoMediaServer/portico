import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';

function nodes(value: any): any[] {
  if (Array.isArray(value)) return value.flatMap(nodes);
  return value?.props ? [value, ...nodes(value.props.children)] : [];
}

async function page(platform: 'phone' | 'tablet', profile: {pinRequired: boolean}) {
  const {ProfileSwitchPage} = await screenModule(new URL('../src-next/pages/touch/accounts/ProfileSwitchPage.tsx', import.meta.url), {
    react: {...React, default: React},
    '../../../../../../packages/i18n/src': {defaultI18n: {t: (key: string, values?: Record<string, unknown>) => values ? `${key}:${JSON.stringify(values)}` : key}},
    '../../../kit/accounts/AccountParts': {AccountNotice: 'AccountNotice'}, '../../../kit/artwork': {ProfilePicker: 'ProfilePicker'},
    '../../../kit/controls': {Button: 'Button', FormError: 'FormError'},
    '../../../kit/frames': {SectionCard: 'SectionCard', SettingsPageFrame: 'SettingsPageFrame', SheetFrame: 'SheetFrame'},
    '../../../kit/shell': {SettingsRow: 'SettingsRow'}, '../../../kit/theme': {useTheme: () => ({platform})},
  });
  const active = {id: 'demo', name: 'Demo', art: 'blue', primary: true, pinRequired: profile.pinRequired};
  const opened: string[] = [];
  const view: any = {active, current: {viewer: {profileId: 'demo'}}, manage: true, lease: {}, profiles: [active], busy: false, backLabel: 'next.shell.settings',
    back() {}, select() {}, openAddition() {}, openEditor: (kind: string) => opened.push(kind), avatarSource: () => undefined,
    localPictures: true, picturePicker: true, hasPicture: () => false, pickPicture: () => opened.push('picture'), removePicture() {}, problem: '', notice: ''};
  return {tree: ProfileSwitchPage({view}), opened};
}

test('Profiles is phone-app 20: Settings back in a sheet, the intro, Add with its limit, then the current profile\'s rows in the frame\'s order', async () => {
  const {tree, opened} = await page('phone', {pinRequired: false});
  assert.equal(tree.type, 'SheetFrame');
  const frame = nodes(tree).find(node => node.type === 'SettingsPageFrame');
  assert.equal(frame.props.back.label, 'next.shell.settings'); assert.equal(frame.props.intro, 'profiles.intro');
  assert.equal(nodes(tree).find(node => node.type === 'ProfilePicker').props.addNote, 'profile.addLimit:{"count":8}');
  const card = nodes(tree).find(node => node.type === 'SectionCard' && node.props.label);
  assert.equal(card.props.label, 'Demo · profile.main');
  const rows = nodes(card).filter(node => node.type === 'SettingsRow');
  assert.deepEqual(rows.map(row => row.props.testID), ['profile-rename', 'profile-picture', 'profile-pin', 'profile-limits', 'profile-forget-devices']);
  assert.equal(rows[2].props.description, 'profile.setPinHelp:{"name":"Demo"}');
  assert.ok(rows.every(row => row.props.navigates));
  rows.forEach(row => row.props.onPress());
  assert.deepEqual(opened, ['rename', 'picture', 'pin', 'limits', 'forget']);
});

test('a profile with a PIN offers Change and Remove PIN without the help; a tablet draws the page on its own', async () => {
  const {tree} = await page('tablet', {pinRequired: true});
  assert.equal(tree.type, 'SettingsPageFrame');
  const ids = nodes(tree).filter(node => node.type === 'SettingsRow').map(row => row.props.testID);
  assert.deepEqual(ids, ['profile-rename', 'profile-picture', 'profile-pin', 'profile-remove-pin', 'profile-limits', 'profile-forget-devices']);
  assert.equal(nodes(tree).find(node => node.props.testID === 'profile-pin').props.description, undefined);
});
