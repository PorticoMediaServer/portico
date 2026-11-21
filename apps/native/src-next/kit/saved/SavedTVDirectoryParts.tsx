import React from 'react';
import {ScrollView, Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {Library} from '../../../../../packages/client-core/src/index';
import type {PersonalResource} from '../../../../../packages/client-core/src/personal-saved';
import type {PlaylistCard, PlaylistListFilter} from '../../../../../packages/client-core/src/saved';
import {Button} from '../controls';
import {AmbientArt, CoverMosaic, MediaArt} from '../artwork';
import {CardFrame} from '../cards/primitives';
import {FocusItem, FocusRegion} from '../focus';
import {Icon} from '../Icon';
import {GuideProgrammeTitle} from '../live/GuideProgrammeTitle';
import {StateView} from '../states';
import {alpha} from '../browse/presentation';
import {textLine, useTheme} from '../theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import {tileId, type NativeSavedTVDirectoryProps, type useTVSavedDirectory} from '../../pages/shared/saved/useTVSavedDirectory';
import {SavedTVLoading} from './SavedTVLoading';
import {PlaylistToolbar} from './PlaylistToolbar';
import {savedItemCount, savedLibraryName, savedTitleCount, savedVisibility} from '../../screens/saved/presentation';

const t = defaultI18n.t;

export function SavedTVDirectoryBody({view, runtime, libraries, regionId, edges, pageRef, openPlaylist, openResource, model}:
  Pick<NativeSavedTVDirectoryProps, 'view' | 'runtime' | 'libraries' | 'regionId' | 'edges' | 'pageRef' | 'openPlaylist' | 'openResource'>
  & {model: ReturnType<typeof useTVSavedDirectory>}) {
  const theme = useTheme(), page = React.useRef<ScrollView>(null), offset = React.useRef(0);
  const [measuredColumns, setColumns] = React.useState(1);
  const attachPage = React.useCallback((host: ScrollView | null) => {if (page.current !== host) {page.current = host; offset.current = 0;}}, []);
  React.useImperativeHandle(pageRef, () => ({scrollTop: () => page.current?.scrollTo({y: 0, animated: false}), atTop: () => offset.current <= 0}), []);
  const {saved, playlists, resources, filters, sorts, error, loading, count, countText, gridId, toolbarId, moreId,
    createEnabled, interactive, createId, hasToolbar, hasMore, entries, firstId, start, openCreate} = model;
  const columns = view === 'views' ? {columnGap: 32, rowGap: 36} : {columnGap: 34, rowGap: 44};
  return <FocusRegion id={regionId} entryRegion={firstId ? gridId : hasToolbar ? toolbarId : undefined} enabled={interactive} edges={edges} style={{flex: 1}}>
    <FocusRegion id={toolbarId} entry="tv-saved-sort" orientation="horizontal" enabled={interactive && hasToolbar}
      edges={{...edges, down: gridId}} style={{height: 76, flexDirection: 'row', alignItems: 'center', gap: 14, paddingLeft: theme.gutter, paddingRight: theme.layout.gutter.tvRight}}>
      {view === 'playlists' && !!sorts.length && <PlaylistToolbar sorts={sorts} filters={filters} enabled={interactive} focusScope={toolbarId}
        value={{sort: saved?.projection?.query.sort ?? sorts[0]?.id ?? '',
          direction: saved?.projection?.query.direction === 'asc' ? 'asc' : 'desc',
          filter: (saved?.projection?.query.category ?? 'all') as PlaylistListFilter}}
        onSelect={model.choose}/>}
      <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, marginLeft: 'auto'}]}>{countText}</Text>
    </FocusRegion>
    {error && !loading ? <View style={{marginLeft: theme.gutter, marginRight: theme.layout.gutter.tvRight}}>
      <StateView variant="panel" {...runtime.error(error, model.retry)}/>
    </View> : loading ? <SavedTVLoading shape={view === 'views' ? 'views' : 'squares'} inset/>
      : !count && !createEnabled ? <View style={{marginLeft: theme.gutter, marginRight: theme.layout.gutter.tvRight}}>
        <StateView variant="panel" icon={view === 'playlists' ? 'i-queue' : view === 'collections' ? 'i-stack' : 'i-filter'}
          title={t(view === 'playlists' ? 'web.saved.empty.playlists' : view === 'collections' ? 'web.saved.empty.collections' : 'web.saved.empty.views')}
          body={t(view === 'playlists' ? 'web.saved.empty.playlistsBody' : view === 'collections' ? 'web.saved.empty.collectionsBody' : 'web.saved.empty.viewsBody')}/>
      </View>
      : <ScrollView key={view} ref={attachPage} onScroll={event => {if (model.scrollCurrent()) offset.current = event.nativeEvent.contentOffset.y;}} scrollEventThrottle={16} contentContainerStyle={{paddingLeft: theme.gutter, paddingRight: theme.layout.gutter.tvRight, paddingTop: theme.space.lg, paddingBottom: 40}}>
        <FocusRegion id={gridId} enabled={interactive} entry={firstId} grid={{columns: measuredColumns, count: entries.length + (createEnabled ? 1 : 0)}}
          edges={{...edges, ...(hasToolbar ? {up: toolbarId} : {}), ...(hasMore ? {down: moreId} : {})}}
          neighbors={{previous: hasToolbar ? toolbarId : edges?.up, ...(hasMore ? {next: moreId} : {})}} screenStart={start}
          onLayout={event => {const width = event.nativeEvent.layout.width, tileWidth = view === 'views' ? 520 : 236;
            if (width > 0) setColumns(Math.max(1, Math.floor((width + columns.columnGap) / (tileWidth + columns.columnGap))));}}
          style={{flexDirection: 'row', flexWrap: 'wrap', ...columns}}>
          {createEnabled && <FocusItem focusId={createId} cell={{index: 0}}><CardFrame testID={createId} focusId={createId} width={236}
            title={t(view === 'playlists' ? 'web.saved.newPlaylist' : 'web.saved.newCollection')} disabled={!interactive} onNavigate={openCreate}>
            {() => <View style={{width: 236}}><View style={{height: 236, borderRadius: 14, borderWidth: 2, borderStyle: 'dashed', borderColor: theme.colors.line,
              backgroundColor: theme.colors.raised, alignItems: 'center', justifyContent: 'center'}}><Icon name="i-plus" size={56} color={theme.colors.textSecondary}/></View>
              <Text style={[theme.type.rowLabel, {color: theme.colors.textSecondary, marginTop: 14}]}>{t(view === 'playlists' ? 'web.saved.newPlaylist' : 'web.saved.newCollection')}</Text></View>}
          </CardFrame></FocusItem>}
          {view === 'playlists' ? playlists.map((card, index) => <FocusItem key={card.id} focusId={tileId(view, card.id)} cell={{index: index + (createEnabled ? 1 : 0)}}><SavedTVPlaylistTile disabled={!interactive} card={card} runtime={runtime} onOpen={() => openPlaylist(card)}/></FocusItem>)
            : view === 'collections' ? resources.map((resource, index) => <FocusItem key={resource.id} focusId={tileId(view, resource.id)} cell={{index: index + (createEnabled ? 1 : 0)}}><SavedTVCollectionTile disabled={!interactive} resource={resource} runtime={runtime} onOpen={() => openResource(resource)}/></FocusItem>)
              : resources.map((resource, index) => <FocusItem key={resource.id} focusId={tileId(view, resource.id)} cell={{index: index + (createEnabled ? 1 : 0)}}><SavedTVViewTile disabled={!interactive} resource={resource} libraries={libraries} runtime={runtime} onOpen={() => openResource(resource)}/></FocusItem>)}
        </FocusRegion>
        {hasMore && <FocusRegion id={moreId} enabled={interactive} entry="tv-saved-more" edges={{...edges, up: gridId}}
          neighbors={{previous: gridId}} screenStart={start} style={{alignItems: 'center', paddingTop: 20}}>
          <Button focusId="tv-saved-more" variant="glass" disabled={!interactive} onPress={model.next}>{t('action.showMore')}</Button>
        </FocusRegion>}
      </ScrollView>}
  </FocusRegion>;
}

/** collections.html 15 puts a pinned playlist's bookmark on its mosaic and the day it changed
 * beside its count; a server that sends neither draws neither. */
function changedDay(value: string | undefined): string | undefined {
  if (!value) return undefined;
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return undefined;
  const now = new Date(), at = new Date(date.getFullYear(), date.getMonth(), date.getDate()), today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const days = Math.round((today.getTime() - at.getTime()) / 86400000);
  if (days === 0) return t('saved.today');
  if (days === 1) return t('relative.yesterday');
  return defaultI18n.date(date, 'dayMonth');
}

function SavedTVPlaylistTile({card, runtime, onOpen, disabled}: {card: PlaylistCard; runtime: NativeShellRuntime; onOpen: () => void; disabled?: boolean}) {
  const theme = useTheme(), covers = (card.artworkPaths ?? []).map(path => runtime.artwork(path)).filter((source): source is NonNullable<typeof source> => !!source), day = changedDay(card.lastChanged);
  return <CardFrame disabled={disabled} testID={tileId('playlists', card.id)} focusId={tileId('playlists', card.id)} title={card.title} width={236} onNavigate={onOpen}>
    {() => <View style={{width: 236}}>
      <View style={{width: 236, height: 236, borderRadius: 14, overflow: 'hidden', backgroundColor: theme.colors.raised}}>
        <CoverMosaic covers={covers} shape="square" width={236} height={236} visible/>
        {card.pinned === true && <View pointerEvents="none" accessibilityElementsHidden importantForAccessibility="no-hide-descendants" style={{position: 'absolute', left: 12, top: 12, width: 40, height: 40, borderRadius: 20,
          alignItems: 'center', justifyContent: 'center', backgroundColor: 'rgba(7,16,24,.8)'}}>
          <Icon name="i-bookmark-f" size={20} color={theme.colors.accentStrong}/>
        </View>}
      </View>
      <TVDirectoryTitle title={card.title}/>
      <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', gap: 8, marginTop: 3}}>
        {!!savedItemCount(card.count) && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{savedItemCount(card.count)}</Text>}
        {!!day && <><View style={{width: 4, height: 4, borderRadius: 2, backgroundColor: theme.colors.textTertiary}}/><Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{day}</Text></>}
      </View>
      {!!card.subtitle && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, marginTop: 3}]}>{card.subtitle}</Text>}
    </View>}
  </CardFrame>;
}

function SavedTVCollectionTile({resource, runtime, onOpen, disabled}: {resource: PersonalResource; runtime: NativeShellRuntime; onOpen: () => void; disabled?: boolean}) {
  const theme = useTheme(), covers = (resource.artworkPaths ?? []).map(path => runtime.artwork(path)).filter((source): source is NonNullable<typeof source> => !!source),
    visibility = savedVisibility(resource, true),
    // collections.html 16's tile says the shorter line; the page (20) keeps the fuller one.
    visibilityText = visibility.id === 'server' ? t('next.saved.visibleToEveryone') : visibility.text;
  return <CardFrame disabled={disabled} testID={tileId('collections', resource.id)} focusId={tileId('collections', resource.id)} title={resource.name} width={236} onNavigate={onOpen}>
    {() => <View style={{width: 236}}>
      <View style={{width: 236, height: 236, borderRadius: 14, overflow: 'hidden'}}>
        <CoverMosaic covers={covers} shape="square" width={236} height={236} visible/>
      </View>
      <TVDirectoryTitle title={resource.name}/>
      {!!savedTitleCount(resource.entryCount) && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, marginTop: 3}]}>{savedTitleCount(resource.entryCount)}</Text>}
      <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', gap: 8, marginTop: 3}}>
        <Icon name={visibility.icon} size={18} color={visibility.strong ? theme.colors.accentStrong : theme.colors.textTertiary}/>
        <Text numberOfLines={2} style={[visibility.strong ? theme.type.captionStrong : theme.type.rowHelp, {color: visibility.strong ? theme.colors.accentStrong : theme.colors.textTertiary, flexShrink: 1}]}>{visibilityText}</Text>
      </View>
    </View>}
  </CardFrame>;
}

function SavedTVViewTile({resource, libraries, runtime, onOpen, disabled}: {resource: PersonalResource; libraries: readonly Library[]; runtime: NativeShellRuntime; onOpen: () => void; disabled?: boolean}) {
  const theme = useTheme(), library = savedLibraryName(libraries, resource.definition?.libraryId), samples = (resource.samples ?? []).slice(0, 4),
    square = samples.length > 0 && ['album', 'song', 'book', 'audiobook_file'].includes(samples[0]!.kind);
  return <CardFrame disabled={disabled} testID={tileId('views', resource.id)} focusId={tileId('views', resource.id)} title={resource.name} width={520} onNavigate={onOpen}>
    {() => <View style={{width: 520, height: 300, borderRadius: 16, overflow: 'hidden', backgroundColor: theme.colors.raised}}>
      {!!samples[0] && <View pointerEvents="none" style={{position: 'absolute', inset: 0, opacity: .32}}><AmbientArt source={runtime.ambient(samples[0]!.backdropUrl ?? samples[0]!.posterUrl)} treatment="library"/></View>}
      <View style={{flex: 1, paddingHorizontal: 28, paddingVertical: 26}}>
        <View style={{flexDirection: 'row', alignItems: 'center', gap: 14}}>
          <View style={{width: 52, height: 52, borderRadius: 13, backgroundColor: alpha(theme.colors.text, .1), alignItems: 'center', justifyContent: 'center'}}>
            <Icon name="i-filter" size={28} color={theme.colors.text}/>
          </View>
          <GuideProgrammeTitle title={resource.name} lines={2} style={[theme.type.rowHeading, {color: theme.colors.text, flex: 1}]}/>
        </View>
        <View style={{flexDirection: 'row', alignItems: 'center', gap: 12, marginTop: 16}}>
          {!!library && <Text style={[theme.type.captionStrong, {color: theme.colors.accentStrong}]}>{library}</Text>}
          {/* collections.html 17 draws the On Home mark beside the library the view reads. */}
          {resource.onHome === true && <View style={{paddingVertical: 3, paddingHorizontal: 12, borderRadius: 8, borderWidth: 1.5, borderColor: alpha(theme.colors.text, .28)}}>
            <Text style={[theme.type.captionStrong, {color: theme.colors.text}]}>{t('next.saved.onHome')}</Text>
          </View>}
        </View>
        {!!resource.summary && <Text numberOfLines={2} style={[theme.type.rowHelp, {color: theme.colors.textSecondary, marginTop: 6}]}>{resource.summary}</Text>}
        <View style={{flexDirection: 'row', alignItems: 'flex-end', gap: 8, marginTop: 'auto'}}>
          {samples.map((entry, index) => <MediaArt key={entry.id} kind={square ? 'square' : 'poster'} source={runtime.artwork(entry.posterUrl)} width={square ? 84 : 56} height={square ? 84 : 84}
            style={{borderRadius: square ? 6 : 6}} visible priority={index === 0}/>)}
          <View style={{marginLeft: 'auto', flexDirection: 'row', alignItems: 'baseline', gap: 6}}>
            <Text style={[theme.type.captionStrong, {color: theme.colors.text}]}>{savedTitleCount(resource.entryCount)}</Text>
            {!!savedTitleCount(resource.entryCount) && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{t('next.saved.rightNow')}</Text>}
          </View>
        </View>
      </View>
    </View>}
  </CardFrame>;
}


function TVDirectoryTitle({title}: {title: string}) {
  const theme = useTheme();
  return <View style={{minHeight: 2 * textLine(theme.type.rowLabel), marginTop: theme.space.sm}}>
    <GuideProgrammeTitle title={title} lines={2} style={[theme.type.rowLabel, {color: theme.colors.text}]}/>
  </View>;
}
