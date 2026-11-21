import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';

const t = (key: string) => key;
async function page(view: Record<string, unknown>) {
  const stub = (names: string[]) => Object.fromEntries(names.map(name => [name, name]));
  const {ServerLibrariesPage} = await screenModule(new URL('../src-next/pages/touch/settings/ServerLibrariesPage.tsx', import.meta.url), {
    react: {...React, default: React}, '../../../../../../packages/i18n/src/index': {defaultI18n: {t, has: () => true}},
    '../../../../../../packages/client-core/src/owner-library-requests': {}, '../../../../../../packages/client-core/src/server-admin/console-catalogue': {},
    '../../../kit/controls': stub(['Button', 'FormError', 'Select', 'Switch', 'Tabs', 'TextField']), '../../../kit/frames': stub(['SectionCard', 'SettingsPageFrame']),
    '../../../kit/overlays': stub(['BottomSheet', 'Menu']), '../../../kit/overlays/surface': stub(['OverlayFootRow']),
    '../../../kit/settings/LibraryParts': stub(['LibraryScanningNote', 'LibraryStatusControl', 'OwnerLibraryHeader', 'OwnerLibraryRow', 'OwnerLibraryRowsLoading', 'SkippedFileLine']),
    '../../../kit/shell': stub(['SettingsRow']), '../../../kit/states': stub(['StateView']),
    '../../../screens/settings-live/owner-libraries/AddFolderPage': stub(['AddFolderPage']), '../../../screens/settings-live/owner-libraries/AddLibraryPage': stub(['AddLibraryPage', 'MetadataAgentChoice']),
    '../../../screens/settings-live/owner-libraries/LibrarySettingsRows': stub(['LibrarySettingsRows']), '../../../screens/settings-live/owner-libraries/LibraryTrashGroup': stub(['LibraryTrashGroup']),
    '../../../screens/settings-live/owner-libraries/presentation': {},
    '../../shared/settings/useServerLibraries': {useServerLibraries: () => ({t, state: {directory: {phase: 'ready'}, busy: false}, rows: [], listPhase: 'ready', menuItems: [], folderMenuItems: [], ...view}), useLibrarySonic: () => ({})},
  });
  return ServerLibrariesPage({ports: {context: {serverName: 'Demo'}}, runtime: {}, server: 'Server'});
}
const children = (node: any): any[] => React.Children.toArray(node.props.children) as any[];

test('Libraries is a settings page: Add library, then the owner\'s libraries in one card, each opening its page or its menu', async () => {
  const opened: string[] = [];
  const tree = await page({rows: [{key: 'l1', name: 'Films', counts: ['96 films'], status: 'Scanned today', live: false, covers: [], square: false, open: () => opened.push('l1'), menu() {}}]});
  const frame = tree.props.children[0];
  assert.equal(frame.type, 'SettingsPageFrame'); assert.equal(frame.props.title, 'settings.server.libraries'); assert.equal(frame.props.server, 'Server');
  const card = children(frame).find(node => node?.props?.help === 'next.ownerLibraries.moveHint');
  const row = children(card)[0];
  assert.equal(row.type, 'OwnerLibraryRow'); assert.equal(row.props.name, 'Films'); row.props.onOpen(); assert.deepEqual(opened, ['l1']);
});

test('a library opens as its own page with Back to Libraries and the five tabs', async () => {
  let closed = 0;
  const library = {id: 'l1', name: 'Films', actions: ['scan'], kind: 'movie'};
  const tree = await page({library, header: {name: 'Films', counts: [], scanning: false, status: 'Scanned', canScan: true}, tab: 'general', closeLibrary: () => {closed++;}});
  const frame = tree.props.children[0];
  assert.equal(frame.props.title, 'Films'); frame.props.back.onPress(); assert.equal(closed, 1);
  assert.deepEqual(frame.props.tabs.props.items.map((item: any) => item.value), ['general', 'folders', 'metadata', 'scanning', 'advanced']);
});
