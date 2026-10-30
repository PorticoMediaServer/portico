import React from 'react';
import {ScrollView, Text, View} from 'react-native';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {Library} from '../../../../../packages/client-core/src/index';
import type {PersonalResource} from '../../../../../packages/client-core/src/personal-saved';
import type {PlaylistCard, PlaylistListFilter} from '../../../../../packages/client-core/src/saved';
import {Focusable} from '../../kit/controls';
import {AmbientArt, CoverMosaic, MediaArt} from '../../kit/artwork';
import {CardFrame} from '../../kit/cards/primitives';
import {FocusItem, FocusRegion, useFocusRegistry, type FocusEdges} from '../../kit/focus';
import {Icon} from '../../kit/Icon';
import {StateView} from '../../kit/states';
import {alpha, browseType} from '../../kit/browse/presentation';
import {useTheme} from '../../kit/theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeSavedViewDestination} from './view-model';
import type {NativeDestinationActivationPort} from '../destinations/focus';
import {SavedNameSheet} from './SavedForms';
import {SavedTVLoading} from './SavedTVLoading';
import {PlaylistToolbar} from './PlaylistToolbar';
import {savedCollectionCount, savedItemCount, savedLibraryName, savedTitleCount, savedViewCount, savedVisibility} from './presentation';

const t = defaultI18n.t;

export type SavedTVDirectoryHandle = Readonly<{scrollTop: () => void; atTop: () => boolean}>;
export type NativeSavedTVDirectoryView = 'playlists' | 'collections' | 'views';

/** The TV landing's three directory bodies (collections.html 15–17). The TV
 * landing owner renders SavedTVTabs above this; it registers no route and owns
 * no destination. */
export type NativeSavedTVDirectoryProps = Readonly<{
  view: NativeSavedTVDirectoryView;
  destination: NativeSavedViewDestination;
  runtime: NativeShellRuntime;
  libraries: readonly Library[];
  enabled: boolean;
  activation: NativeDestinationActivationPort;
  regionId: string;
  edges?: FocusEdges;
  pageRef?: React.Ref<SavedTVDirectoryHandle>;
  select: (route: Readonly<{view: NativeSavedTVDirectoryView; sort?: string; direction?: 'asc' | 'desc'; filter?: PlaylistListFilter}>) => void;
  openPlaylist: (card: PlaylistCard) => void;
  openResource: (resource: PersonalResource) => void;
}>;

export function SavedTVDirectory({view, destination, runtime, libraries, enabled, activation, regionId, edges, pageRef, select, openPlaylist, openResource}: NativeSavedTVDirectoryProps) {
  const theme = useTheme(), registry = useFocusRegistry();
  const [creating, setCreating] = React.useState(false), [busy, setBusy] = React.useState(false), [problem, setProblem] = React.useState<string>();
  const [draft, setDraft] = React.useState<{name: string; summary: string; visibility: 'private' | 'server'}>();
  const mounted = React.useRef(true), flight = React.useRef<object | undefined>(undefined), creation = React.useRef<{scope: object; revision: number} | undefined>(undefined);
  const page = React.useRef<ScrollView>(null), offset = React.useRef(0);
  const [measuredColumns, setColumns] = React.useState(1);
  const attachPage = React.useCallback((host: ScrollView | null) => {if (page.current !== host) {page.current = host; offset.current = 0;}}, []);
  const scope = React.useMemo(() => ({view, destination}), [view, destination]), currentScope = React.useRef(scope); currentScope.current = scope;
  React.useImperativeHandle(pageRef, () => ({scrollTop: () => page.current?.scrollTo({y: 0, animated: false}), atTop: () => offset.current <= 0}), []);
  const snapshot = React.useSyncExternalStore(destination.subscribe, destination.getSnapshot);
  const saved = snapshot.saved, personal = snapshot.personal;
  const playlists = view === 'playlists' ? (saved?.projection?.sections ?? []).flatMap(section => section.entries.filter((entry): entry is PlaylistCard => entry.kind === 'playlist')) : [];
  const resources = view === 'playlists' ? [] : personal?.resources ?? [];
  const filters = view === 'playlists' ? saved?.projection?.filters ?? [] : [];
  const sorts = view === 'playlists' ? saved?.projection?.sorts ?? [] : [];
  const error = snapshot.error ?? saved?.error ?? personal?.error, loading = snapshot.phase === 'loading' && !saved?.projection && !personal?.resources.length;
  const count = view === 'playlists' ? (saved?.projection?.sections[0]?.totalCount ?? playlists.length) : resources.length;
  const countText = view === 'playlists' ? t('next.saved.playlistCount', {count}) : view === 'collections' ? savedCollectionCount(count) : savedViewCount(count);
  // collections.html 15-17: the page starts at 200 and ends 96 from the right, so six 236 tiles
  // (34 apart) or three 520 saved-view tiles (32 apart) fit the frame's row exactly.
  const columns = view === 'views' ? {columnGap: 32, rowGap: 36} : {columnGap: 34, rowGap: 44};
  const gridId = `${regionId}.cards`, toolbarId = `${regionId}.toolbar`, moreId = `${regionId}.more`;
  const createEnabled = enabled && snapshot.phase === 'ready' && !error && !loading && (view === 'playlists' ? saved?.projection?.actions?.includes('create_playlist') === true : view === 'collections');
  const interactive = enabled && !creating;
  const createId = `saved-tv-new-${view}`;
  const restoreTarget = React.useRef<{scope: object; revision: number; childId: string} | undefined>(undefined);
  const returnFocusRef = React.useMemo(() => ({current: {requestTVFocus: () => {const target = restoreTarget.current;
    if (target && mounted.current && currentScope.current === target.scope && activation.getSnapshot().visible && activation.getSnapshot().revision === target.revision)
      registry.focus({regionId: gridId, childId: target.childId});}}}), [activation, gridId, registry]);
  const hasToolbar = view === 'playlists' && !!sorts.length;
  const hasMore = !!(view === 'playlists' ? saved?.pagination.next.length : personal?.nextCursor);
  const entries = view === 'playlists' ? playlists : resources;
  const firstId = createEnabled ? createId : entries[0] ? tileId(view, entries[0].id) : undefined;
  const start = edges?.up ? {regionId: edges.up, childId: view} : firstId ? {regionId: gridId, childId: firstId} : undefined;
  const current = React.useCallback((target: NonNullable<typeof creation.current>) => mounted.current && currentScope.current === target.scope
    && activation.getSnapshot().visible && activation.getSnapshot().revision === target.revision, [activation]);
  React.useLayoutEffect(() => {mounted.current = true; return () => {mounted.current = false; creation.current = undefined; flight.current = undefined;};}, []);
  React.useLayoutEffect(() => {
    const retire = () => {const target = creation.current; if (target && !current(target)) {creation.current = undefined; flight.current = undefined; setCreating(false); setBusy(false);}};
    retire(); return activation.subscribe(retire);
  }, [activation, scope, current]);
  const renderedCreation = creation.current, renderedActivation = activation.getSnapshot();
  const openCreate = () => {
    if (!createEnabled || currentScope.current !== scope || !activation.getSnapshot().visible
      || activation.getSnapshot().revision !== renderedActivation.revision || flight.current) return;
    creation.current = {scope, revision: activation.getSnapshot().revision}; restoreTarget.current = {...creation.current, childId: createId}; setCreating(true);
    if (!personal?.retryPending) {setDraft(undefined); setProblem(undefined);}
  };
  const closeCreate = () => {if (!flight.current && creation.current === renderedCreation) {creation.current = undefined; setCreating(false);}};
  const create = (input: {name: string; summary: string; visibility: 'private' | 'server'}) => {
    const target = renderedCreation;
    if (!target || creation.current !== target || !current(target) || flight.current) return;
    const live = destination.getSnapshot();
    if (live.error ?? live.saved?.error ?? live.personal?.error) return;
    if (view === 'playlists' && !live.saved?.projection?.actions?.includes('create_playlist')) return;
    const ticket = {}; flight.current = ticket; setBusy(true); setProblem(undefined); setDraft(input);
    const held = () => flight.current === ticket && creation.current === target && current(target);
    const work = async () => {
      if (view === 'playlists') await destination.mutateSaved({action: 'create', name: input.name, ...(input.summary ? {summary: input.summary} : {})});
      else {
        const ok = live.personal?.retryPending ? await destination.retryPersonalMutation()
          : await destination.mutatePersonal({action: 'create', kind: 'collection', name: input.name, summary: input.summary, visibility: input.visibility});
        if (!ok) throw destination.getSnapshot().personal?.mutationError ?? Object.assign(new Error('unavailable'), {code: 'unavailable'});
      }
      if (!held()) return;
      const updated = destination.getSnapshot(), newId = view === 'playlists' ? updated.saved?.lastReceipt?.playlistId : updated.personal?.result?.resourceId;
      creation.current = undefined; setCreating(false); setDraft(undefined);
      // The server's directory sort/filter may keep a new item off this page. Never guess an ID.
      const arrived = view === 'playlists' ? updated.saved?.projection?.sections.some(section => section.entries.some(entry => entry.kind === 'playlist' && entry.id === newId))
        : updated.personal?.resources.some(resource => resource.id === newId);
      restoreTarget.current = {...target, childId: arrived && newId ? tileId(view, newId) : createId};
    };
    void work().catch(failure => {if (held()) setProblem(presentError(failure, 'saved', {operation: 'save'}).body);})
      .finally(() => {if (flight.current === ticket) {flight.current = undefined; if (current(target)) setBusy(false);}});
  };
  return <><FocusRegion id={regionId} entryRegion={firstId ? gridId : hasToolbar ? toolbarId : undefined} enabled={interactive} edges={edges} style={{flex: 1}}>
    <FocusRegion id={toolbarId} entry="tv-saved-sort" orientation="horizontal" enabled={interactive && hasToolbar}
      edges={{...edges, down: gridId}} style={{height: 76, flexDirection: 'row', alignItems: 'center', gap: 14, paddingLeft: theme.gutter, paddingRight: theme.layout.gutter.tvRight}}>
      {view === 'playlists' && !!sorts.length && <PlaylistToolbar sorts={sorts} filters={filters} enabled={interactive} focusScope={toolbarId}
        value={{sort: saved?.projection?.query.sort ?? sorts[0]?.id ?? '',
          direction: saved?.projection?.query.direction === 'asc' ? 'asc' : 'desc',
          filter: (saved?.projection?.query.category ?? 'all') as PlaylistListFilter}}
        onSelect={next => select({view: 'playlists', ...(next.sort !== undefined ? {sort: next.sort} : {}),
          ...(next.direction !== undefined ? {direction: next.direction} : {}), ...(next.filter !== undefined ? {filter: next.filter} : {})})}/>}
      <Text style={[browseType(theme, 22), {color: theme.colors.textTertiary, marginLeft: 'auto'}]}>{countText}</Text>
    </FocusRegion>
    {error && !loading ? <StateView variant="panel" {...runtime.error(error, () => {void destination.retry().catch(runtime.reportError);})}/>
      : loading ? <SavedTVLoading shape={view === 'views' ? 'views' : 'squares'} inset/>
      : !count && !createEnabled ? <StateView variant="panel" icon={view === 'playlists' ? 'i-queue' : view === 'collections' ? 'i-stack' : 'i-filter'}
        title={t(view === 'playlists' ? 'web.saved.empty.playlists' : view === 'collections' ? 'web.saved.empty.collections' : 'web.saved.empty.views')}
        body={t(view === 'playlists' ? 'web.saved.empty.playlistsBody' : view === 'collections' ? 'web.saved.empty.collectionsBody' : 'web.saved.empty.viewsBody')}/>
      : <ScrollView key={view} ref={attachPage} onScroll={event => {if (currentScope.current === scope) offset.current = event.nativeEvent.contentOffset.y;}} scrollEventThrottle={16} contentContainerStyle={{paddingLeft: theme.gutter, paddingRight: theme.layout.gutter.tvRight, paddingBottom: 40}}>
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
              <Text style={[browseType(theme, 24, 700), {color: theme.colors.textSecondary, marginTop: 14}]}>{t(view === 'playlists' ? 'web.saved.newPlaylist' : 'web.saved.newCollection')}</Text></View>}
          </CardFrame></FocusItem>}
          {view === 'playlists' ? playlists.map((card, index) => <FocusItem key={card.id} focusId={tileId(view, card.id)} cell={{index: index + (createEnabled ? 1 : 0)}}><SavedTVPlaylistTile disabled={!interactive} card={card} runtime={runtime} onOpen={() => openPlaylist(card)}/></FocusItem>)
            : view === 'collections' ? resources.map((resource, index) => <FocusItem key={resource.id} focusId={tileId(view, resource.id)} cell={{index: index + (createEnabled ? 1 : 0)}}><SavedTVCollectionTile disabled={!interactive} resource={resource} runtime={runtime} onOpen={() => openResource(resource)}/></FocusItem>)
              : resources.map((resource, index) => <FocusItem key={resource.id} focusId={tileId(view, resource.id)} cell={{index: index + (createEnabled ? 1 : 0)}}><SavedTVViewTile disabled={!interactive} resource={resource} libraries={libraries} runtime={runtime} onOpen={() => openResource(resource)}/></FocusItem>)}
        </FocusRegion>
        {hasMore && <FocusRegion id={moreId} enabled={interactive} entry="tv-saved-more" edges={{...edges, up: gridId}}
          neighbors={{previous: gridId}} screenStart={start} style={{alignItems: 'center', paddingTop: 20}}>
          <Focusable accessibilityRole="button" focusId="tv-saved-more" disabled={!interactive} onPress={() => {void destination.next(view === 'playlists' ? saved?.pagination.next[0]?.sectionId : undefined).catch(runtime.reportError);}}
            style={{height: 64, paddingHorizontal: 26, borderRadius: 14, backgroundColor: theme.colors.raised, justifyContent: 'center'}}>
            <Text style={[browseType(theme, 22, 700), {color: theme.colors.text}]}>{t('action.showMore')}</Text>
          </Focusable>
        </FocusRegion>}
      </ScrollView>}
  </FocusRegion>
    <SavedNameSheet open={creating} title={t(view === 'playlists' ? 'web.saved.newPlaylist' : 'web.saved.newCollection')}
      action={t(personal?.retryPending ? 'action.tryAgain' : 'web.saved.create')} withSummary withVisibility={view === 'collections'} busy={busy}
      locked={personal?.retryPending} initialName={draft?.name} initialSummary={draft?.summary} initialVisibility={draft?.visibility}
      error={problem} returnFocusRef={returnFocusRef} testID={`${regionId}.create`} onClose={closeCreate} onSave={create}/>
  </>;
}

function tileId(view: NativeSavedTVDirectoryView, id: string) {return `saved-tv-${view}-${id}`;}

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
      <Text numberOfLines={1} style={[browseType(theme, 24, 700), {color: theme.colors.textSecondary, marginTop: 14}]}>{card.title}</Text>
      <View style={{flexDirection: 'row', alignItems: 'center', gap: 8, marginTop: 3}}>
        {!!savedItemCount(card.count) && <Text numberOfLines={1} style={[browseType(theme, 20), {color: theme.colors.textTertiary}]}>{savedItemCount(card.count)}</Text>}
        {!!day && <><View style={{width: 4, height: 4, borderRadius: 2, backgroundColor: theme.colors.textTertiary}}/><Text numberOfLines={1} style={[browseType(theme, 20), {color: theme.colors.textTertiary}]}>{day}</Text></>}
      </View>
      {!!card.subtitle && <Text numberOfLines={1} style={[browseType(theme, 20), {color: theme.colors.textTertiary, marginTop: 3}]}>{card.subtitle}</Text>}
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
      <Text numberOfLines={1} style={[browseType(theme, 24, 700), {color: theme.colors.textSecondary, marginTop: 14}]}>{resource.name}</Text>
      {!!savedTitleCount(resource.entryCount) && <Text numberOfLines={1} style={[browseType(theme, 20), {color: theme.colors.textTertiary, marginTop: 3}]}>{savedTitleCount(resource.entryCount)}</Text>}
      <View style={{flexDirection: 'row', alignItems: 'center', gap: 8, marginTop: 3}}>
        <Icon name={visibility.icon} size={18} color={visibility.strong ? theme.colors.accentStrong : theme.colors.textTertiary}/>
        <Text numberOfLines={2} style={[browseType(theme, 20, visibility.strong ? 700 : 600), {color: visibility.strong ? theme.colors.accentStrong : theme.colors.textTertiary, flexShrink: 1}]}>{visibilityText}</Text>
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
          <Text numberOfLines={2} style={[browseType(theme, 32, 800, -.025, 35.2), {color: theme.colors.text, flex: 1}]}>{resource.name}</Text>
        </View>
        <View style={{flexDirection: 'row', alignItems: 'center', gap: 12, marginTop: 16}}>
          {!!library && <Text style={[browseType(theme, 20, 700), {color: theme.colors.accentStrong}]}>{library}</Text>}
          {/* collections.html 17 draws the On Home mark beside the library the view reads. */}
          {resource.onHome === true && <View style={{paddingVertical: 3, paddingHorizontal: 12, borderRadius: 8, borderWidth: 1.5, borderColor: alpha(theme.colors.text, .28)}}>
            <Text style={[browseType(theme, 20, 600), {color: theme.colors.text}]}>{t('next.saved.onHome')}</Text>
          </View>}
        </View>
        {!!resource.summary && <Text numberOfLines={2} style={[browseType(theme, 22, 400, 0, 30.8), {color: theme.colors.textSecondary, marginTop: 6}]}>{resource.summary}</Text>}
        <View style={{flexDirection: 'row', alignItems: 'flex-end', gap: 8, marginTop: 'auto'}}>
          {samples.map((entry, index) => <MediaArt key={entry.id} kind={square ? 'square' : 'poster'} source={runtime.artwork(entry.posterUrl)} width={square ? 84 : 56} height={square ? 84 : 84}
            style={{borderRadius: square ? 6 : 6}} visible priority={index === 0}/>)}
          <View style={{marginLeft: 'auto', flexDirection: 'row', alignItems: 'baseline', gap: 6}}>
            <Text style={[browseType(theme, 22, 700), {color: theme.colors.text}]}>{savedTitleCount(resource.entryCount)}</Text>
            {!!savedTitleCount(resource.entryCount) && <Text style={[browseType(theme, 20, 500), {color: theme.colors.textTertiary}]}>{t('next.saved.rightNow')}</Text>}
          </View>
        </View>
      </View>
    </View>}
  </CardFrame>;
}

