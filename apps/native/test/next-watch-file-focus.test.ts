// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import * as React from 'react';
import {surfacePaintFixture, screenModule} from './helpers/screen-harness.mjs';
import {createFabricHookHost} from './helpers/fabric-hooks.mjs';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';
import {deletionAllowed} from '../../../packages/client-core/src/administration.ts';
import {mergeExplanation} from '../../../packages/client-core/src/title-identity.ts';
import {durationText} from '../../../packages/client-core/src/presentation/kit-model/text.ts';
import {nativeFileInfoFile, nativeSplitCandidates} from '../src-next/screens/watch/file-info.ts';
import {fakeSession, groupRow, groupsPage, memberRow, membersPage, ownerSurface} from './helpers/file-info-windows.ts';
import {shellLayout} from '../src-next/shell/model.ts';
import * as registry from '../src-next/kit/focus/registry.ts';
import * as hostRefs from '../src-next/kit/focus/useHostRef.ts';
const react = {...React, default: React}, theme = {isTV: true, colors: {}, type: {body: {}, caption: {}, captionStrong: {}, bodyStrong: {}}};
const controls = {Button: 'Button', TextField: 'TextField'};
const themed = {useTheme: () => theme, nativeType: () => ({})};
const {NativeFileInfoContent} = await screenModule(new URL('../src-next/screens/watch/FileInfoContent.tsx', import.meta.url), {
  react, 'react-native': {Platform: {select: (options: Record<string, unknown>) => options.ios ?? options.default}, ScrollView: 'ScrollView', StyleSheet: {absoluteFill: {}}, Text: 'Text', View: 'View'},
  'react-native-svg': {default: 'Svg', Defs: 'Defs', RadialGradient: 'RadialGradient', Rect: 'Rect', Stop: 'Stop'},
  '../../../../../packages/i18n/src': {defaultI18n},
  '../../kit/controls': controls, '../../kit/Icon': {Icon: 'Icon'}, '../../kit/theme': themed,
});
const {NativeWatchOverlays} = await screenModule(new URL('../src-next/screens/watch/WatchOverlays.tsx', import.meta.url), {
  '../../kit/overlays/SurfacePaintHost': surfacePaintFixture, './FileInfoContent': {NativeFileInfoContent},
  react, 'react-native': {Modal: 'Modal', Platform: {isTV: true}, ScrollView: 'ScrollView', StyleSheet: {absoluteFill: {}}, Text: 'Text', View: 'View', useWindowDimensions: () => ({width: 1920, height: 1080})},
  '../../../../../packages/i18n/src': {defaultI18n}, '../../../../../packages/client-core/src/administration': {deletionAllowed}, '../../../../../packages/client-core/src/title-identity': {mergeExplanation},
  '../../../../../packages/client-core/src/presentation/kit-model/text': {durationText},
  './file-info': {nativeFileInfoFile, nativeSplitCandidates}, '../../shell/model': {shellLayout}, '../../kit/controls': controls, '../../kit/artwork': {Artwork: 'Artwork'}, '../../kit/states': {StateView: 'StateView'},
  '../../kit/overlays': {BottomSheet: 'BottomSheet', Dialog: 'Dialog', Menu: 'Menu'}, '../../kit/overlays/Menu': {TouchMenuHeader: 'TouchMenuHeader'},
  '../../kit/overlays/TVPanelMotion': {TVPanelTransition: 'TVPanelTransition', TVPanelSlide: 'TVPanelSlide', TVPanelScrim: 'TVPanelScrim', useTVPanelPresence: (value: unknown) => ({value, open: value !== undefined, exited() {}}), useTVPanelInteractive: () => true},
  './DiscTitlesSheet': {NativeDiscTitlesSheet: 'DiscTitlesSheet'},
  './VersionChooser': {NativeVersionChooser: 'VersionChooser'}, '../../kit/overlays/surface': {FocusBoundary: 'FocusBoundary', PanelBackground: 'PanelBackground'}, '../../kit/overlays/OverlayHost': {useOverlayLifecycle() {}}, '../../kit/theme': themed,
});
const contexts: {current: unknown}[] = [];
const focus = await screenModule(new URL('../src-next/kit/focus/FocusRegion.tsx', import.meta.url), {
  react: {...react, createContext: (current: unknown) => {const value = {current}; contexts.push(value); return value;}, useContext: (context: {current: unknown}) => context.current},
  'react-native': {findNodeHandle: () => null, TVFocusGuideView: 'Guide', View: 'View'}, '../theme': themed, './registry': registry, './useHostRef': hostRefs,
});
function targets(value: any): any[] {
  if (Array.isArray(value)) return value.flatMap(targets);
  if (!React.isValidElement(value)) return [];
  const node: any = value;
  if (typeof node.type === 'function') return targets(node.type(node.props));
  return [...(node.type === 'Button' || node.type === 'TextField' ? [node] : []), ...targets(node.props.children), ...targets(node.props.footer), ...targets(node.props.headerContent)];
}
// The owner's File info session, as the factory opens it: one version of one file, read from the server's windows.
const {session} = fakeSession(path => path.includes('/groups/members') ? membersPage('qm', 'g1', [memberRow('asset', 'asset', {path: '/actual/asset.mkv'})])
  : groupsPage('qg', ownerSurface, [groupRow(1, {headOccurrence: 'asset'})]));
await session.groups(); await session.members('g1');
const entry = {id: 'actual-title', kind: 'movie', title: 'Actual title', libraryId: 'actual-library'}, other = {...entry, id: 'other-title'};
const model = {files: [], owner: {heading: 'Manage files', locations: [{id: 'asset', actions: [{id: 'delete-version', label: 'Delete'}]}], actions: [{id: 'split-title', label: 'Split'}, {id: 'merge-title', label: 'Merge'}]}};
for (const phase of ['file-info', 'delete', 'split-pick', 'split', 'merge-search', 'merge-confirm']) test(`real ${phase} controls mount the strict TV focus hook using stable member/action identities`, async () => {
  const value = {phase: phase.startsWith('merge') ? 'merge' : phase, capture: {entry}, busy: false, candidates: [other], confirmation: '', keep: entry.id,
    ...(phase === 'merge-confirm' ? {other} : {}), fileId: 'asset', owner: {session, kind: 'movie', changed: async () => {}},
    content: NativeFileInfoContent({model, onManage() {}})};
  const presenters = {subscribe: () => () => {}, getSnapshot: () => value, current: () => true, overlayCurrent: () => true, close() {}, backToInfo() {}, search: async () => {}};
  let buttons: any[] = [];
  const host = createFabricHookHost(() => {buttons = targets(React.createElement(NativeWatchOverlays, {presenters, runtime: {}, common: {}}));});
  await host.mount();
  try {
    assert.ok(buttons.length >= 2);
    if (phase === 'split-pick') assert.ok(buttons.some(node => node.props.focusId === 'watch-split-file-g1'), 'a one-file version is offered by its group id');
    const ids = buttons.map(node => node.props.focusId ?? node.props.testID);
    assert.equal(new Set(ids).size, ids.length); assert.ok(ids.every(id => typeof id === 'string' && id.startsWith('watch-')));
    const region = registry.createFocusRegistry(); region.configure({id: 'watch-file-manage', orientation: 'vertical'});
    contexts[0]!.current = region; contexts[1]!.current = 'watch-file-manage';
    for (const button of buttons) {
      const mounted = createFabricHookHost(() => {focus.useFocusTarget({focusId: button.props.focusId, testID: button.props.testID, disabled: button.props.disabled});});
      await mounted.mount(); await mounted.unmount();
    }
  } finally {await host.unmount();}
});

test('File info draws the real version cards, the informational note and the owner part', () => {
  const texts = (node: any): string[] => Array.isArray(node) ? node.flatMap(texts)
    : node?.props ? [typeof node.props.children === 'string' ? node.props.children : '', ...texts(node.props.children)] : [];
  const model = {files: [{id: 'v1', display: {heading: {text: 'Version 1'}, summary: [{text: '4K HDR'}],
    video: [{id: 'v', parts: [{id: 'codec', text: 'HEVC'}]}], audio: [{id: 'a', parts: [{id: 'lang', text: 'English'}]}], subtitles: []},
    rows: [{id: 'resolution', label: 'Resolution', parts: [{text: '4K'}]}, {id: 'size', label: 'Size', parts: [{text: '61.4 GB'}]},
      {id: 'length', label: 'Length', parts: [{text: '2 h 46 min'}]}]}],
    owner: {heading: 'Location and manage', locations: [], actions: []}} as any;
  const tree = NativeFileInfoContent({model, onManage() {}}), words = texts(tree);
  assert.ok(words.includes('Version 1'));
  assert.ok(words.includes(defaultI18n.t('next.overlays.native.fileInfoNote')));
  assert.ok(words.includes('Location and manage'));
});
