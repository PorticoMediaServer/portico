import test from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';

async function page(view: Record<string, unknown>) {
  const played: string[] = [];
  const {RememberedDownloadsPage} = await screenModule(new URL('../src-next/pages/touch/downloads/RememberedDownloadsPage.tsx', import.meta.url), {
    react: {...React, default: React},
    '../../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key, locale: 'en'}},
    '../../../kit/controls': {Button: 'Button'}, '../../../kit/frames': {BrowseFrame: 'BrowseFrame'}, '../../../kit/overlays': {Banner: 'Banner'},
    '../../../kit/saved/DownloadInventory': {DownloadEntryList: 'DownloadEntryList', LiveDownloadRow: 'LiveDownloadRow'},
    '../../../kit/saved/OfflinePlayerPanel': {OfflinePlayerPanel: 'OfflinePlayerPanel'},
    '../../../screens/downloads/presentation': {nativeDownloadsCopy: {emptyBody: 'x'}},
    '../../shared/downloads/useRememberedDownloads': {
      useRememberedDownloads: () => ({inventory: {}, entries: ['a', 'b'], busy: false, current: () => true, run() {}, back() {}, closeReading() {},
        play: (entry: {id: string}) => played.push(entry.id), ...view}),
      useRememberedPlayer: () => ({}), useRememberedShortcut: () => undefined},
  });
  return {played, RememberedDownloadsPage};
}

const device = {playabilityReason: () => null};

test('a phone\'s downloads offline are Downloads\' header with Back and the downloads that play here as Downloads draws them', async () => {
  let backs = 0;
  const {RememberedDownloadsPage} = await page({back: () => {backs++;}});
  const tree = RememberedDownloadsPage({source: {device, player: {}}, onBack() {}});
  assert.equal(tree.type, 'BrowseFrame');
  assert.equal(tree.props.header.title, 'downloads.title');
  tree.props.header.back.onPress(); assert.equal(backs, 1);
  const list = tree.props.grid;
  assert.equal(list.type, 'DownloadEntryList');
  assert.deepEqual(list.props.ids, ['a', 'b']);
  assert.equal(list.props.empty, 'downloads.emptyTitle');
  const first = list.props.renderRow('a', true), second = list.props.renderRow('b', false);
  assert.equal(first.type, 'LiveDownloadRow'); assert.equal(first.props.separator, false); assert.equal(second.props.separator, true);
  assert.equal(first.props.device, device); assert.equal(first.props.onMenu, undefined);
  assert.equal(typeof first.props.onPlay, 'function');
  assert.ok(list.props.header, 'the player sits above the list when the phone keeps one');
});

test('while one is opening, the rows wait; with no player and nothing wrong there is no header', async () => {
  const {RememberedDownloadsPage} = await page({busy: true});
  const list = RememberedDownloadsPage({source: {device}, onBack() {}}).props.grid;
  assert.equal(list.props.renderRow('a', true).props.onPlay, undefined);
  assert.equal(list.props.header, undefined);
});
