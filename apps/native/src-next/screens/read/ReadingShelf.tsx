import React, {useRef, useState, type ReactNode} from 'react';
import {FlatList, View} from 'react-native';
import {SkeletonBlock} from '../../kit/states';

/** Fixed-pitch, touch-only Reading shelf. Only visible covers and a small
 * overscan are mounted; the source retains its signed cursor and own lifetime. */
export function ReadingShelf<T>({items, getKey, renderItem, width, height, artHeight, gap = 14, gutter = 20,
  paddingTop = 0, paddingBottom = 4, pending = false, onNext, scrollMemory, testID}: {
  items: readonly T[]; getKey: (item: T) => string; renderItem: (item: T, index: number) => ReactNode;
  width: number; height: number; artHeight: number; gap?: number; gutter?: number; paddingTop?: number; paddingBottom?: number;
  pending?: boolean; onNext?: () => void; testID?: string;
  /** A parent year window retains its horizontal position while this row is unmounted. */
  scrollMemory?: {offset: number};
}) {
  const [viewport, setViewport] = useState(0), stride = width + gap;
  const initialOffset = useRef(scrollMemory ? {x: scrollMemory.offset, y: 0} : undefined).current;
  return <FlatList horizontal testID={testID} data={items} keyExtractor={getKey} renderItem={({item, index}) => <View style={{width, height}}>{renderItem(item, index)}</View>}
    onLayout={event => setViewport(event.nativeEvent.layout.width)} showsHorizontalScrollIndicator={false}
    getItemLayout={(_data, index) => ({length: stride, offset: gutter + index * stride, index})}
    ItemSeparatorComponent={() => <View style={{width: gap}}/>}
    contentContainerStyle={{paddingHorizontal: gutter, paddingTop, paddingBottom}}
    initialNumToRender={4} maxToRenderPerBatch={6} windowSize={5} removeClippedSubviews={false}
    initialScrollIndex={initialOffset && initialOffset.x > 0 && items.length ? Math.min(items.length - 1, Math.floor(initialOffset.x / stride)) : undefined}
    contentOffset={initialOffset} scrollEventThrottle={64} onScroll={scrollMemory ? event => {scrollMemory.offset = Math.max(0, event.nativeEvent.contentOffset.x);} : undefined}
    onEndReached={pending ? undefined : onNext} onEndReachedThreshold={viewport > 0 ? 10 * stride / viewport : 0}
    ListFooterComponent={pending ? <View style={{width, marginLeft: gap}}><SkeletonBlock width={width} height={artHeight} radius={10}/></View> : null}/>;
}
