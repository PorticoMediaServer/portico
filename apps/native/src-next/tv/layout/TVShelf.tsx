import React, {type ReactNode} from 'react';
import {View} from 'react-native';
import {useTheme} from '../../kit/theme';
import {Row, type RowProps} from '../../kit/layout/Row';
import type {LayoutItemContext, LayoutSelection} from '../../kit/layout/types';

export const tvItemKey = (item: {id: string}) => item.id;
const unselected: LayoutSelection = {active: false, isSelected: () => false, onToggle: () => {}, onEnter: () => {}};
/** TV compositions own headings/commands; the shared shelf owns visible slots and glide. */
export function TVShelf<T>({items, getKey, renderItem, itemWidth, itemHeight, gap = 28, onVisibleRange, focus, getFocusId, isFocusable, paging, refocus}: {
  items: readonly T[]; getKey: (item: T) => string; getFocusId?: RowProps<T>['getFocusId']; isFocusable?: RowProps<T>['isFocusable']; focus?: RowProps<T>['focus']; renderItem: (item: T, context: LayoutItemContext) => ReactNode;
  paging?: RowProps<T>['paging']; refocus?: RowProps<T>['refocus'];
  itemWidth: number; itemHeight: number; gap?: number; onVisibleRange?: (first: number, last: number) => void;
}) {
  // W-11: the focused card scales up and draws its ring outside its edge. The track reserves that
  // room on both sides (as Row already does above it) and is pulled out by the same amount, so
  // the cards' drawn edge stays where the composition puts it and the ring is never clipped.
  const bleed = tvShelfBleed(useTheme(), itemWidth);
  return <View style={{marginHorizontal: -bleed}}>
    <Row title="" showHeading={false} items={items} getKey={getKey} getFocusId={getFocusId} isFocusable={isFocusable} focus={focus} renderItem={renderItem} itemWidth={itemWidth} itemHeight={itemHeight}
      gap={gap} gutter={bleed} rightGutter={bleed} selection={unselected} paging={paging} refocus={refocus} onVisibleRange={onVisibleRange}/>
  </View>;
}

/** How far a focused card of this width reaches past its own edge: half its growth, plus the
 * ring's offset and stroke at the focused scale. */
export function tvShelfBleed(theme: Readonly<{spec: {focus: {scale: {card: {tv: number}}; offsetOuter: {tv: number}; ringWidth: {tv: number}}}}>, itemWidth: number): number {
  const {scale, offsetOuter, ringWidth} = theme.spec.focus;
  return Math.ceil(itemWidth * (scale.card.tv - 1) / 2 + (offsetOuter.tv + ringWidth.tv) * scale.card.tv);
}
