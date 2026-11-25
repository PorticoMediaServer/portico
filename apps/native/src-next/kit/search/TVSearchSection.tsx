import React, {memo, useCallback, useMemo, useState, type RefObject} from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {searchGroupLabel} from '../../../../../packages/client-core/src/presentation/search-model';
import type {SearchEntry, SearchGroupResult} from '../../../../../packages/client-core/src/search';
import {Focusable} from '../controls';
import {FocusItem, FocusRegion, useCovered, useFocusRegistry, type FocusAddress} from '../focus';
import {useRowPageBoundary} from '../focus/page-boundary';
import {useReplacementRefocus} from '../focus/replacement-refocus';
import {LoadingPaint, SkeletonBlock, StateView} from '../states';
import {useTheme} from '../theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import {Row} from '../layout/Row';
import type {LayoutItemContext} from '../layout/types';
import {TVLoadedGrid} from '../../tv/layout/TVLoadedGrid';
import {NativeSearchDestination, nativeSearchEntryKey, nativeSearchTailKey, type NativeSearchSnapshot} from '../../screens/search/model';
import {SearchGridCard} from './SearchGridCard';
import type {NativeSearchActions, NativeSearchSelectionPort} from '../../screens/search/types';

type Cell = Readonly<{kind: 'entry'; entry: SearchEntry}> | Readonly<{kind: 'tail'; id: string}>;
const emptyRowSelection = {active: false, isSelected: () => false, onToggle: () => {}, onEnter: () => {}};

export const TVSearchSection = memo(function TVSearchSection({group, destination, runtime, actions, selection, enabled, regionId, keyboardRegion, previousRegion, previousFallback, nextRegion,
  screenStart, scrollY, viewportHeight, contentRoot, width, layoutRevision, paging, singleRow = false, focusRoom = 0}: {
  group: SearchGroupResult; destination: NativeSearchDestination; runtime: NativeShellRuntime; actions: NativeSearchActions; selection?: NativeSearchSelectionPort;
  enabled: boolean; regionId: string; keyboardRegion: string; previousRegion?: string; previousFallback?: string; nextRegion?: string; screenStart?: FocusAddress;
  scrollY: number; viewportHeight: number; contentRoot: RefObject<View | null>; width: number; layoutRevision: number;
  singleRow?: boolean; paging?: NonNullable<NativeSearchSnapshot['paging']>[SearchGroupResult['id']];
  /** Room the parent results scroll leaves left of the drawn edge for a focused card's scale and ring. */
  focusRoom?: number;
}) {
  const theme = useTheme(), registry = useFocusRegistry();
  const songs = group.id === 'songs', cardWidth = songs ? Math.max(1, width) : group.id === 'live-tv' ? 400 : 196;
  const [focusedIndex, setFocusedIndex] = useState<number>();
  const [columns, setColumns] = useState(Math.max(1, Math.floor((width + 28) / (cardWidth + 28))));
  const geometry = useCallback((value: number) => setColumns(value), []);
  const tailId = group.nextCursor ? nativeSearchTailKey(group.id, group.nextCursor) : undefined;
  const entryItems = useMemo<readonly Cell[]>(() => group.items.map(entry => ({kind: 'entry' as const, entry})), [group.items]);
  const items = useMemo<readonly Cell[]>(() => tailId ? [...entryItems, {kind: 'tail' as const, id: tailId}] : entryItems, [entryItems, tailId]);
  const getKey = useCallback((cell: Cell) => cell.kind === 'entry' ? nativeSearchEntryKey(group.id, cell.entry) : cell.id, [group.id]);
  // A row read again from its first page (its cursor refused: access decided again) names where TV focus stays in it.
  const replacedId = paging?.replaced?.entryId, replacedSerial = paging?.replaced?.serial;
  const refocus = useMemo(() => replacedId !== undefined && replacedSerial !== undefined ? {focusId: replacedId, serial: replacedSerial} : undefined, [replacedId, replacedSerial]);
  const drawnAsRow = singleRow && !songs && group.items.length > 0;
  const cellKeys = useMemo(() => new Set(items.map(getKey)), [getKey, items]);
  // The grid's own (a Row keeps its focus by its `refocus`, below): a cell the replacement took away has lost focus.
  useReplacementRefocus(registry, drawnAsRow ? undefined : regionId, refocus, useCovered(), id => !cellKeys.has(id));
  const pagingLoading = !!paging?.loading;
  const pagingError = paging?.error;
  const canRetry = pagingError?.retryable !== false;
  const retry = useCallback(() => {if (enabled && canRetry) void destination.retryGroup(group.id).catch(runtime.reportError);}, [canRetry, destination, enabled, group.id, runtime]);
  useRowPageBoundary(enabled && (!singleRow || songs), registry, regionId, tailId, tailId ? {hasMore: true, waitingId: tailId,
    failed: pagingError?.retryable, loading: pagingLoading, requestNext: retry} : undefined);
  const request = useCallback(() => {
    if (enabled && tailId && !pagingLoading && !pagingError) void destination.next(group.id).catch(runtime.reportError);
  }, [destination, enabled, group.id, pagingError, pagingLoading, runtime, tailId]);
  const [caption, setCaption] = useState<{firstId: string | undefined; height: number}>();
  const firstId = group.items[0]?.id;
  const measureCaption = useCallback((measuredHeight: number) => setCaption(current =>
    current && current.firstId === firstId && current.height >= measuredHeight ? current : {firstId, height: measuredHeight}), [firstId]);
  const height = songs ? 84 : group.id === 'live-tv' ? 304 : group.id === 'episodes' ? 210 : ['movies', 'shows'].includes(group.id) ? 390 : 290;
  const artHeight = group.id === 'movies' || group.id === 'shows' ? cardWidth * 1.5
    : group.id === 'live-tv' || group.id === 'episodes' ? cardWidth * 9 / 16 : cardWidth;
  const rowHeight = Math.max(height, artHeight + (caption?.firstId === firstId ? caption?.height ?? 0 : 0));
  const failure = pagingError ? runtime.error(pagingError, retry, 'search') : undefined;
  const groupFailure = group.status === 'error' ? runtime.error({code: group.errorCode}, pagingError ? retry : () => {void destination.retry().catch(runtime.reportError);}) : undefined;
  const edges = useMemo(() => ({left: keyboardRegion, ...(previousRegion ? {up: previousRegion} : {}), ...(nextRegion ? {down: nextRegion} : {})}), [keyboardRegion, nextRegion, previousRegion]);
  const neighbors = useMemo(() => ({...(nextRegion ? {next: nextRegion} : {}), ...(previousFallback ? {previous: previousFallback} : {})}), [nextRegion, previousFallback]);
  const firstEntryKey = group.items[0] ? nativeSearchEntryKey(group.id, group.items[0]) : undefined;
  const rowFocus = useMemo(() => ({
    id: regionId, enabled, entry: firstEntryKey, edges, neighbors, screenStart,
  }), [edges, enabled, firstEntryKey, neighbors, regionId, screenStart]);
  const waitingId = tailId ?? paging?.fulfilled?.waitingId;
  const fulfilled = paging?.fulfilled;
  const requestRowNext = useCallback(() => {if (pagingError) retry(); else request();}, [pagingError, request, retry]);
  const rowPaging = useMemo(() => waitingId ? {
    hasMore: !!tailId, waitingId, waitingItem: {kind: 'tail' as const, id: waitingId}, loading: pagingLoading,
    failed: !!pagingError, fulfilled, requestNext: requestRowNext,
  } : undefined, [fulfilled, pagingError, pagingLoading, requestRowNext, tailId, waitingId]);
  const renderRowItem = useCallback((cell: Cell, context: LayoutItemContext) => (
    <SearchGridCard entry={cell.kind === 'entry' ? cell.entry : undefined} group={group.id} width={cardWidth} runtime={runtime} actions={actions}
      titleLines={0} onCaptionLayout={measureCaption} focusId={context.focusId} testID={context.testID} index={context.index}
      selection={cell.kind === 'entry' ? selection?.item(cell.entry, group.id) : undefined} disabled={!enabled}
      onFocus={() => {context.onFocus(); if (context.waiting) request();}} onBlur={context.onBlur}/>
  ), [actions, cardWidth, enabled, group.id, measureCaption, request, runtime, selection]);
  const heading = <View style={{flexDirection: 'row', alignItems: 'baseline', gap: 14}}><Text accessibilityRole="header" style={[theme.type.rowHeading, {color: theme.colors.text}]}>{defaultI18n.t(searchGroupLabel(group.id))}</Text>
    {group.totalCountExact && <Text style={[theme.type.captionStrong, {color: theme.colors.textTertiary}]}>{group.totalCount}</Text>}</View>;
  if (drawnAsRow) {
    return <View style={{marginTop: 46}}>{heading}<Row<Cell> title="" showHeading={false} style={{marginTop: 30, marginLeft: -focusRoom}}
      items={entryItems} getKey={getKey} getFocusId={getKey} itemWidth={cardWidth} itemHeight={rowHeight} gap={28} gutter={focusRoom} rightGutter={0} initialViewportWidth={width + focusRoom}
      selection={emptyRowSelection}
      focus={rowFocus}
      refocus={refocus}
      paging={rowPaging}
      renderItem={renderRowItem}/>
    </View>;
  }
  return <FocusRegion id={regionId} enabled={enabled} entry={items[0] && getKey(items[0])} orientation={items.length ? 'free' : 'vertical'}
    grid={items.length ? {columns, count: items.length} : undefined} edges={edges} stops={{right: true, ...(!nextRegion ? {down: true} : {})}}
    neighbors={neighbors} screenStart={screenStart} style={{marginTop: 46}}>
    {heading}
    {groupFailure && <StateView testID={`tv-search-group-error:${group.id}`} {...groupFailure}/>}
    <TVLoadedGrid items={items} getKey={getKey} itemWidth={cardWidth} itemHeight={group.id === 'live-tv' ? rowHeight : height} gap={28} rowGap={songs ? 0 : 40}
      scrollY={scrollY} viewportHeight={viewportHeight} coordinateRoot={contentRoot} initialWidth={width} layoutRevision={layoutRevision} onGeometry={geometry} style={{marginTop: 30}}
      renderItem={(cell, position) => <FocusItem focusId={getKey(cell)} cell={{index: position.index, pending: cell.kind === 'tail'}} waiting={cell.kind === 'tail' && !failure}>
        {cell.kind === 'entry' ? <SearchGridCard index={position.index} previousFocused={focusedIndex === position.index - 1} entry={cell.entry} group={group.id} width={cardWidth} runtime={runtime} actions={actions}
          selection={selection?.item(cell.entry, group.id)} titleLines={0} onCaptionLayout={group.id === 'live-tv' ? measureCaption : undefined} disabled={!enabled} onFocus={() => {setFocusedIndex(position.index); position.onFocus(); if (position.index >= group.items.length - columns) request();}}
          onBlur={() => setFocusedIndex(current => current === position.index ? undefined : current)}/>
          : <Focusable focusId={cell.id} focusKind="card" disabled={!enabled || !!pagingError && !pagingError.retryable} focusableWhenDisabled={!!pagingError && !pagingError.retryable}
            accessibilityRole={failure ? 'button' : undefined} accessibilityLabel={typeof failure?.title === 'string' ? failure.title : defaultI18n.t('saved.loading')}
            onFocus={() => {position.onFocus(); if (pagingError?.retryable) retry(); else request();}} onPress={pagingError?.retryable ? retry : undefined}
            style={{width: cardWidth, height, borderRadius: 12, justifyContent: 'center'}}>
            {failure ? <Text style={[theme.type.captionStrong, {color: theme.colors.textSecondary}]}>{failure.title}</Text>
              : <LoadingPaint ready={false} style={{width: cardWidth, height}} skeleton={<SkeletonBlock width={cardWidth} height={height} radius={12}/>}/>}
          </Focusable>}
      </FocusItem>}/>
  </FocusRegion>;
});
