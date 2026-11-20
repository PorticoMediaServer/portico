import React, {useEffect, useRef, useState, useSyncExternalStore, type RefObject} from 'react';
import {Text, View, useWindowDimensions} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {browseToolbarModel} from '../../../../../packages/client-core/src/presentation/kit-model';
import {serverTextLabel} from '../../../../../packages/client-core/src/presentation/server-text';
import {homeRowReasonParts} from '../../../../../packages/client-core/src/presentation/home-row-reason';
import {HomeRowCard} from '../../kit/browse/HomeRowCard';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import {Button, Chip, Focusable} from '../../kit/controls';
import {BrowseToolbar, FiltersButton, QuickToggle, SortMenu, type BrowseLabels} from '../../kit/browse';
import {BrowseFrame} from '../../kit/frames';
import {CardFrame, Parts} from '../../kit/cards/primitives';
import {MediaArt} from '../../kit/artwork';
import {browseType} from '../../kit/browse/presentation';
import {cardTitleLine} from '../../kit/cards/caption-metrics';
import {AmbientArt} from '../../kit/artwork';
import {FocusItem, FocusRegion, useFocusRegistry} from '../../kit/focus';
import {WindowedGrid, type LayoutSelection} from '../../kit/layout';
import {Icon} from '../../kit/Icon';
import {GridSkeleton, LoadingBoundary, LoadingPaint, SkeletonBlock, StateView} from '../../kit/states';
import {nativeType, useTheme} from '../../kit/theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import {authorityKey} from '../../shell/model';
import {homeBrowseLabel} from './browse-label';
import {homeSeeAllCardWidth, homeSeeAllQuickOrder} from './see-all-model';
import {homeCardText} from './model';
import {NativeHomeRowController} from './row-controller';
import {hasHomeRowQueue} from '../../../../../packages/client-core/src/home-row-browse';

export {homeBrowseLabel};

/** Requested row only. Membership IDs locate grid cells; displayed episodes and
 * their actual playback targets retain their independent server identities. */
export type HomeSeeAllProps = {
  controller: NativeHomeRowController; runtime: NativeShellRuntime; active: boolean;
  onBack: () => void; onPlay: (shuffle: boolean) => void; onOpen: (entry: ContentEntry, current: () => boolean) => void; onPlayEntry: (entry: ContentEntry, current: () => boolean) => void;
  onMenu?: (entry: ContentEntry, anchor: RefObject<View | null>) => void; onFilters: () => void; onReshuffle: () => void;
  kindLabel: (id: string) => string; selection?: LayoutSelection;
};

/** One boundary stays mounted across loading and ready, so the content's own fade
 * can run; the error branch stays immediate and unchanged. */
export function HomeSeeAll(props: HomeSeeAllProps) {
  const runtime = props.runtime, theme = useTheme(), window = useWindowDimensions();
  const [surface, setSurface] = useState(window.width), [slow, setSlow] = useState(false);
  const state = useSyncExternalStore(props.controller.subscribe, props.controller.getSnapshot);
  useEffect(() => {void props.controller.load();}, [props.controller]);
  const loading = !state.row || !state.capabilities || !state.sort;
  useEffect(() => {
    setSlow(false);
    if (!loading || !props.active || state.phase === 'error') return;
    const timer = setTimeout(() => setSlow(true), 8000);
    return () => clearTimeout(timer);
  }, [loading, props.active, state.phase]);
  if (state.phase === 'error') return <StateView {...runtime.error(state.error, () => {void props.controller.load();}, 'home')} variant="centered"/>;
  return <View style={{flex: 1, overflow: 'hidden'}} onLayout={event => setSurface(event.nativeEvent.layout.width)}>
    <LoadingBoundary ready={!loading} immediate={!loading} style={{flex: 1}} skeleton={<HomeSeeAllSkeleton runtime={runtime} surface={surface}/> }>
      {!loading && <HomeSeeAllReady {...props} surface={surface}/>}
    </LoadingBoundary>
    {loading && slow && <Text accessibilityLiveRegion="polite" style={[theme.type.caption, {color: theme.colors.textSecondary, padding: 16}]}>{runtime.text.t('next.states.stillLoading')}</Text>}
  </View>;
}

function HomeSeeAllSkeleton({runtime, surface}: {runtime: NativeShellRuntime; surface: number}) {
  const theme = useTheme(), window = useWindowDimensions(), tv = theme.isTV, phone = !tv && theme.platform === 'phone', top = useSafeAreaInsets().top;
  const gutter = tv ? 200 : theme.gutter, right = tv ? 96 : gutter, gap = tv ? 32 : phone ? 10 : 12;
  const width = homeSeeAllCardWidth(surface, gutter, tv ? 200 : theme.spec.card.width.poster[theme.column], (runtime.appearanceCardSizePercent ?? 100) / 100, phone);
  const content = Math.max(1, surface - gutter - right), columns = Math.max(1, Math.floor((content + gap) / (width + gap)));
  return <View testID="home-see-all-skeleton" style={{paddingLeft: gutter, paddingRight: right, paddingTop: tv ? 64 : top + 20}}>
    <SkeletonBlock width={tv ? 100 : 64} height={tv ? 26 : 18} style={{marginBottom: tv ? 18 : 16}}/>
    <SkeletonBlock width="78%" height={tv ? 62 : phone ? 36 : 30} style={{marginBottom: tv ? 12 : 8}}/>
    <SkeletonBlock width="90%" height={tv ? 26 : 14}/><SkeletonBlock width="68%" height={tv ? 26 : 14} style={{marginTop: 7, marginBottom: 24}}/>
    <View style={{flexDirection: 'row', gap: tv ? 16 : 10, marginBottom: tv ? 24 : 16}}>
      <SkeletonBlock width={phone ? (content - 10) / 2 : tv ? 180 : 112} height={tv ? 64 : 48} radius={12}/>
      <SkeletonBlock width={phone ? (content - 10) / 2 : tv ? 200 : 126} height={tv ? 64 : 48} radius={12}/>
    </View>
    <View style={{flexDirection: 'row', gap: 8, overflow: 'hidden', marginBottom: phone ? 8 : 16}}>{[0, 1, 2, 3].map(index => <SkeletonBlock key={index} width={tv ? 160 : 82} height={tv ? 48 : 36} radius={18}/>)}</View>
    {phone && <>
      <View style={{flexDirection: 'row', gap: 8, marginBottom: 14}}>{[0, 1, 2].map(index => <SkeletonBlock key={index} width={96} height={32} radius={16}/>)}</View>
      <View style={{flexDirection: 'row', alignItems: 'center', gap: 10, marginBottom: 16}}><SkeletonBlock width={62} height={14}/><View style={{flex: 1}}/><SkeletonBlock width={116} height={36} radius={18}/><SkeletonBlock width={72} height={36} radius={10}/></View>
    </>}
    <GridSkeleton elapsedMs={300} scrollOffset={0} viewportHeight={Math.max(0, window.height - (tv ? 470 : phone ? 380 : 290))} width={content}
      cardWidth={width} columns={columns} gap={gap} rowGap={tv ? 40 : 22} captionLines={2}/>
  </View>;
}

function HomeSeeAllReady({controller, runtime, active, onBack, onPlay, onOpen, onPlayEntry, onMenu, onFilters, onReshuffle, kindLabel, selection, surface}: HomeSeeAllProps & {surface: number}) {
  const theme = useTheme(), registry = useFocusRegistry(), t = runtime.text.t, labels = {t, has: defaultI18n.has};
  const [view, setView] = useState<'grid' | 'list'>('grid'),
    list = !theme.isTV && view === 'list';
  const visible = useRef(active); visible.current = active;
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const data = useSyncExternalStore(controller.collection.subscribe, controller.collection.getSnapshot);
  const entered = useRef(false), gridId = `home-see-all:${authorityKey(runtime.getSnapshot().authority)}:${controller.rowId}`;
  const focusRevision = useSyncExternalStore(registry.subscribe, registry.getSnapshot);
  useEffect(() => {
    if (!theme.isTV || !active || entered.current || state.phase !== 'ready' || !registry.child({regionId: gridId, childId: 'play'})) return;
    if (registry.focus({regionId: gridId, childId: 'play'})) entered.current = true;
  }, [active, focusRevision, gridId, registry, state.phase, theme.isTV]);
  // The outer boundary owns error/loading presentation; this ready view only narrows its props.
  if (state.phase === 'error' || !state.row || !state.capabilities || !state.sort) return null;
  const row = state.row;
  const model = browseToolbarModel(state.capabilities, state.capabilities.library.kind || 'movies', state.filters, state.sort, {t: runtime.text.t, locale: runtime.text.locale,
    textEntry: !theme.isTV, scope: 'group', owner: runtime.getSnapshot().account.viewerRole === 'owner'});
  const total = state.counts.find(value => value.id === (state.selectedKind ?? 'all'));
  const title = serverTextLabel(labels, row.titleText, row.title);
  const reason = row.reason?.text ?? row.why ?? row.subtitleText;
  // The server's why sentence, with the titles it names in bold (as web and desktop-browse frame 6 draw it).
  const reasonParts = !row.reason?.text && row.why ? homeRowReasonParts(row, labels) : undefined;
  const artwork = row.entries.find(entry => entry.backdropUrl || entry.posterUrl);
  const tv = theme.isTV, phone = !tv && theme.platform === 'phone', gutter = tv ? 200 : theme.gutter, cardScale = (runtime.appearanceCardSizePercent ?? 100) / 100,
    width = homeSeeAllCardWidth(surface, gutter, tv ? 200 : theme.spec.card.width.poster[theme.column], cardScale, phone);
  const playable = hasHomeRowQueue(row.id);
  const intro = reason ? reasonParts ? reasonParts.map((part, index) => part.titleId ? <Text key={index} style={{color: theme.colors.text, fontWeight: '700'}}>{part.text}</Text> : part.text)
    : serverTextLabel(labels, reason) : undefined;
  // TV's own header: Back, the 62 pt title, the reason and the hero-size Play and Shuffle.
  const header = <View style={{paddingLeft: gutter, paddingRight: 96, paddingTop: 64, paddingBottom: 24}}>
    <Focusable focusId="back" onPress={onBack} accessibilityRole="button" accessibilityLabel={t('next.home.shell.home')} style={{height: 32, marginBottom: 6, flexDirection: 'row', alignItems: 'center', gap: 6, alignSelf: 'flex-start'}}>
      <Icon name="i-left" size={22} color={theme.colors.textTertiary}/><Text style={[nativeType({size: 22, weight: 600, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textTertiary}]}>{t('next.home.shell.home')}</Text>
    </Focusable>
    <Text accessibilityRole="header" style={[nativeType({size: 62, weight: 800, tracking: -.04, lineHeight: 62}, theme.textScale), {color: theme.colors.text, marginBottom: 12}]}>{title}</Text>
    {intro && <Text style={[nativeType({size: 24, weight: 400, tracking: 0, lineHeight: 34.8}, theme.textScale), {maxWidth: 1100, color: theme.colors.textSecondary, marginBottom: 24}]}>{intro}</Text>}
    {playable && <View style={{flexDirection: 'row', gap: 16}}><Button focusId="play" icon="i-play" size="hero" onPress={() => onPlay(false)}>{t('entry.play')}</Button>
      <Button focusId="shuffle" variant="glass" icon="i-shuffle" size="hero" onPress={() => onPlay(true)}>{t('title.shuffle')}</Button></View>}
  </View>;
  const sortControl = model.sorts.length > 0 && <FocusItem focusId="sort"><SortMenu scope={gridId} sorts={model.sorts} value={model.sort} label={value => homeBrowseLabel(value, labels)} onValueChange={controller.setSort} onReshuffle={onReshuffle}/></FocusItem>;
  const filterControl = model.groups.length > 0 && <FocusItem focusId="filters"><FiltersButton count={state.filters.length} onPress={onFilters}/></FocusItem>;
  const kindControls = controller.kinds.length > 0 && <>
    <FocusItem focusId="kind:all"><Chip selected={!state.selectedKind} onPress={() => controller.selectKind(undefined)} count={state.counts.find(value => value.id === 'all')?.total}>{t('search.chip.all')}</Chip></FocusItem>
    {controller.kinds.map(kind => <FocusItem key={kind.id} focusId={`kind:${kind.id}`}><Chip selected={state.selectedKind === kind.id} onPress={() => controller.selectKind(kind.id)} count={state.counts.find(value => value.id === kind.id)?.total}>{kindLabel(kind.id)}</Chip></FocusItem>)}
  </>;
  const quickControls = model.quick.filter(quick => homeSeeAllQuickOrder(quick.id, tv)).map(quick => <FocusItem key={quick.id} focusId={`quick:${quick.id}`}><QuickToggle checked={quick.active} onCheckedChange={checked => {
    const remaining = state.filters.filter(node => JSON.stringify(node) !== JSON.stringify(quick.query)); controller.setFilters(checked ? [...remaining, quick.query] : remaining);
  }}>{quick.label?.text ?? homeBrowseLabel(quick, labels)}</QuickToggle></FocusItem>);
  const countLabel = total && <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{t('count.titles', {count: total.total})}</Text>;
  // TV's own page (the frame does not carry its focus regions yet): the sort, filters, kinds and quick filters in one row.
  const toolbar = <View style={{paddingLeft: gutter, paddingRight: 96, flexDirection: 'row', alignItems: 'center', gap: 14, paddingBottom: 34}}>
    {sortControl}{filterControl}
    <View style={{width: 1, height: 40, backgroundColor: theme.colors.line, marginHorizontal: 6}}/>
    {kindControls}
    <View style={{width: 1, height: 40, backgroundColor: theme.colors.line, marginHorizontal: 6}}/>
    {quickControls}
    <View style={{marginLeft: 'auto'}}>{countLabel}</View>
  </View>;
  const wash = artwork && <View pointerEvents="none" style={{position: 'absolute', inset: 0, opacity: .26}}><AmbientArt source={runtime.ambient(artwork.backdropUrl ?? artwork.posterUrl)} treatment="hero" visible={active}/></View>;
  const grid = () => data.error ? <StateView {...runtime.error(data.error, () => controller.collection.ensureRange(0, 0), 'home')} variant="centered"/> :
    <WindowedGrid collection={controller.collection} getKey={item => item.membershipId} selection={selection}
      itemWidth={list ? surface - 32 : width} itemHeight={list ? 70 : width * 1.5 + (tv ? 74 : 48 + cardTitleLine(theme))} captionHeight={list ? 70 : tv ? 74 : 48 + cardTitleLine(theme)}
      gap={list ? 0 : tv ? 32 : phone ? 10 : 12} rowGap={list ? 0 : tv ? 40 : 22} gutter={list ? 16 : gutter} rightGutter={list ? 16 : tv ? 96 : theme.gutter}
      paddingBottom={40} style={{flex: 1}} testID={list ? 'home-see-all-list' : 'home-see-all-grid'}
      renderPlaceholder={index => {
        const placeholderWidth = list ? surface - 32 : width, placeholderHeight = list ? 70 : width * 1.5;
        return <LoadingPaint key={index} ready={false} skeleton={<SkeletonBlock width={placeholderWidth} height={placeholderHeight}/>}
          style={{width: placeholderWidth, height: placeholderHeight}}/>;
      }}
      renderItem={(item, cell) => <FocusItem focusId={item.membershipId}>{list
        ? <HomeListRow entry={item.entry} runtime={runtime} width={surface - 32} onFocus={cell.onFocus} onBlur={cell.onBlur}
            onOpen={() => {if (controller.isCurrentItem(item, cell.index)) onOpen(item.entry, () => visible.current && controller.isCurrentItem(item, cell.index));}}
            onPlay={item.entry.playback ? () => {
              const current = () => visible.current && controller.isCurrentItem(item, cell.index);
              if (current()) onPlayEntry(item.entry, current);
            } : undefined}
            onMenu={onMenu ? anchor => {if (controller.isCurrentItem(item, cell.index)) onMenu(item.entry, anchor);} : undefined}/>
        : <HomeRowCard entry={item.entry} runtime={runtime} width={width} onFocus={cell.onFocus} onBlur={cell.onBlur}
            onOpen={() => {if (controller.isCurrentItem(item, cell.index)) onOpen(item.entry, () => visible.current && controller.isCurrentItem(item, cell.index));}}
            onPlay={() => {
              const current = () => visible.current && controller.isCurrentItem(item, cell.index);
              if (current()) onPlayEntry(item.entry, current);
            }}
            onMenu={onMenu ? anchor => {if (controller.isCurrentItem(item, cell.index)) onMenu(item.entry, anchor);} : undefined}/>}
      </FocusItem>}/>;
  if (!tv) {
    // Phone and tablet: the library frame (`BrowseFrame`) draws See all as web's does: Back, the row's title, its reason, Play and
    // Shuffle; then the frame's one toolbar row (Filters, then Sort, then the kind chips and quick filters scrolling in one
    // line, the view toggle); then the grid. The frame keeps the header and toolbar while the grid scrolls under them.
    const browseLabels: BrowseLabels = {field: value => homeBrowseLabel(value, labels), sort: value => homeBrowseLabel(value, labels), quick: value => homeBrowseLabel(value, labels)};
    const toolbarModel = {...model, quick: model.quick.filter(quick => homeSeeAllQuickOrder(quick.id, false))};
    // Touch moves no focus between controls, so the page has no focus region here (as the library's touch page has none).
    return <View testID="home-see-all" style={{flex: 1}}>{wash}
        <BrowseFrame testID="home-see-all-frame" header={{title, back: {label: t('next.home.shell.home'), onPress: onBack, testID: 'home-see-all-back'}, intro,
          // The frame shares a phone's action row in equal halves; the buttons add no sizing of their own (a flex here collapsed them).
          actions: playable ? <><Button focusId="play" icon="i-play" onPress={() => onPlay(false)}>{t('entry.play')}</Button>
            <Button focusId="shuffle" variant="glass" icon="i-shuffle" onPress={() => onPlay(true)}>{t('title.shuffle')}</Button></> : undefined}}
          toolbar={<BrowseToolbar testID="home-see-all-toolbar" model={toolbarModel} labels={browseLabels} resultCount={total && t('count.titles', {count: total.total})} resultTotal={total?.total}
            onFilters={onFilters} onSortChange={controller.setSort} onReshuffle={onReshuffle} view={{value: view, onValueChange: setView}}
            onQuickChange={(id, checked) => {
              const quick = toolbarModel.quick.find(value => value.id === id);
              if (!quick) return;
              const remaining = state.filters.filter(node => JSON.stringify(node) !== JSON.stringify(quick.query)); controller.setFilters(checked ? [...remaining, quick.query] : remaining);
            }}>{kindControls}</BrowseToolbar>}
          grid={grid()}/>
    </View>;
  }
  return <View testID="home-see-all" style={{flex: 1}}>
    {wash}
    <FocusRegion id={gridId} entry={playable ? 'play' : 'back'} enabled={active} style={{flex: 1}}>
      {header}{toolbar}
      {grid()}
    </FocusRegion>
  </View>;
}

/** The drawn phone list row for See all: same activation, play and menu as the poster. */
function HomeListRow({entry, runtime, width, onOpen, onPlay, onMenu, onFocus, onBlur}: {entry: ContentEntry; runtime: NativeShellRuntime; width: number;
  onOpen: () => void; onPlay?: () => void; onMenu?: (anchor: RefObject<View | null>) => void; onFocus: () => void; onBlur: () => void}) {
  const anchor = useRef<View>(null), theme = useTheme(), text = homeCardText(entry, false, runtime.text), title = text.title;
  const square = ['album', 'song', 'book', 'audiobook_file'].includes(entry.kind);
  const height = 70 * (theme.fontScale ?? 1);
  return <CardFrame testID={`home-see-all-list-${encodeURIComponent(entry.id)}`} title={title} caption={text.caption} detail={text.detail} width={width} onNavigate={onOpen} onPlay={onPlay}
    onMore={onMenu ? () => onMenu(anchor) : undefined} menuAnchorRef={anchor} onFocus={onFocus} onBlur={onBlur}
    pressableStyle={{height, borderRadius: 10, justifyContent: 'center'}}>
    {() => <View style={{height, flexDirection: 'row', alignItems: 'center', gap: 16, paddingHorizontal: 12}}>
      <MediaArt kind={square ? 'square' : 'poster'} source={runtime.artwork(entry.posterUrl)} width={square ? 54 : 36} height={54} style={{borderRadius: 5}}/>
      <View style={{flex: 1, minWidth: 0, gap: 3}}>
        <Text numberOfLines={1} style={[browseType(theme, 15, 600), {color: theme.colors.text}]}>{title}</Text>
        <Parts parts={text.caption} style={[browseType(theme, 12.5, 400), {color: theme.colors.textSecondary}]}/>
      </View>
      <View pointerEvents="none" style={{position: 'absolute', left: 12, right: 12, bottom: 0, height: 1, backgroundColor: theme.colors.line}}/>
    </View>}
  </CardFrame>;
}
