// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {PlaybackService} from '../../../packages/client-core/src';
import {NativePlaybackCoordinator} from '../src-next/platform/playback/NativePlaybackCoordinator';
import {NativeOfflinePlayback} from '../src-next/platform/playback/offline-playback';
import {NativeOfflineTracks} from '../src-next/platform/playback/offline-tracks';
import * as listening from '../src-next/platform/playback/listening-controls';
import * as engines from '../src-next/platform/playback/engine-choice';
import * as captions from '../src-next/platform/playback/caption-style';
import {mediaRecovered} from '../src-next/platform/playback/playback-recovery';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {screenModule} from './helpers/screen-harness.mjs';

test('the cold-launch player carries every part and exposes fenced cached audiobook controls without a server', async t => {
  let active = true, sequence = 0, fact: (value: unknown) => void = () => {};
  const commands: any[] = [], digest = 'a'.repeat(64);
  const owner = {authority: 'local', serverId: 'server', accountId: 'account', profileId: 'profile', sessionFamilyId: 'family'} as const;
  const entry = {id: 'copy', itemId: 'book', title: 'Book', kind: 'audiobook_file', libraryId: 'library', downloadId: 'dl_book',
    owner: JSON.stringify(['local', 'account', 'profile', 'server']), artifact: {sha256: digest, bytes: 200}, state: 'ready'};
  const parts = [{url: 'file:///downloads/first.mp3', offsetSeconds: 0, durationSeconds: 10, startSeconds: 0},
    {url: 'file:///downloads/second.mp3', offsetSeconds: 10, durationSeconds: 10, startSeconds: 0}];
  const identity = {owner, current: () => active, subscribe: () => () => {}};
  const device = {getSnapshot: () => ({entries: [entry]}), subscribe: () => () => {}, playabilityReason: () => null,
    recordProgress() {}, localMedia: async () => ({entryId: 'copy', itemId: 'book', playId: 'play', fileUrl: parts[0].url,
      durationSeconds: 20, startSeconds: 0, parts, extras: {bundle: {media: {container: 'mp3'}, chapters: [{index: 0, title: 'First', startSeconds: 0, endSeconds: 10},
        {index: 1, title: 'Second', startSeconds: 10, endSeconds: 20}]}}})};
  const settings = {engine: 'native', videoRate: 1, audiobookRate: 1, audiobook: {skipBackMs: 10000, skipForwardMs: 10000, resumeRewindMs: 0},
    subtitles: {size: 'medium', color: 'white', background: 'translucent', position: 'bottom', font: 'default', keepStyling: true, useSystemCaptionStyle: false}};
  const {createNativeReceiptOnlyPlayback} = await screenModule(new URL('../src-next/platform/playback/receipt-only.tsx', import.meta.url), {
    react: {...React, default: React},
    'react-native': {Platform: {OS: 'android'}, UIManager: {hasViewManagerConfig: () => true}, requireNativeComponent: () => 'NativeHost',
      NativeModules: {PorticoRuntime: {playbackRequestId: () => 'request'}, PorticoPlaybackModule: {
        acquire: async () => ({lease: 'lease', instanceId: 'engine'}), release: async () => {},
        command: async (command: unknown) => {commands.push(command);}}},
      NativeEventEmitter: class {addListener(_name: string, listener: typeof fact) {fact = listener; return {remove() {}};}}},
    '../../../../../packages/client-core/src': {PlaybackService}, '../../../../../packages/client-core/src/presentation/service-text': {serviceText: (key: string) => key},
    './dynamic-range-matching': {nativeDynamicRangeMatchingAvailable: () => false}, './engine-choice': engines,
    './NativePlaybackCoordinator': {NativePlaybackCoordinator}, './offline-playback': {NativeOfflinePlayback}, './offline-tracks': {NativeOfflineTracks},
    './caption-style': captions, './playback-recovery': {mediaRecovered}, './listening-controls': listening,
    './native-surface': {nativePlaybackView: () => 'NativeHost'},
  });
  const player = createNativeReceiptOnlyPlayback({identity, device, isCurrent: () => active, settings: () => settings, subscribeSettings: () => () => {}});
  const host = createFabricHookHost(() => player.Surface());
  await act(() => host.mount());
  t.after(async () => {player.dispose(); await host.unmount();});
  const life = new AbortController(), lifetime = {signal: life.signal, isCurrent: () => active};
  await act(() => player.open(entry, lifetime));
  const command = commands.at(-1); assert.ok(command);
  assert.deepEqual(command.localParts, parts); assert.ok(command.source.startsWith('file:'));
  await act(() => fact({type: 'ready', instanceId: 'engine', binding: command.binding, lastCommandRevision: command.commandRevision,
    factSequence: ++sequence, position: 0, paused: false, supportedRates: [1, 1.5]}));
  const fence = player.fence(); assert.ok(fence);
  const reader = player.listeningRead(fence, lifetime); assert.ok(reader); await reader.readChapters();
  let snapshot = reader.getSnapshot(); assert.equal(snapshot.phase, 'current');
  assert.equal(snapshot.chapters.data.chapters[1].startSeconds, 10); assert.equal(snapshot.chapters.data.chapters[0].index, 1);
  await act(() => player.setRate(fence, 1.5));
  snapshot = reader.getSnapshot(); assert.equal(snapshot.rate.selected, 1.5); assert.equal(commands.at(-1).rate, 1.5);
  await act(() => player.setRate(fence, 3)); assert.equal(commands.at(-1).rate, 1.5, 'an unoffered speed is refused');
  await act(() => player.seek(fence, 15)); assert.equal(commands.at(-1).pendingSeek.positionSeconds, 15);
  life.abort(); assert.equal(reader.current(), false);
  active = false; const before = commands.length; player.setRate(fence, 1); assert.equal(commands.length, before);
});
