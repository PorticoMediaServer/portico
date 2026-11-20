import React, {type ReactNode} from 'react';
import type {LayoutChangeEvent} from 'react-native';
import type Animated from 'react-native-reanimated';
import type {AnimatedRef} from 'react-native-reanimated';
import {Row} from '../layout';
import {cardCaptionHeight} from '../cards/caption-metrics';
import {useTheme} from '../theme';
import type {RowProps} from '../layout/Row';
import type {LayoutItemContext} from '../layout/types';

const noSelection = {active: false, isSelected: () => false, onToggle: () => {}, onEnter: () => {}};

export function LibraryShelf<T>({title, items, shape, getKey, onVisibleRange, onSeeAll, focus, renderItem}: {
  title: string; items: readonly T[]; shape: 'wide' | 'square' | 'poster'; getKey: (item: T) => string;
  focus?: RowProps<T>['focus'];
  onVisibleRange: (first: number, last: number) => void; onSeeAll?: () => void;
  renderItem: (item: T, context: LayoutItemContext, width: number) => ReactNode;
}) {
  const theme = useTheme(), card = theme.spec.card, width = card.width[shape][theme.column];
  const caption = cardCaptionHeight(theme, 2);
  return <Row title={title} items={items} selection={noSelection} itemWidth={width} itemHeight={width / card.aspect[shape] + caption} captionHeight={caption}
    getKey={getKey} focus={focus} onVisibleRange={onVisibleRange} onSeeAll={onSeeAll}
    renderItem={(item, context) => renderItem(item, context, width)}/>;
}

export function useLibraryMixAnchor(scroll: AnimatedRef<Animated.ScrollView>, enabled: boolean) {
  const rows = React.useRef(0), mix = React.useRef<number | undefined>(undefined), done = React.useRef(false);
  React.useEffect(() => {if (!enabled) done.current = false;}, [enabled]);
  const reveal = React.useCallback(() => {
    if (!enabled || done.current || mix.current === undefined || !scroll.current) return;
    done.current = true;
    scroll.current.scrollTo({y: rows.current + mix.current, animated: false});
  }, [enabled, scroll]);
  return {reveal,
    rowsLayout: (event: LayoutChangeEvent) => {rows.current = event.nativeEvent.layout.y;},
    layout: (event: LayoutChangeEvent) => {mix.current = event.nativeEvent.layout.y; reveal();},
  };
}
