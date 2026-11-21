import React, {useEffect, useRef, useState} from 'react';
import {View, type ScrollView, type LayoutChangeEvent, type NativeScrollEvent, type NativeSyntheticEvent} from 'react-native';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import {useTheme} from '../theme';
import {listEdges, noListEdges, type ListEdges} from './list-edges';

/**
 * The one drawn edge that says a list continues past it (live-owner frames 21 and 27, tv-detail frame 5l): ink fading in
 * over `length` points to `opacity` at the list's top or bottom edge. It never takes focus, a press or a screen reader's
 * attention, and over an empty page it fades ink into ink and disappears. The list's own window places it.
 */
export function ListEdgeFade({edge, length, opacity, color, testID}: {edge: 'top' | 'bottom'; length: number; opacity: number; /** The surface the list sits on (a panel's raised fill); ink when it is the page. */ color?: string; testID?: string}) {
  const theme = useTheme(), id = `list-edge-fade-${edge}`, fill = color ?? theme.colors.ink;
  return <View testID={testID} pointerEvents="none" accessible={false} importantForAccessibility="no-hide-descendants"
    style={{position: 'absolute', left: 0, right: 0, height: length, ...(edge === 'top' ? {top: 0} : {bottom: 0})}}>
    <Svg accessible={false} width="100%" height="100%"><Defs><LinearGradient id={id} x1="0" y1={edge === 'top' ? '1' : '0'} x2="0" y2={edge === 'top' ? '0' : '1'}>
      <Stop offset="0" stopColor={fill} stopOpacity={0}/><Stop offset="1" stopColor={fill} stopOpacity={opacity}/>
    </LinearGradient></Defs><Rect width="100%" height="100%" fill={`url(#${id})`}/></Svg>
  </View>;
}

/** A vertical ScrollView's `listEdges`, kept current from its own scroll, layout and content size: spread `scroll` on it
 * and draw a `ListEdgeFade` at each edge it names. The same on every platform: Android's own fading edge
 * (`fadingEdgeLength`) is drawn at full strength whether or not the list goes on past it (ReactScrollView's edge
 * strength does not follow the scroll), so a list that fits would show dimmed ends. */
type EdgeScrollProps = Readonly<{scrollEventThrottle?: number; onScroll?: (event: NativeSyntheticEvent<NativeScrollEvent>) => void;
  onLayout?: (event: LayoutChangeEvent) => void; onContentSizeChange?: (width: number, height: number) => void}>;
export function useListEdges(): {edges: ListEdges; scroll: EdgeScrollProps} {
  const [edges, setEdges] = useState<ListEdges>(noListEdges), at = useRef({offset: 0, content: 0, viewport: 0});
  const show = () => {const next = listEdges(at.current.offset, at.current.content, at.current.viewport); setEdges(now => now.top === next.top && now.bottom === next.bottom ? now : next);};
  return {edges, scroll: {
    scrollEventThrottle: 64,
    onScroll: (event: NativeSyntheticEvent<NativeScrollEvent>) => {const {contentOffset, contentSize, layoutMeasurement} = event.nativeEvent;
      at.current = {offset: contentOffset.y, content: contentSize.height, viewport: layoutMeasurement.height}; show();},
    onLayout: (event: LayoutChangeEvent) => {at.current.viewport = event.nativeEvent.layout.height; show();},
    onContentSizeChange: (_width: number, height: number) => {at.current.content = height; show();},
  }};
}

/** TV (owner, 20 Nov 20:30): a scrolled list or row stops only on whole items and never shows one cut at either end, under
 * its fade or not. When a scroll for focus settles, the scroller moves to the nearest offset where an item starts and the
 * focused item shows whole with its ring room (`lead` and `trail`: the content's ring room before the first item and after
 * the last). An item the window still cuts stays focusable (tvOS refuses focus to a view drawn even partly see-through, so
 * nothing here fades it): moving onto it scrolls it whole into view. Then, by `mode`:
 * - `fit` (a row): the scroller's length ends before the cut item, which lies outside it, clipped. Spread `container` on
 *   the view that gives the row its room.
 * - `blank` (a list whose length is fixed, between pinned items): the caller draws a cut item's contents blank (`cut(key)`),
 *   keeping the focusable item itself.
 * Spread `scroll` on the ScrollView (merging handlers it already has), `item(key)` on a View around each item, and call
 * `focus(key)` from the item's onFocus. Phones keep their partly shown chip (owner, 20 Nov 17:35): TV only. */
type ItemBox = Readonly<{start: number; length: number}>;
export function useWholeItems(axis: 'x' | 'y', mode: 'fit' | 'blank', keys: readonly string[], lead: number, trail: number) {
  const ref = useRef<ScrollView>(null), boxes = useRef(new Map<string, ItemBox>()), focused = useRef<string | undefined>(undefined);
  const settle = useRef<ReturnType<typeof setTimeout> | undefined>(undefined), at = useRef(0);
  const [room, setRoom] = useState(0), [offset, setOffset] = useState(0), [, measured] = useState(0);
  useEffect(() => () => clearTimeout(settle.current), []);
  const pick = (event: LayoutChangeEvent) => axis === 'x' ? event.nativeEvent.layout.width : event.nativeEvent.layout.height;
  // Shown whole: inside the window. With its ring room: where a scroll for focus may stop.
  const whole = (box: ItemBox, from: number) => box.start >= from - 1 && box.start + box.length <= from + room + 1;
  const roomy = (box: ItemBox, from: number) => box.start >= from - 1 && (from > 0 || box.start >= lead - 1) && box.start + box.length + trail <= from + room + 1;
  const sorted = () => keys.flatMap(key => {const box = boxes.current.get(key); return box ? [box] : [];}).sort((a, b) => a.start - b.start);
  // tvOS scrolls a list on its own to keep focus clear of the edge; once that settles, move to the nearest offset where no
  // item is cut at the start and the focused item shows whole, with its ring room at the end.
  const snap = () => {
    const box = focused.current && boxes.current.get(focused.current);
    if (!box || room <= 0) return;
    // A stop is the very start, or just past an item's end (the item before is wholly out of view, never a sliver of it).
    const items = sorted(), stops = [0, ...items.slice(0, -1).map(item => item.start + item.length)].filter(stop => roomy(box, stop));
    if (!stops.length) return;
    // A stop at or after where tvOS left the list leaves nothing cut at its start; take the nearest of those.
    const ahead = stops.filter(stop => stop >= at.current - 1);
    const best = (ahead.length ? ahead : stops).reduce((a, b) => Math.abs(b - at.current) < Math.abs(a - at.current) ? b : a);
    if (Math.abs(best - at.current) < 1) return;
    ref.current?.scrollTo(axis === 'x' ? {x: best, animated: true} : {y: best, animated: true});
  };
  const later = () => {clearTimeout(settle.current); settle.current = setTimeout(snap, 150);};
  // fit: the window ends after the last whole item's ring room, or where the next (cut) item starts, if that is sooner.
  const items = sorted(), cutAfter = items.find(box => box.start + box.length > offset + room + 1);
  const length = mode === 'fit' && room > 0 && cutAfter
    ? Math.max(0, Math.min(room, cutAfter.start - offset, Math.max(0, ...items.filter(box => whole(box, offset)).map(box => box.start + box.length + trail - offset)))) : undefined;
  return {
    scroll: {
      ref, scrollEventThrottle: 32,
      onScroll: (event: NativeSyntheticEvent<NativeScrollEvent>) => {
        at.current = event.nativeEvent.contentOffset[axis];
        setOffset(now => Math.abs(now - at.current) < 1 ? now : at.current);
        later();
      },
      onLayout: mode === 'blank' ? (event: LayoutChangeEvent) => setRoom(pick(event)) : undefined,
      style: length === undefined ? undefined : axis === 'x' ? {width: length, flexGrow: 0} : {height: length, flexGrow: 0},
    },
    container: {onLayout: (event: LayoutChangeEvent) => setRoom(pick(event))},
    focus: (key: string) => {focused.current = key; later();},
    cut: (key: string) => {const box = boxes.current.get(key); return mode === 'blank' && !!box && room > 0 && !whole(box, offset);},
    item: (key: string) => ({
      onLayout: (event: LayoutChangeEvent) => {const {x, y, width, height} = event.nativeEvent.layout, start = axis === 'x' ? x : y, size = axis === 'x' ? width : height;
        const old = boxes.current.get(key); if (old && old.start === start && old.length === size) return;
        boxes.current.set(key, {start, length: size}); measured(n => n + 1);},
    }),
  };
}
