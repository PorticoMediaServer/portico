import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {nativeDestinationFocusIds} from '../src-next/screens/destinations/focus.ts';

test('Saved TV tabs retain real route IDs, actual selection and the Rail Saved edge while hidden callbacks retire', async () => {
  const host = hooks(), requests: unknown[] = [], selections: unknown[] = [];
  const {useSavedTVTabs} = await screenModule(new URL('../src-next/screens/saved/useSavedTVTabs.ts', import.meta.url), {
    react: host.react, '../../../../../packages/i18n/src': {defaultI18n: {t: (id: string) => id}},
    '../destinations/focus': {nativeDestinationFocusIds}, '../destinations/useEntryFocus': {useDestinationEntryFocus: (...args: unknown[]) => requests.push(args)},
  });
  let visible = true;
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'actual-epoch', viewerFence: 'home-fence'};
  const activation = {subscribe() {}, getSnapshot: () => ({visible, intent: 'enter', revision: 1})};
  const destination = {subscribe() {}, getSnapshot: () => ({route: {view: 'collections'}}), select: async (route: unknown) => {selections.push(route);}};
  const tabs = host.render(() => useSavedTVTabs({destination, activation, authority, reportError() {}}));
  assert.deepEqual(tabs.items.map(item => tabs.itemProps(item).focusId), ['watchlist', 'favorites', 'playlists', 'collections', 'views', 'history']);
  assert.equal(tabs.region.edges.left, nativeDestinationFocusIds(authority).rail);
  assert.equal(tabs.region.edges.down, undefined, 'an absent real body region is never fabricated');
  // The entry follows the shown tab, which is the destination's actual selected route.
  assert.deepEqual((requests[0] as unknown[])[1], {regionId: nativeDestinationFocusIds(authority).savedTabs, childId: 'collections'});
  assert.equal(tabs.value, 'collections');
  tabs.itemProps(tabs.items[1]).onFocus(); assert.deepEqual(selections, [{view: 'favorites'}]);
  visible = false; tabs.onValueChange('playlists'); assert.equal(selections.length, 1, 'a late native event cannot select a hidden retained screen');
});
