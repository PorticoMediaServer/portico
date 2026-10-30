import React, {useEffect, useRef, useState, useSyncExternalStore, type ReactNode, type RefObject} from 'react';
import {ScrollView, Text, View, useWindowDimensions} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {AmbientArt, Artwork, StandardBackdrop} from '../../kit/artwork';
import {PosterCard, type CardSelection} from '../../kit/cards';
import {CardFrame} from '../../kit/cards/primitives';
import {SelectionCircle} from '../../kit/selection';
import {Button, Chip, Focusable} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import {WindowedGrid, WindowedRow} from '../../kit/layout';
import {Menu, type MenuEntry} from '../../kit/overlays';
import {Quiet, StateView} from '../../kit/states';
import {browseType} from '../../kit/browse/presentation';
import {useTheme} from '../../kit/theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeEntryMenuState} from '../destinations/entry-actions';
import type {NativeSavedSelectionPort} from '../saved/SavedScreen';
import {savedDuration} from '../saved/presentation';
import type {NativeCollectionSort, NativeCollectionState} from './controller';

export type NativeCollectionScreenProps = Readonly<{
  focusScope: string; state: NativeCollectionState;
  /** False while a title opened from this page covers it (the page stays mounted). */
  active?: boolean;
  runtime: NativeShellRuntime;
  onPlay: () => void;
  onShuffle: () => void;
  onSort: (sort: string, direction: 'asc' | 'desc') => void;
  onOpen: (entry: ContentEntry) => void;
  onBack: () => void;
  onRetry: () => void;
  actions?: Readonly<{menuState?: NativeEntryMenuState; menu: (entry: ContentEntry, anchor?: RefObject<View | null>, select?: () => void) => void;
    isMenuOpen?: (entry: ContentEntry) => boolean}>;
  selection?: NativeSavedSelectionPort;
}>;

/** The window's own key for a member (the controller's `keyOf`). */
const nativeCollectionEntryKey = (entry: ContentEntry) => `${entry.kind}:${entry.id}`;
const sortLabel = (sort: NativeCollectionSort) => defaultI18n.has(sort.labelKey) ? defaultI18n.t(sort.labelKey as never) : sort.id;
/** The year span, only when every member is in memory: a partial window never guesses it. */
const years = (state: NativeCollectionState) => {
  if (!state.totalCount || !state.complete) return undefined;
  const values = state.complete.map(entry => entry.year).filter((year): year is number => Number.isFinite(year));
  if (!values.length) return undefined;
  const min = Math.min(...values), max = Math.max(...values);
  return min === max ? String(min) : `${min} – ${max}`;
};

/** tv-detail#11 / phone-detail#8: the collection's own member order and progress.
 * Members render from a bounded window (LR05): only the cards in view are mounted,
 * and the focused card's page stays in memory. */
export function NativeCollectionScreen(props: NativeCollectionScreenProps) {
  const {state, runtime, onBack, actions, selection} = props, theme = useTheme(), t = defaultI18n.t, dimensions = useWindowDimensions(), insets = useSafeAreaInsets();
  useSyncExternalStore(actions?.menuState?.subscribe ?? (() => () => {}), actions?.menuState?.getSnapshot ?? (() => 0));
  useSyncExternalStore(selection?.subscribe ?? (() => () => {}), selection?.getSnapshot ?? (() => 0));
  const tv = theme.isTV, compact = !tv && theme.platform === 'phone', [sortOpen, setSortOpen] = useState(false), [pageMenu, setPageMenu] = useState(false);
  const active = props.active !== false;
  useEffect(() => {
    if (!tv || !active) return;
    return runtime.registerTVPageBack?.(() => {onBack(); return true;});
  }, [active, onBack, runtime, tv]);
  const collection = state.collection, backdrop = state.first?.backdropUrl;
  const ambient = runtime.ambient(backdrop), foreground = runtime.artwork(backdrop);
  const facts = [t('next.browse.filmCount', {count: state.totalCount}), years(state)].filter((value): value is string => !!value);
  const sort = state.sorts.find(value => value.id === state.sort);
  const sortItems: MenuEntry[] = state.sorts.map(value => ({id: value.id, label: sortLabel(value), selected: value.id === state.sort,
    onSelect: () => props.onSort(value.id, value.directions[0] ?? 'asc')}));
  const cardWidth = tv ? 214 : Math.max(120, (dimensions.width - 50) / 2), error = state.error;
  const empty = state.phase === 'ready' && state.totalCount === 0, failed = !!error && !state.totalCount;
  const controls = <View style={{flexDirection: 'row', alignItems: 'center', gap: tv ? 18 : 10, marginTop: tv ? 30 : compact ? 18 : 16,
    ...(compact ? {paddingHorizontal: 20} : {})}}>
    <Button testID="collection-play-all" focusId="collection-play-all" hasTVPreferredFocus={tv} icon="i-play" onPress={props.onPlay}
      size={tv ? 'hero' : undefined} style={compact ? {flex: 1, height: 48} : undefined}>{t('next.cards.playAll')}</Button>
    <Button testID="collection-shuffle" focusId="collection-shuffle" variant="glass" icon="i-shuffle" onPress={props.onShuffle}
      size={tv ? 'hero' : undefined} iconOnly={compact} accessibilityLabel={t('title.shuffle')}
      style={compact ? {width: 48, height: 48, borderRadius: 24} : undefined}>{t('title.shuffle')}</Button>
    {compact && <Button testID="collection-menu" variant="glass" icon="i-more" iconOnly accessibilityLabel={t('card.more', {title: state.title})}
      onPress={() => setPageMenu(true)} style={{width: 48, height: 48, borderRadius: 24}}/>}
  </View>;
  const hero = <View style={{height: tv ? 620 : compact ? 300 : 360}}>
    <AmbientArt source={ambient} foreground={foreground} treatment="title" height={tv ? 760 : 460}/>
    <View style={{position: 'absolute', left: tv ? 200 : 20, right: tv ? 200 : 20, bottom: tv ? 70 : compact ? 4 : 26, alignItems: compact ? 'center' : undefined}}>
      <Text style={[browseType(theme, tv ? 24 : compact ? 13 : 12, 700), {color: theme.colors.accentStrong}]}>{t('next.home.collection')}</Text>
      <Text accessibilityRole="header" numberOfLines={2} style={[browseType(theme, tv ? 76 : compact ? 46 : 40, 800, -.035), {color: theme.colors.text, marginTop: tv ? 6 : compact ? 8 : 4, marginBottom: compact ? 10 : undefined, textAlign: compact ? 'center' : 'left'}]}>{state.title}</Text>
      <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: tv ? 20 : 12, justifyContent: compact ? 'center' : 'flex-start', marginTop: tv ? 12 : compact ? 0 : 6}}>
        {facts.map((fact, index) => <Text key={index} style={[browseType(theme, tv ? 24 : 13, index === 0 ? 700 : 500), {color: theme.colors.textSecondary}]}>{fact}</Text>)}
      </View>
      {!compact && controls}
    </View>
  </View>;
  const automatic = compact && state.projection?.entity?.origin === 'automatic' ? <View style={{flexDirection: 'row', alignItems: 'flex-start', gap: 10, marginHorizontal: 20, marginTop: 12}}>
    <Icon name="i-info" size={16} color={theme.colors.textTertiary}/>
    <Text style={[browseType(theme, 12.5, 400, 0, 18), {color: theme.colors.textTertiary, flex: 1}]}>{t('next.collection.automaticDescription')}</Text>
  </View> : null;
  const toolbar = <View style={{flexDirection: 'row', alignItems: 'center', gap: 14, marginBottom: tv ? 28 : compact ? 8 : 14, ...(compact ? {paddingHorizontal: 20} : {})}}>
    {compact && <Text style={[browseType(theme, 13, 600), {color: theme.colors.textTertiary}]}>{facts[0]}</Text>}
    {compact && <View style={{flex: 1}}/>}
    {state.sorts.length > 1 && <Chip testID="collection-sort" onPress={() => setSortOpen(true)}>{sort ? sortLabel(sort) : defaultI18n.t('web.filters.sortBy')}</Chip>}
    {!compact && <View style={{flex: 1}}/>}
    {!tv && !compact && selection?.start && <Chip icon="i-check" onPress={() => selection.start?.()}>{t('next.selection.select')}</Chip>}
    {state.phase === 'loading' ? <Quiet><Text style={[browseType(theme, tv ? 22 : 13, 500), {color: theme.colors.textTertiary}]}>{t('status.loading')}</Text></Quiet> : null}
  </View>;
  const status: ReactNode = failed ? <StateView variant="inline" {...runtime.error(error, props.onRetry)}/>
    : empty ? <StateView variant="inline" title={t('next.collection.empty')}/>
    // A failed page further along keeps the members already shown and offers to try again.
    : error ? <StateView variant="inline" {...runtime.error(error, props.onRetry)}/> : null;
  const placeholder = () => <View style={{flex: 1, borderRadius: compact ? 0 : 8, backgroundColor: compact ? 'transparent' : theme.colors.raised}}/>;
  return <View style={{flex: 1}} testID="native-collection"><StandardBackdrop/>
    {!tv && <Focusable testID="collection-back" accessibilityLabel={t('action.back')} onPress={props.onBack} focusRadius={20}
      style={{position: 'absolute', left: 10, top: insets.top + 6, width: 40, height: 40, alignItems: 'center', justifyContent: 'center', zIndex: 5}}>
      <Icon name="i-left" size={22} color={theme.colors.text}/></Focusable>}
    {tv ? <ScrollView>{hero}<View style={{paddingLeft: 200, paddingRight: 96, paddingTop: 34, paddingBottom: 80}}>
      {toolbar}
      {status}
      {/* The row runs from the screen edge so focused cards slide under the rail instead of a clip at x 200. */}
      {!failed && !empty && <WindowedRow testID="collection-row" collection={collection} getKey={nativeCollectionEntryKey} itemWidth={214} itemHeight={321} captionHeight={72}
        gap={28} gutter={200} rightGutter={96} style={{marginLeft: -200, marginRight: -96}} renderPlaceholder={placeholder}
        renderItem={(entry, context) => <CollectionCard entry={entry} width={214} runtime={runtime} onOpen={props.onOpen} actions={actions}
          selection={selection?.item(entry)} onFocus={context.onFocus} onBlur={context.onBlur}/>}/>}
    </View></ScrollView>
      : <WindowedGrid testID={compact ? 'collection-list' : 'collection-grid'} collection={collection} getKey={nativeCollectionEntryKey}
        header={<>{hero}{compact && controls}{automatic}
          <View style={{paddingHorizontal: compact ? 0 : 20, paddingTop: compact ? 14 : 18}}>{toolbar}{status}</View></>}
        itemWidth={compact ? dimensions.width : cardWidth} itemHeight={compact ? 98 : cardWidth * 1.5} captionHeight={compact ? 0 : 46}
        gap={compact ? 0 : 10} rowGap={compact ? 0 : 18} gutter={compact ? 0 : 20} rightGutter={compact ? 0 : 20} paddingBottom={80}
        renderPlaceholder={failed || empty ? () => null : placeholder}
        renderItem={(entry, context) => compact
          ? <CollectionRow entry={entry} runtime={runtime} onOpen={props.onOpen} actions={actions} selection={selection?.item(entry)}/>
          : <CollectionCard entry={entry} width={cardWidth} runtime={runtime} onOpen={props.onOpen} actions={actions} selection={selection?.item(entry)}
            onFocus={context.onFocus} onBlur={context.onBlur}/>}/>}
    {selection?.render()}
    <Menu testID={`${props.focusScope}.sort-menu`} open={sortOpen} onOpenChange={setSortOpen} title={defaultI18n.t('web.filters.sortBy')} items={sortItems}/>
    <Menu testID="collection-page-menu" open={pageMenu} onOpenChange={setPageMenu} title={state.title} items={[
      {id: 'play', label: t('next.cards.playAll'), icon: 'i-play', onSelect: props.onPlay},
      {id: 'shuffle', label: t('title.shuffle'), icon: 'i-shuffle', onSelect: props.onShuffle},
      ...(selection?.start ? [{id: 'select', label: t('next.selection.select'), icon: 'i-check' as const, onSelect: () => selection.start?.()}] : []),
    ]}/>
  </View>;
}

export function CollectionCard({entry, width, runtime, onOpen, actions, selection, onFocus, onBlur}: {entry: ContentEntry; width: number; runtime: NativeShellRuntime;
  onOpen: (entry: ContentEntry) => void; actions?: NativeCollectionScreenProps['actions']; selection?: CardSelection; onFocus?: () => void; onBlur?: () => void}) {
  const anchor = useRef<View>(null);
  return <PosterCard testID={`collection-item-${entry.id}`} title={entry.displayTitle || entry.title} width={width}
    artwork={runtime.artwork(entry.posterUrl)} watched={entry.watched} isNew={entry.new} selection={selection} menuAnchorRef={anchor}
    progress={entry.duration && entry.progressSeconds ? Math.min(1, entry.progressSeconds / entry.duration) : undefined}
    caption={entry.year === undefined ? [] : [{id: 'year', text: String(entry.year), role: 'context'}]}
    onFocus={onFocus} onBlur={onBlur}
    onNavigate={() => onOpen(entry)} onMore={actions ? () => actions.menu(entry, anchor, selection?.onEnter) : undefined} moreExpanded={actions?.isMenuOpen?.(entry)}/>;
}

function CollectionRow({entry, runtime, onOpen, actions, selection}: {entry: ContentEntry; runtime: NativeShellRuntime;
  onOpen: (entry: ContentEntry) => void; actions?: NativeCollectionScreenProps['actions']; selection?: CardSelection}) {
  const theme = useTheme(), anchor = useRef<View>(null), t = defaultI18n.t;
  const progress = entry.duration && entry.progressSeconds ? Math.min(1, entry.progressSeconds / entry.duration) : undefined;
  const more = actions ? () => actions.menu(entry, anchor, selection?.onEnter) : undefined;
  return <View style={{paddingVertical: 7, paddingLeft: 20, paddingRight: 12}}><CardFrame testID={`collection-item-${entry.id}`} title={entry.displayTitle || entry.title}
    menuAnchorRef={anchor} onNavigate={() => onOpen(entry)} onMore={more} moreExpanded={actions?.isMenuOpen?.(entry)} selection={selection}
    pressableStyle={{flexDirection: 'row', alignItems: 'center', gap: 14}}>
    {() => <>
      {selection?.active && <SelectionCircle selected={selection.selected} selecting interactive={false} variant="row" size={24} accessibilityLabel={t('card.select', {title: entry.title})}/>}
      <Artwork source={runtime.artwork(entry.posterUrl)} width={56} height={84} style={{borderRadius: 7}}/>
      <View style={{flex: 1, minWidth: 0}}><Text numberOfLines={1} style={[browseType(theme, 15.5, 700), {color: theme.colors.text}]}>{entry.displayTitle || entry.title}</Text>
        <View style={{flexDirection: 'row', gap: 10, marginTop: 2}}>
          {entry.year !== undefined && <Text style={[browseType(theme, 12.5), {color: theme.colors.textTertiary}]}>{entry.year}</Text>}
          {entry.duration !== undefined && <Text style={[browseType(theme, 12.5), {color: theme.colors.textTertiary}]}>{savedDuration(entry.duration)}</Text>}
        </View>
        {(entry.watched || progress !== undefined) && <View style={{flexDirection: 'row', alignItems: 'center', gap: 6, marginTop: 6}}>
          {entry.watched ? <Icon name="i-check" size={16} color={theme.colors.accentStrong}/>
            : <View style={{width: 72, height: 3, borderRadius: 2, backgroundColor: theme.colors.raisedBright}}><View style={{width: `${(progress ?? 0) * 100}%`, height: 3, backgroundColor: theme.colors.accentStrong}}/></View>}
          <Text style={[browseType(theme, 12.5, 700), {color: theme.colors.textSecondary}]}>{entry.watched ? t('saved.watched') : t('next.kit.remaining', {time: savedDuration(Math.max(0, (entry.duration ?? 0) - (entry.progressSeconds ?? 0)))})}</Text>
        </View>}
      </View>
      {more && <Focusable accessibilityLabel={t('card.more', {title: entry.title})} onPress={more} style={{width: 44, height: 44, alignItems: 'center', justifyContent: 'center'}}><Icon name="i-more" size={20} color={theme.colors.textTertiary}/></Focusable>}
    </>}
  </CardFrame></View>;
}
