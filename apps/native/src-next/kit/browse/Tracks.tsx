import React, {useCallback, useEffect, useRef, useState, useSyncExternalStore, type ReactNode, type RefObject} from 'react';
import {FlatList, Text, View, VirtualizedList, type ViewToken} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {CardTextModel, FactPart} from '../../../../../packages/client-core/src/presentation/kit-model';
import type {WindowedCollection} from '../../../../../packages/client-core/src/collections';
import {Button, Chip, Focusable, type FocusableProps} from '../controls';
import {Icon} from '../Icon';
import {CardFrame} from '../cards/primitives';
import {cardAccessibilityLabel} from '../cards/model';
import {useTheme} from '../theme';
import {alpha, browseType} from './presentation';

const t = defaultI18n.t;
export type TrackRowData = Readonly<{
  id: string; text: CardTextModel; number?: number; plays?: number; addedAt?: string;
  duration?: FactPart; artwork?: ReactNode; favorite?: boolean; playing?: boolean;
  done?: boolean; current?: boolean; progress?: number;
}>;
export type TrackSelection = Readonly<{
  active: boolean; isSelected: (id: string) => boolean; onToggle: (id: string) => void; onEnter: (id: string) => void;
  /** The supplied selection family renders a nonfocusable mark inside the TV row. */
  renderControl: (row: TrackRowData, selected: boolean, onPress: () => void) => ReactNode;
}>;
type TrackActions = Readonly<{
  onPlay: (id: string) => void; onMore?: (id: string, anchor?: RefObject<View | null>) => void;
  isMenuOpen?: (id: string) => boolean;
  onToggleFavorite?: (id: string, favorite: boolean) => void;
}>;
type TVTrackFocus = Pick<FocusableProps, 'focusId' | 'testID' | 'onFocus' | 'onBlur' | 'disabled'> & Readonly<{index?: number; previousFocused?: boolean}>;
type TrackVariant = 'album' | 'top-songs' | 'songs' | 'chapters';
export type TrackColumn = 'number' | 'title' | 'artist' | 'album' | 'plays' | 'duration' | 'added';
export type TrackTableProps = TrackActions & Readonly<{
  variant: TrackVariant; columns?: readonly TrackColumn[]; selection: TrackSelection;
  sort?: Readonly<{column: TrackColumn; direction: 'asc' | 'desc'}>;
  onSort?: (column: TrackColumn) => void; sortable?: readonly TrackColumn[];
  onVisibleRange?: (first: number, last: number) => void; empty?: ReactNode;
  testID?: string;
}> & (
  | Readonly<{rows: readonly TrackRowData[]; collection?: never; groups?: never; disc?: never}>
  | Readonly<{variant: 'songs'; collection: WindowedCollection<TrackRowData>; rows?: never; groups?: never; disc?: never}>
  | Readonly<{groups: readonly Readonly<{id: string; title: ReactNode; discNumber: number; rows: readonly TrackRowData[]}>[]; disc: Readonly<{value: string; onValueChange: (id: string) => void}>; rows?: never; collection?: never}>
);

const defaultColumns = {
  album: ['number', 'title', 'plays', 'duration'],
  'top-songs': ['number', 'title', 'plays', 'duration'],
  songs: ['number', 'title', 'artist', 'album', 'duration', 'plays', 'added'],
  chapters: ['number', 'title', 'duration'],
} as const;
const columnKeys = {number: 'next.browse.native.number', title: 'next.browse.native.title', artist: 'next.browse.native.artist', album: 'next.browse.native.album', plays: 'next.browse.native.plays', duration: 'filter.field.duration', added: 'next.browse.native.added'} as const;
const viewability = {itemVisiblePercentThreshold: 1} as const;
const boundedProgress = (value: number | undefined) => value !== undefined && Number.isFinite(value) ? Math.max(0, Math.min(1, value)) : undefined;

function trackLabel(row: TrackRowData): string {
  return cardAccessibilityLabel(row.text.title, row.text.caption, row.duration ? [...row.text.detail, row.duration] : row.text.detail,
    [row.playing ? t('next.kit.session.playing') : row.current ? t('profile.current') : undefined, row.done ? t('title.finished') : undefined]);
}

function LevelMeter() {
  const theme = useTheme();
  return <View accessible={false} style={{height: 14, width: 18, flexDirection: 'row', alignItems: 'flex-end', justifyContent: 'center', gap: 2}}>{[8, 14, 10].map((height, index) => <View key={index} style={{height, width: 3, borderRadius: 1, backgroundColor: theme.colors.accentStrong}}/>)}</View>;
}

/** tv-more6/8: the whole 84 dp row owns focus and its focused Menu action. */
function TVTrackRow({row, onPress, onMenu, selection, selected, selectionMode = false, showArtwork = false, showCaption = false, index = Math.max(0, (row.number ?? 1) - 1), previousFocused = false, ...focus}: TVTrackFocus & {
  row: TrackRowData; onPress: () => void; onMenu?: () => void; selection?: ReactNode; selected: boolean;
  selectionMode?: boolean; showArtwork?: boolean; showCaption?: boolean;
}) {
  const theme = useTheme(), duration = row.duration ?? row.text.detail.find(part => part.id === 'duration');
  const grow = theme.fontScale > 1 || theme.textScale > 1;
  return <Focusable {...focus} accessibilityRole={selectionMode ? 'checkbox' : 'button'} focusKind="row" focusScale={1.015} focusRadius={16} onPress={onPress} onLongPressAction={onMenu} accessibilityLabel={t(selectionMode ? 'card.select' : 'next.browse.native.play', {title: trackLabel(row)})} accessibilityState={selectionMode ? {checked: selected} : {selected}} style={{height: grow ? undefined : 84, minHeight: 84, borderRadius: 16}}>
    {({focused}) => <View style={{height: grow ? undefined : 84, minHeight: 84, paddingVertical: grow ? 6 : undefined, paddingLeft: 18, paddingRight: 28, flexDirection: 'row', gap: 22, alignItems: 'center', borderRadius: 16, borderTopWidth: index === 0 || focused || previousFocused ? 0 : 1, borderTopColor: alpha(theme.colors.line, .6), backgroundColor: focused ? theme.colors.raisedHigh : selected ? alpha(theme.colors.accent, .18) : row.playing ? alpha(theme.colors.text, .07) : undefined}}>
      {selection}
      <View style={{width: 44, alignItems: 'center'}}>{row.playing ? <LevelMeter/> : <Text style={[browseType(theme, 24, 600), {color: theme.colors.textTertiary, fontVariant: ['tabular-nums']}]}>{row.number}</Text>}</View>
      {showArtwork && row.artwork && <View style={{width: 56, height: 56, borderRadius: 8, overflow: 'hidden'}}>{row.artwork}</View>}
      <View style={{flex: 1, minWidth: 0}}><Text numberOfLines={grow ? undefined : 1} style={[browseType(theme, 28, 700), {color: row.playing ? theme.colors.accentStrong : theme.colors.text}]}>{row.text.title}</Text>{showCaption && <View style={{flexDirection: 'row', gap: 12, marginTop: 2}}>{row.text.caption.map(part => <Text key={part.id} numberOfLines={grow ? undefined : 1} style={[browseType(theme, 20, 600), {color: theme.colors.textTertiary, flexShrink: 1}]}>{part.text}</Text>)}</View>}</View>
      {row.favorite && <Icon name="i-heart-f" size={22} color={theme.colors.accentStrong}/>}
      <Text style={[browseType(theme, 24), {color: theme.colors.textTertiary, width: 80, textAlign: 'right', fontVariant: ['tabular-nums']}]}>{duration?.text}</Text>
    </View>}
  </Focusable>;
}

/** The row's title/caption are already canonical parts, never a parsed subtitle. */
export function SongRow({row, onPlay, onMore, selection, selected = false, onSelectionChange, onEnter, isMenuOpen, ranked = false, focusId, testID, onFocus, onBlur, disabled, index, previousFocused}: TrackActions & TVTrackFocus & {
  row: TrackRowData; selection?: ReactNode; selected?: boolean; onSelectionChange?: () => void; onEnter: () => void; ranked?: boolean;
}) {
  const theme = useTheme(), tv = theme.isTV, duration = row.duration ?? row.text.detail.find(part => part.id === 'duration');
  const grow = theme.fontScale > 1 || theme.textScale > 1;
  const menuAnchorRef = useRef<View>(null);
  const caption = row.text.caption.filter(part => part.id === (ranked ? 'album' : 'artist'));
  if (tv) return <TVTrackRow {...{focusId, testID, onFocus, onBlur, disabled, index, previousFocused}} row={row} selected={selected} selectionMode={onSelectionChange !== undefined} selection={selection} onPress={onSelectionChange ?? (() => onPlay(row.id))} onMenu={onMore ? () => onMore(row.id) : undefined} showArtwork showCaption/>;
  return <View style={{height: grow ? undefined : ranked ? 62 : 58, minHeight: ranked ? 62 : 58, paddingVertical: grow ? 6 : undefined, flexDirection: 'row', alignItems: 'center', gap: tv ? 20 : 14, paddingLeft: 6, paddingRight: 12, borderRadius: 10, backgroundColor: selected ? alpha(theme.colors.accent, .18) : undefined}}>
    {selection}
    <CardFrame menuAnchorRef={menuAnchorRef} moreExpanded={isMenuOpen?.(row.id)} title={row.text.title} primaryAction="play" onPlay={() => onPlay(row.id)} onMore={onMore ? () => onMore(row.id, menuAnchorRef) : undefined}
      selection={{active: !!onSelectionChange, selected, onToggle: onSelectionChange ?? onEnter, onEnter}}
      accessibilityLabel={onSelectionChange ? trackLabel(row) : t('next.browse.native.play', {title: trackLabel(row)})} style={{flex: 1, height: grow ? undefined : '100%'}} pressableStyle={{flex: 1, justifyContent: 'center'}}>
      {() =>
      <View style={{flexDirection: 'row', alignItems: 'center', gap: tv ? 20 : 14, flex: 1, minWidth: 0}}>
        {ranked && row.number !== undefined && <View style={{width: 22, alignItems: 'center'}}>{row.playing ? <LevelMeter/> : <Text style={[browseType(theme, tv ? 22 : 15, 700), {color: theme.colors.textTertiary}]}>{row.number}</Text>}</View>}
        {row.artwork && <View style={{width: tv ? 72 : ranked ? 46 : 44, height: tv ? 72 : ranked ? 46 : 44, borderRadius: 6, overflow: 'hidden'}}>{row.artwork}</View>}
        <View style={{flex: 1, minWidth: 0}}>
          <Text numberOfLines={grow ? undefined : 1} style={[browseType(theme, tv ? 24 : 15, 700), {color: row.playing ? theme.colors.accentStrong : theme.colors.text}]}>{row.text.title}</Text>
          {caption.length > 0 && <View style={{flexDirection: 'row', gap: 10, marginTop: 2}}>{caption.map(part => <Text key={part.id} numberOfLines={grow ? undefined : 1} style={[browseType(theme, 13, 400), {color: theme.colors.textTertiary, flexShrink: 1}]}>{part.text}</Text>)}</View>}
        </View>
        {duration && <Text style={[browseType(theme, tv ? 20 : 13), {color: theme.colors.textTertiary, fontVariant: ['tabular-nums']}]}>{duration.text}</Text>}
      </View>
      }
    </CardFrame>
    {onMore && <Button variant="icon" size="small" iconOnly icon="i-more" accessibilityLabel={t('next.browse.native.moreFor', {title: row.text.title})} onPress={() => onMore(row.id, menuAnchorRef)}/>}
  </View>;
}

function columnStyle(column: TrackColumn, variant: TrackVariant) {
  if (column === 'title') return {flex: variant === 'songs' ? 2.3 : 1, minWidth: 0};
  if (column === 'artist') return {flex: 1.25, minWidth: 0};
  if (column === 'album') return {flex: 1.7, minWidth: 0};
  return {width: column === 'number' ? 44 : column === 'duration' ? variant === 'songs' ? 86 : variant === 'chapters' ? 90 : 80 : column === 'plays' ? variant === 'songs' ? 64 : 90 : 124};
}

export function TrackRow({row, variant, columns = defaultColumns[variant], selection, onPlay, onMore, onToggleFavorite, isMenuOpen, focusId, testID, onFocus, onBlur, disabled, index, previousFocused}: TrackActions & TVTrackFocus & {row: TrackRowData; variant: TrackVariant; columns?: readonly TrackColumn[]; selection: TrackSelection}) {
  const theme = useTheme(), phone = theme.platform === 'phone', tv = theme.isTV, selected = selection.isSelected(row.id);
  const grow = theme.fontScale > 1 || theme.textScale > 1;
  const height = phone ? 54 : variant === 'chapters' && row.current ? 76 : variant === 'songs' ? 56 : 60;
  const menuAnchorRef = useRef<View>(null);
  const duration = row.duration ?? row.text.detail.find(part => part.id === 'duration');
  const progress = boundedProgress(row.progress ?? row.text.progress), remaining = row.text.detail.find(part => part.id === 'remaining');
  if (tv) return <TVTrackRow {...{focusId, testID, onFocus, onBlur, disabled, index, previousFocused}} row={row} selected={selected} selectionMode={selection.active} selection={selection.active ? selection.renderControl(row, selected, () => selection.onToggle(row.id)) : undefined} onPress={() => selection.active ? selection.onToggle(row.id) : onPlay(row.id)} onMenu={onMore ? () => onMore(row.id) : undefined} showArtwork={variant === 'top-songs' || variant === 'songs'} showCaption={variant === 'top-songs' || variant === 'songs'}/>;
  const actions = <View style={{width: phone ? 44 : variant === 'chapters' ? 60 : 84, flexDirection: 'row', justifyContent: 'flex-end', alignItems: 'center'}}>
    {variant === 'chapters' ? <Button variant="icon" size="small" iconOnly icon="i-play" accessibilityLabel={t('next.browse.native.play', {title: row.text.title})} onPress={() => onPlay(row.id)}/> : <>
    {row.favorite !== undefined && onToggleFavorite && !phone && <Button variant="icon" size="small" iconOnly icon={row.favorite ? 'i-heart-f' : 'i-heart'} accessibilityLabel={t(row.favorite ? 'next.browse.native.unfavorite' : 'next.browse.native.favorite')} accessibilityState={{checked: row.favorite}} onPress={() => onToggleFavorite(row.id, !row.favorite)}/>}
    {onMore && <Button variant="icon" size="small" iconOnly icon="i-more" accessibilityLabel={t('next.browse.native.moreFor', {title: row.text.title})} onPress={() => onMore(row.id, menuAnchorRef)}/>}
    </>}
  </View>;
  const select = () => selection.onToggle(row.id);
  return <View style={{height: grow ? undefined : height, minHeight: height, paddingVertical: grow ? 6 : undefined, paddingLeft: phone ? 20 : 8, paddingRight: 12, flexDirection: 'row', alignItems: 'center', gap: phone ? 14 : 16, borderRadius: 10, backgroundColor: selected ? alpha(theme.colors.accent, .18) : !phone && (row.playing || row.current) ? alpha(theme.colors.text, .08) : undefined}}>
    {selection.active && selection.renderControl(row, selected, select)}
    <CardFrame menuAnchorRef={menuAnchorRef} moreExpanded={isMenuOpen?.(row.id)} title={row.text.title} primaryAction="play" onPlay={() => onPlay(row.id)} onMore={onMore ? () => onMore(row.id, menuAnchorRef) : undefined}
      selection={{active: selection.active, selected, onToggle: select, onEnter: () => selection.onEnter(row.id)}}
      accessibilityLabel={selection.active ? trackLabel(row) : t('next.browse.native.play', {title: trackLabel(row)})} style={{flex: 1, height: grow ? undefined : '100%'}} pressableStyle={{flex: 1, justifyContent: 'center'}}>
      {() =>
      <View style={{flex: 1, flexDirection: 'row', alignItems: 'center', gap: phone ? 14 : 16, minWidth: 0}}>
        {(phone ? (['number', 'title', 'duration'] as const) : columns).map(column => {
          if (column === 'number') return <View key={column} style={{width: phone ? 18 : 44, alignItems: 'center'}}>{row.playing ? <LevelMeter/> : row.done ? <Icon name="i-check" size={phone ? 16 : 20} color={theme.colors.textTertiary}/> : row.number !== undefined ? <Text style={[browseType(theme, tv ? 22 : phone ? 14 : 15, row.current ? 800 : 600), {color: row.current ? theme.colors.accentStrong : theme.colors.textTertiary}]}>{row.number}</Text> : null}</View>;
          if (column === 'title') return <View key={column} style={[columnStyle(column, variant), {flexDirection: 'row', alignItems: 'center', gap: 14}]}>
            {!phone && (variant === 'songs' || variant === 'top-songs') && row.artwork && <View style={{width: variant === 'songs' ? 40 : 42, height: variant === 'songs' ? 40 : 42, borderRadius: 6, overflow: 'hidden'}}>{row.artwork}</View>}
            <View style={{flex: 1, minWidth: 0}}><Text numberOfLines={grow ? undefined : 1} style={[browseType(theme, tv ? 24 : phone ? 15.5 : variant === 'songs' ? 15 : 16, row.done ? 500 : 600), {color: row.current || row.playing ? theme.colors.accentStrong : row.done ? theme.colors.textTertiary : theme.colors.text}]}>{row.text.title}</Text>
              {!phone && variant === 'top-songs' && row.text.caption.some(part => part.id === 'album') && <View style={{flexDirection: 'row', gap: 10, marginTop: 2}}>{row.text.caption.filter(part => part.id === 'album').map(part => <Text key={part.id} numberOfLines={grow ? undefined : 1} style={[browseType(theme, 13), {color: theme.colors.textTertiary, flexShrink: 1}]}>{part.text}</Text>)}</View>}
              {row.current && progress !== undefined && <View style={{flexDirection: 'row', alignItems: 'center', gap: 12, marginTop: 8, maxWidth: 360}}><View style={{flex: 1, height: 4, borderRadius: 4, backgroundColor: alpha(theme.colors.text, .22)}}><View style={{height: 4, width: `${progress * 100}%`, borderRadius: 4, backgroundColor: theme.colors.accentStrong}}/></View>{remaining && <Text style={[browseType(theme, 12), {color: theme.colors.textTertiary}]}>{remaining.text}</Text>}</View>}
            </View>
          </View>;
          const part = column === 'duration' ? duration?.text : column === 'artist' || column === 'album' ? row.text.caption.find(fact => fact.id === column)?.text : column === 'plays' ? row.plays === undefined ? undefined : defaultI18n.number(row.plays) : row.addedAt;
          return <Text key={column} numberOfLines={grow ? undefined : 1} style={[phone ? {maxWidth: 70, marginLeft: 'auto'} : columnStyle(column, variant), browseType(theme, tv ? 20 : phone ? 13.5 : 14), {color: column === 'artist' || column === 'album' ? theme.colors.textSecondary : theme.colors.textTertiary, textAlign: column === 'plays' || column === 'duration' ? 'right' : 'left', fontVariant: ['tabular-nums']}]}>{part}</Text>;
        })}
      </View>
      }
    </CardFrame>
    {actions}
  </View>;
}

function TrackHeader({variant, columns = defaultColumns[variant], sort, sortable = [], onSort}: Pick<TrackTableProps, 'variant' | 'columns' | 'sort' | 'sortable' | 'onSort'>) {
  const theme = useTheme();
  const grow = theme.fontScale > 1 || theme.textScale > 1;
  if (theme.platform === 'phone' || theme.isTV || variant === 'chapters') return null;
  return <View style={{height: grow ? undefined : 42, minHeight: 42, paddingLeft: 8, paddingRight: 12, flexDirection: 'row', alignItems: 'center', gap: 16, borderBottomWidth: 1, borderBottomColor: theme.colors.line, marginBottom: 6}}>
    {columns.map(column => <View key={column} style={columnStyle(column, variant)}>{column === 'number' ? <Text accessibilityLabel={t('next.browse.native.number')} style={[browseType(theme, 13), {color: theme.colors.textTertiary, textAlign: 'center'}]}>{t('next.browse.native.numberSign')}</Text> : (onSort && sortable.includes(column) ? <Focusable accessibilityRole="button" focusKind="row" onPress={() => onSort(column)} style={{height: grow ? undefined : 42, minHeight: 42, flexDirection: 'row', alignItems: 'center', gap: 6}}><Text style={[browseType(theme, 13), {color: sort?.column === column ? theme.colors.text : theme.colors.textTertiary}]}>{t(columnKeys[column])}</Text>{sort?.column === column && <Icon name={sort.direction === 'asc' ? 'i-down' : 'i-up'} size={14} color={theme.colors.accentStrong}/>}</Focusable> : <Text style={[browseType(theme, 13), {color: theme.colors.textTertiary}]}>{t(columnKeys[column])}</Text>)}</View>)}
    <View style={{width: 84}}/>
  </View>;
}

function WindowedTrackSlot({collection, index, render}: {collection: WindowedCollection<TrackRowData>; index: number; render: (row: TrackRowData) => ReactNode}) {
  const theme = useTheme(), page = collection.pageOf(index);
  const subscribe = useCallback((changed: () => void) => collection.subscribePage(page, changed), [collection, page]);
  const snapshot = useCallback(() => collection.getPageVersion(page), [collection, page]);
  useSyncExternalStore(subscribe, snapshot, snapshot);
  const row = collection.itemAt(index);
  if (row) return <>{render(row)}</>;
  return <View accessible={false} importantForAccessibility="no-hide-descendants" style={{height: theme.isTV ? 84 : theme.platform === 'phone' ? 54 : 56, flexDirection: 'row', gap: 14, alignItems: 'center', paddingHorizontal: 20}}><View style={{width: 40, height: 40, borderRadius: 6, backgroundColor: theme.colors.raisedHigh}}/><View style={{width: '40%', height: 12, borderRadius: 6, backgroundColor: theme.colors.raisedHigh}}/></View>;
}

function useVisibleRange(onVisibleRange: TrackTableProps['onVisibleRange']) {
  const callback = useRef(onVisibleRange);
  useEffect(() => {callback.current = onVisibleRange;}, [onVisibleRange]);
  return useCallback(({viewableItems}: {viewableItems: ViewToken[]}) => {
    const indices = viewableItems.flatMap(item => item.index === null ? [] : [item.index]);
    if (indices.length) callback.current?.(Math.min(...indices), Math.max(...indices));
  }, []);
}

function WindowedTracks({collection, ...props}: Omit<TrackTableProps, 'rows' | 'groups' | 'collection'> & {collection: WindowedCollection<TrackRowData>}) {
  const theme = useTheme(), snapshot = useSyncExternalStore(collection.subscribe, collection.getSnapshot, collection.getSnapshot);
  const grow = theme.fontScale > 1 || theme.textScale > 1;
  const [focused, setFocused] = useState<number>();
  const rowHeight = theme.isTV ? 84 : theme.platform === 'phone' ? 54 : 56, headerHeight = theme.isTV || theme.platform === 'phone' ? 0 : 48, rangeCallback = useRef(props.onVisibleRange);
  useEffect(() => {rangeCallback.current = props.onVisibleRange;}, [props.onVisibleRange]);
  useEffect(() => {collection.ensureRange(0, 12);}, [collection]);
  const visible = useCallback(({viewableItems}: {viewableItems: ViewToken[]}) => {
    const indices = viewableItems.flatMap(item => item.index === null ? [] : [item.index]);
    if (!indices.length) return;
    const first = Math.min(...indices), last = Math.max(...indices);
    collection.ensureRange(first, last); rangeCallback.current?.(first, last);
  }, [collection]);
  return <VirtualizedList<number> testID={props.testID} data={collection} getItem={(_data, index) => index} getItemCount={() => snapshot.total ?? 0} keyExtractor={index => collection.keyAt(index)} extraData={`${snapshot.generation}:${theme.fontScale}:${theme.textScale}`}
    renderItem={({item: index}) => <WindowedTrackSlot collection={collection} index={index} render={row => <TrackRow row={row} {...props} testID={props.testID ? `${props.testID}.row.${row.id}` : undefined} index={index} previousFocused={focused === index - 1} onFocus={() => setFocused(index)} onBlur={() => setFocused(value => value === index ? undefined : value)}/>}/>}
    getItemLayout={grow ? undefined : (_data, index) => ({length: rowHeight, offset: headerHeight + rowHeight * index, index})} onViewableItemsChanged={visible} viewabilityConfig={viewability} initialNumToRender={12} maxToRenderPerBatch={8} windowSize={5} removeClippedSubviews={!theme.isTV}
    ListHeaderComponent={<TrackHeader {...props}/>} ListEmptyComponent={<>{props.empty}</>}/>;
}

/** Songs accepts the canonical sparse collection; no eager array is created from its total. */
export function TrackTable(props: TrackTableProps) {
  const theme = useTheme(), visible = useVisibleRange(props.onVisibleRange);
  const [focused, setFocused] = useState<number>();
  const focus = (index: number) => ({index, previousFocused: focused === index - 1, onFocus: () => setFocused(index), onBlur: () => setFocused(value => value === index ? undefined : value)});
  if (props.collection) return <WindowedTracks {...props} collection={props.collection}/>;
  if (props.groups) return <FlatList testID={props.testID} data={props.groups.find(group => group.id === props.disc.value)?.rows ?? []} keyExtractor={row => row.id} renderItem={({item, index}) => <TrackRow {...props} {...focus(index)} testID={props.testID ? `${props.testID}.row.${item.id}` : undefined} row={item}/>} onViewableItemsChanged={visible} viewabilityConfig={viewability} initialNumToRender={12} maxToRenderPerBatch={8} windowSize={5}
    ListHeaderComponent={<><View style={{flexDirection: 'row', gap: theme.isTV ? 14 : 8, paddingHorizontal: theme.isTV ? 18 : 8, marginBottom: 16}}>{props.groups.map(group => <Chip key={group.id} variant="pill" selected={group.id === props.disc.value} onPress={() => props.disc.onValueChange(group.id)} accessibilityRole="radio" accessibilityState={{checked: group.id === props.disc.value}}>{group.title ?? t('next.browse.native.disc', {number: group.discNumber})}</Chip>)}</View><TrackHeader {...props}/></>}
    ListEmptyComponent={<>{props.empty}</>}/>;
  return <FlatList testID={props.testID} data={props.rows} keyExtractor={row => row.id} renderItem={({item, index}) => <TrackRow {...props} {...focus(index)} testID={props.testID ? `${props.testID}.row.${item.id}` : undefined} row={item}/>} onViewableItemsChanged={visible} viewabilityConfig={viewability} initialNumToRender={12} maxToRenderPerBatch={8} windowSize={5} ListHeaderComponent={<TrackHeader {...props}/>} ListEmptyComponent={<>{props.empty}</>}/>;
}
