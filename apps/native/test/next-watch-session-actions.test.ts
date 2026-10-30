import {playbackUserMessage} from '../../../packages/client-core/src/playback-messages.ts';
import {clockText} from '../../../packages/client-core/src/presentation/kit-model/text.ts';
import {nativePostPlayNavigation} from '../src-next/screens/watch/post-play-navigation.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule, hooks} from './helpers/screen-harness.mjs';
import {playbackFence, sameFence} from '../../../packages/player-core/src/identity.ts';
import {durationText} from '../../../packages/client-core/src/presentation/kit-model/text.ts';
import {NativeSubtitleStyleChoices, nativeSubtitleStyleKeys, nativeSubtitleStyleValue} from '../src-next/platform/playback/subtitle-style-choices.ts';
import {NativeTitleStarts} from '../src-next/screens/watch/title-starts.ts';
import {NativeVersionChooserController} from '../src-next/screens/watch/version-destination.ts';

const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'};
const fence = playbackFence({kind: 'queue', playback: {scope: {...authority, controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane', authority: 'local'}, run: 1},
  sessionId: 'session', itemId: 'item', sourceId: 'failed', entryId: 'entry', generation: 3});
const defaults = Object.freeze({size: 'medium', color: 'white', background: 'outline', position: 'bottom', font: 'default', keepStyling: true, useSystemCaptionStyle: false} as const);
const t = (id: string) => id;
const settle = () => new Promise<void>(resolve => setImmediate(resolve));

async function fixture() {
  const {react} = hooks(), defaultI18n = {t, locale: 'en-US'};
  const {sameAuthority} = await screenModule(new URL('../src-next/shell/model.ts', import.meta.url), {
    react, '../screens/read/platform': {}, '../platform/layout/tv-canvas': {},
  });
  const versionsModel = await screenModule(new URL('../src-next/screens/watch/version-options.ts', import.meta.url), {
    react, '../../../../../packages/client-core/src/presentation/kit-model/text': {durationText},
    '../../../../../packages/client-core/src/video-track-labels': {videoTrackWords: () => ({}), videoPictureName: () => '', videoAudioFormatName: () => '', formatVideoBytes: () => undefined},
  });
  let live = true, revision = 1, currentAuthority = authority;
  const routeListeners = new Set<() => void>(), authorityListeners = new Set<() => void>(), playbackListeners = new Set<() => void>();
  const subscribe = (listeners: Set<() => void>) => (fn: () => void) => {listeners.add(fn); return () => {listeners.delete(fn);};};
  const styleChoices = new NativeSubtitleStyleChoices(); styleChoices.bind(fence); styleChoices.set(fence, 'playback.subtitleColor', 'cyan');
  const entry = {id: 'item', kind: 'movie', libraryId: 'library', title: 'The failed film', playback: {itemId: 'item', startSeconds: 120}};
  let source: any = {authority, retired: false, fence, entry, playback: {phase: 'error', intent: 'paused'}, dock: {local: {fence, kind: 'video', expanded: true}},
    capabilities: {retry: false}, subtitleStyle: styleChoices.read(fence, defaults),
    session: {id: 'session', kind: 'vod', role: 'local', itemId: entry.id, versionId: 'failed', presentation: {generation: 3}},
    options: {itemId: entry.id, versions: [{id: 'failed', edition: 'Failed version', video: [], audio: [], subtitles: []}, {id: 'alternate', edition: 'Alternate version', video: [], audio: [], subtitles: []}], qualities: []}};
  const patches: any[][] = [], captures: any[] = [], versionCalls: any[][] = [], versionStarts: any[][] = [], starts: any[] = [], menus: any[] = [], reads: string[] = [], openings: any[] = [], presented: any[] = [];
  const liveGuideArguments: any[][] = [], guides: any[] = [], actions: any[] = [];
  let savedWrites = 0, guideCalls = 0;
  const preferences: any = {registry: {fields: [
    {key: 'playback.subtitleSize', labelKey: 'size', allowedValues: ['small', 'medium', 'large', 'extra-large']},
    {key: 'playback.subtitleColor', labelKey: 'color', allowedValues: ['white', 'yellow', 'cyan', 'green', 'pink']},
    {key: 'playback.subtitleBackground', labelKey: 'background', allowedValues: ['none', 'outline', 'shadow', 'translucent', 'opaque']},
    {key: 'playback.subtitlePosition', labelKey: 'position', allowedValues: ['bottom', 'raised', 'top']},
    {key: 'playback.subtitleFont', labelKey: 'font', allowedValues: ['default', 'serif', 'mono', 'rounded']},
  ]}, effective: Object.freeze({'playback.subtitleColor': 'white', 'playback.skipBackMs': 10000, 'playback.skipForwardMs': 30000})};
  const context: any = {current: () => live, authority: () => currentAuthority, session: () => ({viewer: {...authority, authority: 'local'}}), api: {}, watch: {},
    preferences: () => preferences, subscribePreferences: () => () => {}, subscribeAuthority: subscribe(authorityListeners),
    navigation: {getSnapshot: () => ({authority: currentAuthority, revision, route: {kind: 'title', entry}}), subscribe: subscribe(routeListeners)},
    playback: {getSnapshot: () => source, subscribe: subscribe(playbackListeners), start: async (input: any) => {starts.push(input);},
      setSubtitleStyle: (captured: any, key: string, value: string) => {
        patches.push([captured, key, value]);
        if (!sameFence(captured, source.fence)) return false;
        const accepted = styleChoices.set(captured, key, value);
        if (accepted) source = {...source, subtitleStyle: styleChoices.read(source.fence, defaults)};
        return accepted;
      }}};
  const {createNativeWatchScreens} = await screenModule(new URL('../src-next/screens/watch/factory.tsx', import.meta.url), {'../share/share-actions': {shareLinkActions: () => []}, 
    react, '../../../../../packages/client-core/src/detail': {DetailService: class {
      async select() {} getSnapshot() {return {data: {item: source.entry, actions: [{id: 'play', enabled: true, playback: source.entry.playback}]}};}
      subscribe() {return () => {};} dispose() {}
    }},
    '../../../../../packages/client-core/src/playback-v1/options': {PlaybackOptionsClient: class {async options(id: string) {reads.push(id); return source.options;}}},
    '../../../../../packages/client-core/src/continue-watching': {}, '../../../../../packages/client-core/src/playback-v1/local-http': {localV1Http: (api: any) => api},
    '../../../../../packages/player-core/src': {sameFence}, '../../../../../packages/i18n/src': {defaultI18n}, '../../platform/layout/full-window': {}, '../../shell/model': {sameAuthority},
    './title-controller': {}, './title-starts': {NativeTitleStarts}, './TitleScreen': {}, './FileInfoContent': {}, './FileInfoPanel': {}, './file-info': {}, '../../../../../packages/client-core/src/file-info-session': {}, './playback-info': {}, './minimize': {minimizeTarget: (dock: any, pressed: any) => dock.local?.expanded ? dock.local.fence : pressed}, './track-identity': {},
    './version-options': versionsModel, './version-destination': {NativeVersionChooserController}, './disc-titles-controller': {}, './disc-titles': {},
    './controls': {NativeWatchControls: class {route() {} source() {} dispose() {} back() {return false;}}},
    './WatchChrome': {}, './picture-slot': {}, './WatchHost': {NativeWatchHost: 'WatchHost'}, './EpisodePanel': {},
  });
  let ports: any, screens: any;
  const {createNativeWatchModule} = await screenModule(new URL('../src-next/screens/watch/module.tsx', import.meta.url), {'./system-media': {bindNativeVideoSystemMedia: () => () => {}}, './post-play-navigation': {nativePostPlayNavigation}, '../../../../../packages/client-core/src/playback-messages': {playbackUserMessage}, '../../../../../packages/client-core/src/presentation/kit-model/text': {clockText}, '../../kit/states/DetailsPanel': {DetailsPanel: 'DetailsPanel'}, 
    react, 'react-native': {Platform: {isTV: false}}, '../settings-live/group-port': {subscribeGroupPort: () => () => {}}, '../../platform/device-settings': {},
    '../../../../../packages/client-core/src/audiobook-preferences': {}, '../../../../../packages/player-core/src': {sameFence}, '../../../../../packages/i18n/src': {defaultI18n},
    '../../../../../packages/client-core/src/playback-v1/quality': {}, '../../shell/model': {sameAuthority},
    '../../platform/playback/subtitle-style-choices': {nativeSubtitleStyleKeys, nativeSubtitleStyleValue}, '../../../../../packages/client-core/src/presentation/browse-toolbar': {browseValueLabel: (value: string) => value},
    '../../platform/server-preferences': {nativeServerPreferences: () => ({applyPreferences: () => {savedWrites++; throw new Error('Player must not persist preferences');}})},
    './factory': {createNativeWatchScreens: (value: any) => {
      ports = value; screens = createNativeWatchScreens(value);
      return {...screens, capture: (...args: any[]) => {const captured = screens.capture(...args); captures.push(captured); return captured;},
        versions: (...args: any[]) => {versionCalls.push(args); return screens.versions(...args);}};
    }},
    './PlaybackInfoPanel': {}, './WatchPanels': {NativeWatchPanels: 'Panels'}, './panel-options': {NativeWatchPanelOptions: class {dispose() {}}},
    './minimize': {minimizeTarget: (dock: any, pressed: any) => dock.local?.expanded ? dock.local.fence : pressed}, './track-identity': {nativeWatchTracks: () => ({})}, './ChannelRoute': {},
    './live-guide': {NativeTVLiveGuide: class {constructor(...args: any[]) {liveGuideArguments.push(args); guides.push(this);} guideAction() {return () => {guideCalls++;};} close() {return false;} dispose() {}}},
    './live-actions': {NativeTVLiveActions: class {constructor(...args: any[]) {actions.push(args);} close() {return false;} dispose() {}}},
    './LiveActionPanels': {NativeLiveActionPanels: 'LiveActions'}, './PlaybackFailed': {NativePlaybackFailed: 'TVFailure'},
    './HandheldPlaybackFailed': {NativeHandheldPlaybackFailed: 'HandheldFailure'}, './PostPlayScreen': {}, './panel-names': {},
  });
  const bindings: any = {title: {reportError: assert.fail, renderError: assert.fail, navigate() {}}, names: {}, requestId: async () => 'request',
    openMenu: (_capture: any, menu: any) => {menus.push(menu);},
    openVersions: (capture: any, destination: any) => {presented.push({capture, destination});},
    beginVersions: (capture: any) => {openings.push(capture); return {capture, live: () => live};}, startVersion: async (...args: any[]) => {versionStarts.push(args);}};
  const instance = createNativeWatchModule(context, bindings);
  const host = () => ({source, runtime: {reportError: assert.fail, text: {t}}});
  return {instance, context, bindings, preferences, patches, captures, versionCalls, versionStarts, starts, menus, reads, openings, presented, liveGuideArguments, guides, actions,
    source: () => source, replace: (value: any) => {source = value; for (const fn of playbackListeners) fn();}, savedWrites: () => savedWrites, guideCalls: () => guideCalls,
    panel: () => ports.player.panel(host()).props.style, failed: () => ports.player.failed(host(), {fence, sessionId: 'session', positionSeconds: 120}, true),
    menu: () => screens.menu(source.entry, () => live),
    chrome: () => ports.player.chrome(source, host().runtime), capturedCurrent: (capture: any) => screens.isCurrent(capture),
    navigation: () => {revision++; for (const fn of routeListeners) fn();}, retire: () => {live = false; for (const fn of authorityListeners) fn();},
    authority: () => {currentAuthority = {...authority, epoch: 'replacement'}; for (const fn of authorityListeners) fn();}};
}

test('the player appearance panel reads session overrides and changes the captured fence without saving defaults', async () => {
  const f = await fixture();
  try {
    const panel = f.panel();
    assert.equal(panel.fields.length, 5);
    assert.equal(panel.fields.find((field: any) => field.key === 'playback.subtitleColor').current, 'cyan');
    assert.equal(f.preferences.effective['playback.subtitleColor'], 'white');
    await panel.apply('playback.subtitleColor', 'pink');
    assert.deepEqual(f.patches, [[fence, 'playback.subtitleColor', 'pink']]);
    assert.equal(f.panel().fields.find((field: any) => field.key === 'playback.subtitleColor').current, 'pink');
    assert.equal(f.savedWrites(), 0); assert.equal(f.preferences.effective['playback.subtitleColor'], 'white');
    await assert.rejects(panel.apply('playback.subtitleColor', 'purple'), /invalid_playback_preference/);
    f.preferences.registry.fields = f.preferences.registry.fields.filter((field: any) => field.key !== 'playback.subtitleColor');
    await assert.rejects(panel.apply('playback.subtitleColor', 'yellow'), /invalid_playback_preference/);
    assert.equal(f.patches.length, 1);
  } finally {f.instance.dispose();}
});

test('retained appearance actions cannot adopt a later source or a retired module', async () => {
  const f = await fixture();
  try {
    const panel = f.panel(), current = f.source();
    f.replace({...current, fence: playbackFence({...fence, generation: 4})});
    await panel.apply('playback.subtitleColor', 'yellow');
    assert.equal(f.patches[0][0], fence, 'the panel does not recapture a newer fence at tap time');
    assert.equal(f.source().subtitleStyle.color, 'cyan', 'the real full-fence guard refuses the stale source');
    f.retire(); await panel.apply('playback.subtitleColor', 'pink');
    assert.equal(f.patches.length, 1); assert.equal(f.savedWrites(), 0);
  } finally {f.instance.dispose();}
});

test('missing appearance or its runtime command keeps the style panel unavailable', async () => {
  const f = await fixture();
  try {
    f.replace({...f.source(), subtitleStyle: undefined}); assert.equal(f.panel(), undefined);
    f.replace({...f.source(), subtitleStyle: defaults}); f.context.playback.setSubtitleStyle = undefined; assert.equal(f.panel(), undefined);
  } finally {f.instance.dispose();}
});

test('a failed handheld opens real alternate-version recovery through its captured entry and failed version', async () => {
  const f = await fixture();
  try {
    const failure = f.failed(); assert.equal(failure.type, 'HandheldFailure'); assert.equal(typeof failure.props.differentVersion, 'function');
    const capture = f.captures[0]; assert.equal(capture.entry.id, 'item'); assert.equal(capture.expectedFence, fence);
    assert.equal(f.capturedCurrent(capture), true); failure.props.differentVersion(); await settle();
    assert.equal(f.versionCalls[0][0], capture); assert.equal(f.versionCalls[0][1], 'failed');
    assert.deepEqual(f.reads, ['item']); assert.equal(f.openings[0], capture);
    assert.deepEqual(f.presented[0].destination.rows.map((row: any) => row.id), ['alternate']);
  } finally {f.instance.dispose();}
});

test('version recovery derives the current failed session target when display metadata has no playback property', async () => {
  const f = await fixture();
  try {
    const source = f.source(); f.replace({...source, entry: {...source.entry, playback: undefined}});
    const failure = f.failed(); assert.equal(typeof failure.props.differentVersion, 'function');
    failure.props.differentVersion(); await settle();
    const capture = f.captures[0]; assert.equal(capture.entry.playback.itemId, source.session.itemId);
    assert.equal(f.source().entry.playback, undefined, 'display metadata is not mutated');
    assert.equal(await f.presented[0].destination.choose('alternate'), true);
    assert.equal(f.versionStarts.length, 1);
    assert.equal(f.versionStarts[0][0].target.itemId, source.session.itemId);
    assert.equal(f.versionStarts[0][1].versionId, 'alternate'); assert.equal(f.versionStarts[0][1].expectedFence, fence);
  } finally {f.instance.dispose();}
});

test('a chapter menu preserves its explicit chapter offset while ordinary title menus keep server resume', async () => {
  for (const kind of ['chapter', 'movie', 'audiobook_file'] as const) {
    const f = await fixture();
    try {
      const source = f.source(), target = {itemId: 'item', startSeconds: 125};
      f.replace({...source, entry: {...source.entry, kind, ...(kind === 'chapter' ? {id: 'item:2'} : {}), playback: target}});
      await f.menu(); assert.equal(f.menus.length, 1); await f.menus[0].entry.play();
      assert.equal(f.starts.length, 1); assert.equal(f.starts[0].target, target);
      assert.equal(f.starts[0].startIntent, kind === 'chapter' ? 'explicit' : undefined);
    } finally {f.instance.dispose();}
  }
});

test('alternate-version recovery requires another version, matching item and all real presenter ports', async () => {
  const missing = ['another-version', 'matching-item', 'entry', 'openVersions', 'beginVersions', 'startVersion'] as const;
  for (const absent of missing) {
    const f = await fixture();
    try {
      const source = f.source();
      if (absent === 'another-version') f.replace({...source, options: {...source.options, versions: [source.options.versions[0]]}});
      else if (absent === 'matching-item') f.replace({...source, options: {...source.options, itemId: 'other'}});
      else if (absent === 'entry') f.replace({...source, entry: undefined});
      else f.bindings[absent] = undefined;
      assert.equal(f.failed().props.differentVersion, undefined, absent);
      assert.equal(f.reads.length, 0); assert.equal(f.openings.length, 0);
    } finally {f.instance.dispose();}
  }
});

for (const change of ['navigation', 'authority', 'collapse', 'replacement', 'recovered', 'retire'] as const) test(`retained version recovery refuses after ${change}`, async () => {
  const f = await fixture();
  try {
    const action = f.failed().props.differentVersion, capture = f.captures[0]; assert.equal(typeof action, 'function');
    if (change === 'collapse') f.replace({...f.source(), dock: {local: {...f.source().dock.local, expanded: false}}});
    else if (change === 'replacement') f.replace({...f.source(), fence: playbackFence({...fence, generation: 4})});
    else if (change === 'recovered') f.replace({...f.source(), playback: {phase: 'ready', intent: 'playing'}});
    else f[change]();
    assert.equal(f.capturedCurrent(capture), false); action(); await settle();
    assert.equal(f.reads.length, 0); assert.equal(f.openings.length, 0); assert.equal(f.presented.length, 0);
  } finally {f.instance.dispose();}
});

test('handheld module supplies the same live guide and record/actions owners to player chrome', async () => {
  const f = await fixture();
  try {
    assert.equal(f.liveGuideArguments.length, 1); assert.equal(f.liveGuideArguments[0][0], f.context); assert.equal(f.liveGuideArguments[0][1], true);
    assert.equal(f.actions[0][1], f.guides[0]); assert.equal(f.actions[0][2](), true);
    const chrome = f.chrome(); assert.equal(chrome.liveGuide, f.guides[0]); assert.ok(chrome.liveActions);
    chrome.onGuide(); assert.equal(f.guideCalls(), 1);
    f.replace({...f.source(), dock: {local: {...f.source().dock.local, expanded: false}}}); assert.equal(f.actions[0][2](), false);
  } finally {f.instance.dispose();}
});
