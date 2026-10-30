import React, {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore, type RefObject} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n, type MessageId} from '../../../../../packages/i18n/src';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';
import type {Library} from '../../../../../packages/client-core/src/index';
import type {PersonalResource} from '../../../../../packages/client-core/src/personal-saved';
import type {PlaylistCard, PlaylistListFilter, SavedRoute} from '../../../../../packages/client-core/src/saved';
import {Chip, Focusable} from '../../kit/controls';
import {Artwork, type ArtworkSource, StandardBackdrop} from '../../kit/artwork';

import {Icon, type IconName} from '../../kit/Icon';
import {Banner, Menu, type MenuEntry} from '../../kit/overlays';
import {StateView} from '../../kit/states';
import {browseType, alpha} from '../../kit/browse/presentation';
import {useTheme} from '../../kit/theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeEntryMenuState} from '../destinations/entry-actions';
import type {NativeDestinationActivationPort} from '../destinations/focus';
import type {NativeSavedViewDestination} from './view-model';
import {SavedConfirmSheet, SavedNameSheet} from './SavedForms';
import {SavedSortMenu} from './SavedSortMenu';
import {SavedLoading, SavedTail} from './SavedLoading';
import {savedNearEnd} from './paging';
import {savedItemCount, savedLibraryName, personalMosaic, savedTitleCount, savedViewCount, savedVisibility} from './presentation';
import type {NativeSavedViewSummaries} from './state';
import type {NativeSavedDirectoryCommands, SavedDirectoryCommandLease} from './directory-commands';

const noSummarySubscribe = (_listener: () => void) => () => {};
const noSummaryRevision = () => 0;
const t = defaultI18n.t;

type DirectoryView = 'playlists' | 'collections' | 'views';
export type NativeSavedDirectoryRoute = Readonly<{view: DirectoryView; sort?: string; direction?: 'asc' | 'desc'; filter?: PlaylistListFilter; shape?: 'posters' | 'squares'}>;

type DirectoryTarget = Readonly<{resource: PersonalResource; lease: SavedDirectoryCommandLease}>;

export type NativeSavedDirectoryProps = Readonly<{
  route: NativeSavedDirectoryRoute;
  destination: NativeSavedViewDestination;
  activation: NativeDestinationActivationPort;
  runtime: NativeShellRuntime;
  actions: Readonly<{menuState?: NativeEntryMenuState}>;
  libraries: readonly Library[];
  back: () => void;
  open: (route: SavedRoute) => void;
  changed?: () => void;
  /** The Saved views directory's real condition summaries, when its owner is live. */
  summaries?: NativeSavedViewSummaries;
  /** Real pin/rename/delete over the same lifetime, for the directory tiles. */
  commands?: NativeSavedDirectoryCommands;
  /** The exact host route is still current, from the owning Saved route host. */
  routeCurrent?: () => boolean;
  inline?: boolean;
}>;

/** collections.html 10a–10c. Each directory is the server's own bounded page;
 * the + sends the real create command and the rows open the pushed pages. */
export function SavedDirectoryScreen({route, destination, activation, runtime, libraries, back, open, changed, summaries, commands, routeCurrent, inline}: NativeSavedDirectoryProps) {
  const theme = useTheme(), insets = useSafeAreaInsets();
  useSyncExternalStore(summaries?.subscribe ?? noSummarySubscribe, summaries?.getSnapshot ?? noSummaryRevision);
  const active = useSyncExternalStore(activation.subscribe, activation.getSnapshot);
  const snapshot = useSyncExternalStore(destination.subscribe, destination.getSnapshot);
  const [creating, setCreating] = useState(false), [busy, setBusy] = useState(false), [problem, setProblem] = useState<string>();
  const [menu, setMenu] = useState<{target: DirectoryTarget; anchor: RefObject<View | null>; opening: object}>(), [renaming, setRenaming] = useState<DirectoryTarget>(),
    [removing, setRemoving] = useState<DirectoryTarget>(), [commandBusy, setCommandBusy] = useState(false);
  const [paneWidth, setPaneWidth] = useState(0), viewport = useRef({offset: 0, height: 0, extent: 0});
  const commandTicket = useRef<Readonly<{ticket: object; lease: SavedDirectoryCommandLease}> | undefined>(undefined);
  const mounted = useRef(false);
  const renameOpening = useRef<DirectoryTarget | undefined>(undefined), removeOpening = useRef<DirectoryTarget | undefined>(undefined), menuOpening = useRef<object | undefined>(undefined);
  useEffect(() => {void destination.select(route).catch(runtime.reportError);}, [destination, route, runtime]);
  const saved = snapshot.saved, personal = snapshot.personal;
  const error = snapshot.error ?? saved?.error ?? personal?.error;
  const loading = (snapshot.phase === 'idle' || snapshot.phase === 'loading') && !saved?.projection && !personal?.resources.length;
  // A refused or unreadable directory retires the tile menu and its command sheets at
  // layout, so a retained callback from that projection has no opening to dispatch.
  useLayoutEffect(() => {
    if (!error) return;
    menuOpening.current = undefined; setMenu(undefined);
    renameOpening.current = undefined; setRenaming(undefined);
    removeOpening.current = undefined; setRemoving(undefined);
  }, [error]);
  const playlists = route.view === 'playlists' ? (saved?.projection?.sections ?? []).flatMap(section => section.entries.filter((entry): entry is PlaylistCard => entry.kind === 'playlist')) : [];
  const resources = route.view === 'playlists' ? [] : personal?.resources ?? [];
  const title = t(route.view === 'playlists' ? 'saved.tab.playlists' : route.view === 'collections' ? 'saved.tab.collections' : 'saved.tab.savedViews');
  const summaryText = {t: runtime.text.t, has: defaultI18n.has, locale: runtime.text.region?.uiLocale ?? defaultI18n.locale};
  // A refused or unreadable directory never advertises create or its query commands.
  const createEnabled = !error && (route.view === 'playlists' ? saved?.projection?.actions?.includes('create_playlist') === true : route.view === 'collections');
  const filters = route.view === 'playlists' ? saved?.projection?.filters ?? [] : [];
  const sorts = route.view === 'playlists' ? saved?.projection?.sorts ?? [] : [];
  const tablet = theme.platform !== 'phone';
  const tileColumns = paneWidth >= 1000 ? 4 : paneWidth >= 640 ? 3 : 2, tileWidth = paneWidth > 0 ? (paneWidth - 12 * (tileColumns - 1)) / tileColumns : 0;
  const create = (input: {name: string; summary: string; visibility: 'private' | 'server'}) => {
    if (busy) return;
    setBusy(true); setProblem('');
    const task = route.view === 'playlists'
      ? destination.mutateSaved({action: 'create', name: input.name, ...(input.summary ? {summary: input.summary} : {})})
      : destination.mutatePersonal({action: 'create', kind: 'collection', name: input.name, summary: input.summary, visibility: input.visibility});
    void Promise.resolve(task).then(result => {if (result === false) throw new Error(t('error.unavailable')); setCreating(false); changed?.();})
      .catch((failure: unknown) => setProblem(presentError(failure, 'saved', {operation: 'save'}).body))
      .finally(() => setBusy(false));
  };
  const retry = () => {void destination.retry().catch(runtime.reportError);};
  const runCommand = (lease: SavedDirectoryCommandLease, work: () => Promise<void>) => {
    if (commandBusy || commandTicket.current || !mounted.current) return;
    const now = activation.getSnapshot();
    if (!now.visible || now.revision !== lease.revision || now.intent !== lease.intent || routeCurrent?.() === false) return;
    const ticket = {};
    commandTicket.current = {ticket, lease};
    const held = () => commandTicket.current?.ticket === ticket;
    const current = () => {
      if (!mounted.current || !held() || routeCurrent?.() === false) return false;
      const live = activation.getSnapshot();
      return live.visible && live.revision === lease.revision && live.intent === lease.intent;
    };
    setCommandBusy(true); setProblem('');
    void work().then(() => {if (!current()) return; changed?.(); void destination.retry().catch(runtime.reportError);})
      .catch((failure: unknown) => {if (current()) setProblem(presentError(failure, 'saved', {operation: 'save'}).body);})
      .finally(() => {if (!held()) return; const stillCurrent = current(); commandTicket.current = undefined; if (stillCurrent) setCommandBusy(false);});
  };
  useLayoutEffect(() => {mounted.current = true; return () => {mounted.current = false; commandTicket.current = undefined; renameOpening.current = undefined; removeOpening.current = undefined; menuOpening.current = undefined;};}, []);
  useLayoutEffect(() => {
    const off = activation.subscribe(() => {
      const now = activation.getSnapshot(), held = commandTicket.current;
      if (held && (!now.visible || now.revision !== held.lease.revision || now.intent !== held.lease.intent)) {commandTicket.current = undefined; setCommandBusy(false);}
      if (menu && (!now.visible || now.revision !== menu.target.lease.revision || now.intent !== menu.target.lease.intent)) {menuOpening.current = undefined; setMenu(undefined);}
      if (renaming && (!now.visible || now.revision !== renaming.lease.revision || now.intent !== renaming.lease.intent)) {renameOpening.current = undefined; setRenaming(undefined);}
      if (removing && (!now.visible || now.revision !== removing.lease.revision || now.intent !== removing.lease.intent)) {removeOpening.current = undefined; setRemoving(undefined);}
    });
    return () => {off();};
  }, [activation, menu, removing, renaming]);

  const menuFor = (target: DirectoryTarget, opening: object) => {
    const resource = target.resource, items: MenuEntry[] = [];
    // The menu belongs to this exact opening; a retained row of a retired opening does nothing.
    const live = () => menuOpening.current === opening;
    if (resource.actions.includes('pin')) items.push({id: 'pin', label: t(resource.pinned ? 'pins.unpin' : 'pins.pin'), icon: (resource.pinned ? 'i-bookmark-f' : 'i-bookmark') as IconName,
      onSelect: () => {if (!live()) return; setMenu(undefined); runCommand(target.lease, () => commands!.pin(resource, target.lease, !resource.pinned));}});
    if (resource.actions.includes('update')) items.push({id: 'rename', label: t('web.saved.rename'), icon: 'i-edit' as IconName,
      onSelect: () => {if (!live()) return; setMenu(undefined); renameOpening.current = target; setRenaming(target);}});
    if (resource.actions.includes('delete')) items.push({id: 'delete', label: t(resource.kind === 'view' ? 'web.saved.deleteView' : 'web.saved.deleteCollection'),
      icon: 'i-trash' as IconName, destructive: true, onSelect: () => {if (!live()) return; setMenu(undefined); removeOpening.current = target; setRemoving(target);}});
    return items;
  };
  const openMenu = (target: DirectoryTarget, anchor: RefObject<View | null>) => {
    // The tile's own render cannot open after unmount, an activation replacement or a
    // refresh that replaced its resource; the live page must still be the readable one.
    if (!mounted.current) return;
    const now = activation.getSnapshot();
    if (!now.visible || now.revision !== target.lease.revision || now.intent !== target.lease.intent) return;
    if (routeCurrent?.() === false) return;
    const live = destination.getSnapshot();
    if (live.error ?? live.saved?.error ?? live.personal?.error) return;
    if (!live.personal?.resources.includes(target.resource)) return;
    const opening = {};
    menuOpening.current = opening; setMenu({target, anchor, opening});
  };
  const next = () => {
    if (!activation.getSnapshot().visible || routeCurrent?.() === false || destination.getSnapshot().phase !== 'ready') return;
    const hasMore = route.view === 'playlists' ? !!saved?.pagination.next.length : !!personal?.nextCursor;
    if (hasMore && savedNearEnd(viewport.current.offset, viewport.current.height, viewport.current.extent, tablet ? tileWidth + 110 : 80, tablet ? tileColumns : 1)) {
      void destination.next(route.view === 'playlists' ? saved?.pagination.next[0]?.sectionId : undefined).catch(runtime.reportError);
    }
  };
  const empty = (id: MessageId, body: MessageId) => <StateView variant="inline" title={t(id)} body={t(body)}/>;
  return <View testID={`native-saved-directory-${route.view}`} pointerEvents={active.visible ? 'auto' : 'none'} accessibilityElementsHidden={!active.visible}
    importantForAccessibility={active.visible ? 'auto' : 'no-hide-descendants'} style={{flex: 1}}><StandardBackdrop/>
    <View style={{paddingTop: inline ? 0 : insets.top, backgroundColor: alpha(theme.colors.ink, .94), borderBottomColor: alpha(theme.colors.text, .07), borderBottomWidth: 1}}>
      <View style={{height: 44, paddingLeft: 12, paddingRight: 10, flexDirection: 'row', alignItems: 'center'}}>
        {!inline && <><Focusable testID="saved-directory-back" accessibilityRole="button" onPress={back} accessibilityLabel={t('saved.title')} style={{height: 38, marginLeft: -4, paddingRight: 6, justifyContent: 'center'}}>
          <View style={{flexDirection: 'row', alignItems: 'center'}}><Icon name="i-left" size={22} color={theme.colors.textSecondary}/>
            <Text style={[browseType(theme, 16, 600), {color: theme.colors.textSecondary}]}>{t('saved.title')}</Text></View>
        </Focusable>
        <View pointerEvents="none" style={{position: 'absolute', left: 118, right: 118, top: 0, height: 44, alignItems: 'center', justifyContent: 'center'}}>
          <Text numberOfLines={1} style={[browseType(theme, 17, 700), {color: theme.colors.text}]}>{title}</Text>
        </View>
        </>}
        {createEnabled && <View style={{marginLeft: 'auto'}}>
          <Focusable testID={`saved-new-${route.view}`} accessibilityRole="button" accessibilityLabel={t(route.view === 'playlists' ? 'web.saved.newPlaylist' : 'web.saved.newCollection')}
            onPress={() => setCreating(true)} style={{width: 38, height: 38, alignItems: 'center', justifyContent: 'center'}}>
            <Icon name="i-plus" size={24} color={theme.colors.text}/>
          </Focusable>
        </View>}
      </View>
    </View>
    <ScrollView onLayout={event => {viewport.current.height = event.nativeEvent.layout.height; next();}}
      onContentSizeChange={(_width, height) => {viewport.current.extent = height; next();}}
      onScroll={event => {viewport.current.offset = event.nativeEvent.contentOffset.y; next();}} scrollEventThrottle={64} contentContainerStyle={{paddingBottom: 28 + insets.bottom}}>
      {route.view === 'playlists' && !error && (!!filters.length || !!sorts.length) && <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{paddingHorizontal: theme.gutter, paddingTop: 10, paddingBottom: 10, gap: 8}}>
        {!!sorts.length && <SavedSortMenu scope="saved-directory" sorts={sorts} sort={saved?.projection?.query.sort} direction={saved?.projection?.query.direction}
          onChange={(sort, direction) => {
            if (!activation.getSnapshot().visible || destination.getSnapshot().saved?.projection !== saved?.projection) return;
            const filter = saved?.projection?.query.category as PlaylistListFilter | undefined;
            void destination.select({view: 'playlists', sort, direction, ...(filter ? {filter} : {})}).catch(runtime.reportError);
          }}/>}
        {filters.map(filter => filter.options.map(option => <Chip key={`${filter.id}:${option.id}`} focusId={`dir-filter-${option.id}`}
          selected={(saved?.projection?.query.category ?? 'all') === option.id} count={option.count} onPress={() => {void destination.select({view: 'playlists', filter: option.id as PlaylistListFilter,
            ...(saved?.projection?.query.sort ? {sort: saved.projection.query.sort, direction: saved.projection.query.direction as 'asc' | 'desc'} : {})}).catch(runtime.reportError);}}>
          {option.label}</Chip>))}
      </ScrollView>}
      {route.view === 'views' && <Text style={[browseType(theme, 13, 400, 0, 19.5), {color: theme.colors.textTertiary, marginHorizontal: theme.gutter, marginTop: 12, marginBottom: 4}]}>{t('next.saved.viewsHelp')}</Text>}
      {problem !== '' && <View style={{paddingHorizontal: theme.gutter, paddingTop: 10}}><Banner variant="notice" tone="error" placement="inline" message={problem} action={{label: t('action.dismiss'), onPress: () => setProblem('')}}/></View>}
      {error && !loading ? <StateView variant="inline" {...runtime.error(error, retry)}/> : loading ? <SavedLoading kind="directory"/>
        : route.view === 'playlists' ? (playlists.length ? (tablet ? <View onLayout={event => setPaneWidth(event.nativeEvent.layout.width)} style={{flexDirection: 'row', flexWrap: 'wrap', gap: 12, marginHorizontal: theme.gutter, marginTop: 6}}>
          {playlists.map(playlist => <View key={playlist.id} style={{width: tileWidth}}>
            <SavedDirectoryTile id={`playlist-${playlist.id}`} title={playlist.title} caption={[savedItemCount(playlist.count), playlist.subtitle].filter(Boolean) as string[]}
              mosaic={(playlist.artworkPaths ?? []).map(path => runtime.artwork(path))} pinned={playlist.pinned === true}
              onPress={() => open({view: 'playlist', playlistId: playlist.id})} onMenu={() => {}} commands={false}/>
          </View>)}</View>
          : <View style={{marginTop: 6}}>{playlists.map(playlist => <SavedDirectoryRow key={playlist.id} id={`playlist-${playlist.id}`}
            title={playlist.title} caption={[savedItemCount(playlist.count), playlist.subtitle].filter(Boolean)} mosaic={(playlist.artworkPaths ?? []).map(path => runtime.artwork(path))}
            pinned={playlist.pinned === true} onPress={() => open({view: 'playlist', playlistId: playlist.id})}/>)}</View>)
          : empty('web.saved.empty.playlists', 'web.saved.empty.playlistsBody'))
        : resources.length ? (tablet ? <View onLayout={event => setPaneWidth(event.nativeEvent.layout.width)} style={{flexDirection: 'row', flexWrap: 'wrap', gap: 12, marginHorizontal: theme.gutter, marginTop: 6}}>
          {resources.map(resource => {
            const paths = route.view === 'views' ? personalMosaic(resource) : resource.artworkPaths ?? [];
            const target: DirectoryTarget = {resource, lease: active};
            return <View key={resource.id} style={{width: tileWidth}}>
              <SavedDirectoryTile id={`resource-${resource.id}`} title={resource.name}
                caption={(route.view === 'collections' ? [savedTitleCount(resource.entryCount)] : [resource.definition ? savedLibraryName(libraries, resource.definition.libraryId) : undefined,
                  savedViewCount(resource.entryCount)]).filter((value): value is string => !!value)}
                note={route.view === 'collections' ? savedVisibility(resource) : resource.onHome ? {text: t('next.saved.onHome'), icon: 'i-home', strong: true} : undefined}
                summary={route.view === 'views' ? summaries?.line(resource, summaryText) : undefined} filterTile={route.view === 'views' && !paths.length} pinned={resource.pinned}
                mosaic={paths.map(path => runtime.artwork(path))} menuOpen={menu?.target.resource.id === resource.id}
                onPress={() => open({view: 'resource', resourceId: resource.id})} onMenu={anchor => openMenu(target, anchor)}
                commands={!!commands && resource.actions.some(action => action === 'pin' || action === 'update' || action === 'delete')}/>;
            </View>;
          })}</View>
          : <View style={{marginTop: 6}}>{resources.map(resource => {
            const paths = route.view === 'views' ? personalMosaic(resource) : resource.artworkPaths ?? [];
            return <SavedDirectoryRow key={resource.id} id={`resource-${resource.id}`} title={resource.name}
              caption={route.view === 'collections' ? [savedTitleCount(resource.entryCount)] : [resource.definition ? savedLibraryName(libraries, resource.definition.libraryId) : undefined,
                savedViewCount(resource.entryCount)]} note={route.view === 'collections' ? savedVisibility(resource)
                  : resource.onHome ? {text: t('next.saved.onHome'), icon: 'i-home', strong: true} : undefined}
              summary={route.view === 'views' ? summaries?.line(resource, summaryText) : undefined} filterTile={route.view === 'views' && !paths.length} pinned={resource.pinned}
              mosaic={paths.map(path => runtime.artwork(path))} onPress={() => open({view: 'resource', resourceId: resource.id})}/>;
          })}</View>)
          : empty(route.view === 'collections' ? 'web.saved.empty.collections' : 'web.saved.empty.views', route.view === 'collections' ? 'web.saved.empty.collectionsBody' : 'web.saved.empty.viewsBody')}
      {snapshot.phase === 'loading' && !!(playlists.length || resources.length) && <View style={{marginHorizontal: theme.gutter, marginTop: 12}}><SavedTail row/></View>}
    </ScrollView>
    <SavedNameSheet open={creating} title={t(route.view === 'playlists' ? 'web.saved.newPlaylist' : 'web.saved.newCollection')} action={t('web.saved.create')}
      withSummary withVisibility={route.view === 'collections'} busy={busy} error={problem || undefined} testID="saved-create" onClose={() => setCreating(false)} onSave={create}/>
    {!!commands && <Menu testID="saved-directory-menu" open={!!menu && !error} onOpenChange={open => {if (open) return;
      const opening = menu?.opening; if (opening && menuOpening.current === opening) menuOpening.current = undefined;
      setMenu(current => current === menu ? undefined : current);}} anchorRef={menu?.anchor} returnFocusRef={menu?.anchor}
      title={menu?.target.resource.name} items={menu && !error ? menuFor(menu.target, menu.opening) : []}/>}
    <SavedNameSheet open={!!renaming && !error} title={t(renaming?.resource.kind === 'view' ? 'web.saved.renameView' : 'web.saved.renameCollection')}
      action={t('action.save')} initialName={renaming?.resource.name ?? ''} initialSummary={renaming?.resource.summary ?? ''} withSummary busy={commandBusy}
      error={problem || undefined} testID="saved-directory-rename"
      onClose={() => {const target = renaming; if (!target) return; if (renameOpening.current === target) renameOpening.current = undefined; setRenaming(current => current === target ? undefined : current);}}
      onSave={input => {const target = renaming; if (!target || renameOpening.current !== target) return; renameOpening.current = undefined; setRenaming(current => current === target ? undefined : current);
        if (commands) runCommand(target.lease, () => commands.rename(target.resource, target.lease, input.name, input.summary));}}/>
    <SavedConfirmSheet open={!!removing && !error} title={t('web.saved.deleteTitle', {name: removing?.resource.name ?? ''})} destructive busy={commandBusy}
      body={t(removing?.resource.kind === 'view' ? 'web.saved.deleteViewBody' : 'web.saved.deleteCollectionBody')}
      action={t(removing?.resource.kind === 'view' ? 'web.saved.deleteViewAction' : 'web.saved.deleteCollectionAction')}
      error={problem || undefined} testID="saved-directory-delete"
      onClose={() => {const target = removing; if (!target) return; if (removeOpening.current === target) removeOpening.current = undefined; setRemoving(current => current === target ? undefined : current);}}
      onConfirm={() => {const target = removing; if (!target || removeOpening.current !== target) return; removeOpening.current = undefined; setRemoving(current => current === target ? undefined : current);
        if (commands) runCommand(target.lease, () => commands.remove(target.resource, target.lease));}}/>
  </View>;
}

function SavedDirectoryTile({id, title, caption, note, summary, mosaic, filterTile, pinned, menuOpen, commands, onPress, onMenu}: {
  id: string; title: string; caption: readonly string[]; note?: Readonly<{text: string; icon: IconName; strong: boolean}>; summary?: string;
  mosaic: readonly (ArtworkSource | undefined)[]; filterTile?: boolean; pinned?: boolean; menuOpen?: boolean; commands: boolean;
  onPress: () => void; onMenu: (anchor: RefObject<View | null>) => void;
}) {
  const theme = useTheme(), [artWidth, setArtWidth] = useState(0), anchor = useRef<View>(null), paths = mosaic.filter((source): source is NonNullable<typeof source> => !!source).slice(0, 4);
  return <View style={{position: 'relative', borderRadius: 14, backgroundColor: theme.colors.raised, overflow: 'hidden', borderWidth: menuOpen ? 1 : 0, borderColor: theme.colors.accent}}>
    <Focusable testID={`saved-directory-${encodeURIComponent(id)}`} onPress={onPress} focusKind="row" focusRadius={14} accessibilityRole="button" accessibilityLabel={title}>
      <View onLayout={event => setArtWidth(event.nativeEvent.layout.width)} style={{width: '100%', aspectRatio: 1, flexDirection: 'row', flexWrap: 'wrap', backgroundColor: theme.colors.ink}}>
        {paths.map((source, index) => <Artwork key={`${source.cacheKey}:${index}`} source={source} width={Math.max(1, artWidth / 2)} height={Math.max(1, artWidth / 2)} visible priority={index === 0} style={{borderRadius: 0}}/>)}
        {!paths.length && filterTile && <View style={{flex: 1, alignItems: 'center', justifyContent: 'center'}}><Icon name="i-filter" size={32} color={theme.colors.text}/></View>}
      </View>
      <View style={{padding: 12, gap: 4}}>
        <Text numberOfLines={1} style={[browseType(theme, 16, 700, -.01), {color: theme.colors.text}]}>{title}</Text>
        {!!caption.length && <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 8}}>{caption.map(part => <Text key={part} numberOfLines={1} style={[browseType(theme, 13, 400), {color: theme.colors.textTertiary}]}>{part}</Text>)}</View>}
        {note && <View style={{flexDirection: 'row', alignItems: 'center', gap: 5, marginTop: 1}}><Icon name={note.icon} size={13} color={note.strong ? theme.colors.accentStrong : theme.colors.textTertiary}/>
          <Text numberOfLines={2} style={[browseType(theme, 13, note.strong ? 700 : 600), {color: note.strong ? theme.colors.accentStrong : theme.colors.textTertiary, flexShrink: 1}]}>{note.text}</Text></View>}
        {!!summary && <Text numberOfLines={2} style={[browseType(theme, 12.5, 400, 0, 17.5), {color: theme.colors.textSecondary}]}>{summary}</Text>}
        {pinned && <Icon name="i-bookmark-f" size={16} color={theme.colors.accentStrong}/>}
      </View>
    </Focusable>
    {commands && <Focusable ref={anchor} testID={`saved-directory-more-${encodeURIComponent(id)}`} accessibilityRole="button" accessibilityLabel={t('card.more', {title})} onPress={() => onMenu(anchor)} focusRadius={16}
      style={{position: 'absolute', top: 8, right: 8, width: 32, height: 32, borderRadius: 16, alignItems: 'center', justifyContent: 'center', backgroundColor: theme.colors.ink}}>
      <Icon name="i-more" size={20} color={theme.colors.text}/>
    </Focusable>}
  </View>;
}

function SavedDirectoryRow({id, title, caption, note, summary, mosaic, filterTile, pinned, onPress}: {
  id: string; title: string; caption: readonly (string | undefined)[]; note?: Readonly<{text: string; icon: IconName; strong: boolean}>; summary?: string; mosaic: readonly (ArtworkSource | undefined)[]; filterTile?: boolean; pinned?: boolean; onPress: () => void;
}) {
  const theme = useTheme(), paths = mosaic.filter((source): source is NonNullable<typeof source> => !!source).slice(0, 4);
  return <Focusable testID={`saved-directory-${encodeURIComponent(id)}`} focusId={`saved-directory-${id}`} accessibilityRole="button" accessibilityLabel={title} onPress={onPress}
    focusKind="row" style={{flexDirection: 'row', alignItems: 'center', gap: 14, paddingVertical: 8, paddingLeft: theme.gutter, paddingRight: 18, minHeight: 80}}>
      {filterTile ? <View style={{width: 56, height: 56, borderRadius: 12, backgroundColor: theme.colors.raisedBright, alignItems: 'center', justifyContent: 'center'}}>
        <Icon name="i-filter" size={24} color={theme.colors.text}/></View>
        : <View style={{width: 64, height: 64, borderRadius: 10, overflow: 'hidden', backgroundColor: theme.colors.ink, flexDirection: 'row', flexWrap: 'wrap', gap: 1}}>
          {paths.map((source, index) => <Artwork key={`${source.cacheKey}:${index}`} source={source} width={31.5} height={31.5} visible priority={index === 0} style={{borderRadius: 0}}/>)}
        </View>}
      <View style={{flex: 1, minWidth: 0, gap: 3}}>
        <Text numberOfLines={1} style={[browseType(theme, 16, 700, -.01), {color: theme.colors.text}]}>{title}</Text>
        {!!caption.filter(Boolean).length && <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 8}}>{caption.filter((part): part is string => !!part).map(part =>
          <Text key={part} numberOfLines={1} style={[browseType(theme, 13, 400), {color: theme.colors.textTertiary}]}>{part}</Text>)}</View>}
        {note && <View style={{flexDirection: 'row', alignItems: 'center', gap: 5, marginTop: 1}}><Icon name={note.icon} size={13} color={note.strong ? theme.colors.accentStrong : theme.colors.textTertiary}/>
          <Text numberOfLines={2} style={[browseType(theme, 13, note.strong ? 700 : 600), {color: note.strong ? theme.colors.accentStrong : theme.colors.textTertiary, flexShrink: 1}]}>{note.text}</Text></View>}
        {!!summary && <Text numberOfLines={2} style={[browseType(theme, 12.5, 400, 0, 17.5), {color: theme.colors.textSecondary}]}>{summary}</Text>}
      </View>
      {pinned && <Icon name="i-bookmark-f" size={16} color={theme.colors.accentStrong}/>}
      <Icon name="i-right" size={18} color={theme.colors.lineStrong}/>
    <View pointerEvents="none" style={{position: 'absolute', left: 98, right: 0, bottom: 0, height: 1, backgroundColor: alpha(theme.colors.lineStrong, .35)}}/>
  </Focusable>;
}
