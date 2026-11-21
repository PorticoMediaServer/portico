import React, {useRef, useState, type ReactNode} from 'react';
import {ScrollView, Text, View, useWindowDimensions} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {NativeListActions} from '../../pages/shared/saved/useSavedList';
import type {NativeSavedSelectionPort} from '../../pages/shared/saved/useSavedPage';
import {savedCardText} from '../../screens/home/model';
import {savedNearEnd} from '../../screens/saved/paging';
import type {NativeShellRuntime} from '../../shell/runtime';
import {ChipRail} from '../browse';
import {PosterCard, SquareCard, type CardSelection} from '../cards';
import {cardCaptionHeight} from '../cards/caption-metrics';
import {useTheme} from '../theme';
import {SavedLoading, SavedTail} from './SavedLoading';

export function SavedListTools({children}: {children: ReactNode}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', marginHorizontal: theme.gutter, marginVertical: theme.space.xs}}>
    <ChipRail>{children}</ChipRail>
  </View>;
}

export function SavedListBody({testID, visible, entries, runtime, actions, selection, toolbar, count, loading, state, paging, onNearEnd, footer}: {
  testID: string; visible: boolean; entries: readonly ContentEntry[]; runtime: NativeShellRuntime; actions: NativeListActions;
  selection?: NativeSavedSelectionPort['item']; toolbar?: ReactNode; count?: string; loading: boolean; state?: ReactNode;
  paging: boolean; onNearEnd: () => void; footer?: ReactNode;
}) {
  const theme = useTheme(), insets = useSafeAreaInsets(), window = useWindowDimensions();
  const [paneWidth, setPaneWidth] = useState(window.width), [measuredCaption, setMeasuredCaption] = useState(0);
  const viewport = useRef({offset: 0, height: 0, extent: 0});
  const columns = theme.isTV ? 7 : theme.platform === 'phone' ? 3 : paneWidth >= 900 ? 5 : 4;
  const gap = theme.isTV ? theme.space.lg : theme.space.sm, gutter = theme.gutter;
  const width = Math.max(1, Math.floor((paneWidth - gutter * 2 - gap * (columns - 1)) / columns));
  const caption = Math.max(cardCaptionHeight(theme, 2), measuredCaption);
  const rowHeight = width * 1.5 + caption;
  const load = () => {
    if (savedNearEnd(viewport.current.offset, viewport.current.height, viewport.current.extent, rowHeight + gap, columns)) onNearEnd();
  };
  return <View testID={testID} pointerEvents={visible ? 'auto' : 'none'} accessibilityElementsHidden={!visible}
    importantForAccessibility={visible ? 'auto' : 'no-hide-descendants'}
    onLayout={event => setPaneWidth(event.nativeEvent.layout.width)} style={{flex: 1}}>
    {toolbar}
    <ScrollView onLayout={event => {viewport.current.height = event.nativeEvent.layout.height; load();}}
      onContentSizeChange={(_width, height) => {viewport.current.extent = height; load();}}
      onScroll={event => {viewport.current.offset = event.nativeEvent.contentOffset.y; load();}}
      scrollEventThrottle={64} contentContainerStyle={{paddingBottom: theme.space.lg + insets.bottom}}>
      {count !== undefined && <Text style={[theme.type.caption, {color: theme.colors.textTertiary,
        marginHorizontal: gutter, marginVertical: theme.space.sm}]}>{count}</Text>}
      {loading ? <SavedLoading columns={columns}/> : state ?? <View style={{flexDirection: 'row', flexWrap: 'wrap', gap, paddingHorizontal: gutter}}>
        {entries.map(entry => <View key={entry.id} style={{minHeight: rowHeight}}>
          <SavedListCard entry={entry} width={width} runtime={runtime} actions={actions} selection={selection?.(entry)}
            onCaptionLayout={height => setMeasuredCaption(current => Math.max(current, height))}/>
        </View>)}
      </View>}
      {paging && <View style={{paddingHorizontal: gutter, marginTop: theme.space.sm}}><SavedTail width={width}/></View>}
    </ScrollView>
    {footer}
  </View>;
}

function SavedListCard({entry, width, runtime, actions, selection, onCaptionLayout}: {
  entry: ContentEntry; width: number; runtime: NativeShellRuntime; actions: NativeListActions;
  selection?: CardSelection; onCaptionLayout: (height: number) => void;
}) {
  const anchor = useRef<View>(null), text = savedCardText(entry, runtime.text);
  const progress = entry.duration && entry.progressSeconds ? Math.min(1, entry.progressSeconds / entry.duration) : undefined;
  const props = {testID: `saved-list-${encodeURIComponent(entry.id)}`, focusId: `saved-list-${entry.id}`,
    title: entry.displayTitle || entry.title, caption: text.caption, titleLines: 2 as const, onCaptionLayout, density: 'grid' as const,
    artwork: runtime.artwork(entry.posterUrl), progress, watched: entry.watched, width, onNavigate: () => actions.open(entry),
    onPlay: entry.playback ? () => actions.play(entry) : undefined, onMore: () => actions.menu(entry, anchor, selection?.onEnter),
    menuAnchorRef: anchor, selection, moreExpanded: actions.isMenuOpen?.(entry)};
  return ['album', 'song', 'book', 'audiobook_file'].includes(entry.kind) ? <SquareCard {...props}/> : <PosterCard {...props}/>;
}
