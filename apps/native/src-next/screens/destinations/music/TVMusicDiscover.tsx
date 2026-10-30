import React, {useLayoutEffect, useMemo, useRef, useSyncExternalStore, type ReactNode} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import {serverTextLabel} from '../../../../../../packages/client-core/src/presentation/server-text';
import {useFocusRegistry} from '../../../kit/focus';
import {Row, type LayoutItemContext, type LayoutSelection} from '../../../kit/layout';
import {discoverCell} from '../../library/discover-cells';
import {StateView} from '../../../kit/states';
import {browseType} from '../../../kit/browse/presentation';
import {useTheme} from '../../../kit/theme';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {nativeDestinationFocusIds, type NativeDestinationActivation} from '../focus';
import {useDestinationEntryFocus} from '../useEntryFocus';
import type {NativeLibraryDestination, NativeLibrarySnapshot} from '../../library/model';
import {MusicMixesRow} from './MusicMixesRow';

type Position = Readonly<{regionId: string; childId: string; y: number}>;
// UI memory belongs to one real destination and vanishes with its authority.
const positions = new WeakMap<NativeLibraryDestination, Position>();
const entryId = (entry: ContentEntry) => entry.id;
const noSubscribe = () => () => {};
const noMixes = () => undefined;
const noRowSelection: LayoutSelection = {active: false, isSelected: () => false, onToggle: () => {}, onEnter: () => {}};

/** music-plus18: real library Discover, Recently played before profile Mixes.
 * Each server rail keeps its own bounded continuation and native focus region, and
 * mounts only the cards in view (LR05) through the kit Row, which keeps the focused
 * and remembered cards mounted so focus and Back land where they were. */
export function TVMusicDiscover({destination, state, runtime, active, anchor, onEntered, renderEntry}: {
  destination: NativeLibraryDestination; state: NativeLibrarySnapshot; runtime: NativeShellRuntime;
  active: NativeDestinationActivation; anchor?: boolean; onEntered: () => void;
  renderEntry: (entry: ContentEntry, width: number, context: LayoutItemContext) => ReactNode;
}) {
  const theme = useTheme(), registry = useFocusRegistry(), scroll = useRef<ScrollView>(null);
  const mixes = useSyncExternalStore(runtime.mixes?.subscribe ?? noSubscribe, runtime.mixes?.getSnapshot ?? noMixes);
  const ids = nativeDestinationFocusIds(runtime.getSnapshot().authority), prefix = `${ids.libraryTabs(state.library.id)}.discover`;
  const tabs = ids.libraryTabs(state.library.id), mixRegion = `${prefix}.mixes`;
  const sections = state.discover.appended.filter(section => section.entries.length > 0);
  const recent = sections.find(section => section.id === 'recently_played');
  const ordered = [...(recent ? [recent] : []), ...sections.filter(section => section !== recent)];
  const hasMixes = mixes?.phase === 'ready' && mixes.rows.length > 0;
  const regions = [...(recent ? [`${prefix}.${recent.id}`] : []), ...(hasMixes ? [mixRegion] : []),
    ...ordered.filter(section => section !== recent).map(section => `${prefix}.${section.id}`)];
  const measured = useRef(new Map<string, number>());
  const firstChild = (region: string) => region === mixRegion ? `music-mix:${mixes!.rows[0]!.id}`
    : ordered.find(section => `${prefix}.${section.id}` === region)?.entries[0]?.id;
  const eligible = (region: string, child: string) => region === mixRegion ? !!mixes?.rows.some(row => `music-mix:${row.id}` === child)
    : !!ordered.find(section => `${prefix}.${section.id}` === region)?.entries.some(entry => entry.id === child);
  const rememberedChild = (region: string) => {const prior = registry.remembered(region); return prior && eligible(region, prior) ? prior : firstChild(region);};
  const saved = positions.get(destination);
  const region = active.intent === 'restore' && saved && eligible(saved.regionId, saved.childId) ? saved.regionId
    : anchor && hasMixes ? mixRegion : regions[0] ?? tabs;
  const child = region === tabs ? 'discover' : rememberedChild(region);
  const entryActivation = useMemo(() => ({...active, intent: active.intent === 'restore' ? 'enter' as const : active.intent}), [active]);
  const focused = (regionId: string, childId: string) => {
    const y = Math.max(0, measured.current.get(regionId) ?? 0);
    positions.set(destination, {regionId, childId, y});
    scroll.current?.scrollTo({y, animated: false}); onEntered();
  };
  // More waits for the real row; a temporarily loading Mixes read must not land
  // on an unrelated rail and consume the opening's focus request.
  const waitingForMixes = anchor && (mixes?.phase === 'idle' || mixes?.phase === 'loading');
  useDestinationEntryFocus(entryActivation, !waitingForMixes && region && child ? {regionId: region, childId: child} : undefined,
    () => {if (region && child) focused(region, child);});
  useLayoutEffect(() => {if (!regions.length && !waitingForMixes) onEntered();}, [regions.length, waitingForMixes, onEntered]);
  const edges = (id: string) => {const index = regions.indexOf(id); return {up: regions[index - 1] ?? tabs, down: regions[index + 1], left: ids.rail};};
  const mixNode = runtime.mixes && <View key="mixes" onLayout={event => measured.current.set(mixRegion, event.nativeEvent.layout.y)}>
    <MusicMixesRow port={runtime.mixes} artwork={runtime.artwork} reportError={runtime.reportError} active={active.visible}
      focus={{id: mixRegion, ...edges(mixRegion)}} onCardFocus={id => focused(mixRegion, id)}/>
  </View>;
  return <ScrollView ref={scroll} testID="tv-music-discover" showsVerticalScrollIndicator={false} contentContainerStyle={{paddingTop: 36, paddingBottom: 64}}>
    {state.discover.error && <StateView {...runtime.error(state.discover.error, destination.retry)}/>}
    {anchor && mixes?.phase === 'error' && runtime.mixes && <StateView {...runtime.error(mixes.error, () => {void runtime.mixes?.load().catch(runtime.reportError);})}/>}
    {state.discover.projection?.empty && !sections.length && !hasMixes && <StateView title={serverTextLabel(defaultI18n, state.discover.projection.empty)}/>}
    {!recent && mixNode}
    {ordered.map(section => {
      const id = `${prefix}.${section.id}`, width = section.entries.every(entry => entry.kind === 'category') ? 320 : 200, cell = discoverCell(theme, width, section.entries);
      return <React.Fragment key={section.id}>
        <View onLayout={event => measured.current.set(id, event.nativeEvent.layout.y)} style={{marginTop: section === ordered[0] ? 0 : 44}}>
          <Text accessibilityRole="header" style={[browseType(theme, 32, 700, -.019), {color: theme.colors.text, marginHorizontal: theme.gutter, marginBottom: 22}]}>{serverTextLabel(defaultI18n, section.heading)}</Text>
          <Row title={serverTextLabel(defaultI18n, section.heading)} showHeading={false} items={section.entries} getKey={entryId}
            itemWidth={width} itemHeight={cell.art} captionHeight={cell.caption} gap={28} gutter={theme.gutter} selection={noRowSelection}
            focus={{id, enabled: active.visible, entry: section.entries[0]!.id, edges: edges(id)}}
            onVisibleRange={(_first, last) => {
              if (active.visible && section.nextCursor && last >= section.entries.length - 10) void destination.nextDiscover(section.id).catch(runtime.reportError);
            }}
            renderItem={(entry, context) => renderEntry(entry, width, {...context, onFocus: () => {context.onFocus(); focused(id, entry.id);}})}/>
        </View>
        {section === recent && mixNode}
      </React.Fragment>;
    })}
  </ScrollView>;
}
