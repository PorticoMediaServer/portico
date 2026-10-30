import React, {useCallback, useLayoutEffect, useMemo, useRef, useState, type ReactNode, type RefObject} from 'react';
import {View, type LayoutChangeEvent, type StyleProp, type ViewStyle} from 'react-native';
import {gridWindow} from '../../kit/layout/model';

/** A window over an already-paged TV projection inside its existing page scroller.
 * It owns no collection, commands, scroll engine or focus graph. Shared grid geometry
 * supplies the window; the last focused native cell remains mounted for return.
 * Initial height estimates reserve space only; unchanged native children measure the
 * actual font/row height, so virtualization never substitutes guessed text metrics. */
export function TVLoadedGrid<T>({items, getKey, renderItem, itemWidth, itemHeight, gap = 28, rowGap = 40, scrollY, viewportHeight,
  coordinateRoot, initialWidth = itemWidth, layoutRevision = 0, singleRow = false, getRowHeight, onLayout, onGeometry, style}: {
  items: readonly T[]; getKey: (item: T) => string;
  renderItem: (item: T, cell: {index: number; top: number; onFocus: () => void}) => ReactNode;
  itemWidth: number; itemHeight: number; gap?: number; rowGap?: number; scrollY: number; viewportHeight: number;
  coordinateRoot: RefObject<View | null>; initialWidth?: number; layoutRevision?: number; singleRow?: boolean; getRowHeight?: (index: number) => number;
  onLayout?: (event: LayoutChangeEvent) => void; onGeometry?: (columns: number, stride: number) => void; style?: StyleProp<ViewStyle>;
}) {
  const root = useRef<View>(null), [box, setBox] = useState({top: 0, width: initialWidth}), [remembered, remember] = useState<{key: string; index: number}>();
  const [naturalHeight, setNaturalHeight] = useState<number>(), [rowSizes, setRowSizes] = useState<ReadonlyMap<string, number>>(new Map());
  const columns = singleRow ? Math.max(1, items.length) : Math.max(1, Math.floor((box.width + gap) / (itemWidth + gap)));
  const stride = (naturalHeight ?? itemHeight) + rowGap;
  const positions = useMemo(() => {
    if (!getRowHeight) return undefined;
    const result = [0];
    for (let index = 0; index < items.length; index++) result.push(result[index]! + (rowSizes.get(getKey(items[index]!)) ?? getRowHeight(index)));
    return result;
  }, [getRowHeight, getKey, items, rowSizes]);
  const height = positions ? positions[positions.length - 1]! : Math.max(0, Math.ceil(items.length / columns) * stride - rowGap);
  const localTop = scrollY - box.top;
  const window = gridWindow(items.length, columns, stride, Math.max(0, localTop), viewportHeight);
  const indices = new Set<number>();
  if (positions) {
    // Binary search avoids walking the whole loaded history for each focus/scroll.
    const at = (y: number) => {
      let low = 0, high = items.length;
      while (low < high) {const middle = (low + high) >>> 1; if (positions[middle + 1]! < y) low = middle + 1; else high = middle;}
      return low;
    };
    const first = Math.max(0, at(localTop) - 2), last = Math.min(items.length - 1, at(localTop + viewportHeight) + 2);
    for (let index = first; index <= last; index++) indices.add(index);
  } else if (box.top <= scrollY + viewportHeight + stride * 2 && box.top + height >= scrollY - stride * 2) {
    for (let index = window.startRow * columns; index <= Math.min(items.length - 1, (window.endRow + 1) * columns - 1); index++) indices.add(index);
  }
  const rememberedIndex = remembered === undefined ? -1 : items[remembered.index] && getKey(items[remembered.index]!) === remembered.key ? remembered.index : items.findIndex(item => getKey(item) === remembered.key);
  if (rememberedIndex >= 0) indices.add(rememberedIndex);
  const measure = useCallback(() => {
    const ancestor = coordinateRoot.current;
    if (ancestor) root.current?.measureLayout(ancestor, (_x, top, width) => setBox(current => current.top === top && current.width === width ? current : {top, width}));
  }, [coordinateRoot]);
  useLayoutEffect(measure, [measure, layoutRevision, items.length, itemHeight, gap, rowGap]);
  useLayoutEffect(() => {onGeometry?.(columns, stride);}, [columns, stride, onGeometry]);
  return <View ref={root} collapsable={false} onLayout={event => {measure(); onLayout?.(event);}} style={[style, {height, position: 'relative'}]}>
    {[...indices].sort((a, b) => a - b).map(index => {
      const item = items[index]!, key = getKey(item), top = positions ? positions[index]! : Math.floor(index / columns) * stride;
      return <View key={key} onLayout={event => {
        const measured = event.nativeEvent.layout.height;
        if (measured <= 0) return;
        if (positions) setRowSizes(current => current.get(key) === measured ? current : new Map(current).set(key, measured));
        else setNaturalHeight(current => current === undefined || measured > current ? measured : current);
      }} style={{position: 'absolute', left: positions ? 0 : index % columns * (itemWidth + gap), top, width: positions ? '100%' : itemWidth}}>
        {renderItem(item, {index, top, onFocus: () => remember({key, index})})}
      </View>;
    })}
  </View>;
}
