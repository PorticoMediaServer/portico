import React, {type ReactNode} from 'react';
import {FlatList, Text, View, type ViewToken} from 'react-native';
import {Focusable} from '../controls';
import {FocusRegion, type FocusRegionProps} from '../focus';
import {useFrameMetrics} from '../frames/metrics';
import {Icon} from '../Icon';
import {useTheme} from '../theme';

export type GenreListItem = Readonly<{id: string; name: string; count: string}>;

/** All genres (desktop-search's "All genres ›"): one row a genre, its name and how many titles, and a chevron, in a raised
 * list at the page's gutter. The list reads its next or previous page as it reaches either end and says which rows are in
 * view; a later page's failure shows under the rows. On TV the rows are one vertical region. */
export function GenreList<T extends GenreListItem>({items, region, onOpen, onFocusChange, onInView, onScroll, onNearStart, onNearEnd, footer, testID}: {
  items: readonly T[]; region?: Omit<FocusRegionProps, 'children' | 'style'>; onOpen: (item: T) => void; onFocusChange?: (item: T, focused: boolean) => void;
  onInView?: (ids: readonly string[]) => void; onScroll?: (y: number, height: number, contentHeight: number) => void;
  onNearStart?: () => void; onNearEnd?: () => void; footer?: ReactNode; testID?: string;
}) {
  const theme = useTheme(), frame = useFrameMetrics();
  const viewable = React.useRef(({viewableItems}: {viewableItems: ViewToken<T>[]}) => onInViewRef.current?.(viewableItems.filter(item => item.isViewable).map(item => item.item.id))).current;
  const onInViewRef = React.useRef(onInView); onInViewRef.current = onInView;
  const list = <FlatList testID={testID} data={items} keyExtractor={item => item.id} style={{flexGrow: 0}} initialNumToRender={20} maxToRenderPerBatch={20} windowSize={5}
    maintainVisibleContentPosition={{minIndexForVisible: 0}} onViewableItemsChanged={viewable} keyboardShouldPersistTaps="handled" scrollEventThrottle={16}
    onScroll={event => {const {contentOffset, contentSize, layoutMeasurement} = event.nativeEvent; onScroll?.(contentOffset.y, layoutMeasurement.height, contentSize.height);}}
    onStartReachedThreshold={.5} onEndReachedThreshold={.5} onStartReached={onNearStart} onEndReached={onNearEnd}
    contentContainerStyle={{paddingHorizontal: frame.cardInset, paddingBottom: frame.bottomClear}}
    renderItem={({item, index}) => <Focusable focusId={`genre:${item.id}`} testID={`all-genres-${encodeURIComponent(item.id)}`} accessibilityRole="button"
      accessibilityLabel={`${item.name}, ${item.count}`} focusKind="row" onPress={() => onOpen(item)}
      onFocus={() => onFocusChange?.(item, true)} onBlur={() => onFocusChange?.(item, false)}
      style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.sm, minHeight: frame.rowTwoLine, paddingHorizontal: frame.cardPadding,
        backgroundColor: theme.colors.raised, borderTopLeftRadius: index === 0 ? frame.cardRadius : 0, borderTopRightRadius: index === 0 ? frame.cardRadius : 0,
        borderBottomLeftRadius: index === items.length - 1 ? frame.cardRadius : 0, borderBottomRightRadius: index === items.length - 1 ? frame.cardRadius : 0,
        borderTopWidth: index ? theme.space.hairline : 0, borderTopColor: theme.colors.lineSoft}}>
      <View style={{flex: 1, minWidth: 0}}>
        <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{item.name}</Text>
        <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{item.count}</Text>
      </View>
      <Icon name="i-right" size={theme.type.rowLabel.fontSize} color={theme.colors.textTertiary}/>
    </Focusable>}
    ListFooterComponent={footer ? <View style={{marginTop: frame.sectionGap}}>{footer}</View> : null}/>;
  // The list is sized by its rows inside a box that takes the room (a list told to grow inside a TV focus region drew 1px tall).
  const box = <View style={{flex: 1}}>{list}</View>;
  return region ? <FocusRegion orientation="vertical" {...region} style={{flex: 1}}>{box}</FocusRegion> : box;
}
