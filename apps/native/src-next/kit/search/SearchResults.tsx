import React, {useRef, type Ref} from 'react';
import {FlatList, ScrollView, Text, View} from 'react-native';
import {frameSpec} from '@portico/design';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {SearchEntry, SearchGroup} from '../../../../../packages/client-core/src/search';
import {channelTint, programmeTimeRange} from '../../../../../packages/client-core/src/presentation/kit-model';
import {nowNext} from '../../../../../packages/client-core/src/guide/now-next';
import type {NativeShellRuntime} from '../../shell/runtime';
import {nativeSearchCardText} from '../../screens/home/model';
import {nativeSearchEntryKey} from '../../screens/search/model';
import {SearchGridCard} from '../../screens/search/SearchGridCard';
import type {NativeSearchActions, NativeSearchGenre, NativeSearchSelectionPort} from '../../screens/search/types';
import type {NativeSearchOnNowSnapshot} from '../../screens/search/on-now';
import {Artwork} from '../artwork';
import {GenreTile, LiveCard, type CardSelection} from '../cards';
import {CardFrame, Parts} from '../cards/primitives';
import {Button} from '../controls';
import {useFrameMetrics} from '../frames/metrics';
import {TVRow} from '../layout/TVRow';
import {SelectionCircle} from '../selection';
import {GridSkeleton, LoadingPaint, SkeletonBlock, StateView} from '../states';
import {useTheme} from '../theme';

export function SearchSection({title, detail, action, children}: {
  title: string; detail?: string; action?: {label: string; onPress: () => void}; children: React.ReactNode;
}) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <View style={{gap: frame.rowHeadingGap}}>
    <View style={{marginHorizontal: frame.gutter, flexDirection: 'row', flexWrap: 'wrap', alignItems: 'baseline', gap: theme.space.xs}}>
      <Text accessibilityRole="header" style={[theme.type.rowHeading, {color: theme.colors.text}]}>{title}</Text>
      {detail && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{detail}</Text>}
      <View style={{flex: 1}}/>
      {action && <Button variant="text" size="small" onPress={action.onPress}>{action.label}</Button>}
    </View>
    {children}
  </View>;
}

export function SearchGenres({genres, backdrop, runtime, open}: {
  genres: readonly NativeSearchGenre[]; backdrop: boolean; runtime: NativeShellRuntime; open: (genre: NativeSearchGenre) => void;
}) {
  const theme = useTheme(), frame = useFrameMetrics(), gap = theme.space.xs, width = (frame.width - 2 * frame.gutter - gap) / 2;
  return <View style={{marginHorizontal: frame.gutter, gap, flexDirection: 'row', flexWrap: 'wrap'}}>
    {genres.map(genre => {
      const tint = channelTint(genre.id), source = backdrop ? runtime.artwork(genre.backdropPath) : undefined;
      return <GenreTile key={genre.id} testID={`search-genre-${encodeURIComponent(genre.id)}`} title={genre.name}
        caption={backdrop ? [{id: 'count', text: runtime.text.t('next.selection.titles', {count: genre.count}), role: 'context'}] : undefined}
        variant="video" width={width} backdrop={source} palette={{first: tint, second: tint}}
        covers={backdrop ? [] : genre.coverPaths.slice(0, 2).flatMap(path => {
          const cover = runtime.artwork(path); return cover ? [cover] : [];
        })}
        onNavigate={() => open(genre)}/>;
    })}
  </View>;
}

export function SearchOnNow({snapshot, runtime, now, onGuide}: {
  snapshot: NativeSearchOnNowSnapshot; runtime: NativeShellRuntime; now: number; onGuide: () => void;
}) {
  const theme = useTheme(), frame = useFrameMetrics(), t = runtime.text.t;
  const width = Math.min(frameSpec.search.onNowCardWidth, frame.width - 2 * frame.gutter);
  return <SearchSection title={t('next.home.onNow')} action={{label: t('next.controls.guide'), onPress: onGuide}}>
    <TVRow><ScrollView testID="search-on-now" horizontal showsHorizontalScrollIndicator={false}
      contentContainerStyle={{paddingHorizontal: frame.gutter, gap: theme.space.sm}}>
      {snapshot.cards.map(({channel, programmes}) => {
        const current = nowNext(programmes, now), programme = current.now;
        const range = programme && programmeTimeRange(programme.start, programme.end, runtime.text);
        return <LiveCard key={`${channel.sourceId}:${channel.id}`} testID={`search-on-now-${encodeURIComponent(channel.id)}`}
          title={programme?.title ?? t('guide.noInformation')} channel={channel.name} progress={programme ? current.progress : undefined}
          mark={{id: channel.id, sourceId: channel.sourceId, text: channel.mark?.text ?? '', seed: channel.mark?.seed ?? `${channel.sourceId}:${channel.id}`}}
          logo={runtime.artwork(channel.logoUrl)} width={width} visible
          flags={programme?.flags?.live ? [{id: 'live', text: t('next.kit.live'), role: 'status'}] : []}
          range={range ? t('guide.ends', {time: range.end}) : undefined} primaryAction="navigate" onNavigate={onGuide}/>;
      })}
    </ScrollView></TVRow>
  </SearchSection>;
}

export function SearchResultRow({entry, group, runtime, actions, selection}: {
  entry: SearchEntry; group: SearchGroup; runtime: NativeShellRuntime; actions: NativeSearchActions; selection?: CardSelection;
}) {
  const theme = useTheme(), frame = useFrameMetrics(), t = runtime.text.t, anchor = useRef<View>(null);
  const content = entry.kind !== 'person' && entry.kind !== 'channel' ? entry as ContentEntry : undefined;
  const text = content ? nativeSearchCardText(content, runtime.text) : undefined;
  const shape = group === 'movies' || group === 'shows' ? 'poster' : group === 'episodes' || group === 'live-tv' ? 'wide' : 'square';
  const size = frameSpec.search.resultRowArt[shape], title = content?.displayTitle || entry.title;
  const key = nativeSearchEntryKey(group, entry), menu = actions.canMenu?.(entry, group) === false ? undefined : () => actions.menu(entry, group, anchor);
  return <View style={{marginHorizontal: frame.gutter, flexDirection: 'row', alignItems: 'center', gap: theme.space.xs, paddingVertical: theme.space.tight}}>
    <CardFrame testID={`search-result-${key}`} focusId={key} title={title} selection={selection} menuAnchorRef={anchor}
      moreExpanded={actions.isMenuOpen?.(entry, group)} onNavigate={() => actions.open(entry, group)} onMore={menu} style={{flex: 1}}
      pressableStyle={{flexDirection: 'row', gap: theme.space.sm, alignItems: 'center'}}>
      {() => <>
        {selection?.active && <SelectionCircle selected={selection.selected} selecting interactive={false} variant="row"
          accessibilityLabel={t('card.select', {title})}/>}
        <View style={{width: size.width, height: size.height, borderRadius: theme.radii.artwork, overflow: 'hidden', backgroundColor: theme.colors.raised}}>
          <Artwork source={runtime.artwork(group === 'episodes' ? content?.stillUrl ?? content?.backdropUrl ?? entry.posterUrl : entry.posterUrl)}
            width={size.width} height={size.height}/>
        </View>
        <View style={{flex: 1, minWidth: 0, gap: theme.space.hairline}}>
          <Text style={[theme.type.listLabel, {color: selection?.selected ? theme.colors.accentStrong : theme.colors.text}]}>{title}</Text>
          <Parts parts={text?.caption} fit="wrap" gap={theme.space.xs} style={theme.type.rowHelp}/>
        </View>
      </>}
    </CardFrame>
    {!selection?.active && menu && <Button testID={`search-more-${key}`} variant="text" size="small" icon="i-more" iconOnly align="center" iconColor={theme.colors.textSecondary}
      accessibilityLabel={t('card.more', {title})} onPress={menu}/>}
  </View>;
}

export function SearchResultsGrid({entries, group, runtime, actions, selection, scrollRef, footer, onMore}: {
  entries: readonly SearchEntry[]; group: SearchGroup; runtime: NativeShellRuntime; actions: NativeSearchActions; selection?: NativeSearchSelectionPort;
  scrollRef: Ref<FlatList<SearchEntry>>; footer?: React.ReactElement; onMore?: () => void;
}) {
  const frame = useFrameMetrics(), theme = useTheme(), gap = theme.space.sm, columns = 3;
  const width = (frame.width - 2 * frame.gutter - (columns - 1) * gap) / columns;
  return <FlatList ref={scrollRef} key={group} data={entries} numColumns={columns} testID={`search-grid-${group}`}
    columnWrapperStyle={{gap}} contentContainerStyle={{paddingHorizontal: frame.gutter, paddingTop: frame.sectionGap / 2, gap, paddingBottom: frame.bottomClear}}
    keyboardDismissMode="on-drag" keyboardShouldPersistTaps="handled" keyExtractor={entry => nativeSearchEntryKey(group, entry)}
    onEndReached={onMore} onEndReachedThreshold={.5} ListFooterComponent={footer}
    renderItem={({item}) => <SearchGridCard entry={item} group={group} runtime={runtime} actions={actions}
      selection={selection?.item(item, group)} width={width} titleLines={0}/>}/>;
}

export function SearchLoading({elapsed}: {elapsed: number}) {
  const frame = useFrameMetrics();
  return <GridSkeleton elapsedMs={elapsed} scrollOffset={0} viewportHeight={frame.width} width={frame.width} columns={3} totalCount={9}/>;
}

export function SearchResultShelf({entries, group, title, count, runtime, actions, selection, loading, failure, onMore}: {
  entries: readonly SearchEntry[]; group: SearchGroup; title: string; count?: string; runtime: NativeShellRuntime; actions: NativeSearchActions;
  selection?: NativeSearchSelectionPort; loading?: boolean; failure?: React.ComponentProps<typeof StateView>; onMore?: () => void;
}) {
  const frame = useFrameMetrics(), theme = useTheme(), columns = 7, gap = theme.space.sm;
  const width = Math.max(1, Math.floor((frame.width - frame.gutter - frame.rightGutter - (columns - 1) * gap) / columns));
  const footer = failure ? <View style={{width: width * 2, paddingLeft: gap}}><StateView {...failure}/></View> : loading
    ? <LoadingPaint ready={false} style={{marginLeft: gap}} skeleton={<SkeletonBlock width={width} height={group === 'movies' || group === 'shows' ? 1.5 * width : width}/>}/> : null;
  return <SearchSection title={title} detail={count}><TVRow><FlatList horizontal testID={`search-shelf-${group}`} data={entries}
    contentContainerStyle={{paddingLeft: frame.gutter, paddingRight: frame.rightGutter, gap}}
    keyboardShouldPersistTaps="handled" showsHorizontalScrollIndicator={false}
    keyExtractor={entry => nativeSearchEntryKey(group, entry)} initialNumToRender={columns}
    onEndReached={onMore} onEndReachedThreshold={1} ListFooterComponent={footer}
    renderItem={({item}) => <SearchGridCard entry={item} group={group} runtime={runtime} actions={actions}
      selection={selection?.item(item, group)} width={width} titleLines={0}/>}/></TVRow></SearchSection>;
}
