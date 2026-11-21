import {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore, type RefObject} from 'react';
import type {View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import type {Library} from '../../../../../../packages/client-core/src/index';
import type {PersonalResource} from '../../../../../../packages/client-core/src/personal-saved';
import type {PlaylistCard, PlaylistListFilter, SavedRoute} from '../../../../../../packages/client-core/src/saved';
import type {IconName} from '../../../kit/Icon';
import type {MenuEntry} from '../../../kit/overlays';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeDestinationActivationPort} from '../../../screens/destinations/focus';
import type {NativeSavedViewDestination} from '../../../screens/saved/view-model';
import {savedItemCount, savedLibraryName, personalMosaic, savedTitleCount, savedViewCount, savedVisibility} from '../../../screens/saved/presentation';
import type {NativeSavedViewSummaries} from '../../../screens/saved/state';
import type {NativeSavedDirectoryCommands, SavedDirectoryCommandLease} from '../../../screens/saved/directory-commands';

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

export function useSavedDirectory({route, destination, activation, runtime, libraries, open, changed, summaries, commands, routeCurrent}: NativeSavedDirectoryProps) {
  useSyncExternalStore(summaries?.subscribe ?? noSummarySubscribe, summaries?.getSnapshot ?? noSummaryRevision);
  const active = useSyncExternalStore(activation.subscribe, activation.getSnapshot);
  const snapshot = useSyncExternalStore(destination.subscribe, destination.getSnapshot);
  const [creating, setCreating] = useState(false), [busy, setBusy] = useState(false), [problem, setProblem] = useState<string>();
  const [menu, setMenu] = useState<{target: DirectoryTarget; anchor: RefObject<View | null>; opening: object}>(), [renaming, setRenaming] = useState<DirectoryTarget>(),
    [removing, setRemoving] = useState<DirectoryTarget>(), [commandBusy, setCommandBusy] = useState(false);
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
  const query = saved?.projection?.query;
  const currentProjection = () => activation.getSnapshot().visible && routeCurrent?.() !== false &&
    destination.getSnapshot().saved?.projection === saved?.projection;
  const chooseSort = (sort: string, direction: 'asc' | 'desc') => {
    if (!currentProjection()) return;
    const filter = query?.category as PlaylistListFilter | undefined;
    void destination.select({view: 'playlists', sort, direction, ...(filter ? {filter} : {})}).catch(runtime.reportError);
  };
  const chooseFilter = (filter: PlaylistListFilter) => {
    if (!currentProjection()) return;
    void destination.select({view: 'playlists', filter,
      ...(query?.sort ? {sort: query.sort, direction: query.direction as 'asc' | 'desc'} : {})}).catch(runtime.reportError);
  };
  const next = () => {
    const current = destination.getSnapshot();
    if (!activation.getSnapshot().visible || routeCurrent?.() === false || current.phase !== 'ready') return;
    if (route.view === 'playlists') {
      const continuation = saved?.pagination.next[0];
      if (continuation && current.saved === saved) void destination.next(continuation.sectionId).catch(runtime.reportError);
    } else if (personal?.nextCursor && current.personal === personal) {
      void destination.next().catch(runtime.reportError);
    }
  };
  const rows = route.view === 'playlists' ? playlists.map(playlist => ({
    id: `playlist-${playlist.id}`, title: playlist.title,
    caption: [savedItemCount(playlist.count), playlist.subtitle].filter((word): word is string => !!word),
    mosaic: (playlist.artworkPaths ?? []).map(path => runtime.artwork(path)), pinned: playlist.pinned === true,
    onPress: () => open({view: 'playlist', playlistId: playlist.id}),
  })) : resources.map(resource => {
    const paths = route.view === 'views' ? personalMosaic(resource) : resource.artworkPaths ?? [];
    const target: DirectoryTarget = {resource, lease: active};
    return {
      id: `resource-${resource.id}`, title: resource.name,
      caption: (route.view === 'collections' ? [savedTitleCount(resource.entryCount)]
        : [resource.definition ? savedLibraryName(libraries, resource.definition.libraryId) : undefined, savedViewCount(resource.entryCount)])
        .filter((word): word is string => !!word),
      note: route.view === 'collections' ? savedVisibility(resource)
        : resource.onHome ? {text: t('next.saved.onHome'), icon: 'i-home' as const, strong: true} : undefined,
      summary: route.view === 'views' ? summaries?.line(resource, summaryText) : undefined,
      filterTile: route.view === 'views' && !paths.length,
      pinned: resource.pinned, mosaic: paths.map(path => runtime.artwork(path)), menuOpen: menu?.target.resource.id === resource.id,
      onPress: () => open({view: 'resource', resourceId: resource.id}),
      onMenu: commands && resource.actions.some(action => action === 'pin' || action === 'update' || action === 'delete')
        ? (anchor: RefObject<View | null>) => openMenu(target, anchor) : undefined,
    };
  });
  const closeMenu = (opened: boolean) => {
    if (opened) return;
    const opening = menu?.opening;
    if (opening && menuOpening.current === opening) menuOpening.current = undefined;
    setMenu(current => current === menu ? undefined : current);
  };
  const closeRename = () => {
    if (!renaming) return;
    if (renameOpening.current === renaming) renameOpening.current = undefined;
    setRenaming(current => current === renaming ? undefined : current);
  };
  const closeDelete = () => {
    if (!removing) return;
    if (removeOpening.current === removing) removeOpening.current = undefined;
    setRemoving(current => current === removing ? undefined : current);
  };
  return {
    active, error, loading, title, createEnabled, filters, sorts, query, rows, problem, creating, busy, menu, renaming, removing, commandBusy,
    paging: snapshot.phase === 'loading' && rows.length > 0,
    create, retry, chooseSort, chooseFilter, next, closeMenu, closeRename, closeDelete,
    dismissProblem: () => setProblem(''), showCreate: () => setCreating(true), closeCreate: () => setCreating(false),
    menuItems: menu && !error ? menuFor(menu.target, menu.opening) : [],
    rename: (input: {name: string; summary: string}) => {
      if (!renaming || renameOpening.current !== renaming) return;
      closeRename();
      if (commands) runCommand(renaming.lease, () => commands.rename(renaming.resource, renaming.lease, input.name, input.summary));
    },
    remove: () => {
      if (!removing || removeOpening.current !== removing) return;
      closeDelete();
      if (commands) runCommand(removing.lease, () => commands.remove(removing.resource, removing.lease));
    },
  };
}
