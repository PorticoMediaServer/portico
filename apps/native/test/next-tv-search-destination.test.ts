import {retainRecognizerReturn} from '../src-next/kit/focus/recognizer-return.ts';
import {sameAuthority} from '../src-next/shell/model.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import {nativeSearchGenreTiles} from '../src-next/screens/search/genre-tiles.ts';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {createTheme} from '../src-next/kit/theme.ts';
import {nativeDestinationFocusIds} from '../src-next/screens/destinations/focus.ts';
import {createFocusRegistry} from '../src-next/kit/focus/registry.ts';
import {nativeSearchEntryKey} from '../src-next/screens/search/model.ts';

type Element = {type: any; props: Record<string, any>};
function elements(node: any): Element[] {
  if (Array.isArray(node)) return node.flatMap(elements);
  return node && typeof node === 'object' && 'props' in node ? [node, ...elements(node.props.children)] : [];
}

test('the actual TV Search caller binds genuine query, navigation activation and Rail Search without a fake selection port', async () => {
  const host = hooks(), calls: string[] = [], registry = createFocusRegistry(), theme = createTheme({isTV: true, width: 1920});
  const authority = {serverId: 'server-id', accountId: 'account-id', profileId: 'profile-id', epoch: 'epoch', viewerFence: 'home-fence'};
  const ids = nativeDestinationFocusIds(authority), entry = {id: 'actual-entry', kind: 'movie' as const, libraryId: 'actual-library', title: 'Actual result'};
  const group = {id: 'movies', status: 'success', items: [entry], totalCount: 1, totalCountExact: true};
  const state = {text: 'actual query', group: 'all', groups: [group], search: {phase: 'ready', data: {capabilities: {groups: [{id: 'movies', available: true}]}}}, recent: []};
  let activation = {visible: true, intent: 'enter', revision: 5};
  let pageBack: (() => boolean) | undefined;
  const effects: (() => void | (() => void))[] = [], cleanups: (() => void)[] = [];
  const effect = (fn: () => void | (() => void)) => effects.push(fn);
  const render = (fn: () => any) => {
    cleanups.splice(0).forEach(cleanup => cleanup()); effects.length = 0;
    const tree = host.render(fn);
    effects.forEach(fn => {const cleanup = fn(); if (cleanup) cleanups.push(cleanup);}); return tree;
  };
  const {TVSearchScreen} = await screenModule(new URL('../src-next/screens/search/TVSearchScreen.tsx', import.meta.url), {
    react: {...host.react, useEffect: effect, useId: () => 'gradient'}, 'react-native': {ScrollView: 'ScrollView', View: 'View', Text: 'Text', useWindowDimensions: () => ({width: 1920, height: 1080})},
    'react-native-svg': {default: 'Svg'}, '../../../../../packages/client-core/src/presentation/kit-model': {},
    '../../../../../packages/client-core/src/presentation/search-model': {orderedSearchGroups: (groups: unknown) => groups, SEARCH_CHIPS: [], topSearchResult: () => undefined, searchGroupLabel: (id: string) => id},
    '../../../../../packages/i18n/src': {defaultI18n: {t: (id: string) => id}}, '../../kit/artwork': {StandardBackdrop: 'StandardBackdrop'}, '../../kit/controls': {}, '../../kit/cards/primitives': {},
    '../../kit/focus': {FocusRegion: 'FocusRegion', useFocusRegistry: () => registry}, '../../kit/Icon': {Icon: 'Icon'}, '../../kit/states': {}, '../../kit/browse/presentation': {alpha: () => '', browseType: () => ({})},
    '../../kit/theme': {useTheme: () => theme}, '../destinations/focus': {nativeDestinationFocusIds}, '../home/model': {}, './model': {nativeSearchEntryKey},
    './TVSearchSection': {TVSearchSection: 'TVSearchSection'}, './SearchGridCard': {SearchGridCard: 'SearchGridCard'}, './TVKeyboard': {NativeTVKeyboard: 'NativeTVKeyboard'}, '../settings-live/use-tv-voice-search': {useTVVoiceSearch: () => ({start: undefined, begin: undefined, busy: false})},
    '../../kit/focus/recognizer-return': {retainRecognizerReturn}, '../../shell/model': {sameAuthority}, './genre-tiles': {nativeSearchGenreTiles},
  });
  const props = {destination: {subscribe() {}, getSnapshot: () => state, setText: (text: string) => calls.push(text)},
    runtime: {getSnapshot: () => ({authority}), registerScrollToTop: () => () => {},
      registerTVPageBack: (handler: () => boolean, destination: unknown) => {assert.deepEqual(destination, {kind: 'search'}); pageBack = handler; return () => {if (pageBack === handler) pageBack = undefined;};}}, activation: {subscribe() {}, getSnapshot: () => activation}, explore: {subscribe() {}, getSnapshot: () => ({genres: []})},
    actions: {}, copy: {preferToTalk: 'voice-title', voiceHint: 'voice-body'}};
  const tree = render(() => TVSearchScreen(props));
  const keyboard = elements(tree).find(node => node.type === 'NativeTVKeyboard')!;
  assert.equal(keyboard.props.value, state.text); assert.equal(keyboard.props.activation, activation);
  assert.equal(keyboard.props.id, ids.searchKeyboard); assert.equal(keyboard.props.leftRegionId, ids.rail);
  assert.equal(keyboard.props.rightRegionId, ids.searchResults); keyboard.props.onChangeText('actual query a'); assert.deepEqual(calls, ['actual query a']);
  const resultRegion = elements(tree).find(node => node.type === 'FocusRegion' && node.props.id === ids.searchResults)!;
  assert.equal(resultRegion.props.entryRegion, ids.searchSection('movies')); assert.equal(resultRegion.props.edges.left, ids.searchKeyboardRow(0));
  const section = elements(tree).find(node => node.type === 'TVSearchSection')!;
  assert.equal(section.props.group.items[0], entry); assert.equal(section.props.selection, undefined); assert.equal(section.props.regionId, ids.searchSection('movies'));
  registry.configure({id: ids.searchSection('movies'), parent: ids.searchResults});
  const row = ids.searchKeyboardRow(3), address = {regionId: row, childId: 't'};
  let focusRequests = 0;
  registry.configure({id: row, entry: 's'});
  registry.attach(address, {requestTVFocus: () => {focusRequests++; registry.didFocus(address);}});
  registry.didFocus(address); keyboard.props.onRowFocus(row);
  render(() => TVSearchScreen(props));
  registry.didFocus({regionId: ids.searchSection('movies'), childId: nativeSearchEntryKey('movies', entry)});
  assert.equal(pageBack!(), true, 'results Back requests the real previously focused key in row3');
  assert.equal(focusRequests, 1);
  assert.equal(pageBack!(), false, 'Back already on keyboard falls through instead of trapping');
  registry.didFocus({regionId: 'actual-menu', childId: 'play'});
  assert.equal(pageBack!(), false, 'a menu owns its own Back');
  registry.didFocus({regionId: ids.rail, childId: 'search'});
  assert.equal(pageBack!(), false, 'rail keeps precedence');
  registry.didFocus({regionId: ids.searchSection('movies'), childId: nativeSearchEntryKey('movies', entry)});
  const prior = pageBack!;
  activation = {visible: false, intent: 'restore', revision: 6};
  assert.equal(prior(), false, 'hidden activation refuses even before React effect cleanup');
  const held = render(() => TVSearchScreen(props));
  assert.equal(held.props.pointerEvents, 'none'); assert.equal(elements(held).find(node => node.type === 'FocusRegion' && node.props.id === ids.searchResults)!.props.enabled, false);
  assert.equal(elements(held).find(node => node.type === 'NativeTVKeyboard')!.props.activation, activation);
  assert.equal(pageBack, undefined, 'hidden retained page unregisters');
  activation = {visible: true, intent: 'restore', revision: 7};
  assert.equal(prior(), false, 'a retired registration cannot revive after Title/Back');
  render(() => TVSearchScreen(props));
  assert.equal(pageBack!(), true, 'restored page retains the real remembered key');
  registry.attach(address, null);
  registry.didFocus({regionId: ids.searchSection('movies'), childId: nativeSearchEntryKey('movies', entry)});
  assert.equal(pageBack!(), false, 'missing key hosts never manufacture focus success');
  cleanups.splice(0).forEach(cleanup => cleanup());
  assert.equal(pageBack, undefined, 'unmount releases the handler');
});
