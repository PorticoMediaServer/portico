import React, {useCallback, useEffect, useState, useSyncExternalStore, type ReactNode} from 'react';
import {View, type StyleProp, type ViewStyle} from 'react-native';
import Animated, {runOnJS, useAnimatedScrollHandler, useSharedValue} from 'react-native-reanimated';
import type {WindowedCollection} from '../../../../../packages/client-core/src/collections/windowed';
import {useTheme} from '../theme';
import {useFocusAnchor} from './FocusAnchor';
import {captionItemHeight} from './model';
import {itemSelection, type LayoutItemContext, type LayoutSelection} from './types';

export type WindowedRowProps<T> = {
  collection: WindowedCollection<T>;
  getKey: (item: T) => string;
  renderItem: (item: T, context: LayoutItemContext) => ReactNode;
  renderPlaceholder: (index: number) => ReactNode;
  itemWidth: number;
  itemHeight: number;
  /** Caption allowance at OS fontScale 1, already including any app text-size preference. */
  captionHeight?: number;
  gap?: number;
  gutter?: number;
  rightGutter?: number;
  /** Cells mounted beyond each edge, so D-pad focus search always finds the next card. */
  overscan?: number;
  selection?: LayoutSelection;
  onItemFocus?: (item: T, index: number) => void;
  style?: StyleProp<ViewStyle>;
  testID?: string;
};

/** One horizontal row over a sparse windowed collection: the row's horizontal
 * counterpart of WindowedGrid. Only the cells in view (plus overscan, plus the
 * focused cell) are mounted, the collection holds a bounded set of pages, and
 * the focused card's page is pinned so it is never evicted. Scroll extent is the
 * whole row, so the scroll indicator and positions never jump. */
function Row<T>({collection, getKey, renderItem, renderPlaceholder, itemWidth, itemHeight: authoredItemHeight, captionHeight = 0, gap, gutter, rightGutter, overscan = 3, selection, onItemFocus, style, testID}: WindowedRowProps<T>) {
  const theme = useTheme();
  const itemHeight = captionItemHeight(authoredItemHeight, captionHeight, theme.fontScale);
  const columnGap = gap ?? (theme.isTV ? 28 : 12), leading = gutter ?? theme.gutter, trailing = rightGutter ?? leading;
  const stride = itemWidth + columnGap;
  const [width, setWidth] = useState(0), [scroll, setScroll] = useState(0), [focused, setFocused] = useState<number>();
  // tv-focus "Rows and grids": on TV the focused card holds the left slot and the row slides under it, as Row does.
  const anchor = useFocusAnchor(true, focused !== undefined, theme.isTV ? 'start' : 'visible');
  // A horizontal ScrollView clips its content. On TV reserve the focused card's 1.08 scale and
  // outer ring above and below the track without moving the drawn card edge (as Row does).
  const ringRoom = theme.isTV ? Math.ceil(itemHeight * (theme.spec.focus.scale.card.tv - 1) / 2 + (theme.spec.focus.offsetOuter.tv + theme.spec.focus.ringWidth.tv) * theme.spec.focus.scale.card.tv) : 0;
  const snapshot = useSyncExternalStore(collection.subscribe, collection.getSnapshot, collection.getSnapshot);
  // Before the first page answers, the cells that fit stand in as placeholders.
  const total = snapshot.total ?? Math.max(1, Math.ceil(width / stride));
  const first = Math.max(0, Math.floor((scroll - leading) / stride) - overscan);
  const last = Math.min(total - 1, Math.ceil((scroll + width - leading) / stride) + overscan);
  const firstPage = collection.pageOf(first), lastPage = collection.pageOf(Math.max(first, last));
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
  useEffect(() => {if (width > 0 && last >= first) collection.ensureRange(first, last);}, [collection, first, last, width]);
  // As in WindowedGrid: the last focused card keeps its page until another takes focus.
  useEffect(() => {if (focused !== undefined) collection.pin(focused);}, [collection, focused]);
  useEffect(() => () => collection.pin(undefined), [collection]);
  const naturalExtent = leading + Math.max(0, total * stride - columnGap) + trailing;
  // As Row: on TV every card, a short row's last one included, can reach the left slot (blank track, never a stop).
  const extent = theme.isTV && total > 0 && width > 0 ? Math.max(naturalExtent, width + (total - 1) * stride) : naturalExtent;
  const reported = useSharedValue(-1), offset = anchor.offset;
  const onScroll = useAnimatedScrollHandler(event => {
    offset.value = event.contentOffset.x;
    const cell = Math.floor(event.contentOffset.x / stride);
    if (reported.value !== cell) {reported.value = cell; runOnJS(setScroll)(event.contentOffset.x);}
  });
  const indices: number[] = [];
  for (let index = first; index <= last; index++) indices.push(index);
  if (focused !== undefined && focused < total && !indices.includes(focused)) indices.push(focused);
  return <Animated.ScrollView ref={anchor.scrollRef} horizontal testID={testID} style={[style, ringRoom ? {marginVertical: -ringRoom} : undefined, anchor.scrollStyle]} showsHorizontalScrollIndicator={false}
    onLayout={event => setWidth(event.nativeEvent.layout.width)} onScroll={onScroll} scrollEventThrottle={16}
    onScrollBeginDrag={anchor.stop} onTouchStart={anchor.stop} scrollAnimationEnabled={false} removeClippedSubviews={false}>
    <View style={{width: extent, height: itemHeight + 2 * ringRoom}}>
      {indices.map(index => {
        const slot = collection.slotAt(index), id = slot.kind === 'item' ? getKey(slot.item) : slot.key;
        const focus = () => {
          setFocused(index);
          anchor.anchor(leading + index * stride, itemWidth, width, extent, theme.isTV ? leading : 0, theme.isTV ? trailing : 0);
          if (slot.kind === 'item') onItemFocus?.(slot.item, index);
        };
        return <View key={id} testID={testID ? `${testID}-cell-${id}` : undefined}
          style={{position: 'absolute', top: ringRoom, left: leading + index * stride, width: itemWidth, height: itemHeight}}>
          {slot.kind === 'item' ? renderItem(slot.item, {index, id, selection: itemSelection(selection, id), onFocus: focus,
            onBlur: () => setFocused(current => current === index ? undefined : current)}) : renderPlaceholder(index)}
        </View>;
      })}
    </View>
  </Animated.ScrollView>;
}

export const WindowedRow = Row as <T>(props: WindowedRowProps<T>) => React.ReactElement;
