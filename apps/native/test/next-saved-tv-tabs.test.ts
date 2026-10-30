import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {createTheme} from '../src-next/kit/theme.ts';
import {nativeDestinationFocusIds} from '../src-next/screens/destinations/focus.ts';

function elements(node: any): any[] {
  if (Array.isArray(node)) return node.flatMap(elements);
  return node && typeof node === 'object' && 'props' in node ? [node, ...elements(node.props.children)] : [];
}
test('Saved TV tabs retain real route IDs, actual selection and the Rail Saved edge while hidden callbacks retire', async () => {
  const host = hooks(), requests: unknown[] = [], selections: unknown[] = [], theme = createTheme({width: 1920, isTV: true});
  const {SavedTVTabs} = await screenModule(new URL('../src-next/screens/saved/SavedTVTabs.tsx', import.meta.url), {
    react: host.react, 'react-native': {Text: 'Text'}, '../../../../../packages/i18n/src': {defaultI18n: {t: (id: string) => id}},
    '../../kit/browse': {LibraryHeader: 'LibraryHeader'}, '../../kit/browse/presentation': {browseType: () => ({})},
    '../../kit/controls': {Focusable: 'Focusable'}, '../../kit/focus': {FocusRegion: 'FocusRegion'}, '../../kit/theme': {useTheme: () => theme},
    '../destinations/focus': {nativeDestinationFocusIds}, '../destinations/useEntryFocus': {useDestinationEntryFocus: (...args: unknown[]) => requests.push(args)},
  });
  let visible = true;
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'actual-epoch', viewerFence: 'home-fence'};
  const activation = {subscribe() {}, getSnapshot: () => ({visible, intent: 'enter', revision: 1})};
  const destination = {subscribe() {}, getSnapshot: () => ({route: {view: 'collections'}}), select: async (route: unknown) => {selections.push(route);}};
  const tree = host.render(() => SavedTVTabs({destination, activation, authority, reportError() {}}));
  const nodes = elements(tree), tabs = nodes.filter(node => node.type === 'Focusable'), region = nodes.find(node => node.type === 'FocusRegion');
  assert.deepEqual(tabs.map(node => node.props.focusId), ['watchlist', 'favorites', 'playlists', 'collections', 'views', 'history']);
  assert.equal(region.props.edges.left, nativeDestinationFocusIds(authority).rail);
  assert.equal(region.props.edges.down, undefined, 'an absent real body region is never fabricated');
  // The entry follows the shown tab, which is the destination's actual selected route.
  assert.deepEqual((requests[0] as unknown[])[1], {regionId: nativeDestinationFocusIds(authority).savedTabs, childId: 'collections'});
  assert.equal(tabs[3].props.accessibilityState.selected, true);
  tabs[1].props.onFocus(); assert.deepEqual(selections, [{view: 'favorites'}]);
  visible = false; tabs[2].props.onPress(); assert.equal(selections.length, 1, 'a late native event cannot select a hidden retained screen');
});
