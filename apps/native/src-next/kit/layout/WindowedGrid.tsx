import React, {forwardRef, useCallback, useEffect, useImperativeHandle, useLayoutEffect, useRef, useState, useSyncExternalStore, type ReactNode} from 'react';
import {View, type RefreshControlProps, type StyleProp, type ViewStyle} from 'react-native';
import Animated, {runOnJS, useAnimatedScrollHandler, useSharedValue} from 'react-native-reanimated';
import type {WindowedCollection} from '../../../../../packages/client-core/src/collections/windowed';
import {useTheme} from '../theme';
import {useFocusAnchor} from './FocusAnchor';
import {captionItemHeight, captureGridAnchor, gridAnchorOffset, gridWindow, type GridAnchor} from './model';
import {itemSelection, type LayoutItemContext, type LayoutSelection} from './types';
import {nativeSlotOwnerKey, projectNativeSlots, type NativeSlotProjection} from '../focus/windowed-slots';

export type WindowedGridHandle = {scrollToIndex: (index: number) => void; scrollToLetter: (key: string) => void; scrollToOffset: (offset: number) => void};
export type NativeGridSlotContext = LayoutItemContext & Readonly<{focusId: string; entityId?: string; waiting: boolean; source: string}>;
export type WindowedGridProps<T> = {
  collection: WindowedCollection<T>;
  getKey: (item: T) => string;
  renderItem: (item: T, context: LayoutItemContext) => ReactNode;
  renderPlaceholder: (index: number) => ReactNode;
  /** TV-only stable native host composition; defaults/touch retain the original render paths. */
  renderNativeSlot?: (item: T | undefined, context: NativeGridSlotContext) => ReactNode;
  itemWidth: number;
  itemHeight: number;
  /** Caption allowance at OS fontScale 1, already including any app text-size preference. */
  captionHeight?: number;
  gap?: number;
  rowGap?: number;
  gutter?: number;
  rightGutter?: number;
  paddingTop?: number;
  paddingBottom?: number;
  stickyOffset?: number;
  /** Scrolls before the grid; the toolbar remains pinned beneath the shell. */
  header?: ReactNode;
  stickyHeader?: ReactNode;
  overscanRows?: number;
  initialIndex?: number;
  selection?: LayoutSelection;
  onVisibleRange?: (first: number, last: number) => void;
  /** Actual native contentOffset.y, including a final position within a row. */
  onScrollOffset?: (offset: number) => void;
  onItemFocus?: (item: T, index: number) => void;
  /** Touch pull-to-refresh; never passed on TV. */
  refreshControl?: React.ReactElement<RefreshControlProps>;
  /** Existing measured layout, for declarative native focus-cell boundaries. */
  onGeometry?: (columns: number, stride: number) => void;
  style?: StyleProp<ViewStyle>;
  testID?: string;
};

function Grid<T>({collection, getKey, renderItem, renderPlaceholder, renderNativeSlot, itemWidth, itemHeight: authoredItemHeight, captionHeight = 0, gap, rowGap, gutter, rightGutter, paddingTop: authoredPaddingTop = 0, paddingBottom = 0, stickyOffset: contentStickyOffset = 0, header, stickyHeader, overscanRows = 2, initialIndex = 0, selection, onVisibleRange, onScrollOffset, onItemFocus, onGeometry, refreshControl, style, testID}: WindowedGridProps<T>, ref: React.ForwardedRef<WindowedGridHandle>) {
  const theme = useTheme();
  const itemHeight = captionItemHeight(authoredItemHeight, captionHeight, theme.fontScale);
  const committedSlots = useRef<NativeSlotProjection | undefined>(undefined);
  const pendingOpening = useRef({});
  // The scroller clips its content. On TV reserve the focused card's scale and outer ring above the first row
  // with padding, and pull the scroller up by the same amount so the drawn cards do not move (as WindowedRow does).
  const ringRoom = theme.isTV ? Math.ceil(itemHeight * (theme.spec.focus.scale.card.tv - 1) / 2 + (theme.spec.focus.offsetOuter.tv + theme.spec.focus.ringWidth.tv) * theme.spec.focus.scale.card.tv) : 0;
  const contentPaddingTop = authoredPaddingTop + ringRoom;
  const columnGap = gap ?? (theme.isTV ? 28 : 12), verticalGap = rowGap ?? (theme.isTV ? 36 : 22);
  const leading = gutter ?? theme.gutter, trailing = rightGutter ?? (theme.isTV ? theme.layout.gutter.tvRight : theme.gutter);
  const [viewport, setViewport] = useState({width: 0, height: 0});
  const [headerHeight, setHeaderHeight] = useState(0), [toolbarHeight, setToolbarHeight] = useState(0);
  const leadingHeight = (header ? headerHeight : 0) + (stickyHeader ? toolbarHeight : 0);
  const paddingTop = contentPaddingTop + leadingHeight;
  const stickyOffset = contentStickyOffset + (stickyHeader ? toolbarHeight : 0);
  const [scroll, setScroll] = useState(0), [focused, setFocused] = useState<number>();
  const anchor = useFocusAnchor(false, focused !== undefined);
  // The last focused card keeps its page in memory until another card takes focus, so
  // a covered page, an interrupted scroll, Back or Undo always finds it (never on blur:
  // opening a title blurs the card it came from).
  useEffect(() => {if (focused !== undefined) collection.pin(focused);}, [collection, focused]);
  useEffect(() => () => collection.pin(undefined), [collection]);
  const snapshot = useSyncExternalStore(collection.subscribe, collection.getSnapshot, collection.getSnapshot);
  const columns = Math.max(1, Math.floor((viewport.width - leading - trailing + columnGap) / (itemWidth + columnGap)));
  const stride = itemHeight + verticalGap;
  useLayoutEffect(() => {if (viewport.width > 0) onGeometry?.(columns, stride);}, [columns, onGeometry, stride, viewport.width]);
  const total = snapshot.total ?? collection.pageSize;
  const window = gridWindow(total, columns, stride, Math.max(0, scroll + stickyOffset - paddingTop), Math.max(0, viewport.height - stickyOffset), overscanRows);
  const firstMounted = window.startRow * columns, lastMounted = Math.min(total - 1, (window.endRow + 1) * columns - 1);
  const firstPage = collection.pageOf(firstMounted), lastPage = collection.pageOf(Math.max(firstMounted, lastMounted));
  const subscribePages = useCallback((listener: () => void) => {
    const cleanup: (() => void)[] = [];
    for (let page = firstPage; page <= lastPage; page++) cleanup.push(collection.subscribePage(page, listener));
    return () => cleanup.forEach(run => run());
  }, [collection, firstPage, lastPage]);
  const pageVersion = useCallback(() => {
    const values = [];
    for (let page = firstPage; page <= lastPage; page++) values.push(collection.getPageVersion(page));
    return values.join('|');
  }, [collection, firstPage, lastPage]);
  useSyncExternalStore(subscribePages, pageVersion, pageVersion);
  useEffect(() => {
    if (!viewport.height || window.last < window.first) return;
    collection.ensureRange(window.first, window.last);
    onVisibleRange?.(window.first, window.last);
  }, [collection, onVisibleRange, viewport.height, window.first, window.last]);
  const height = paddingTop + Math.max(0, window.height - verticalGap) + paddingBottom;
  const moveToIndex = useCallback((index: number) => {
    const bounded = Math.max(0, Math.min(total - 1, Math.floor(index)));
    // An index/rail jump lands directly; traversing every intervening viewport
    // would ask the sparse collection for pages that the user never sees.
    anchor.move(Math.max(0, Math.min(height - viewport.height, paddingTop + Math.floor(bounded / columns) * stride - stickyOffset)), true);
  }, [anchor, columns, height, paddingTop, stickyOffset, stride, total, viewport.height]);
  const moveToOffset = useCallback((offset: number) => {
    if (Number.isFinite(offset)) anchor.move(Math.max(0, offset), true);
  }, [anchor]);
  useImperativeHandle(ref, () => ({scrollToIndex: moveToIndex, scrollToOffset: moveToOffset, scrollToLetter(key) {
    const index = collection.indexOfLetter(key);
    if (index !== undefined) moveToIndex(index);
  }}), [collection, moveToIndex, moveToOffset]);
  const geometry = useRef<{collection: WindowedCollection<T>; generation: number; columns: number; stride: number; paddingTop: number; stickyOffset: number} | undefined>(undefined);
  const retained = useRef<{collection: WindowedCollection<T>; generation: number; anchor: GridAnchor; id?: string} | undefined>(undefined);
  const moveAnchor = anchor.move, measuredOffset = anchor.offset;
  const captureBeforeLayout = () => {
    const previous = geometry.current;
    if (!previous) return;
    const captured = captureGridAnchor(measuredOffset.value, previous.columns, previous.stride, previous.paddingTop, previous.stickyOffset, total);
    if (!captured) return;
    const slot = previous.collection.slotAt(captured.index);
    retained.current = {collection: previous.collection, generation: previous.generation, anchor: captured, id: slot.kind === 'item' ? getKey(slot.item) : undefined};
  };
  useLayoutEffect(() => {
    if (!viewport.width || !viewport.height) return;
    const previous = geometry.current;
    const witness = retained.current;
    retained.current = undefined;
    geometry.current = {collection, generation: snapshot.generation, columns, stride, paddingTop, stickyOffset};
    if (!previous) {
      const opening = initialIndex === 0 ? 0 : Math.max(0, Math.min(height - viewport.height, paddingTop + Math.floor(initialIndex / columns) * stride - stickyOffset));
      if (opening) {moveAnchor(opening, true); setScroll(opening);}
      return;
    }
    // A new result generation owns its own restoration. Never carry an old
    // library/filter anchor into it, and never scan the collection to find one.
    if (previous.collection !== collection || previous.generation !== snapshot.generation
      || previous.columns === columns && previous.stride === stride && previous.paddingTop === paddingTop && previous.stickyOffset === stickyOffset) return;
    const captured = witness?.collection === collection && witness.generation === snapshot.generation ? witness.anchor
      : captureGridAnchor(measuredOffset.value, previous.columns, previous.stride, previous.paddingTop, previous.stickyOffset, total);
    if (!captured) return; // The viewport is still in the header, not the grid.
    if (witness?.id) {
      const current = collection.slotAt(captured.index);
      if (current.kind !== 'item' || getKey(current.item) !== witness.id) return;
    }
    const next = gridAnchorOffset(captured, columns, stride, paddingTop, stickyOffset, height, viewport.height);
    moveAnchor(next, true); setScroll(next); onScrollOffset?.(next);
  }, [collection, columns, getKey, height, initialIndex, measuredOffset, moveAnchor, onScrollOffset, paddingTop, snapshot.generation, stickyOffset, stride, total, viewport.height, viewport.width]);
  const reported = useSharedValue('');
  const scrollOffset = anchor.offset;
  const onScroll = useAnimatedScrollHandler(event => {
    scrollOffset.value = event.contentOffset.y;
    if (onScrollOffset) runOnJS(onScrollOffset)(event.contentOffset.y);
    const y = event.contentOffset.y, next = `${Math.floor((y + stickyOffset - paddingTop) / stride)}:${Math.ceil((y + viewport.height - paddingTop) / stride)}`;
    if (reported.value !== next) {reported.value = next; runOnJS(setScroll)(y);}
  });
  const indices: number[] = [];
  for (let index = firstMounted; index <= lastMounted; index++) indices.push(index);
  // Keep only the actually focused cell alive during an interrupted row scroll.
  if (theme.isTV && focused !== undefined && focused < total && !indices.includes(focused)) indices.push(focused);
  const nativeRows = theme.isTV && renderNativeSlot ? indices.map(index => {
    const slot = collection.slotAt(index);
    return {slot, entityId: slot.kind === 'item' ? getKey(slot.item) : undefined};
  }) : undefined;
  const projectedSlots = nativeRows ? projectNativeSlots(committedSlots.current, collection, nativeSlotOwnerKey(collection), snapshot.generation,
    nativeRows.map(({slot, entityId}) => ({index: slot.index, entityId, waiting: slot.kind !== 'item'})), nativeSlotOwnerKey(pendingOpening.current)) : undefined;
  useLayoutEffect(() => {committedSlots.current = projectedSlots;}, [projectedSlots]);
  return <Animated.ScrollView ref={anchor.scrollRef} testID={testID} style={[style, ringRoom ? {marginTop: -ringRoom} : undefined, anchor.scrollStyle]} refreshControl={theme.isTV ? undefined : refreshControl}
    stickyHeaderIndices={stickyHeader ? [header ? 1 : 0] : undefined}
    onLayout={event => {captureBeforeLayout(); setViewport(event.nativeEvent.layout);}} onScroll={onScroll} scrollEventThrottle={16}
    onScrollBeginDrag={anchor.stop} onTouchStart={anchor.stop} scrollAnimationEnabled={false} removeClippedSubviews={false}>
    {header && <View onLayout={event => setHeaderHeight(event.nativeEvent.layout.height)}>{header}</View>}
    {stickyHeader && <View onLayout={event => setToolbarHeight(event.nativeEvent.layout.height)}>{stickyHeader}</View>}
    <View style={{height: Math.max(0, height - leadingHeight), position: 'relative'}}>
      {indices.map((index, position) => {
        const slot = nativeRows?.[position]?.slot ?? collection.slotAt(index), nativeSlot = projectedSlots?.slots[position], id = slot.kind === 'item' ? getKey(slot.item) : slot.key;
        const focus = () => {
          setFocused(index);
          anchor.anchor(paddingTop + Math.floor(index / columns) * stride, itemHeight, viewport.height, height, stickyOffset + (theme.isTV ? 24 : 0), theme.isTV ? 24 : 0);
          if (slot.kind === 'item') onItemFocus?.(slot.item, index);
        };
        return <View key={nativeSlot?.key ?? id} testID={testID ? `${testID}-cell-${id}` : undefined} style={{position: 'absolute', top: contentPaddingTop + Math.floor(index / columns) * stride, left: leading + index % columns * (itemWidth + columnGap), width: itemWidth, height: itemHeight}}>
          {nativeSlot && renderNativeSlot ? renderNativeSlot(slot.kind === 'item' ? slot.item : undefined,
            {index, id, focusId: nativeSlot.focusId, entityId: nativeSlot.entityId, source: nativeSlot.source, waiting: nativeSlot.waiting,
              selection: slot.kind === 'item' ? itemSelection(selection, id) : undefined, onFocus: focus, onBlur: () => setFocused(current => current === index ? undefined : current)})
            : slot.kind === 'item' ? renderItem(slot.item, {index, id, selection: itemSelection(selection, id), onFocus: focus, onBlur: () => setFocused(current => current === index ? undefined : current)}) : renderPlaceholder(index)}
        </View>;
      })}
    </View>
  </Animated.ScrollView>;
}

export const WindowedGrid = forwardRef(Grid) as <T>(props: WindowedGridProps<T> & {ref?: React.Ref<WindowedGridHandle>}) => React.ReactElement;
