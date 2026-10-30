import React, {createContext, useCallback, useContext, useLayoutEffect, useMemo, useRef, useState, type ReactNode} from 'react';
import {View, type ScrollView as NativeScrollView, type ScrollViewProps} from 'react-native';
import Animated, {runOnJS, useAnimatedScrollHandler, useSharedValue} from 'react-native-reanimated';
import {musicWindow, musicWindowShift} from './music-window';

type Window = Readonly<{content: React.RefObject<View | null>; scroll: React.RefObject<NativeScrollView | null>; offset: number; height: number; layout: number; origin: number; move: (offset: number) => void}>;
const Viewport = createContext<Window | undefined>(undefined);

/** The existing page stays one scroller. JS receives only crossed row ranges;
 * each section mounts its fixed rows plus the actual focused host. */
export function MusicScrollView({children, onNearEnd, ...props}: Omit<ScrollViewProps, 'onScroll'> & {onNearEnd?: () => void}) {
  const content = useRef<View>(null), scroll = useRef<NativeScrollView>(null);
  const [viewport, setViewport] = useState({offset: 0, height: 0}), [layout, setLayout] = useState(0), [origin, setOrigin] = useState(0);
  const reported = useSharedValue(-1), tail = useSharedValue(-1);
  const publish = useCallback((offset: number, height: number) => setViewport({offset, height}), []);
  const move = useCallback((offset: number) => {scroll.current?.scrollTo({y: Math.max(0, offset), animated: false}); setViewport(value => ({...value, offset: Math.max(0, offset)}));}, []);
  const onScroll = useAnimatedScrollHandler(event => {
    const offset = event.contentOffset.y, height = event.layoutMeasurement.height, range = Math.floor(offset / 54);
    if (range !== reported.value) {reported.value = range; runOnJS(publish)(offset, height);}
    if (onNearEnd && offset + height >= event.contentSize.height - 840) {
      if (tail.value !== event.contentSize.height) {tail.value = event.contentSize.height; runOnJS(onNearEnd)();}
    } else tail.value = -1;
  });
  const value = useMemo(() => ({content, scroll, ...viewport, layout, origin, move}), [viewport, layout, origin, move]);
  return <Viewport.Provider value={value}><Animated.ScrollView {...props} ref={scroll} onScroll={onScroll} scrollEventThrottle={16}
    onLayout={event => {const {height} = event.nativeEvent.layout; setViewport(value => ({...value, height})); props.onLayout?.(event);}}>
    <View ref={content} collapsable={false} onLayout={event => {setOrigin(event.nativeEvent.layout.y); setLayout(value => value + 1);}}>{children}</View>
  </Animated.ScrollView></Viewport.Provider>;
}

export function MusicWindow<T extends {id: string}>({items, itemHeight, columns = 1, columnWidth, gap = 0, expanded, focusedId, renderItem}: {
  items: readonly T[]; itemHeight: number; columns?: number; columnWidth?: number; gap?: number;
  /** The row that holds focus, from the row's own focus event. A TV View does not hear its
   * child's focus, so a caller on TV names it; the row then stays mounted however far the list scrolls. */
  focusedId?: string;
  /** One single-column row drawn taller (the playing chapter); the rows after it move down by `extra`. */
  expanded?: Readonly<{index: number; extra: number}>;
  renderItem: (item: T, index: number) => ReactNode;
}) {
  const viewport = useContext(Viewport), root = useRef<View>(null), [top, setTop] = useState(0), [focused, setFocused] = useState<string>();
  const stride = itemHeight + gap, ids = useMemo(() => items.map(item => item.id), [items]);
  const previous = useRef<Readonly<{ids: readonly string[]; columns: number; stride: number}> | undefined>(undefined);
  useLayoutEffect(() => {
    const before = previous.current; previous.current = {ids, columns, stride};
    if (!viewport || !before || before.columns !== columns || before.stride !== stride || before.ids === ids) return;
    const shift = musicWindowShift(before.ids, ids, columns, stride, viewport.offset - top);
    if (shift) viewport.move(viewport.offset + shift);
  }, [ids, columns, stride, top, viewport]);
  const measure = useCallback(() => {
    const relative = viewport?.content.current;
    if (relative) root.current?.measureLayout(relative, (_x, y) => setTop(y + (viewport?.origin ?? 0)));
  }, [viewport?.content, viewport?.origin]);
  useLayoutEffect(measure, [measure, viewport?.layout, ids, columns, stride]);
  const held = focusedId ?? focused, pin = held === undefined ? undefined : ids.indexOf(held);
  const taller = columns === 1 && expanded && expanded.index >= 0 && expanded.index < items.length ? expanded : undefined;
  const extra = taller?.extra ?? 0;
  const at = (index: number) => Math.floor(index / columns) * stride + (taller && index > taller.index ? extra : 0);
  // The taller row shifts the rest by less than a row; one more overscan row covers it.
  const indices = viewport ? musicWindow(items.length, columns, stride, top + (extra ? stride : 0), viewport.offset, viewport.height + (extra ? stride : 0), pin) : ids.map((_, index) => index);
  return <View ref={root} collapsable={false} onLayout={measure} style={{height: Math.max(0, Math.ceil(items.length / columns) * stride - gap + extra), width: columnWidth ? columns * columnWidth + (columns - 1) * gap : '100%'}}>
    {indices.map(index => {
      const item = items[index]!, height = itemHeight + (taller?.index === index ? extra : 0);
      return <View key={item.id} onFocus={() => {
        setFocused(item.id);
        if (!viewport?.height) return;
        const y = top + at(index);
        if (y < viewport.offset + 24) viewport.scroll.current?.scrollTo({y: Math.max(0, y - 24), animated: true});
        else if (y + height > viewport.offset + viewport.height - 24) viewport.scroll.current?.scrollTo({y: Math.max(0, y + height - viewport.height + 24), animated: true});
      }} onBlur={() => setFocused(value => value === item.id ? undefined : value)}
        style={{position: 'absolute', top: at(index), left: columnWidth ? index % columns * (columnWidth + gap) : 0, width: columnWidth ?? '100%', height}}>
        {renderItem(item, index)}
      </View>;
    })}
  </View>;
}

/** A horizontal release row keeps its measured height while offscreen covers
 * are represented by spacers. Focus pins do not widen the mounted range. */
export function MusicShelf<T extends {id: string}>({items, width, gap, inset = 0, renderItem, onNearEnd}: {
  items: readonly T[]; width: number; gap: number; inset?: number; renderItem: (item: T, index: number) => ReactNode; onNearEnd?: () => void;
}) {
  const scroll = useRef<NativeScrollView>(null), [viewport, setViewport] = useState({offset: 0, width: 0}), [focused, setFocused] = useState<string>();
  const stride = width + gap, ids = useMemo(() => items.map(item => item.id), [items]), previous = useRef<readonly string[]>([]);
  const reported = useSharedValue(-1), tail = useSharedValue(-1);
  const publish = useCallback((offset: number, width: number) => setViewport({offset, width}), []);
  const onScroll = useAnimatedScrollHandler(event => {
    const offset = event.contentOffset.x, size = event.layoutMeasurement.width, range = Math.floor(offset / stride);
    if (range !== reported.value) {reported.value = range; runOnJS(publish)(offset, size);}
    if (onNearEnd && offset + size >= event.contentSize.width - 10 * stride) {
      if (tail.value !== event.contentSize.width) {tail.value = event.contentSize.width; runOnJS(onNearEnd)();}
    } else tail.value = -1;
  });
  useLayoutEffect(() => {
    const before = previous.current; previous.current = ids;
    const shift = musicWindowShift(before, ids, 1, stride, viewport.offset - inset);
    if (before !== ids && shift) {const offset = Math.max(0, viewport.offset + shift); scroll.current?.scrollTo({x: offset, animated: false}); setViewport(value => ({...value, offset}));}
  }, [ids, stride, viewport.offset, inset]);
  const indices = [...musicWindow(items.length, 1, stride, inset, viewport.offset, viewport.width || width * 3, focused === undefined ? undefined : ids.indexOf(focused))].sort((a, b) => a - b);
  let previousIndex = 0;
  const cells = indices.map(index => {
    const space = (index - previousIndex) * stride; previousIndex = index + 1;
    return <React.Fragment key={items[index]!.id}>{space > 0 ? <View style={{width: space}}/> : null}
      <View onFocus={() => {
        setFocused(items[index]!.id);
        const x = inset + index * stride;
        if (x < viewport.offset) scroll.current?.scrollTo({x, animated: true});
        else if (x + width > viewport.offset + viewport.width) scroll.current?.scrollTo({x: Math.max(0, x + width - viewport.width), animated: true});
      }} onBlur={() => setFocused(value => value === items[index]!.id ? undefined : value)} style={{width, marginRight: index === items.length - 1 ? 0 : gap}}>
        {renderItem(items[index]!, index)}
      </View>
    </React.Fragment>;
  });
  const after = Math.max(0, (items.length - previousIndex) * stride - (previousIndex < items.length ? gap : 0));
  return <Animated.ScrollView ref={scroll} horizontal showsHorizontalScrollIndicator={false} onScroll={onScroll} scrollEventThrottle={16}
    onLayout={event => {const {width} = event.nativeEvent.layout; setViewport(value => ({...value, width}));}} contentContainerStyle={{paddingHorizontal: inset}}>
    {cells}{after > 0 ? <View style={{width: after}}/> : null}
  </Animated.ScrollView>;
}
