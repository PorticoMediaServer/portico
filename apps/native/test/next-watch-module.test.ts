import {nativeSubtitleStyleKeys, nativeSubtitleStyleValue} from '../src-next/platform/playback/subtitle-style-choices.ts';
import {nativePostPlayNavigation} from '../src-next/screens/watch/post-play-navigation.ts';
import {playbackUserMessage} from '../../../packages/client-core/src/playback-messages.ts';
import {clockText} from '../../../packages/client-core/src/presentation/kit-model/text.ts';
import {nativeDeviceSettings} from '../src-next/platform/device-settings';
import * as groupPort from '../src-next/screens/settings-live/group-port.ts';
import {browseValueLabel} from '../../../packages/client-core/src/presentation/browse-toolbar.ts';
import {sameAuthority} from '../src-next/shell/model.ts';
import {NativeShellNavigation} from '../src-next/shell/routes.ts';
import test from 'node:test';
import {audiobookPreferences} from '../../../packages/client-core/src/audiobook-preferences';
import assert from 'node:assert/strict';
import {screenModule, hooks} from './helpers/screen-harness.mjs';
import {sameFence, playbackFence, localPlaybackFence} from '../../../packages/player-core/src/index.ts';
import {qualityOptionSelected} from '../../../packages/client-core/src/playback-v1/quality.ts';
import {nativeWatchTracks} from '../src-next/screens/watch/track-identity.ts';
import {createNativeWatchPanelNames} from '../src-next/screens/watch/panel-names.ts';
import {lossSentence} from '../src-next/screens/watch/playback-info.ts';
import {videoTrackWords} from '../../../packages/client-core/src/video-track-labels.ts';
import {codecName} from '../../../packages/client-core/src/presentation/playback-info.ts';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';

const {react} = hooks();
const infoPanel = await screenModule(new URL('../src-next/screens/watch/PlaybackInfoPanel.tsx', import.meta.url), {'../../kit/states': {Quiet: ({children}: any) => children}, 
  react, 'react-native': {ActivityIndicator: 'ActivityIndicator', ScrollView: 'ScrollView', Text: 'Text', View: 'View'},
  '../../../../../packages/i18n/src': {defaultI18n}, '../../../../../packages/player-core/src': {sameFence},
  '../../../../../packages/client-core/src/video-track-labels': {videoTrackWords},
  '../../../../../packages/client-core/src/presentation/playback-info': {codecName},
  '../../kit/controls': {Button: 'Button', Focusable: 'Focusable'}, '../../kit/theme': {nativeType: (value: unknown) => value, useTheme: () => ({})},
  './playback-info': {lossSentence},
});
const menuCalls: any[][] = [];
let captured: any, optionsDisposed = 0, screensDisposed = 0;
const {createNativeWatchModule, nativeWatchChromeFacts} = await screenModule(new URL('../src-next/screens/watch/module.tsx', import.meta.url), {
  '../settings-live/group-port': groupPort,
  '../../platform/device-settings': {nativeDeviceSettings},
  './system-media': {bindNativeVideoSystemMedia: () => () => {}}, './post-play-navigation': {nativePostPlayNavigation},
  '../../../../../packages/client-core/src/playback-messages': {playbackUserMessage},
  '../../../../../packages/client-core/src/presentation/kit-model/text': {clockText}, '../../kit/states/DetailsPanel': {DetailsPanel: 'DetailsPanel'},
  '../../shell/model': {sameAuthority},
  '../../../../../packages/client-core/src/presentation/browse-toolbar': {browseValueLabel},
  '../../../../../packages/client-core/src/audiobook-preferences': {audiobookPreferences}, react, 'react-native': {Platform: {isTV: false}}, '../../../../../packages/player-core/src': {sameFence},
  '../../platform/playback/subtitle-style-choices': {nativeSubtitleStyleKeys, nativeSubtitleStyleValue},
  '../../../../../packages/client-core/src/playback-v1/quality': {qualityOptionSelected}, './minimize': {minimizeTarget: (dock: any, pressed: any) => dock.local?.expanded ? dock.local.fence : pressed}, './track-identity': {nativeWatchTracks},
  '../../../../../packages/i18n/src': {defaultI18n}, './panel-names': {createNativeWatchPanelNames},
  './ChannelRoute': {NativeWatchChannelRoute: 'Channel'},
  './PlaybackInfoPanel': {NativePlaybackInfoPanel: 'PlaybackInfo', playbackInfoWords: infoPanel.playbackInfoWords},
  './WatchPanels': {NativeWatchPanels: 'Panels'}, './panel-options': {NativeWatchPanelOptions: class {dispose() {optionsDisposed++;}}},
  './live-guide': {NativeTVLiveGuide: class {guideAction() {return () => {};} close() {return false;} dispose() {}}},
  './live-actions': {NativeTVLiveActions: class {close() {return false;} dispose() {}}},
  './LiveActionPanels': {NativeLiveActionPanels: 'LiveActionPanels'}, './PlaybackFailed': {NativePlaybackFailed: 'PlaybackFailed'},
  './HandheldPlaybackFailed': {NativeHandheldPlaybackFailed: 'HandheldPlaybackFailed'}, './PostPlayScreen': {NativeWatchPostPlay: 'PostPlay'},
  './factory': {createNativeWatchScreens: (ports: any) => {captured = ports; return {title: {render: () => 'title'}, watch: {
    render: (runtime: any) => ports.player.chrome(ports.playback.getSnapshot(), runtime), body: () => 'body', back: () => true,
  }, menu: async (...args: any[]) => {menuCalls.push(args);}, dispose: () => {screensDisposed++;}};}},
});
const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'};
const fence = playbackFence({kind: 'queue', playback: {scope: {serverId: 'server', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane', authority: 'local'}, run: 1}, sessionId: 'session', itemId: 'item', sourceId: 'v2', entryId: 'entry', generation: 3});
const names = {audio: (track: any) => ({primary: track.id}), subtitle: (track: any) => ({primary: track.id}), quality: (option: any) => ({primary: option.id ?? option.mode})};
function source(): any {
  return {retired: false, fence, dock: {local: {fence}}, options: {itemId: 'item', qualities: [{mode: 'original'}, {mode: 'fixed', id: '720p-high'}], versions: [
    {id: 'v1', audio: [], subtitles: []}, {id: 'v2', audio: [{id: 'default', default: true}, {id: 'actual'}], subtitles: [{id: 'actual-subtitle'}]},
  ]}, session: {id: 'session', itemId: 'item', versionId: 'v2', queue: {entryId: 'entry'}, presentation: {generation: 3,
    audioTrackId: 'actual', subtitle: {trackId: 'actual-subtitle'}, quality: {mode: 'fixed', id: '720p-high'}}}};
}

test('chrome facts use current selected IDs and never a preferred version or an unknown output', () => {
  const live = source();
  assert.deepEqual(nativeWatchChromeFacts(live, names), {quality: '720p-high', audio: 'actual', subtitle: 'actual-subtitle'});
  assert.deepEqual(nativeWatchChromeFacts({...live, session: {...live.session, presentation: {...live.session.presentation, generation: 2}}}, names), {quality: ''});
  assert.deepEqual(nativeWatchChromeFacts({...live, session: {...live.session, presentation: {...live.session.presentation, audioTrackId: undefined, subtitle: undefined}}}, names), {quality: '720p-high', audio: undefined, subtitle: 'Off'});
});

test('chrome names actual downloaded selections and Off without a fabricated server version', () => {
  const local = localPlaybackFence({kind: 'local', playback: {kind: 'local', scope: {authority: 'local', serverId: 'server', accountId: 'account', profileId: 'profile', sessionFamilyId: 'family'}, run: 1},
    sessionId: 'local:copy:1', generation: 1, itemId: 'item', entryId: 'copy', playId: 'play', artifact: {sha256: 'a'.repeat(64), bytes: 100}});
  const live: any = {fence: local, retired: false, dock: {local: {fence: local}}, playback: {intentId: 4},
    localTracks: {binding: {intentId: 4, sessionId: local.sessionId, generation: 1}, audioTrackId: 'actual-audio', subtitleTrackId: 'saved', subtitleOffsetMs: 0, subtitleTimingSupported: true,
      audio: [{id: 'actual-audio', codec: 'aac'}], subtitles: [{id: 'saved', format: 'vtt', source: 'sidecar'}]}};
  assert.deepEqual(nativeWatchChromeFacts(live, names), {quality: '', audio: 'actual-audio', subtitle: 'saved'});
  live.localTracks = {...live.localTracks, subtitleTrackId: null};
  assert.deepEqual(nativeWatchChromeFacts(live, names), {quality: '', audio: 'actual-audio', subtitle: 'Off'});
  live.playback.intentId = 5;
  assert.deepEqual(nativeWatchChromeFacts(live, names), {quality: ''});
});

test('module preserves the genuine adapter/outlet, current preferences, body and retirement lifetime', async () => {
  let live = true, back = 5000, forward = 60000, book = false;
  const transitions: any[] = [], layouts: any[] = [], errors: any[] = [];
  const transition = {action: 'collapse'}, playback = {getSnapshot: () => ({...source(), dock: {local: {kind: book ? 'audiobook' : 'video'}}}), subscribe: (_listener: () => void) => () => {}, dock: {collapse: (expected: any) => {assert.equal(expected, fence); return transition;}},
    onTransition: async (value: any) => {transitions.push(value);}}, outlet = {genuine: true};
  const navigation = new NativeShellNavigation(authority, () => authority);
  const context = {current: () => live, authority: () => authority, session: () => ({viewer: {...authority, authority: 'local'}}), api: {},
    subscribeAuthority: () => () => {}, navigation, playback, watch: outlet,
    preferences: () => ({registry:{fields:[]},effective: {'playback.skipBackMs': back, 'playback.skipForwardMs': forward,'audiobooks.skipBackMs':60000,'audiobooks.skipForwardMs':90000,'audiobooks.resumeRewindMs':0}})};
  const menuListeners = new Set<() => void>();
  const entry = {id: 'member', kind: 'movie', libraryId: 'library', title: 'Member'};
  let opened: any;
  const module = createNativeWatchModule(context, {names, title: {renderError() {}, menu: {getSnapshot: () => opened, subscribe: (listener: () => void) => {menuListeners.add(listener); return () => menuListeners.delete(listener);}}}, presentation: (value: any) => {layouts.push(value);}});
  const runtime = {reportError: (error: any) => {errors.push(error);}};
  const memberCurrent = () => true, anchorRef = {current: null};
  await module.entryMenu.open(entry, memberCurrent, anchorRef);
  assert.deepEqual(menuCalls, [[entry, memberCurrent, anchorRef, undefined, undefined]]);
  assert.equal(module.entryMenu.isOpen(entry), false); opened = {...entry};
  assert.equal(module.entryMenu.isOpen(entry), true);
  assert.equal(module.entryMenu.isOpen({...entry, libraryId: 'other'}), false);
  const listener = () => {}; const unsubscribe = module.entryMenu.subscribe(listener);
  assert.equal(menuListeners.has(listener), true); unsubscribe(); assert.equal(menuListeners.size, 0);
  assert.equal(captured.playback, playback); assert.equal(captured.watch, outlet); assert.equal(captured.isCurrent(), true);
  let chrome = module.render(runtime); assert.equal(chrome.skipBackSeconds, 5); assert.equal(chrome.skipForwardSeconds, 60);
  back = 15000; forward = 30000; chrome = module.render(runtime); assert.equal(chrome.skipBackSeconds, 15); assert.equal(chrome.skipForwardSeconds, 30);
  book = true; chrome = module.render(runtime); assert.equal(chrome.skipBackSeconds, 60); assert.equal(chrome.skipForwardSeconds, 90);
  book = false;
  chrome.onMinimize(fence); await Promise.resolve(); assert.deepEqual(transitions, [transition]);
  assert.equal(captured.player.content({}), 'body'); assert.equal(module.back(), true);
  live = false; assert.equal(module.render(runtime), null); assert.equal(module.back(), false); assert.equal(captured.isCurrent(), false);
  await module.entryMenu.open(entry, memberCurrent, anchorRef); assert.equal(menuCalls.length, 1);
  assert.equal(module.entryMenu.isOpen(entry), false);
  module.dispose(); module.dispose(); assert.equal(optionsDisposed, 1); assert.equal(screensDisposed, 1); assert.deepEqual(layouts, [undefined]); assert.deepEqual(errors, []);
});
