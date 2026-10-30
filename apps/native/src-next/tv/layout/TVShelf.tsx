import React, {type ReactNode} from 'react';
import {Row, type RowProps} from '../../kit/layout/Row';
import type {LayoutItemContext, LayoutSelection} from '../../kit/layout/types';

export const tvItemKey = (item: {id: string}) => item.id;
const unselected: LayoutSelection = {active: false, isSelected: () => false, onToggle: () => {}, onEnter: () => {}};
/** TV compositions own headings/commands; the shared shelf owns visible slots and glide. */
export function TVShelf<T>({items, getKey, renderItem, itemWidth, itemHeight, gap = 28, onVisibleRange, focus, getFocusId, isFocusable, paging}: {
  items: readonly T[]; getKey: (item: T) => string; getFocusId?: RowProps<T>['getFocusId']; isFocusable?: RowProps<T>['isFocusable']; focus?: RowProps<T>['focus']; renderItem: (item: T, context: LayoutItemContext) => ReactNode;
  paging?: RowProps<T>['paging'];
  itemWidth: number; itemHeight: number; gap?: number; onVisibleRange?: (first: number, last: number) => void;
}) {
  return <Row title="" showHeading={false} items={items} getKey={getKey} getFocusId={getFocusId} isFocusable={isFocusable} focus={focus} renderItem={renderItem} itemWidth={itemWidth} itemHeight={itemHeight}
    gap={gap} gutter={0} rightGutter={0} selection={unselected} paging={paging} onVisibleRange={onVisibleRange}/>;
}
