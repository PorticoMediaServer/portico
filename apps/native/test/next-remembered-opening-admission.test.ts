import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';

for (const scenario of [
  {name: 'publication-only without AV settings', kinds: ['ebook', 'comic'], av: false, reading: true, available: true},
  {name: 'mixed with independent settings', kinds: ['movie', 'ebook', 'comic'], av: true, reading: true, available: true},
  {name: 'missing Reading settings never widens a publication-only shortcut', kinds: ['ebook'], av: true, reading: false, available: false},
  {name: 'missing Reading settings keeps an AV shortcut', kinds: ['movie', 'ebook'], av: true, reading: false, available: true},
] as const) test(`remembered opening admission: ${scenario.name}`, async () => {
  let alive = true, opened = 0;
  const listeners = new Set<() => void>();
  const entries = scenario.kinds.map((kind, index) => ({id: `entry-${index}`, itemId: `item-${index}`, kind, state: 'ready'}));
  const identity = {current: () => alive, subscribe: () => () => {}, dispose() {alive = false;}};
  const port = {getSnapshot: () => ({supported: true, ready: true, entries}), subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};},
    playabilityReason: () => null};
  const device = {device: port, current: () => alive, publicationSource: (entry: any) => ({sourceId: entry.itemId, revision: 'a'.repeat(64)}),
    open: async () => {}, dispose() {}};
  const {createRememberedDownloadsOpening} = await screenModule(new URL('../src-next/shell/remembered-downloads-opening.ts', import.meta.url), {
    react: {...React, default: React}, 'react-native': {Platform: {isTV: false}},
    '../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
    './remembered-downloads-viewer': {captureRememberedDownloadsViewer: async () => identity},
    './remembered-downloads-device': {createRememberedDownloadsDevice: () => device},
    './remembered-playback-settings': {readRememberedPlaybackSettings: async () => scenario.av ? {} : undefined},
    './remembered-reading-settings': {readRememberedReadingPreferences: async () => scenario.reading ? {} : undefined},
    './remembered-reading-owner': {createRememberedReadingOwner: () => ({})},
    '../platform/playback/receipt-only': {createNativeReceiptOnlyPlayback: () => ({current: () => alive, dispose() {}})},
    '../screens/accounts/downloads-entry-model': {noAccountDownloads: {available: false}},
  });
  const installation = {current: () => alive}, signal = new AbortController();
  const source = await createRememberedDownloadsOpening(installation, {signal: signal.signal, isCurrent: () => alive,
    onOpen: () => {opened++;}, onRetired: () => {}});
  assert.ok(source); assert.equal(source.entry.getSnapshot().available, scenario.available);
  source.entry.open(); assert.equal(opened, scenario.available ? 1 : 0);
  if (scenario.reading) assert.equal(source.canRead(entries.find((entry: any) => entry.kind === 'ebook')), true);
  source.dispose(); assert.equal(source.current(), false);
});
