import React, {useCallback, useMemo, useState} from 'react';
import {FocusItem, FocusRegion, type FocusRegionProps} from '../../kit/focus';
import {TVLoadedGrid} from './TVLoadedGrid';

type Props<T> = Parameters<typeof TVLoadedGrid<T>>[0] & {
  focus: Omit<FocusRegionProps, 'children' | 'grid' | 'onLayout'> & {getFocusId: (item: T) => string; isFocusable?: (item: T) => boolean};
};
/** Actual measured cells declare the native graph; this wrapper never handles keys. */
export function TVFocusGrid<T>({focus, onGeometry, onLayout, renderItem, ...props}: Props<T>) {
  const [columns, setColumns] = useState(1);
  const measured = useCallback((count: number, stride: number) => {setColumns(count); onGeometry?.(count, stride);}, [onGeometry]);
  const {getFocusId, isFocusable, ...region} = focus;
  const {items, getKey} = props;
  const indices = useMemo(() => isFocusable ? new Map(items.filter(isFocusable).map((item, index) => [getKey(item), index])) : undefined,
    [getKey, isFocusable, items]);
  return <FocusRegion {...region} grid={{columns: props.getRowHeight ? 1 : columns, count: indices?.size ?? props.items.length}} onLayout={onLayout}>
    <TVLoadedGrid {...props} onGeometry={measured} renderItem={(item, cell) => indices && !indices.has(props.getKey(item)) ? renderItem(item, cell) : <FocusItem focusId={getFocusId(item)} cell={{index: indices?.get(props.getKey(item)) ?? cell.index}}>
      {renderItem(item, cell)}
    </FocusItem>}/>
  </FocusRegion>;
}
