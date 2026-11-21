import React, {useRef, useState, type ReactNode} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {frameSpec} from '@portico/design';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import {shapeFor} from '../../../../../packages/client-core/src/presentation/card-shape';
import type {PersonalResourceEntry} from '../../../../../packages/client-core/src/personal-saved';
import {clock, dayLabel, historyLine} from '../../pages/shared/saved/useTVSavedPage';
import type {NativeListActions} from '../../pages/shared/saved/useSavedList';
import type {NativeSavedSelectionPort} from '../../pages/shared/saved/useSavedPage';
import {savedCardText} from '../../screens/home/model';
import {savedNearEnd} from '../../screens/saved/paging';
import type {NativeShellRuntime} from '../../shell/runtime';
import {ChipRail} from '../browse';
import {MediaArt} from '../artwork';
import {CardAction, CardFrame} from '../cards/primitives';
import {PosterCard, SquareCard, type CardSelection} from '../cards';
import {cardCaptionHeight} from '../cards/caption-metrics';
import {SelectionCircle} from '../selection';
import {useTheme} from '../theme';
import {browseGridCard} from '../frames/BrowseFrame';
import {useFrameMetrics} from '../frames/metrics';
import {SavedLoading, SavedTail} from './SavedLoading';

export function SavedListTools({children}: {children: ReactNode}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', marginHorizontal: theme.gutter, marginVertical: theme.space.xs}}>
    <ChipRail>{children}</ChipRail>
  </View>;
}

export function SavedListBody({testID, visible, entries, history, runtime, actions, selection, toolbar, count, loading, state, paging, onNearEnd, footer}: {
  testID: string; visible: boolean; entries: readonly ContentEntry[]; runtime: NativeShellRuntime; actions: NativeListActions;
  history?: readonly (PersonalResourceEntry & {media: ContentEntry})[];
  selection?: NativeSavedSelectionPort['item']; toolbar?: ReactNode; count?: string; loading: boolean; state?: ReactNode;
  paging: boolean; onNearEnd: () => void; footer?: ReactNode;
}) {
  const theme = useTheme(), insets = useSafeAreaInsets(), frame = useFrameMetrics();
  const [measuredCaption, setMeasuredCaption] = useState(0);
  const viewport = useRef({offset: 0, height: 0, extent: 0});
  // The same card width and columns as a library grid on this form (frameSpec.browse.grid).
  const {width, columns} = browseGridCard(frame);
  const gap = frameSpec.browse.grid.columnGap[frame.form], rowGap = frameSpec.browse.grid.rowGap[frame.form];
  const gutter = frame.gutter;
  // One shape per grid: square only when every card is square (all albums or songs); otherwise posters, and square art
  // sits whole inside its poster card.
  const square = entries.length > 0 && entries.every(entry => squareEntry(entry));
  const caption = Math.max(cardCaptionHeight(theme, 2), measuredCaption);
  const rowHeight = history ? theme.targets.touch : width * (square ? 1 : 1.5) + caption;
  const load = () => {
    if (savedNearEnd(viewport.current.offset, viewport.current.height, viewport.current.extent, rowHeight + gap, history ? 1 : columns)) onNearEnd();
  };
  return <View testID={testID} pointerEvents={visible ? 'auto' : 'none'} accessibilityElementsHidden={!visible}
    importantForAccessibility={visible ? 'auto' : 'no-hide-descendants'} style={{flex: 1}}>
    {toolbar}
    <ScrollView onLayout={event => {viewport.current.height = event.nativeEvent.layout.height; load();}}
      onContentSizeChange={(_width, height) => {viewport.current.extent = height; load();}}
      onScroll={event => {viewport.current.offset = event.nativeEvent.contentOffset.y; load();}}
      scrollEventThrottle={64} contentContainerStyle={{paddingBottom: theme.space.lg + insets.bottom}}>
      {count !== undefined && <Text style={[theme.type.caption, {color: theme.colors.textTertiary,
        marginHorizontal: gutter, marginVertical: theme.space.sm}]}>{count}</Text>}
      {loading ? <SavedLoading columns={columns}/> : state ?? (history ? <View style={{paddingHorizontal: gutter}}>
        {history.map((item, index) => {
          const day = dayLabel(item.updatedAt, runtime.text.locale), before = history[index - 1]?.updatedAt;
          const sameDay = !!item.updatedAt && !!before && new Date(item.updatedAt).toDateString() === new Date(before).toDateString();
          return <React.Fragment key={item.id}>
            {day && !sameDay && <Text accessibilityRole="header" style={[theme.type.groupLabel, {color: theme.colors.textTertiary, marginTop: theme.space.sm}]}>{day}</Text>}
            <SavedHistoryRow item={item} runtime={runtime} actions={actions} selection={selection?.(item.media)}/>
          </React.Fragment>;
        })}
      </View> : <View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: gap, rowGap, paddingLeft: gutter, paddingRight: frame.rightGutter}}>
        {entries.map(entry => <View key={entry.id} style={{minHeight: rowHeight}}>
          <SavedListCard entry={entry} width={width} square={square} runtime={runtime} actions={actions} selection={selection?.(entry)}
            onCaptionLayout={height => setMeasuredCaption(current => Math.max(current, height))}/>
        </View>)}
      </View>)}
      {paging && <View style={{paddingHorizontal: gutter, marginTop: theme.space.sm}}><SavedTail width={width}/></View>}
    </ScrollView>
    {footer}
  </View>;
}

function SavedHistoryRow({item, runtime, actions, selection}: {
  item: PersonalResourceEntry & {media: ContentEntry}; runtime: NativeShellRuntime; actions: NativeListActions; selection?: CardSelection;
}) {
  const theme = useTheme(), anchor = useRef<View>(null), entry = item.media;
  const title = entry.displayTitle ?? entry.title, subtitle = historyLine(entry), when = clock(item.updatedAt, runtime.text.locale);
  const art = shapeFor(entry.kind, 'list') === 'square' ? 'square' : 'poster';
  const height = theme.targets.touch, width = art === 'square' ? height : height * theme.spec.card.aspect.poster;
  // Every row keeps a square slot for its art so titles start on one line whatever the art's shape.
  const menu = () => actions.menu(entry, anchor, selection?.onEnter);
  return <CardFrame title={title} testID={`saved-history-${encodeURIComponent(item.id)}`} menuAnchorRef={anchor} selection={selection}
    onNavigate={() => actions.open(entry)} onPlay={entry.playback ? () => actions.play(entry) : undefined}
    moreExpanded={actions.isMenuOpen?.(entry)} onMore={menu}>
    {() => <View style={{minHeight: height, paddingVertical: theme.space.xs, flexDirection: 'row', alignItems: 'center', gap: theme.space.sm,
      borderBottomWidth: theme.space.hairline, borderBottomColor: theme.colors.line}}>
      {selection?.active && <SelectionCircle selected={selection.selected} selecting interactive={false} variant="row"
        size={theme.space.md} accessibilityLabel={runtime.text.t('card.select', {title})}/>}
      <View style={{width: height, alignItems: 'center'}}>
        <MediaArt kind={art} source={runtime.artwork(entry.posterUrl)} width={width} height={height}/>
      </View>
      <View style={{flex: 1, minWidth: 0}}>
        <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{title}</Text>
        {(subtitle || when) && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{[subtitle, when].filter(Boolean).join(' · ')}</Text>}
      </View>
      {!selection?.active && <CardAction name="i-more" label={runtime.text.t('card.more', {title})} size={theme.targets.touch}
        color={theme.colors.textSecondary} style={{backgroundColor: 'transparent'}} onPress={menu}/>}
    </View>}
  </CardFrame>;
}

function squareEntry(entry: ContentEntry) {
  return ['album', 'song', 'book', 'audiobook_file'].includes(entry.kind);
}

function SavedListCard({entry, width, square, runtime, actions, selection, onCaptionLayout}: {
  entry: ContentEntry; width: number; square: boolean; runtime: NativeShellRuntime; actions: NativeListActions;
  selection?: CardSelection; onCaptionLayout: (height: number) => void;
}) {
  const anchor = useRef<View>(null), text = savedCardText(entry, runtime.text);
  const progress = entry.duration && entry.progressSeconds ? Math.min(1, entry.progressSeconds / entry.duration) : undefined;
  const props = {testID: `saved-list-${encodeURIComponent(entry.id)}`, focusId: `saved-list-${entry.id}`,
    title: entry.displayTitle || entry.title, caption: text.caption, titleLines: 2 as const, onCaptionLayout, density: 'grid' as const,
    artwork: runtime.artwork(entry.posterUrl), progress, watched: entry.watched, width, onNavigate: () => actions.open(entry),
    onPlay: entry.playback ? () => actions.play(entry) : undefined, onMore: () => actions.menu(entry, anchor, selection?.onEnter),
    menuAnchorRef: anchor, selection, moreExpanded: actions.isMenuOpen?.(entry)};
  return square ? <SquareCard {...props}/> : <PosterCard {...props}/>;
}
