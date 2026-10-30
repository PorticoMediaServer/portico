// @fabric-react
import {subtitleReason} from '../../../packages/client-core/src/subtitles.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {screenModule} from './helpers/screen-harness.mjs';
import {act, createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {React as fabricReact} from './helpers/fabric-host.mjs';
import {sceneHost} from './helpers/scene-host.mjs';
import {playbackFence, localPlaybackFence, sameFence} from '../../../packages/player-core/src/index.ts';
import {parseOptions} from '../../../packages/client-core/src/playback-v1/types.ts';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';
import {nativeWatchTracks} from '../src-next/screens/watch/track-identity.ts';
import {nativeWatchPanelRows} from '../src-next/screens/watch/panel-model.ts';
import {nativeWatchSubtitleTiming} from '../src-next/screens/watch/subtitle-timing.ts';
let isTV = false;
/** The actual panel uses the production React instance owned by Fabric. */
const panelsModule = (react: any) => {
  return screenModule(new URL('../src-next/screens/watch/WatchPanels.tsx', import.meta.url), {'../../kit/states': {Quiet: ({children}: any) => children}, 
  react: {...react, default: react}, 'react-native': {ActivityIndicator: 'Loading', ScrollView: 'Scroll', Text: 'Text', View: 'View'},
  '../../../../../packages/player-core/src': {sameFence}, '../../../../../packages/i18n/src': {defaultI18n},
  '../../../../../packages/client-core/src/presentation/kit-model/text': {clockText: String},
  '../../../../../packages/client-core/src/playback-v1/v1-queue-player': {queueVideoKind: () => false},
  '../../kit/overlays': {Dialog: 'Dialog'}, '../../kit/overlays/TVPanelMotion': {useTVPanelInteractive: () => true},
  '../../shell/model': {sameAuthority: () => false},
  '../../kit/artwork': {Artwork: 'Artwork'},
  '../../kit/controls': {Button: 'Button', Focusable: 'Row'}, '../../kit/focus': {FocusRegion: 'Focus', useFocusRegistry: () => ({focus: () => false})},
  '../../kit/overlays/OverlayHost': {useOverlayLifecycle() {}}, '../../kit/overlays/surface': {PanelBackground: 'Background'},
  '../../kit/Icon': {Icon: 'Icon'}, '../../kit/theme': {nativeType: () => ({}), useTheme: () => ({isTV, colors: {}, type: {bodyStrong: {}, caption: {}}})},
  './track-identity': {nativeWatchTracks}, './panel-model': {nativeWatchPanelRows},
  './subtitle-timing': {nativeWatchSubtitleTiming},
  '../../../../../packages/client-core/src/subtitles': {subtitleReason},
  });
};
const {NativeWatchPanels} = await panelsModule(React);
const fence = playbackFence({kind: 'queue', playback: {scope: {serverId: 's', accountId: 'a', profileId: 'p', controllerId: 'c', controllerEpoch: 'e', commandLaneId: 'l', authority: 'local'}, run: 1}, sessionId: 'session', itemId: 'item', sourceId: 'v2', entryId: 'entry', generation: 3});
const options = parseOptions({itemId: 'item', versions: [{id: 'v2', audio: [{id: 'first', default: true}, {id: 'chosen'}], subtitles: [{id: 'first', default: true}, {id: 'chosen'}]}], qualities: [{mode: 'original'}, {mode: 'automatic'}]});
const descendants = (node: any): any[] => Array.isArray(node) ? node.flatMap(descendants) : React.isValidElement(node)
  ? [node, ...descendants(typeof (node as any).props.children === 'function' ? (node as any).props.children({focused: false}) : (node as any).props.children)] : [];

test('downloaded audio, saved subtitles and timing reuse the real panel without requesting online options', async () => {
  for (const tv of [false, true]) {
    isTV = tv;
    const local = localPlaybackFence({kind: 'local', playback: {kind: 'local', scope: {authority: 'local', serverId: 's', accountId: 'a', profileId: 'p', sessionFamilyId: 'family'}, run: 1},
      sessionId: 'local:copy:1', generation: 1, itemId: 'item', entryId: 'copy', playId: 'play', artifact: {sha256: 'a'.repeat(64), bytes: 100}});
    const calls: unknown[][] = [];
    const source: any = {fence: local, retired: false, dock: {local: {fence: local, expanded: true}}, playback: {intentId: 4, positionSeconds: 12}, queue: {},
      capabilities: {audioTrack: true, subtitleTrack: true}, effectiveRate: 1, supportedRates: [1, 1.5],
      localTracks: {binding: {intentId: 4, sessionId: local.sessionId, generation: 1}, audioTrackId: 'audio-2', subtitleTrackId: 'saved', subtitleOffsetMs: 250, subtitleTimingSupported: true,
        audio: [{id: 'audio-1', codec: 'aac'}, {id: 'audio-2', codec: 'aac'}], subtitles: [{id: 'saved', format: 'vtt', source: 'sidecar'}]},
      subtitles: {catalog: {provider: {enabled: true}}, candidates: []}};
    let context: any = {source, fence: local, state: {fence: local, panel: 'audio', panelOffset: 0}, panelCurrent: () => true,
      runtime: {text: {locale: 'en-US'}, getSnapshot: () => ({serverName: 'Test server'})}, layout: {mode: 'watch'},
      renderer: {width: tv ? 1920 : 390, height: tv ? 1080 : 844, scale: 1}, closePanel() {}, backPanel() {}, rememberPanel() {},
      openPanel: (panel: string) => {context = {...context, state: {...context.state, panel}};}};
    const controller = {getSnapshot: (() => {const snapshot = {phase: 'failed', error: new Error('offline')}; return () => snapshot;})(), subscribe: () => () => {}, load: async () => {assert.fail('a local panel must not request online options');}, clear() {}};
    const playback = {getSnapshot: () => source, change: async (...args: unknown[]) => {calls.push(args);}};
    const names = {audio: (track: any) => ({primary: track.id}), subtitle: (track: any) => ({primary: track.id}), quality: assert.fail, audioFooter: () => [], qualityFooter: () => []};
    let tree: any;
    const host = createFabricHookHost(() => {tree = NativeWatchPanels({context, options: controller, playback, names,
      copy: {sessionLead: '', sessionDefaults: '', audioContinues: ''}, reportError: assert.fail, renderError: assert.fail});});
    await host.mount();
    try {
      assert.equal(descendants(tree).some(node => node.type === 'Loading'), false);
      await act(() => {descendants(tree).find(node => node.props.testID === 'watch-choice-audio-1').props.onPress();});
      assert.deepEqual(calls[0], [local, {audioTrackId: 'audio-1'}]);
      await act(async () => {context.openPanel('subtitles');await host.rerender();});
      assert.equal(descendants(tree).some(node => node.props.testID === 'watch-action-subtitle-search'), false);
      await act(() => {descendants(tree).find(node => node.props.testID === 'watch-choice-subtitle-off').props.onPress();});
      assert.deepEqual(calls[1], [local, {subtitleTrackId: null}]);
      await act(() => {descendants(tree).find(node => node.props.focusId === 'watch-subtitle-offset-500').props.onPress();});
      assert.deepEqual(calls[2], [local, {subtitleTrackId: 'saved', subtitleOffsetMs: 750}]);
      await act(async () => {source.localTracks = {...source.localTracks, subtitleTimingSupported: false};await host.rerender();});
      assert.equal(descendants(tree).some(node => node.props.focusId === 'watch-subtitle-offset-500' || node.props.focusId === 'watch-subtitle-offset--500'), false);
      assert.equal(descendants(tree).some(node => node.props.testID === 'watch-choice-saved'), true);
      await act(async () => {source.localTracks = {...source.localTracks, subtitleError: true};await host.rerender();});
      const subtitleFailure = descendants(tree).find(node => node.props.testID === 'watch-subtitle-local-error');
      assert.equal(subtitleFailure.props.accessibilityRole, 'alert');
      assert.equal(subtitleFailure.props.children, defaultI18n.t('player.opt.unavailable'));
      assert.equal(descendants(tree).find(node => node.props.testID === 'watch-choice-subtitle-off').props.disabled, false);
      await act(async () => {source.localTracks = {...source.localTracks, subtitleError: false};await host.rerender();});
      assert.equal(descendants(tree).some(node => node.props.testID === 'watch-subtitle-local-error'), false);
      await act(async () => {context.openPanel('options');await host.rerender();});
      assert.equal(descendants(tree).some(node => node.props.testID === 'watch-option-quality'), false);
    } finally {await host.unmount();}
  }
});

test('real panel renderer sends exact selected-source track/quality commands, blocks stale actions and retains one focus owner', async () => {
  const {NativeWatchPanels} = await panelsModule(fabricReact);
  for (const tv of [false, true]) {
    isTV = tv; const calls: unknown[][] = [], listeners = new Set<() => void>();
    let source: any = {queue: {}, retired: false, fence, dock: {local: {fence, expanded: true}}, options,
      playback: {positionSeconds: 2}, supportedRates: [1, 1.5], effectiveRate: 1,
      session: {id: 'session', itemId: 'item', versionId: 'v2', queue: {entryId: 'entry'}, presentation: {generation: 3, audioTrackId: 'chosen', quality: {mode: 'original'}}}};
    let context: any = {panelCurrent: () => true, source, fence, state: {fence, panel: 'audio', panelOffset: 0}, layout: {mode: 'watch'}, runtime: {text: {locale: 'en-US'}, getSnapshot: () => ({serverName: 'Test server'})},
      renderer: {width: tv ? 1920 : 390, height: tv ? 1080 : 844, scale: 1},
      closePanel() {}, backPanel() {}, openPanel: (panel: string) => {context = {...context, state: {...context.state, panel}};}, rememberPanel() {}};
    const controller = {getSnapshot: () => snapshot, subscribe: (listener: () => void) => {listeners.add(listener); return () => {listeners.delete(listener);};}, load: async () => {}, clear() {}};
    const snapshot = {phase: 'ready', fence, options};
    const playback = {getSnapshot: () => source, change: async (...args: unknown[]) => {calls.push(args);}, chooseQuality: async (...args: unknown[]) => {calls.push(args);}, setRate: (...args: unknown[]) => {calls.push(args);}};
    const names = {audio: (track: any) => ({primary: track.id}), subtitle: (track: any) => ({primary: track.id}), quality: (choice: any) => ({primary: choice.mode}), audioFooter: () => [], qualityFooter: () => []};
    let tree: any;
    // Real renderer: the panel's store subscription and its cleanup are React's.
    const host = await sceneHost(() => {tree = NativeWatchPanels({context, options: controller, playback, names, copy: {sessionLead: 'Actual localized lead', sessionDefaults: 'Actual localized defaults', audioContinues: 'Actual localized continuing audio'}, reportError: assert.fail, renderError: assert.fail}); return tree;});
    try {
      assert.equal(descendants(tree).filter(node => node.type === 'Focus').length, 1);
      await host.act(() => {descendants(tree).find(node => node.props.testID === 'watch-choice-first').props.onPress();});
      assert.deepEqual(calls[0], [fence, {audioTrackId: 'first'}]);
      context.openPanel('quality'); await host.update();
      await host.act(() => {descendants(tree).find(node => node.props.testID === 'watch-choice-automatic').props.onPress();});
      assert.deepEqual(calls[1], [fence, options.qualities[1]]);
      source = {...source, fence: {...fence, generation: 4}};
      await host.update();
      await host.act(() => {descendants(tree).find(node => node.props.testID === 'watch-choice-automatic').props.onPress();});
      assert.equal(calls.length, 2);
    } finally {await host.unmount();}
    assert.equal(listeners.size, 0);
  }
});

test('a retiring panel command cannot keep a new source busy or report its old failure on the new panel', async () => {
  const {NativeWatchPanels} = await panelsModule(fabricReact);
  isTV = true;
  let rejectOld!: (error: unknown) => void;
  const held = new Promise<void>((_, reject) => {rejectOld = reject;}), errors: unknown[] = [];
  let source: any = {queue: {}, retired: false, fence, dock: {local: {fence, expanded: true}}, options, playback: {positionSeconds: 2},
    session: {id: 'session', itemId: 'item', versionId: 'v2', queue: {entryId: 'entry'}, presentation: {generation: 3, audioTrackId: 'chosen', quality: {mode: 'original'}}}};
  let context: any = {panelCurrent: () => true, source, fence, state: {fence, panel: 'audio', panelOffset: 0}, runtime: {text: {locale: 'en-US'}, getSnapshot: () => ({serverName: 'Test server'})}, layout: {mode: 'full-window'},
    renderer: {width: 1920, height: 1080, scale: 1}, backPanel() {}, closePanel() {}, openPanel() {}, rememberPanel() {}};
  let snapshot = {phase: 'ready', fence, options};
  const controller = {getSnapshot: () => snapshot, subscribe: () => () => {}, load: async () => {}, clear() {}};
  const playback = {getSnapshot: () => source, change: () => held};
  const names = {audio: (track: any) => ({primary: track.id}), subtitle: (track: any) => ({primary: track.id}), quality: (choice: any) => ({primary: choice.mode}), audioFooter: () => [], qualityFooter: () => []};
  let tree: any;
  // Real renderer: replaced props are a genuine React update, so a retained
  // command belongs to an old commit rather than to a replayed callback.
  const host = await sceneHost(() => {tree = NativeWatchPanels({context, options: controller, playback, names,
    copy: {sessionLead: 'Actual localized lead', sessionDefaults: 'Actual localized defaults', audioContinues: 'Actual localized continuing audio'}, reportError: (error: unknown) => errors.push(error), renderError: assert.fail}); return tree;});
  try {
    await host.act(() => {descendants(tree).find(node => node.props.testID === 'watch-choice-first').props.onPress();});
    assert.equal(descendants(tree).find(node => node.props.testID === 'watch-choice-first').props.disabled, false);
    assert.equal(descendants(tree).find(node => node.props.testID === 'watch-choice-first').props.accessibilityState.disabled, true);
    assert.equal(descendants(tree).find(node => node.props.testID === 'watch-choice-first').props.onPress, undefined);
    const next = {...fence, generation: 4};
    source = {...source, fence: next, dock: {local: {fence: next, expanded: true}}, session: {...source.session, presentation: {...source.session.presentation, generation: 4}}};
    snapshot = {...snapshot, fence: next}; context = {...context, source, fence: next, state: {...context.state, fence: next}};
    await host.update();
    assert.equal(descendants(tree).find(node => node.props.testID === 'watch-choice-first').props.disabled, false);
    await host.act(async () => {rejectOld(new Error('retired source command')); await Promise.resolve();});
    assert.deepEqual(errors, []);
    assert.equal(descendants(tree).find(node => node.props.testID === 'watch-choice-first').props.disabled, false);
  } finally {await host.unmount();}
});


for (const tv of [true, false]) test(`${tv ? 'TV' : 'phone'} unavailable choice is inert with platform-appropriate focus admission`, async () => {
  isTV = tv;
  const source: any = {queue: {}, retired: false, fence, dock: {local: {fence, expanded: true}}, options, playback: {positionSeconds: 2},
    session: {id: 'session', itemId: 'item', versionId: 'v2', queue: {entryId: 'entry'}, presentation: {generation: 3, audioTrackId: 'first', quality: {mode: 'original'}}}};
  const context: any = {source, fence, state: {fence, panel: 'audio', panelOffset: 0}, panelCurrent: () => true,
    runtime: {text: {locale: 'en-US'}, getSnapshot: () => ({serverName: 'Test server'})}, layout: {mode: 'full-window'}, renderer: {width: 1920, height: 1080, scale: 1},
    closePanel() {}, backPanel() {}, openPanel() {}, rememberPanel() {}};
  const snapshot = {phase: 'ready', fence, options};
  const controller = {getSnapshot: () => snapshot, subscribe: () => () => {}, load: async () => {}, clear() {}};
  const playback = {getSnapshot: () => source, change: assert.fail};
  const names = {audio: (track: any) => ({primary: track.id, unavailable: track.id === 'first'}), subtitle: (track: any) => ({primary: track.id}), quality: (choice: any) => ({primary: choice.mode}), audioFooter: () => [], qualityFooter: () => []};
  let tree: any;
  const host = createFabricHookHost(() => {tree = NativeWatchPanels({context, options: controller, playback, names,
    copy: {sessionLead: '', sessionDefaults: '', audioContinues: ''}, reportError: assert.fail, renderError: assert.fail});});
  await host.mount();
  try {
    const row = descendants(tree).find(node => node.props.testID === 'watch-choice-first');
    assert.equal(row.props.disabled, !tv); assert.equal(row.props.onPress, undefined);
    assert.equal(row.props.accessibilityState.disabled, true); assert.equal(row.props.accessibilityState.selected, true);
    assert.ok(row.props.accessibilityLabel.includes(defaultI18n.t('player.opt.unavailable')));
    if (tv) {
      assert.ok(row.props.ref, 'current value remains the readable entry even when unavailable');
      source.subtitles = {catalog: {provider: {enabled: false, reason: 'subtitle_provider_not_configured'}}};
      context.state = {...context.state, panel: 'subtitles'}; await host.rerender();
      const search = descendants(tree).find(node => node.props.testID === 'watch-action-subtitle-search');
      assert.ok(search, 'a real unconfigured provider keeps its explanatory row');
      assert.equal(search.props.disabled, false); assert.equal(search.props.onPress, undefined);
      assert.equal(search.props.accessibilityState.disabled, true);
      assert.ok(search.props.accessibilityLabel.includes(subtitleReason('subtitle_provider_not_configured', defaultI18n.t)));
    }
  } finally {await host.unmount();}
});
