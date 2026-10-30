import React, {useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode} from 'react';
import {Pressable, Text, View, type StyleProp, type TextStyle, type ViewStyle} from 'react-native';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import Animated, {runOnJS, useAnimatedScrollHandler, useSharedValue} from 'react-native-reanimated';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Icon} from '../Icon';
import {useTheme} from '../theme';
import {FocusItem, FocusProvider, FocusRegion, focusTestID, useOptionalFocusRegistry, type FocusRegionProps} from '../focus';
import {reconcileRowSlots, indexRowSlots, visibleRowSlots, rowNeedsPage, type RowPage} from './row-state';
import {useRowPageBoundary} from '../focus/page-boundary';
import {useFocusAnchor} from './FocusAnchor';
import {captionItemHeight} from './model';
import {MotionItem} from './MotionItem';
import {itemSelection, type LayoutItemContext, type LayoutSelection} from './types';

export type RowProps<T> = {
  title: string;
  /** Existing TV compositions may already draw their authored heading. */
  showHeading?: boolean;
  /** The drawn heading column for this composition; absent keeps the role's own rowHeading. */
  headingStyle?: StyleProp<TextStyle>;
  subtitle?: ReactNode;
  items: readonly T[];
  getKey: (item: T) => string;
  /** Focus identity may include kind/source/library while selection retains its existing data key. */
  getFocusId?: (item: T) => string;
  /** Which painted slots the native region may land on. Absent means every slot; identities, order,
   * context and positions are untouched - only the declared region order/entry is filtered, and a
   * waiting page host always stays because it is a cursor, not an item. */
  isFocusable?: (item: T) => boolean;
  renderItem: (item: T, context: LayoutItemContext) => ReactNode;
  itemWidth: number;
  itemHeight: number;
  /** Caption allowance at OS fontScale 1; artwork remains unscaled. */
  captionHeight?: number;
  gap?: number;
  gutter?: number;
  rightGutter?: number;
  onSeeAll?: () => void;
  selection: LayoutSelection;
  onVisibleRange?: (first: number, last: number) => void;
  /** Raw data keys for intersecting real slots only; excludes overscan, waiting and separately pinned focus. */
  onVisibleKeys?: (keys: readonly string[]) => void;
  onItemFocus?: (item: T, index: number) => void;
  onRepeatingChange?: (repeating: boolean) => void;
  style?: StyleProp<ViewStyle>;
  testID?: string;
  /** Actual measured width may be supplied before native onLayout (Home uses its measured viewport). */
  initialViewportWidth?: number;
  /** The vertical window may unmount a shelf; this screen-owned memory keeps its place. */
  scrollMemory?: {offset: number};
  focus?: Omit<FocusRegionProps, 'children' | 'order' | 'orientation'>;
  paging?: RowPage<T>;
};

function ArrowBackdrop({back}: {back: boolean}) {
  const {colors} = useTheme(), id = useId();
  return <Svg pointerEvents="none" accessible={false} width="100%" height="100%" style={{position: 'absolute', inset: 0}}>
    <Defs><LinearGradient id={id} x1={back ? '0' : '100%'} x2={back ? '100%' : '0'} y1="0" y2="0">
      <Stop offset="0" stopColor={colors.ink} stopOpacity={.9} /><Stop offset="1" stopColor={colors.ink} stopOpacity={0} />
    </LinearGradient></Defs><Rect width="100%" height="100%" fill={`url(#${id})`} />
  </Svg>;
}

export function Row<T>(props: RowProps<T>) {
  return <FocusProvider><RowContent {...props} /></FocusProvider>;
}

function RowContent<T>({title, showHeading = true, headingStyle, subtitle, items, getKey, getFocusId = getKey, isFocusable, renderItem, itemWidth, itemHeight: authoredItemHeight, captionHeight = 0, gap, gutter, rightGutter, onSeeAll, selection, onVisibleRange, onVisibleKeys, onItemFocus, onRepeatingChange, style, testID, focus, paging, initialViewportWidth = 0, scrollMemory}: RowProps<T>) {
  const theme = useTheme();
  const itemHeight = captionItemHeight(authoredItemHeight, captionHeight, theme.fontScale);
  const initialOffset = useRef(scrollMemory?.offset ?? 0);
  const [width, setWidth] = useState(initialViewportWidth), [offset, setOffset] = useState(initialOffset.current), [hovered, setHovered] = useState(false), [focused, setFocused] = useState<string>();
  const publishOffset = useCallback((value: number) => {if (scrollMemory) scrollMemory.offset = value; setOffset(value);}, [scrollMemory]);
  const registry = useOptionalFocusRegistry();
  const regionId = focus?.id;
  const subscribe = useCallback((listener: () => void) => regionId && registry ? registry.subscribeRegion(regionId, listener) : () => {}, [regionId, registry]);
  const getSnapshot = useCallback(() => regionId && registry ? registry.regionSnapshot(regionId) : 0, [regionId, registry]);
  useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
  const owned = focus ? !!registry?.isRegionActive(focus.id) : focused !== undefined;
  const previous = useRef<{regionId?: string; index: ReturnType<typeof indexRowSlots<T>>} | undefined>(undefined);
  const fulfilledId = paging?.fulfilled?.waitingId, fulfilledItem = paging?.fulfilled?.firstItemId;
  const waitingId = paging?.waitingId, waitingItem = paging?.waitingItem, hasMore = paging?.hasMore, failed = paging?.failed;
  const rowIndex = useMemo(() => {
    const before = previous.current?.regionId === regionId ? previous.current?.index.slots ?? [] : [];
    return indexRowSlots(reconcileRowSlots(before, items, getKey, theme.isTV && owned,
      waitingId && waitingItem !== undefined ? {waitingId, waitingItem, hasMore: !!hasMore, failed, loading: false, requestNext: noop,
        fulfilled: fulfilledId ? {waitingId: fulfilledId, firstItemId: fulfilledItem} : undefined} : undefined, getFocusId));
  }, [failed, fulfilledId, fulfilledItem, getFocusId, getKey, hasMore, items, owned, regionId, theme.isTV, waitingId, waitingItem]);
  useLayoutEffect(() => {previous.current = {regionId, index: rowIndex};}, [regionId, rowIndex]);
  // The native boundary is the actionable part of the same painted order: every slot keeps its
  // identity, and an inert one simply is not a destination the region hands the platform.
  const activeOrder = useMemo(() => {
    if (!isFocusable) return rowIndex.order;
    const inert = new Set(rowIndex.slots.filter(slot => !slot.waiting && !isFocusable(slot.item)).map(slot => slot.focusId));
    return rowIndex.order.filter(focusId => !inert.has(focusId));
  }, [isFocusable, rowIndex]);
  // Only an opted-in predicate filters the declared entry. Without one, the entry is passed on
  // exactly as before, including an entry that the order does not contain.
  const activeEntry = !isFocusable || (focus?.entry && activeOrder.includes(focus.entry)) ? focus?.entry : undefined;
  const focusedSlot = focused ? rowIndex.byFocus.get(focused) : undefined;
  const remembered = focus ? registry?.remembered(focus.id) : focused;
  const [arrowFocus, setArrowFocus] = useState<'back' | 'forward' | 'all'>();
  const anchor = useFocusAnchor(true, focused !== undefined, theme.isTV ? 'start' : 'visible');
  const offsetRestored = useRef(false);
  useLayoutEffect(() => {
    if (offsetRestored.current) return;
    offsetRestored.current = true;
    if (scrollMemory) anchor.offset.value = initialOffset.current;
  }, [anchor.offset, scrollMemory]);
  const anchorItem = anchor.anchor;
  const stride = itemWidth + (gap ?? theme.spec.shelf.gap[theme.platform]);
  const leading = gutter ?? theme.gutter, trailing = rightGutter ?? (theme.isTV ? theme.layout.gutter.tvRight : theme.gutter);
  // The TV track starts directly below the heading margin. Reserve room above
  // its viewport for the scaled outer ring without moving the drawn card edge.
  const top = theme.isTV ? Math.ceil(itemHeight * (theme.spec.focus.scale.card.tv - 1) / 2 + (theme.spec.focus.offsetOuter.tv + theme.spec.focus.ringWidth.tv) * theme.spec.focus.scale.card.tv) : theme.spec.shelf.phonePaddingTop;
  const bottom = theme.isTV ? Math.max(14, top) : theme.spec.shelf.phonePaddingBottom;
  const naturalExtent = leading + Math.max(0, rowIndex.length * stride - (stride - itemWidth)) + trailing;
  // Every real TV slot, including a short row's last card, must be able to
  // reach the left safe edge. This is blank track space, never a focus target.
  const extent = theme.isTV && rowIndex.length ? Math.max(naturalExtent, width + (rowIndex.length - 1) * stride) : naturalExtent;
  const first = Math.max(0, Math.floor((offset - leading) / stride));
  const last = Math.min(rowIndex.length - 1, Math.max(first, Math.ceil((offset + width - leading) / stride) - 1));
  const recovery = focus ? registry?.recovery(focus.id) ?? [] : focused ? [{childId: focused, order: previous.current?.index.order ?? []}] : [];
  const visible = visibleRowSlots(rowIndex, first, last, focused, remembered, recovery);
  // Only an item added after this row's first paint fades in; scrolling into the window never does.
  const seen = useRef<Set<string> | undefined>(undefined);
  useEffect(() => {const ids = seen.current ?? new Set<string>(); for (const slot of rowIndex.slots) ids.add(slot.focusId); seen.current = ids;}, [rowIndex]);
  useEffect(() => {if (width && last >= first) onVisibleRange?.(first, Math.min(items.length - 1, last));}, [first, items.length, last, onVisibleRange, width]);
  const visibleKeys = useMemo(() => {
    const keys: string[] = [];
    if (width) for (let position = first; position <= last; position++) {
      const slot = rowIndex.byPosition.get(position);
      if (slot && !slot.waiting && leading + position * stride < offset + width && leading + position * stride + itemWidth > offset) keys.push(slot.id);
    }
    return keys;
  }, [first, itemWidth, last, leading, offset, rowIndex, stride, width]);
  useEffect(() => {onVisibleKeys?.(visibleKeys);}, [onVisibleKeys, visibleKeys]);
  const anchorIntent = useRef('');
  useEffect(() => {
    if (!focusedSlot || !width) {anchorIntent.current = ''; return;}
    const intent = `${focused}:${focusedSlot?.position}:${width}:${itemWidth}:${leading}:${trailing}:${stride}`;
    if (intent === anchorIntent.current) return;
    anchorIntent.current = intent;
    // motion.html 4.1: content arrivals alone never retarget a focused slot.
    anchorItem(leading + focusedSlot.position * stride, itemWidth, width, extent, leading, trailing);
  }, [anchorItem, extent, focused, focusedSlot, itemWidth, leading, stride, trailing, width]);
  const requestedPage = useRef<string | undefined>(undefined);
  useEffect(() => {
    const requestId = paging?.waitingId;
    if (!requestId || requestedPage.current === requestId || !rowNeedsPage(focusedSlot?.position ?? last, items.length, paging)) return;
    requestedPage.current = requestId;
    paging?.requestNext();
  }, [focusedSlot?.position, items.length, last, paging]);
  useEffect(() => {onRepeatingChange?.(anchor.repeating);}, [anchor.repeating, onRepeatingChange]);
  // Native boundary notifications request data only; the native guide still chooses every target.
  // The tail boundary belongs to the last slot the viewer can actually land on; when the predicate
  // is absent this is exactly the old last painted slot, and a waiting host is still a real host.
  const boundaryId = useMemo(() => {
    const painted = rowIndex.slots.filter(slot => !slot.waiting);
    // Opted in: only a slot the viewer can land on may own the tail, so an all-inert row installs no
    // boundary at all rather than an inert painted one. Absent the predicate the old tail stands.
    if (isFocusable) return painted.filter(slot => isFocusable(slot.item)).at(-1)?.focusId;
    return painted.at(-1)?.focusId;
  }, [isFocusable, rowIndex]);
  useRowPageBoundary(theme.isTV && !!focus, registry, focus?.id, boundaryId, paging);
  const reported = useSharedValue('');
  const scrollOffset = anchor.offset;
  const onScroll = useAnimatedScrollHandler(event => {
    const x = event.contentOffset.x;
    scrollOffset.value = x;
    const next = `${Math.floor((x - leading) / stride)}:${Math.ceil((x + width - leading) / stride)}`;
    if (reported.value !== next) {reported.value = next; runOnJS(publishOffset)(x);}
  });
  const controlStyle = (selected: boolean) => ({borderWidth: selected ? theme.spec.focus.ringWidth[theme.platform] : 0, borderColor: theme.colors.focus, borderRadius: theme.baseRadii.control});
  return <View testID={testID} onPointerEnter={() => setHovered(true)} onPointerLeave={() => setHovered(false)} style={style}>
    {showHeading && <View style={{flexDirection: 'row', alignItems: 'baseline', gap: theme.spec.shelf.headingGap[theme.platform], paddingLeft: leading, paddingRight: trailing, marginBottom: theme.spec.shelf.headingMargin[theme.platform]}}>
      {onSeeAll && !theme.isTV ? <Pressable testID={testID ? `${testID}-see-all` : undefined} accessibilityRole="button" accessibilityLabel={title} accessibilityHint={defaultI18n.t('shelf.seeAll')} onPress={onSeeAll}
        onFocus={() => setArrowFocus('all')} onBlur={() => setArrowFocus(undefined)} style={[{minHeight: 24, flexDirection: 'row', alignItems: 'center', flexShrink: 1}, controlStyle(arrowFocus === 'all')]}>
        <Text accessibilityRole="header" numberOfLines={1} style={[theme.type.rowHeading, headingStyle, {color: theme.colors.text, flexShrink: 1, includeFontPadding: false}]}>{title}</Text><Icon name="i-right" size={18} color={theme.colors.textTertiary} />
      </Pressable> : <Text accessibilityRole="header" numberOfLines={1} style={[theme.type.rowHeading, headingStyle, {color: theme.colors.text, flexShrink: 1, includeFontPadding: false}]}>{title}</Text>}
      {subtitle && <View style={{flexShrink: 1}}>{typeof subtitle === 'string' ? <Text numberOfLines={1} style={[theme.type.rowSubtitle, {color: theme.colors.textTertiary, includeFontPadding: false}]}>{subtitle}</Text> : subtitle}</View>}

    </View>}
    <RowRegion focus={theme.isTV ? focus : undefined} order={activeOrder} entry={activeEntry}
      enabled={isFocusable && activeOrder.length === 0 ? false : undefined}>
    <Animated.ScrollView horizontal ref={anchor.scrollRef} style={[theme.isTV ? {marginTop: -top} : undefined, anchor.scrollStyle]} onLayout={event => setWidth(event.nativeEvent.layout.width)}
      onScroll={onScroll} scrollEventThrottle={16} showsHorizontalScrollIndicator={false} onScrollBeginDrag={anchor.stop}
      contentOffset={scrollMemory ? {x: initialOffset.current, y: 0} : undefined}
      onScrollEndDrag={scrollMemory ? event => publishOffset(event.nativeEvent.contentOffset.x) : undefined}
      onMomentumScrollEnd={scrollMemory ? event => publishOffset(event.nativeEvent.contentOffset.x) : undefined}
      snapToInterval={theme.isTV ? undefined : stride} decelerationRate="fast" disableIntervalMomentum={!theme.isTV}
      scrollAnimationEnabled={false} onTouchStart={anchor.stop} removeClippedSubviews={false}>
      <View style={{width: extent, height: itemHeight + top + bottom}}>
        {visible.map(slot => {
          const {item, id, focusId, waiting, position} = slot;
          const itemTestID = focus ? focusTestID(focus.id, focusId) : testID ? `${testID}-item-${encodeURIComponent(focusId)}` : undefined;
          return <MotionItem key={focusId} enter={!!seen.current && !seen.current.has(focusId)} style={{position: 'absolute', left: leading + position * stride, top, width: itemWidth, height: itemHeight}}>
            <FocusItem focusId={focusId} testID={itemTestID} waiting={waiting}>{renderItem(item, {id, focusId, testID: itemTestID, waiting, index: position, selection: itemSelection(selection, id), onFocus: () => {
              setFocused(focusId); if (!waiting) onItemFocus?.(item, position);
            }, onBlur: () => setFocused(current => current === focusId ? undefined : current)})}</FocusItem>
          </MotionItem>;
        })}
      </View>
    </Animated.ScrollView>
    </RowRegion>
    {!theme.isTV && hovered && width > 0 && extent > width && (['back', 'forward'] as const).map(direction => {
      const back = direction === 'back', available = back ? offset > 0 : offset + width < extent - 1;
      return available && <Pressable testID={testID ? `${testID}-scroll-${direction}` : undefined} key={direction} accessibilityRole="button" accessibilityLabel={defaultI18n.t(back ? 'shelf.scrollBack' : 'shelf.scrollForward')}
        onPress={() => anchor.move(Math.max(0, Math.min(extent - width, anchor.offset.value + (back ? -1 : 1) * Math.max(stride, Math.floor((width - leading - trailing) / stride) * stride))))}
        onFocus={() => setArrowFocus(direction)} onBlur={() => setArrowFocus(undefined)}
        style={[{position: 'absolute', top: theme.spec.shelf.arrowTop, bottom: theme.spec.shelf.arrowBottom, width: theme.spec.shelf.arrowWidth, left: back ? 0 : undefined, right: back ? undefined : 0, alignItems: 'center', justifyContent: 'center', zIndex: theme.layers.rowArrow}, controlStyle(arrowFocus === direction)]}>
        <ArrowBackdrop back={back} /><Icon name={back ? 'i-left' : 'i-right'} size={24} />
      </Pressable>;
    })}
  </View>;
}

function RowRegion({focus, order, entry, enabled, children}: {focus?: RowProps<unknown>['focus']; order: readonly string[]; entry?: string; enabled?: boolean; children: ReactNode}) {
  // `enabled` only ever arrives for an opted-in, all-inert row: nothing can be a destination, so the
  // native guide is off while the painted content, its context and its keys stay as they are.
  return focus ? <FocusRegion {...focus} orientation="horizontal" order={order} entry={entry ?? order[0]} {...(enabled === false ? {enabled: false} : {})}>{children}</FocusRegion> : <>{children}</>;
}
const noop = () => {};
export type {RowPage} from './row-state';
export {Row as Shelf};
