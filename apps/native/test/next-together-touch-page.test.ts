import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';

function nodes(value: any): any[] {
  if (Array.isArray(value)) return value.flatMap(nodes);
  if (!value?.props) return [];
  // Expand the page's own inner pages so the test reads what they draw.
  if (typeof value.type === 'function') return [value, ...nodes(value.type(value.props))];
  return [value, ...nodes(value.props.children)];
}

async function page(lobby: Record<string, unknown>) {
  const opened: string[] = [];
  const {TogetherPage} = await screenModule(new URL('../src-next/pages/touch/together/TogetherPage.tsx', import.meta.url), {
    react: {...React, default: React},
    '../../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key, clock: String, time: String}},
    '../../../../../../packages/client-core/src/room-protocol': {roomEntry: () => undefined, roomPositionMs: () => 0},
    '../../../kit/controls': {Button: 'Button', Segmented: 'Segmented', TextField: 'TextField'},
    '../../../kit/frames': {SectionCard: 'SectionCard', SettingsPageFrame: 'SettingsPageFrame', SheetFrame: 'SheetFrame'},
    '../../../kit/overlays': {Banner: 'Banner', Dialog: 'Dialog', Menu: 'Menu'}, '../../../kit/overlays/surface': {OverlayFootRow: 'OverlayFootRow'},
    '../../../kit/shell': {SettingsRow: 'SettingsRow'},
    '../../../kit/together/TogetherParts': {TogetherActions: 'TogetherActions', TogetherFaces: 'TogetherFaces', TogetherInvitation: 'TogetherInvitation',
      TogetherInviteCode: 'TogetherInviteCode', TogetherMemberRow: 'TogetherMemberRow', TogetherNowCard: 'TogetherNowCard'},
    '../../../kit/theme': {useTheme: () => ({platform: 'phone'})},
    '../../../screens/settings-live/group-port': {groupPort: () => undefined},
    '../../../screens/settings-live/together-presentation': {memberNames: () => ({primary: ''})},
    '../../shared/together/useTogetherLobby': {useTogetherLobby: () => ({...lobby, openForm: (kind: string) => opened.push(kind)}),
      useTogetherLobbyArt: () => undefined},
    '../../shared/together/useTogetherRoom': {useTogetherRoom: () => ({})},
    '../../shared/together/share': {shareText: async () => {}},
  });
  const props = {controller: {}, ports: {}, runtime: {}, page: {}, media: {}, onBack() {}};
  return {tree: TogetherPage(props), opened};
}

const lobby = {state: {directory: [{id: 'g1', name: 'Movie night', members: 3, host: true}], busy: false}, room: false, form: undefined, sheet: undefined,
  busy: false, faces: [], name: '', code: '', who: 'Demo', authority: 'host', codeFailure: false, viewCurrent: () => true, back() {}, run() {}, enter() {},
  closeForm() {}, setName() {}, setCode() {}, setWho() {}, setAuthority() {}};

test('Watch Together on a phone is a page in the Settings sheet: the invitation with Start a group and Join with a code, then the groups you\'re in', async () => {
  const {tree, opened} = await page(lobby);
  assert.equal(tree.type, 'SheetFrame');
  const all = nodes(tree);
  const frame = all.find(node => node.type === 'SettingsPageFrame');
  assert.equal(frame.props.back.label, 'next.shell.settings');
  assert.equal(all.find(node => node.type === 'TogetherInvitation').props.title, 'web.together.tagline');
  all.find(node => node.props.testID === 'together-start').props.onPress();
  all.find(node => node.props.testID === 'together-join').props.onPress();
  assert.deepEqual(opened, ['start', 'join']);
  const group = all.find(node => node.props.testID === 'together-group-g1');
  assert.equal(group.props.label, 'Movie night'); assert.equal(group.props.value, 'web.together.youHost'); assert.equal(group.props.navigates, true);
  assert.equal(all.some(node => node.props.testID === 'together-form'), false);
});

test('Join with a code is the kit task sheet, and Join waits for an eight-character code and a name', async () => {
  const tree = async (code: string) => nodes((await page({...lobby, sheet: 'join', form: {kind: 'join'}, code})).tree)
    .find(node => node.props.testID === 'together-form');
  const short = await tree('KQ7M');
  assert.equal(short.props.kind, 'task'); assert.equal(short.props.title, 'web.together.joinWithCode');
  assert.equal(short.props.action.disabled, true);
  assert.equal((await tree('KQ7M 2XHE')).props.action.disabled, false);
});
