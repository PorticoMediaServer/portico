import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';

const t = (key: string) => key;
async function page(view: Record<string, unknown>) {
  const stub = (names: string[]) => Object.fromEntries(names.map(name => [name, name]));
  const {LiveOwnerPage} = await screenModule(new URL('../src-next/pages/touch/live/LiveOwnerPage.tsx', import.meta.url), {
    react: {...React, default: React},
    '../../../kit/controls': stub(['Button', 'Select', 'Switch', 'Tabs', 'TextField']), '../../../kit/frames': stub(['SectionCard', 'SettingsPageFrame']),
    '../../../kit/overlays': stub(['Dialog', 'Menu']), '../../../kit/overlays/surface': stub(['OverlayFootRow']), '../../../kit/settings/AdvancedSection': stub(['NativeAdvancedSection']),
    '../../../kit/settings/StorageParts': stub(['StorageActions', 'StorageNote']), '../../../kit/live/LiveSourceParts': stub(['LiveFieldCard', 'LiveSourceCard', 'LiveSourcesEmpty', 'LiveSourcesHeading', 'LiveSwitchRow']),
    '../../../kit/shell': stub(['ServerHeader']), '../../../kit/states': stub(['Quiet', 'StateView']),
    '../../../screens/settings-live/live-defaults-presentation': {liveRewindRow: {control: {}}, liveStabilityRow: {control: {}}},
    '../../../screens/settings-live/LiveOwnerSheets': stub(['AddSourceSheet', 'DiscoverSheet']), '../../../screens/settings-live/LiveOwnerUploadSheet': stub(['UploadSourceSheet']),
    '../../../screens/settings-live/LiveOwnerSettingsSheets': stub(['ChannelMapSheet']), '../../../screens/settings-live/LiveSourceSettingsSheet': stub(['SourceSettingsSheet']),
    '../../../screens/settings-live/LiveRecordingOwner': stub(['NativeRecordingOwner']), '../../../screens/settings-live/server/LibraryChannelsPanel': stub(['NativeLibraryChannelsPanel']),
    '../../shared/live/useLiveOwner': {useLiveOwner: () => ({t, state: {list: {sources: []}}, role: 'owner', tab: 'sources', setTab() {}, sources: [], rows: [], addItems: [], menuItems: [],
      removeFlight: {current: undefined}, ...view}), useLiveDefaults: () => ({guideDayChoices: []})},
  });
  return LiveOwnerPage({controller: {load() {}}, recordings: {}, ports: {context: {serverName: 'Demo'}}, runtime: {text: {t}, error: () => ({})}, onClose() {}});
}

test('Live TV & Channels is a settings page with Back, the owner\'s server and three tabs; Sources shows Add source and the sources', async () => {
  const opened: string[] = [];
  const tree = await page({rows: [{key: 's1', name: 'Synthetic', facts: ['14 channels'], status: {text: 'OK', tone: 'healthy'}, open: () => opened.push('s1'), openMenu() {}, opener() {}}]});
  const frame = tree.props.children[0];
  assert.equal(frame.type, 'SettingsPageFrame'); assert.equal(frame.props.title, 'settings.server.live'); assert.equal(frame.props.back.label, 'next.shell.settings');
  assert.equal(frame.props.server.type, 'ServerHeader');
  assert.deepEqual(frame.props.tabs.props.items.map((item: any) => item.value), ['sources', 'recording', 'channels']);
  const all = (node: any): any[] => !node || typeof node !== 'object' ? [] : Array.isArray(node) ? node.flatMap(all) : [node, ...all(node.props?.children)];
  const card = all(frame.props.children).find(node => node.type === 'LiveSourceCard');
  assert.equal(card.props.name, 'Synthetic'); card.props.onPress(); assert.deepEqual(opened, ['s1']);
});

test('the Recording and Library channels tabs are their own panels', async () => {
  assert.equal((await page({tab: 'recording'})).props.children[0].props.children.props.children.type, 'NativeRecordingOwner');
  assert.equal((await page({tab: 'channels'})).props.children[0].props.children.props.children.type, 'NativeLibraryChannelsPanel');
});
