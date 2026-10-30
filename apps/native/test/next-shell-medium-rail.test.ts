import {headerWordmark} from '../src-next/kit/shell/brand';
import test from 'node:test';
import assert from 'node:assert/strict';
import {createTheme, nativeType} from '../src-next/kit/theme.ts';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import * as model from '../src-next/shell/model.ts';
import {sameDestination} from '../src-next/shell/tv-focus.ts';

type Node = {type: unknown; props?: Record<string, unknown>};

function tree(node: unknown, out: Node[] = []): Node[] {
  if (Array.isArray(node)) {for (const child of node) tree(child, out); return out;}
  if (!node || typeof node !== 'object') return out;
  const element = node as Node;
  out.push(element);
  tree(element.props?.children, out);
  return out;
}

test('medium rail draws the drawn order, selects the current destination and pins Settings and Profile', async () => {
  const h = hooks(), theme = createTheme({isTV: false, width: 840});
  const {MediumRail} = await screenModule(new URL('../src-next/shell/MediumRail.tsx', import.meta.url), {
    react: h.react, 'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View'},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 24, bottom: 20, left: 0, right: 0})},
    'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', Rect: 'Rect', Stop: 'Stop'},
    '../../assets/brandSymbol': {brandSymbol: '<svg/>'},
    '../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
    '../kit/controls': {Focusable: 'Focusable'}, '../kit/Icon': {Icon: 'Icon'}, '../kit/artwork': {ProfileFace: 'ProfileFace'},
    '../kit/shell/brand': {headerWordmark}, '../kit/theme': {nativeType, useTheme: () => theme}, '../platform/native-blur': {BlurView: 'BlurView'},
    './model': model, './tv-focus': {sameDestination},
  });
  const libraries = [{id: 'movies', name: 'Movies', kind: 'movie'}, {id: 'shows', name: 'TV Shows', kind: 'tv'},
    {id: 'tunes', name: 'Music', kind: 'music'}, {id: 'talks', name: 'Audiobooks', kind: 'audiobook'}, {id: 'comics', name: 'Comics', kind: 'comic'}];
  const opened: unknown[] = [], profile: unknown[] = [];
  const element = h.render(() => MediumRail({libraries, hasLiveTV: true, destination: {kind: 'home'}, profile: {name: 'D', initial: 'D'} as never,
    unread: true, onNavigate: (next: unknown) => opened.push(next), onProfile: () => profile.push(true), profileRef: {current: null}, short: true}));
  const nodes = tree(element);
  const ids = nodes.map(node => node.props?.testID).filter((id): id is string => typeof id === 'string');
  assert.deepEqual(ids, ['medium-rail', 'medium-rail-search', 'medium-rail-home', 'medium-rail-library:movies', 'medium-rail-library:shows',
    'medium-rail-library:tunes', 'medium-rail-library:talks', 'medium-rail-library:comics', 'medium-rail-live', 'medium-rail-saved',
    'medium-rail-settings', 'medium-rail-profile']);
  const byId = (id: string) => nodes.find(node => node.props?.testID === id)!;
  assert.equal((byId('medium-rail-home').props?.accessibilityState as {selected: boolean}).selected, true);
  assert.equal((byId('medium-rail-search').props?.accessibilityState as {selected: boolean}).selected, false);
  assert.equal(byId('medium-rail-library:talks').props?.accessibilityLabel, 'Audiobooks');
  (byId('medium-rail-library:movies').props?.onPress as () => void)();
  assert.deepEqual(opened, [{kind: 'library', libraryId: 'movies'}]);
  (byId('medium-rail-profile').props?.onPress as () => void)();
  assert.deepEqual(profile, [true]);
});

test('medium rail drops Live TV when the server has none and keeps the current library selected', async () => {
  const h = hooks(), theme = createTheme({isTV: false, width: 890});
  const {MediumRail} = await screenModule(new URL('../src-next/shell/MediumRail.tsx', import.meta.url), {
    react: h.react, 'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View'},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, bottom: 0, left: 0, right: 0})},
    'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', Rect: 'Rect', Stop: 'Stop'},
    '../../assets/brandSymbol': {brandSymbol: '<svg/>'},
    '../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
    '../kit/controls': {Focusable: 'Focusable'}, '../kit/Icon': {Icon: 'Icon'}, '../kit/artwork': {ProfileFace: 'ProfileFace'},
    '../kit/shell/brand': {headerWordmark}, '../kit/theme': {nativeType, useTheme: () => theme}, '../platform/native-blur': {BlurView: 'BlurView'},
    './model': model, './tv-focus': {sameDestination},
  });
  const element = h.render(() => MediumRail({libraries: [{id: 'shows', name: 'TV Shows', kind: 'tv'}], hasLiveTV: false,
    destination: {kind: 'library', libraryId: 'shows'}, profile: {name: 'D', initial: 'D'} as never, unread: false,
    onNavigate: () => {}, onProfile: () => {}, profileRef: {current: null}, short: false}));
  const nodes = tree(element), ids = nodes.map(node => node.props?.testID).filter((id): id is string => typeof id === 'string');
  assert.equal(ids.includes('medium-rail-live'), false);
  const shows = nodes.find(node => node.props?.testID === 'medium-rail-library:shows')!;
  assert.equal((shows.props?.accessibilityState as {selected: boolean}).selected, true);
});

test('an owner sees the icon-only Server activity after Saved, never a Notifications dead end', async () => {
  const h = hooks(), theme = createTheme({isTV: false, width: 840});
  const {MediumRail} = await screenModule(new URL('../src-next/shell/MediumRail.tsx', import.meta.url), {
    react: h.react, 'react-native': {ScrollView: 'ScrollView', Text: 'Text', View: 'View'},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top: 0, bottom: 0, left: 0, right: 0})},
    'react-native-svg': {default: 'Svg', Defs: 'Defs', LinearGradient: 'LinearGradient', Rect: 'Rect', Stop: 'Stop'},
    '../../assets/brandSymbol': {brandSymbol: '<svg/>'},
    '../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}},
    '../kit/controls': {Focusable: 'Focusable'}, '../kit/Icon': {Icon: 'Icon'}, '../kit/artwork': {ProfileFace: 'ProfileFace'},
    '../kit/shell/brand': {headerWordmark}, '../kit/theme': {nativeType, useTheme: () => theme}, '../platform/native-blur': {BlurView: 'BlurView'},
    './model': model, './tv-focus': {sameDestination},
  });
  const opened: unknown[] = [];
  const element = h.render(() => MediumRail({libraries: [{id: 'movies', name: 'Movies', kind: 'movie'}], hasLiveTV: true, destination: {kind: 'home'},
    profile: {name: 'D', initial: 'D'} as never, unread: false, serverAction: {id: 'server', label: 'Dashboard', onPress: () => opened.push('server')},
    onNavigate: () => {}, onProfile: () => {}, profileRef: {current: null}, short: false}));
  const nodes = tree(element), ids = nodes.map(node => node.props?.testID).filter((id): id is string => typeof id === 'string');
  assert.deepEqual(ids, ['medium-rail', 'medium-rail-search', 'medium-rail-home', 'medium-rail-library:movies', 'medium-rail-live', 'medium-rail-saved',
    'medium-rail-server', 'medium-rail-settings', 'medium-rail-profile']);
  (nodes.find(node => node.props?.testID === 'medium-rail-server')!.props?.onPress as () => void)();
  assert.deepEqual(opened, ['server']);
  assert.equal(ids.includes('medium-rail-notifications'), false, 'no screen registers Notifications, so the rail does not draw it');
});
