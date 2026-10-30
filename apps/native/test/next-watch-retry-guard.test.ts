import {nativeSubtitleStyleKeys, nativeSubtitleStyleValue} from '../src-next/platform/playback/subtitle-style-choices.ts';
import {nativePostPlayNavigation} from '../src-next/screens/watch/post-play-navigation.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';
import {playbackFence, sameFence} from '../../../packages/player-core/src/index.ts';
import {sameAuthority} from '../src-next/shell/model.ts';
const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'};
const fence = playbackFence({kind: 'queue', playback: {scope: {serverId: 'server', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane', authority: 'local'}, run: 1}, sessionId: 'session', itemId: 'item', sourceId: 'v2', entryId: 'entry', generation: 3});
let captured: any;
const {createNativeWatchModule} = await screenModule(new URL('../src-next/screens/watch/module.tsx', import.meta.url), {
  react: {...React, default: React}, 'react-native': {Platform: {isTV: false}}, '../settings-live/group-port': {}, '../../platform/device-settings': {},
  '../../../../../packages/client-core/src/audiobook-preferences': {}, '../../../../../packages/player-core/src': {sameFence}, '../../../../../packages/i18n/src': {defaultI18n: {t: (s: string) => s}},
  '../../platform/playback/subtitle-style-choices': {nativeSubtitleStyleKeys, nativeSubtitleStyleValue},
  '../../../../../packages/client-core/src/playback-v1/quality': {}, './factory': {createNativeWatchScreens: (ports: any) => {captured = ports; return {watch: {}, dispose() {}};}},
  './PlaybackInfoPanel': {}, './WatchPanels': {}, './panel-options': {NativeWatchPanelOptions: class {dispose() {}}}, '../../shell/model': {sameAuthority},
  './minimize': {minimizeTarget: (dock: any, pressed: any) => dock.local?.expanded ? dock.local.fence : pressed}, './track-identity': {}, './ChannelRoute': {}, './live-guide': {NativeTVLiveGuide: class {guideAction() {return () => {};} close() {return false;} dispose() {}}}, './live-actions': {NativeTVLiveActions: class {close() {return false;} dispose() {}}}, './LiveActionPanels': {}, './PlaybackFailed': {NativePlaybackFailed: 'TVFailure'},
  './HandheldPlaybackFailed': {NativeHandheldPlaybackFailed: 'HandheldFailure'}, './PostPlayScreen': {}, '../../platform/device-platform': {}, './panel-names': {},
  './system-media': {bindNativeVideoSystemMedia: () => () => {}}, './post-play-navigation': {nativePostPlayNavigation},
  '../../../../../packages/client-core/src/playback-messages': {playbackUserMessage: () => ''},
  '../../../../../packages/client-core/src/presentation/kit-model/text': {clockText: String}, '../../kit/states/DetailsPanel': {DetailsPanel: 'DetailsPanel'},
  '../../platform/server-preferences': {}, '../../../../../packages/client-core/src/presentation/browse-toolbar': {},
});

function fixture() {
  let currentAuthority = authority, revision = 1, current = true, source: any = {fence, dock: {local: {fence, expanded: true}}, capabilities: {retry: true}};
  const routeListeners = new Set<() => void>(), authorityListeners = new Set<() => void>(), playbackListeners = new Set<() => void>(), calls: any[] = [];
  const context: any = {current: () => current, authority: () => currentAuthority, session: () => ({viewer: {...authority, authority: 'local'}}), api: {}, watch: {},
    navigation: {getSnapshot: () => ({revision}), subscribe: (fn: () => void) => {routeListeners.add(fn); return () => routeListeners.delete(fn);}},
    subscribeAuthority: (fn: () => void) => {authorityListeners.add(fn); return () => authorityListeners.delete(fn);},
    playback: {getSnapshot: () => source, subscribe: (fn: () => void) => {playbackListeners.add(fn); return () => playbackListeners.delete(fn);}, retry: (f: any, lifetime: any) => {calls.push({f, lifetime}); return new Promise<void>(() => {});}}};
  const instance = createNativeWatchModule(context, {title: {}, requestId: () => 'id'});
  const failure = {fence, sessionId: 'session', positionSeconds: 12};
  const draw = () => captured.player.failed({runtime: {reportError: assert.fail}}, failure, true);
  return {instance, calls, draw, navigation: () => {revision++; for (const fn of routeListeners) fn();},
    authority: () => {currentAuthority = {...authority, epoch: 'new'}; for (const fn of authorityListeners) fn();},
    replaceFence: () => {source = {...source, fence: playbackFence({...fence, generation: 4})}; for (const fn of playbackListeners) fn();},
    collapse: () => {source = {...source, dock: {local: {...source.dock.local, expanded: false}}}; for (const fn of playbackListeners) fn();},
    ownChange: () => {source = {...source, playback: {phase: 'playing'}, session: {id: 'replacement'}}; for (const fn of playbackListeners) fn();},
    noCapability: () => {source = {...source, capabilities: {retry: false}};}, noPort: () => {context.playback.retry = undefined;},
    retire: () => {current = false; for (const fn of authorityListeners) fn();}};
}
for (const change of ['navigation', 'authority', 'replaceFence', 'collapse', 'retire'] as const) test(`handheld retained retry refuses after ${change}`, () => {
  const f = fixture(); try {const button = f.draw().props.retry; assert.equal(typeof button, 'function'); f[change](); button(); assert.equal(f.calls.length, 0);} finally {f.instance.dispose();}
});
test('retry owns its original fence and survives its own source change but aborts on navigation', () => {
  const f = fixture(); try {f.draw().props.retry(); assert.equal(f.calls.length, 1); assert.equal(f.calls[0].f, fence); assert.equal(f.calls[0].lifetime.isCurrent(), true);
    f.ownChange(); assert.equal(f.calls[0].lifetime.isCurrent(), true); f.navigation(); assert.equal(f.calls[0].lifetime.signal.aborted, true); assert.equal(f.calls[0].lifetime.isCurrent(), false);
  } finally {f.instance.dispose();}
});


test('missing capability or retry port produces no actionable recovery', () => {
  for (const missing of ['noCapability', 'noPort'] as const) {
    const f = fixture(); try {f[missing](); assert.equal(f.draw().props.retry, undefined); assert.equal(f.calls.length, 0);} finally {f.instance.dispose();}
  }
});
