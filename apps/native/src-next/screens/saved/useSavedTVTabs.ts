import {useEffect, useMemo, useSyncExternalStore} from 'react';
import {defaultI18n, type MessageId} from '../../../../../packages/i18n/src';
import type {SavedRoute} from '../../../../../packages/client-core/src/saved';
import type {BrowseHeaderProps} from '../../kit/frames';
import type {ShellAuthority} from '../../shell/model';
import {nativeDestinationFocusIds, type NativeDestinationActivationPort} from '../destinations/focus';
import {useDestinationEntryFocus} from '../destinations/useEntryFocus';
import type {NativeSavedViewDestination} from './view-model';

export type NativeSavedTVTab = Extract<SavedRoute['view'], 'watchlist' | 'favorites' | 'playlists' | 'collections' | 'views' | 'history'>;
export const nativeSavedTVTabs: readonly Readonly<{view: NativeSavedTVTab; label: MessageId}>[] = Object.freeze([
  {view: 'watchlist', label: 'saved.tab.watchlist'}, {view: 'favorites', label: 'saved.tab.favorites'},
  {view: 'playlists', label: 'saved.tab.playlists'}, {view: 'collections', label: 'saved.tab.collections'},
  {view: 'views', label: 'saved.tab.savedViews'}, {view: 'history', label: 'saved.tab.history'},
]);

/** tv-more1–5 / collections15–17. Each tab selects the actual scoped Saved
 * producer; the body remains a separate renderer of that producer's snapshot. */
export function useSavedTVTabs({destination, reportError, authority, activation, contentRegionId}: {
  destination: NativeSavedViewDestination; reportError: (error: unknown) => void;
  authority: ShellAuthority; activation: NativeDestinationActivationPort;
  /** Only supplied once the actual content region is mounted. */
  contentRegionId?: string;
}): NonNullable<BrowseHeaderProps['tabs']> {
  const active = useSyncExternalStore(activation.subscribe, activation.getSnapshot);
  const source = useSyncExternalStore(destination.subscribe, destination.getSnapshot), value = source.route?.view ?? 'watchlist';
  const ids = useMemo(() => nativeDestinationFocusIds(authority), [authority]);
  // The shown tab is the fallback entry only; the landing below takes the first card/tile when
  // there is one (tv-more1/3 and collections 15).
  useDestinationEntryFocus(active, {regionId: ids.savedTabs, childId: value});
  useEffect(() => {if (active.visible && !source.route) void destination.select({view: 'watchlist'}).catch(reportError);}, [active.visible, destination, reportError, source.route]);
  const choose = (id: string) => {
    const tab = nativeSavedTVTabs.find(item => item.view === id);
    if (tab && activation.getSnapshot().visible) void destination.select({view: tab.view}).catch(reportError);
  };
  return {
    items: nativeSavedTVTabs.map(tab => ({value: tab.view, label: defaultI18n.t(tab.label)})),
    value,
    disabled: !active.visible,
    onValueChange: choose,
    itemProps: item => ({focusId: item.value, testID: `saved-tab-${item.value}`, onFocus: () => choose(item.value)}),
    region: {id: ids.savedTabs, enabled: active.visible, edges: {left: ids.rail, down: contentRegionId}},
  };
}
