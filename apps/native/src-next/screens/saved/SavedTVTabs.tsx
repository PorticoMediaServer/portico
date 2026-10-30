import React, {useEffect, useMemo, useSyncExternalStore} from 'react';
import {Text} from 'react-native';
import {defaultI18n, type MessageId} from '../../../../../packages/i18n/src';
import type {SavedRoute} from '../../../../../packages/client-core/src/saved';
import {LibraryHeader} from '../../kit/browse';
import {browseType} from '../../kit/browse/presentation';
import {Focusable} from '../../kit/controls';
import {FocusRegion} from '../../kit/focus';
import {useTheme} from '../../kit/theme';
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
export function SavedTVTabs({destination, reportError, authority, activation, contentRegionId}: {
  destination: NativeSavedViewDestination; reportError: (error: unknown) => void;
  authority: ShellAuthority; activation: NativeDestinationActivationPort;
  /** Only supplied once the actual content region is mounted. */
  contentRegionId?: string;
}) {
  const theme = useTheme(), active = useSyncExternalStore(activation.subscribe, activation.getSnapshot);
  const source = useSyncExternalStore(destination.subscribe, destination.getSnapshot), value = source.route?.view ?? 'watchlist';
  const ids = useMemo(() => nativeDestinationFocusIds(authority), [authority]);
  // The shown tab is the fallback entry only; the landing below takes the first card/tile when
  // there is one (tv-more1/3 and collections 15).
  useDestinationEntryFocus(active, {regionId: ids.savedTabs, childId: value});
  useEffect(() => {if (active.visible && !source.route) void destination.select({view: 'watchlist'}).catch(reportError);}, [active.visible, destination, reportError, source.route]);
  const choose = (view: NativeSavedTVTab) => {if (activation.getSnapshot().visible) void destination.select({view}).catch(reportError);};
  return <LibraryHeader testID="native-saved-tv-header" name={defaultI18n.t('saved.title')} switcher={undefined}>
    <FocusRegion id={ids.savedTabs} entry="watchlist" orientation="horizontal" enabled={active.visible} edges={{left: ids.rail, ...(contentRegionId ? {down: contentRegionId} : {})}}
      style={{flexDirection: 'row', alignItems: 'center', gap: 34}}>
      {nativeSavedTVTabs.map(tab => <Focusable key={tab.view} focusId={tab.view} testID={`saved-tab-${tab.view}`} accessibilityRole="tab"
        accessibilityLabel={defaultI18n.t(tab.label)} accessibilityState={{selected: value === tab.view}} disabled={!active.visible}
        onFocus={() => choose(tab.view)} onPress={() => choose(tab.view)}
        style={{height: 64, justifyContent: 'center', borderBottomWidth: value === tab.view ? 4 : 0, borderBottomColor: theme.colors.accentStrong}}>
        <Text style={[browseType(theme, 24, 700), {color: value === tab.view ? theme.colors.text : theme.colors.textTertiary}]}>{defaultI18n.t(tab.label)}</Text>
      </Focusable>)}
    </FocusRegion>
  </LibraryHeader>;
}
