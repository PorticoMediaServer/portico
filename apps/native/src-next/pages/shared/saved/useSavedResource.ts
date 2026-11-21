import {useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore, type RefObject} from 'react';
import type {View} from 'react-native';
import {defaultI18n, type MessageId} from '../../../../../../packages/i18n/src';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import type {Library} from '../../../../../../packages/client-core/src/index';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import type {PersonalResource, PersonalSavedSnapshot, PersonalSavedIntent} from '../../../../../../packages/client-core/src/personal-saved';
import {resourceContentManual, resourceContentQuery, type PlaylistOccurrence, type ResourceContentQuery, type ResourceOrderWitness, type SavedRoute, type SavedSnapshot} from '../../../../../../packages/client-core/src/saved';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {sameAuthority, type ShellAuthority} from '../../../shell/model';
import type {NativeEntryMenuState} from '../../../screens/destinations/entry-actions';
import type {NativeDestinationActivationPort} from '../../../screens/destinations/focus';
import type {NativeSavedViewDestination} from '../../../screens/saved/view-model';
import type {NativeSavedSelectionPort} from './useSavedPage';
import type {NativeWatchMenuExtra} from '../../../screens/watch/factory';
import {isReadingLibraryKind} from '../../../screens/read/platform';
import {exactDurationSeconds, personalMosaic, playlistEntryMosaic, savedItemCount, savedPlaylistEyebrow,
  savedResourceEyebrow, savedTitleCount, savedVisibility} from '../../../screens/saved/presentation';

import type {MenuEntry, ToastMessage} from '../../../kit/overlays';
import type {PosterGridItem, TitleListItem} from '../../../kit/frames';
import type {CardSelection} from '../../../kit/layout/types';
import {durationText} from '../../../../../../packages/client-core/src/presentation/kit-model/text';

const t = defaultI18n.t;

export type NativeSavedEntryActions = Readonly<{
  menuState?: NativeEntryMenuState;
  open: (entry: ContentEntry) => void;
  play: (entry: ContentEntry) => void;
  /** The real page owner plays the entire named resource as one server queue. */
  playResource?: (shuffle?: boolean) => Promise<void>;
  menu: (entry: ContentEntry, anchorRef?: RefObject<View | null>, select?: () => void, extraActions?: readonly NativeWatchMenuExtra[]) => void;
  isMenuOpen?: (entry: ContentEntry) => boolean;
}>;

export type NativeSavedResourceProps = Readonly<{
  route: SavedRoute;
  destination: NativeSavedViewDestination;
  activation: NativeDestinationActivationPort;
  runtime: NativeShellRuntime;
  actions: NativeSavedEntryActions;
  selection?: NativeSavedSelectionPort;
  libraries: readonly Library[];
  back: () => void;
  changed?: () => void;
  /** Real shortcut into Customize Home for this loaded saved view. */
  showOnHome?: (resource: PersonalResource) => void;
  /** Exact saved definition opened in its real library. */
  openInLibrary?: (resource: PersonalResource) => void;
  /** Read-only canonical plain-words conditions for a saved view. */
  condition?: string;
}>;

export type SavedResourceRowValue = Readonly<{itemOnly?: true; id: string; media: ContentEntry}>;
/** One row-sheet opening: what it names, the route it belongs to, and the row focus it took
 * once when it opened, so no later move can pull focus to a row the person has left. */
type Row = SavedResourceRowValue;
type RowMenu = Readonly<{entry: ContentEntry; entryId: string; route: SavedRoute; focus?: 'open' | 'up' | 'down'}>;
type Command = 'done' | 'failed' | 'busy' | 'stale';
/** The page a command started on: its epoch (which moves with the route) and its authority. */
type PageWitness = Readonly<{epoch: number; authority: ShellAuthority}>;

/** The page's membership rows, from whichever snapshot the caller holds: the render and a
 * command dispatched later must both read the same thing. */
/** Where a page of the resource's own order sits, and the entries the server witnessed around it
 * at the resource's revision. A sorted or filtered page has no witness, so it moves nothing. */
function pageOrder(isPlaylist: boolean, state: Readonly<{saved?: SavedSnapshot; personal?: PersonalSavedSnapshot}>): Readonly<{start: number; witness?: ResourceOrderWitness}> {
  if (!isPlaylist) return {start: state.personal?.page?.start ?? 0, witness: state.personal?.page?.order};
  const section = state.saved?.projection?.sections[0];
  return {start: section?.start ?? 0, witness: state.saved?.projection?.playlistRevision === state.saved?.playlist?.revision ? section?.order : undefined};
}
const contentQuery = (route: SavedRoute | undefined): ResourceContentQuery => {
  if (route?.view !== 'playlist' && route?.view !== 'resource') return {};
  try {return resourceContentQuery(route);} catch {return {};}
};
/** The page's own sorts: the resource's order by its group's name, the rest by the Saved words. */
export const resourceSortKeys = {title: 'web.saved.sort.title', year: 'web.saved.sort.year', duration: 'web.saved.sort.duration'} as const;

function membershipRows(isPlaylist: boolean, state: Readonly<{saved?: SavedSnapshot; personal?: PersonalSavedSnapshot}>): readonly Row[] {
  return isPlaylist
    ? (state.saved?.projection?.sections ?? []).flatMap(section => section.entries.flatMap(entry => entry.kind === 'playlist_entry' && !entry.hidden ? [{id: entry.id, itemOnly: entry.itemOnly, media: entry.media}] : []))
    : (state.personal?.entries ?? []).flatMap(entry => !entry.hidden && entry.media ? [{id: entry.id, itemOnly: entry.itemOnly, media: entry.media}] : []);
}

/** collections.html 11a–13c and 18–20. The page reads the actual Saved route
 * through its canonical service; commands go back through the same service. */
export function useSavedResource({route, destination, activation, runtime, actions, selection, libraries, back, changed, showOnHome, openInLibrary}: NativeSavedResourceProps, tv: boolean) {
  const active = useSyncExternalStore(activation.subscribe, activation.getSnapshot);
  const snapshot = useSyncExternalStore(destination.subscribe, destination.getSnapshot);
  useSyncExternalStore(selection?.subscribe ?? (() => () => {}), selection?.getSnapshot ?? (() => 0));
  useSyncExternalStore(actions.menuState?.subscribe ?? (() => () => {}), actions.menuState?.getSnapshot ?? (() => 0));
  const [pageMenu, setPageMenu] = useState(false), [rowMenuState, setRowMenuState] = useState<RowMenu>(), [renaming, setRenaming] = useState<object>(), [deleting, setDeleting] = useState<object>();
  const [reordering, setReordering] = useState(false), [dragging, setDragging] = useState(false), [infoOpen, setInfoOpen] = useState(false), [problem, setProblem] = useState<string>(), [busy, setBusy] = useState(false);
  const [toast, setToast] = useState<ToastMessage | null>(null), toastId = useRef(0);
  /** The opening this screen is showing, retired by identity so a late result or a second
   * press can only ever act on the sheet it came from; `epoch` moves with the route so a
   * command started on one page cannot report onto the next. */
  const opening = useRef<RowMenu | undefined>(undefined), flight = useRef<Readonly<{token: object}> | undefined>(undefined), pageOpening = useRef<object | undefined>(undefined);
  const nameOpening = useRef<object | undefined>(undefined), deleteOpening = useRef<object | undefined>(undefined);
  const personalRetry = useRef<Readonly<{current: () => boolean; work: (still: () => boolean) => Promise<void>; sheet?: object; draft?: {name: string; summary: string; visibility: 'private' | 'server'}}> | undefined>(undefined);
  const mounted = useRef(true), epoch = useRef(0), shownRoute = useRef(route);
  useLayoutEffect(() => {mounted.current = true; return () => {mounted.current = false; opening.current = undefined; pageOpening.current = undefined; nameOpening.current = undefined; deleteOpening.current = undefined; personalRetry.current = undefined; flight.current = undefined;};}, []);
  useEffect(() => {void destination.select(route).catch(runtime.reportError);}, [destination, route, runtime]);
  useEffect(() => {setPageMenu(false); nameOpening.current = undefined; deleteOpening.current = undefined; setRenaming(undefined); setDeleting(undefined); setReordering(false); setProblem(''); setBusy(false);}, [route]);
  const saved = snapshot.saved as SavedSnapshot | undefined, personal = snapshot.personal as PersonalSavedSnapshot | undefined;
  const playlist = saved?.playlist ?? null, resource = personal?.resource ?? null, isPlaylist = route.view === 'playlist';
  // A sheet and a command belong to exactly one route. A route change retires both here, at
  // render, so nothing the old page started survives it by even one frame.
  if (shownRoute.current !== route) {shownRoute.current = route; epoch.current += 1; opening.current = undefined; nameOpening.current = undefined; deleteOpening.current = undefined; personalRetry.current = undefined; flight.current = undefined;}
  // An opening belongs to the route it was made on: a different page never draws it.
  const rowMenu = rowMenuState?.route === route ? rowMenuState : undefined;
  const rows: readonly Row[] = useMemo(() => membershipRows(isPlaylist, snapshot), [isPlaylist, snapshot]);
  const media = rows.map(row => row.media);
  const count = isPlaylist ? playlist?.entryCount : resource?.entryCount;
  const canPlayResource = !!actions.playResource && (count ?? 0) > 0 && (isPlaylist || resource?.status === 'ready');
  const countText = isPlaylist ? savedItemCount(count) : savedTitleCount(count);
  const duration = exactDurationSeconds(media), artwork = isPlaylist ? playlistEntryMosaic((saved?.projection?.sections ?? []).flatMap(section => section.entries).filter((entry): entry is PlaylistOccurrence => entry.kind === 'playlist_entry')) : personalMosaic(resource);
  const name = isPlaylist ? playlist?.name : resource?.name;
  const eyebrow = isPlaylist ? savedPlaylistEyebrow : resource ? savedResourceEyebrow(resource) : undefined;
  // collections.html 18/20: the sort chip and Filters set a temporary page query on the selected
  // route; the server reads it. Only the resource's own order over every entry can move one.
  const shown = snapshot.route ?? route, query = contentQuery(shown), manual = resourceContentManual(query), order = pageOrder(isPlaylist, snapshot);
  const offers = isPlaylist ? {sorts: saved?.projection?.sorts ?? [], filters: saved?.projection?.filters ?? []} : {sorts: personal?.page?.sorts ?? [], filters: personal?.page?.filters ?? []};
  const [filtersOpen, setFiltersOpen] = useState(false), filtersAnchor = useRef<View>(null);
  const chooseQuery = (next: ResourceContentQuery) => {
    const own = !next.sort || next.sort === 'position', narrowed = next.filter && next.filter !== 'all' ? {filter: next.filter} : {};
    const sorted = own ? {} : {sort: next.sort, ...(next.direction ? {direction: next.direction} : {})};
    const target: SavedRoute | undefined = route.view === 'playlist' ? {view: 'playlist', playlistId: route.playlistId, ...sorted, ...narrowed}
      : route.view === 'resource' && route.resourceId ? {view: 'resource', resourceId: route.resourceId, ...sorted, ...narrowed} : undefined;
    if (target) void destination.select(target).catch(report);
  };
  const reorderGranted = isPlaylist ? playlist?.actions.includes('reorder') === true : resource?.kind === 'collection' && resource.actions.includes('reorder') === true;
  const canReorder = reorderGranted && rows.every(row => !row.itemOnly) && manual && !!order.witness;
  useEffect(() => {if (!canReorder) setReordering(false);}, [canReorder]);
  const visibility = resource?.kind === 'collection' ? savedVisibility(resource, tv) : undefined;
  const report = (error: unknown) => runtime.reportError(error);
  /** The page a command started on, as a witness; `still` also knows its command is the one in
   * flight. A completion that lands late — a toast, a sheet closing, Back — can tell it is no
   * longer on its own page and leaves the successor alone. */
  const witness = (): PageWitness => ({epoch: epoch.current, authority: runtime.getSnapshot().authority});
  const onPage = (at: PageWitness): boolean => mounted.current && epoch.current === at.epoch
    && sameAuthority(runtime.getSnapshot().authority, at.authority) && activation.getSnapshot().visible;
  /** One command at a time, refused synchronously: a held remote OK repeats faster than a
   * render can disable the row it is on. The work is handed a witness, so a completion that
   * lands late — a toast, a sheet closing, Back — can tell it is no longer on its own page. */
  const run = async (work: (still: () => boolean) => Promise<unknown>): Promise<Command> => {
    if (flight.current) return 'busy';
    if (!onPage(witness()) || activation.getSnapshot().revision !== active.revision) return 'stale';
    const token = {}, at = witness(), activationRevision = active.revision;
    flight.current = {token};
    setBusy(true); setProblem('');
    const current = () => onPage(at) && activation.getSnapshot().revision === activationRevision && flight.current?.token === token;
    try {await work(current); if (!current()) return 'stale'; changed?.(); return 'done';}
    catch (error) {if (!current()) return 'stale'; setProblem(presentError(error, 'saved', {operation: 'save'}).body); return 'failed';}
    finally {if (flight.current?.token === token) {flight.current = undefined; if (mounted.current && epoch.current === at.epoch) setBusy(false);}}
  };
  const personalFailure = () => destination.getSnapshot().personal?.mutationError ?? Object.assign(new Error('unavailable'), {code: 'unavailable'});
  /** The canonical service owns the operation ID and bytes. A retry retains this
   * opening's completion as well; it cannot close a later sheet or a later route. */
  const changePersonal = async (intent: PersonalSavedIntent, still: () => boolean, done?: () => void, sheet?: object,
    draft?: {name: string; summary: string; visibility: 'private' | 'server'}) => {
    if (destination.getSnapshot().personal?.retryPending) throw personalFailure();
    const at = witness(), revision = activation.getSnapshot().revision;
    const current = () => onPage(at) && activation.getSnapshot().revision === revision;
    const command = {current, sheet, draft, work: async (live: () => boolean) => {
      if (!current() || personalRetry.current !== command) return;
      const ok = destination.getSnapshot().personal?.retryPending ? await destination.retryPersonalMutation() : await destination.mutatePersonal(intent);
      if (!ok) {
        if (!destination.getSnapshot().personal?.retryPending && personalRetry.current === command) personalRetry.current = undefined;
        throw personalFailure();
      }
      if (personalRetry.current === command) personalRetry.current = undefined;
      if (live() && current()) done?.();
    }};
    personalRetry.current = command; await command.work(still);
  };
  const renderedPersonalRetry = personalRetry.current, renderedRetryError = personal?.mutationError;
  const retryPersonal = () => {
    const command = personalRetry.current;
    if (command !== renderedPersonalRetry || !command && destination.getSnapshot().personal?.mutationError !== renderedRetryError) return;
    if (!destination.getSnapshot().personal?.retryPending) return;
    void run(async still => {
      if (command?.current()) await command.work(still);
      else {
        if (!await destination.retryPersonalMutation()) throw personalFailure();
        // Returning may reconcile the original bytes, but never revives that
        // old opening's close/Back completion.
        if (personalRetry.current === command) personalRetry.current = undefined;
      }
    });
  };
  /** The page as the server last stated it, for a sheet that has outlived a refresh. */
  const page = () => {
    const state = destination.getSnapshot();
    return {rows: membershipRows(isPlaylist, state), order: pageOrder(isPlaylist, state), playlist: state.saved?.playlist ?? null, resource: state.personal?.resource ?? null};
  };
  /** A press acts only from the exact opening that drew it, while this page is on screen. */
  const openingLive = (menu: RowMenu): boolean =>
    mounted.current && opening.current === menu && menu.route === route && activation.getSnapshot().visible;
  const play = (shuffle = false) => {
    if (!canPlayResource || !actions.playResource) return;
    const playResource = actions.playResource;
    void run(() => playResource(shuffle));
  };
  /** One page-menu opening; every row is bound to it by identity. */
  const openPageMenu = () => {
    // Only the page that is still mounted, activated and readable may mint an opening;
    // the live document must be the exact one this render drew, never a later replacement.
    if (!mounted.current || !onPage(witness()) || !name) return;
    const nowActivation = activation.getSnapshot();
    if (!nowActivation.visible || nowActivation.revision !== active.revision || nowActivation.intent !== active.intent) return;
    const live = destination.getSnapshot();
    if (live.error ?? live.saved?.error ?? live.personal?.error) return;
    if (isPlaylist ? (live.saved?.playlist ?? null) !== playlist : (live.personal?.resource ?? null) !== resource) return;
    const page = {};
    pageOpening.current = page; setPageMenu(true);
  };
  const pin = () => {
    if (isPlaylist ? !playlist : !resource) return;
    void run(async () => {
      if (isPlaylist) await destination.mutateSaved({action: 'pin', pinned: playlist!.pinned !== true});
      else await destination.mutatePersonal({action: 'pin', pinned: resource!.pinned !== true});
    });
  };
  /** Retires this exact opening. A press from a sheet that has since been replaced or
   * unmounted can only ever touch the opening it was drawn from. */
  const closeOpening = (target: RowMenu) => {
    if (opening.current !== target) return;
    opening.current = undefined;
    if (mounted.current) setRowMenuState(current => current === target ? undefined : current);
  };
  const remove = (menu: RowMenu) => {
    if (!openingLive(menu)) return;
    const live = page(), index = live.rows.findIndex(value => value.id === menu.entryId);
    const removable = isPlaylist ? live.playlist?.actions.includes('remove') === true
      : live.resource?.kind === 'collection' && live.resource.role !== 'viewer' && live.resource.actions.includes('entries') === true;
    // The row, and the action on it, are checked against the page as it is now.
    if (index < 0 || live.rows[index]?.itemOnly || !removable) {closeOpening(menu); return;}
    const row = live.rows[index]!;
    closeOpening(menu);
    void run(async still => {
      if (isPlaylist) {
        await destination.mutateSaved({action: 'remove', entryId: row.id});
        const token = destination.getSnapshot().saved?.lastReceipt?.undoToken;
        if (token && still()) showUndo(t('saved.removal.fromPlaylist', {title: row.media.title}), () => destination.mutateSaved({action: 'restore', undoToken: token}), t('saved.removal.fromPlaylist', {title: row.media.title}));
      } else {
        await destination.mutatePersonal({action: 'entries', removeEntryIds: [row.id]});
        const undo = destination.getSnapshot().personal?.result?.undo;
        if (undo && still()) showUndo(t('saved.removal.fromCollection', {title: row.media.title}), () => destination.mutatePersonal({action: 'entries', undoToken: undo.token}), t('saved.removal.fromCollection', {title: row.media.title}));
      }
    });
  };
  /** The toast's Undo belongs to the page that created it and to that one toast: the press
   * never dispatches from a successor page, and a late result never clears or replaces a newer
   * toast on the same page. */
  const showUndo = (message: string, undo: () => Promise<unknown>, failure: string) => {
    const id = String(++toastId.current), at = witness();
    setToast({id, message, action: {label: t('action.undo'), onPress: () => {
      if (!onPage(at)) return;
      const settle = (next: () => ToastMessage | null) => {if (onPage(at)) setToast(current => current?.id === id ? next() : current);};
      void undo().then(() => settle(() => null)).catch(() => settle(() => ({id: `${id}-failed`, message: failure})));
    }}});
  };
  /** collections.html 19: a TV playlist's sheet stays open so Move up and Move down can be
   * pressed again, and a failed move closes the sheet it was pressed from so the page's own
   * problem line is read. G33 gives authorized TV collections the same behavior. */
  const move = (menu: RowMenu, where: 'up' | 'down' | 'top' | 'bottom') => {
    if (!openingLive(menu)) return;
    const keepSheetOpen = tv, live = page();
    const ids = live.rows.map(value => value.id), index = live.rows.findIndex(value => value.id === menu.entryId);
    const reorderable = isPlaylist ? live.playlist?.actions.includes('reorder') === true
      : live.resource?.kind === 'collection' && live.resource.actions.includes('reorder') === true;
    // The membership row and the reorder it asks for are the current page's, not the render's.
    if (index < 0 || !reorderable) {closeOpening(menu); return;}
    const row = live.rows[index]!, {start, witness: seen} = live.order;
    // The front of this page is the resource's front only on its first page; past it, the entry
    // the server witnessed just before the page. The bottom is the witnessed last entry.
    const front = start === 0 ? '' : seen?.previous;
    if (!seen) return;
    if (where === 'up' && index === 0) {if (seen.previous) place(menu, row, undefined, keepSheetOpen, [row.id, seen.previous]); return;}
    let after: string | undefined;
    if (where === 'up') after = index === 1 ? front : ids[index - 2];
    else if (where === 'down') after = index === ids.length - 1 ? seen.next : ids[index + 1];
    else if (where === 'top') {if (index !== 0 || start !== 0) after = '';}
    else if (seen.last && seen.last !== row.id) after = seen.last;
    if (after === undefined) return;
    place(menu, row, after, keepSheetOpen);
  };
  /** Moves one entry after `after` ("" the front); without `after`, swaps `entryIds` among their places. */
  const place = (menu: RowMenu, row: Row, after: string | undefined, keepSheetOpen = false, entryIds: readonly string[] = [row.id]) => {
    if (!openingLive(menu)) return;
    const intent = after === undefined ? {action: 'reorder' as const, entryIds} : {action: 'reorder' as const, entryIds, afterEntryId: after};
    void run(async still => {
      if (isPlaylist) {
        try {await destination.mutateSaved(intent);}
        catch (error) {
          const code = (error as {code?: string}).code;
          if (code !== 'playlist_conflict' && code !== 'revision_mismatch') throw error;
          await destination.loadOrder();
          if (!still() || !openingLive(menu)) return;
          const current = page();
          if (!current.playlist?.actions.includes('reorder') || after === undefined || !current.rows.some(value => value.id === row.id)
            || after !== '' && !current.rows.some(value => value.id === after)) return;
          await destination.mutateSaved(intent);
        }
      } else {
        await changePersonal(intent, still);
      }
    }).then(result => {if (result === 'failed' && keepSheetOpen) closeOpening(menu);});
  };
  const saveName = (input: {name: string; summary: string; visibility: 'private' | 'server'}) => {
    const sheet = renaming;
    if (!sheet || nameOpening.current !== sheet) return;
    if (!isPlaylist && personalRetry.current?.sheet === sheet && personal?.retryPending) {retryPersonal(); return;}
    const done = () => {if (nameOpening.current === sheet) {nameOpening.current = undefined; setRenaming(undefined);}};
    void run(async still => {
      if (isPlaylist) {await destination.mutateSaved({action: 'rename', name: input.name, ...(playlist?.actions.includes('summary') ? {summary: input.summary} : {})}); if (still()) done();}
      else await changePersonal({action: 'update', name: input.name, summary: input.summary,
        ...(tv && resource?.kind === 'collection' && resource.role === 'owner' ? {visibility: input.visibility} : {})}, still, done, sheet, input);
    });
  };
  const confirmDelete = () => {
    const sheet = deleting;
    if (!sheet || deleteOpening.current !== sheet) return;
    if (!isPlaylist && personalRetry.current?.sheet === sheet && personal?.retryPending) {retryPersonal(); return;}
    const done = () => {if (deleteOpening.current === sheet) {deleteOpening.current = undefined; changed?.(); setDeleting(undefined); back();}};
    void run(async still => {
      if (isPlaylist) {await destination.mutateSaved({action: 'delete'}); if (still()) done();}
      else await changePersonal({action: 'delete'}, still, done, sheet);
    });
  };
  const rowMenuEntries = (menu: RowMenu): readonly MenuEntry[] => {
    const index = rows.findIndex(row => row.id === menu.entryId), last = rows.length - 1;
    // Past the page's edge only through the witnessed neighbour.
    const atFirst = index < 0 || index === 0 && !order.witness?.previous, atLast = index < 0 || index >= last && !order.witness?.next;
    // G33 permits the same retained Move up/down sheet on authorized collections.
    // Every press re-checks the live membership and grants before it dispatches.
    const tvMoves = tv, moves = canReorder;
    return [
      {id: 'open', label: t('web.saved.goToItem'), icon: 'i-info', onSelect: () => {if (!openingLive(menu)) return; closeOpening(menu); actions.open(menu.entry);}},
      ...(tv ? [{id: 'item-actions', label: t('card.more', {title: menu.entry.title}), icon: 'i-more' as const,
        onSelect: () => {if (!openingLive(menu)) return; closeOpening(menu); actions.menu(menu.entry, undefined, selection?.item(menu.entry)?.onEnter);}}] : []),
      ...(tv && selection?.item(menu.entry) ? [{id: 'select', label: t('next.selection.select'), icon: 'i-check' as const,
        onSelect: () => {if (!openingLive(menu)) return; closeOpening(menu); selection.item(menu.entry)?.onEnter();}}] : []),
      ...(moves && !tvMoves ? [
        {id: 'top', label: t('order.moveTop'), icon: 'i-up', disabled: index < 0 || index === 0 && order.start === 0, onSelect: () => move(menu, 'top')},
        {id: 'up', label: t('player.queueMoveUp'), icon: 'i-up', disabled: atFirst, onSelect: () => move(menu, 'up')},
        {id: 'down', label: t('player.queueMoveDown'), icon: 'i-down', disabled: atLast, onSelect: () => move(menu, 'down')},
        {id: 'bottom', label: t('order.moveBottom'), icon: 'i-down', disabled: index < 0 || !order.witness?.last || order.witness.last === menu.entryId, onSelect: () => move(menu, 'bottom')},
      ] as const : []),
      // A TV sheet keeps its move rows attached while a command runs: disabling the row the
      // person is on would move focus away from it. The flight refusal above is the guard.
      ...(moves && tvMoves ? [
        {id: 'up', label: t('player.queueMoveUp'), icon: 'i-up', disabled: atFirst, closeOnSelect: false, onSelect: () => move(menu, 'up')},
        {id: 'down', label: t('player.queueMoveDown'), icon: 'i-down', disabled: atLast, closeOnSelect: false, onSelect: () => move(menu, 'down')},
      ] as const : []),
      // The page's current membership row, not the object captured when the sheet opened: the
      // refresh after each move replaces the projection's objects while the sheet stays open.
      ...(index >= 0 && !rows[index]?.itemOnly && (isPlaylist ? playlist?.actions.includes('remove') : resource?.kind === 'collection' && resource.role !== 'viewer' && resource.actions.includes('entries'))
        ? [{id: 'remove', separator: true as const}, {id: 'remove-entry', label: t(isPlaylist ? 'web.saved.removeFromPlaylist' : 'web.saved.removeFromCollection'), icon: 'i-trash' as const, destructive: true, disabled: busy, onSelect: () => remove(menu)}]
        : []),
    ];
  };
  const openRowMenu = (row: Row) => {
    // Enter on the first action. Later moves preserve the viewer's current row.
    const next: RowMenu = {entry: row.media, entryId: row.id, route, ...(tv ? {focus: 'open' as const} : {})};
    opening.current = next;
    setRowMenuState(next);
  };
  const openEntryMenu = (row: Row, anchor?: RefObject<View | null>) => {
    if (!active.visible || !onPage(witness()) || !page().rows.some(value => value.id === row.id && value.media === row.media)) return;
    if (tv) {openRowMenu(row); return;}
    const next: RowMenu = {entry: row.media, entryId: row.id, route};
    opening.current = next;
    const extras = rowMenuEntries(next).flatMap(item => 'onSelect' in item && item.id !== 'open' ? [item as NativeWatchMenuExtra] : []);
    actions.menu(row.media, anchor, selection?.item(row.media)?.onEnter, extras);
  };
  const reorderOpening = (row: Row): RowMenu | undefined => {
    if (!reordering || !canReorder || !onPage(witness())) return;
    const live = page();
    if (!live.rows.some(value => value.id === row.id && value.media === row.media)) return;
    const permitted = isPlaylist ? live.playlist?.actions.includes('reorder') : live.resource?.kind === 'collection' && live.resource.actions.includes('reorder');
    if (!permitted) return;
    const next: RowMenu = {entry: row.media, entryId: row.id, route}; opening.current = next; return next;
  };
  const moveRow = (row: Row, where: 'up' | 'down' | 'top' | 'bottom') => {const next = reorderOpening(row); if (next) move(next, where);};
  const placeRow = (row: Row, after: string, ids: readonly string[]) => {
    const live = page();
    if (ids.length !== live.rows.length || live.rows.some((value, index) => value.id !== ids[index]) || after === row.id
      || after !== '' && !live.rows.some(value => value.id === after)) return;
    const anchor = after === '' && live.order.start !== 0 ? live.order.witness?.previous : after;
    if (anchor === undefined) return;
    const next = reorderOpening(row); if (next) place(next, row, anchor);
  };
  const stateError = snapshot.error ?? saved?.error ?? personal?.error;
  const menuItems: readonly MenuEntry[] = (() => {
    // A page whose document was refused or unreadable has no command to advertise.
    if (stateError || personal?.retryPending) return [];
    // Every row belongs to this exact page-menu opening; a retained row from a retired
    // opening dispatches nothing.
    const page = pageOpening.current, livePage = () => !!page && pageOpening.current === page;
    const items: MenuEntry[] = [], pinned = playlist?.pinned === true || resource?.pinned === true;
    // G33 permits collection management on TV; every command keeps its actual grant.
    const changeableHere = !(tv && resource?.kind === 'view');
    if (isPlaylist ? playlist?.actions.includes('pin') : resource?.actions.includes('pin')) items.push({id: 'pin', label: t(pinned ? 'pins.unpin' : 'pins.pin'),
      icon: pinned ? 'i-bookmark-f' : 'i-bookmark', onSelect: () => {if (!livePage()) return; pageOpening.current = undefined; setPageMenu(false); pin();}});
    const library = !isPlaylist && resource?.definition ? libraries.find(value => value.id === resource.definition!.libraryId) : undefined;
    // The shortcut may only act on the exact resource the page rendered and still shows.
    const shortcut = (action: ((value: PersonalResource) => void) | undefined) => {
      if (!action || !resource || !active.visible) return;
      const nowActivation = activation.getSnapshot();
      // The exact activation snapshot this render drew must still be the current one:
      // leaving and coming back produces a new revision and cannot reuse this menu.
      if (!nowActivation.visible || nowActivation.revision !== active.revision || nowActivation.intent !== active.intent) return;
      const live = destination.getSnapshot().personal?.resource;
      if (live !== resource || live.revision !== resource.revision) return;
      action(resource);
    };
    if (!tv && !isPlaylist && resource?.kind === 'view' && resource.definition && library && !isReadingLibraryKind(library.kind) && openInLibrary
      && defaultI18n.has('next.saved.openInLibrary' as MessageId)) items.push({id: 'openInLibrary',
      label: t('next.saved.openInLibrary' as MessageId, {library: library.name}), icon: 'i-grid', onSelect: () => {if (!livePage()) return; pageOpening.current = undefined; setPageMenu(false); shortcut(openInLibrary);}});
    if (!tv && !isPlaylist && resource?.kind === 'view' && resource.onHome !== true && showOnHome) items.push({id: 'showOnHome',
      label: t('home.customize.showRow', {title: name ?? ''}), icon: 'i-home', onSelect: () => {if (!livePage()) return; pageOpening.current = undefined; setPageMenu(false); shortcut(showOnHome);}});
    if (changeableHere && (isPlaylist ? playlist?.actions.includes('rename') : resource?.actions.includes('update'))) items.push({id: 'rename', label: t('web.saved.rename'),
      icon: 'i-edit', onSelect: () => {if (!livePage()) return; pageOpening.current = undefined; setPageMenu(false); const sheet = {}; nameOpening.current = sheet; setRenaming(sheet);}});
    if (changeableHere && (isPlaylist ? playlist?.actions.includes('delete') : resource?.actions.includes('delete'))) items.push({id: 'delete',
      label: t(isPlaylist ? 'web.saved.deletePlaylist' : resource?.kind === 'view' ? 'web.saved.deleteView' : 'web.saved.deleteCollection'),
      icon: 'i-trash', destructive: true, onSelect: () => {if (!livePage()) return; pageOpening.current = undefined; setPageMenu(false); const sheet = {}; deleteOpening.current = sheet; setDeleting(sheet);}});
    return items;
  })();
  // A refused document retires every command surface opened from it (page menu, row menu,
  // rename and delete sheets) at layout, so a retained callback has no opening to dispatch.
  // Only the UI openings retire: a service's in-flight or uncertain mutation/retry state
  // (and `flight`) is left exactly as it was.
  useLayoutEffect(() => {
    if (!stateError) return;
    opening.current = undefined; pageOpening.current = undefined; setRowMenuState(undefined);
    setPageMenu(false); nameOpening.current = undefined; deleteOpening.current = undefined; setRenaming(undefined); setDeleting(undefined);
  }, [stateError]);
  const hasMore = !!(isPlaylist ? saved?.pagination.next.length : personal?.nextCursor);
  const next = () => {
    if (!active.visible || !activation.getSnapshot().visible || dragging || !hasMore || snapshot.phase !== 'ready' || destination.getSnapshot() !== snapshot) return;
    void destination.next(isPlaylist ? saved?.pagination.next[0]?.sectionId : undefined).catch(report);
  };
  const retry = () => {void destination.retry().catch(report);};
  const setVisibility = (visible: boolean) => run(async () => {
    await destination.mutatePersonal({action: 'update', visibility: visible ? 'server' : 'private'});
  });
  return {active, phase: snapshot.phase, playlist, resource, personal, isPlaylist, rows, media, canPlayResource, count, countText, duration,
    artwork, name, eyebrow, query, manual, offers, chooseQuery, reorderGranted, canReorder, visibility, dragging, setDragging,
    reordering, setReordering, infoOpen, setInfoOpen, busy, problem, clearProblem: () => setProblem(''), stateError, retry, next,
    play, pin, setVisibility, openEntryMenu, moveRow, placeRow, openPageMenu, retryPersonal, filtersOpen, setFiltersOpen, filtersAnchor,
    pageMenu, menuItems, setPageMenu: (open: boolean) => {if (!open) pageOpening.current = undefined; setPageMenu(open);},
    rowMenu, rowMenuItems: rowMenu && !stateError ? rowMenuEntries(rowMenu) : [],
    closeRowMenu: () => {if (rowMenu) closeOpening(rowMenu);},
    renaming, saveName, nameDraft: personal?.retryPending && personalRetry.current?.sheet === renaming ? personalRetry.current?.draft : undefined,
    closeName: () => {if (nameOpening.current === renaming) {nameOpening.current = undefined; setRenaming(undefined);}},
    deleting, confirmDelete,
    closeDelete: () => {if (deleteOpening.current === deleting) {deleteOpening.current = undefined; setDeleting(undefined);}},
    toast, clearToast: () => setToast(null)};
}

const length = (seconds: number | undefined) => seconds ? durationText(seconds, t) : undefined;
const squareKinds = new Set(['album', 'song', 'book', 'audiobook_file']);

/** One row of a playlist or collection as the kit's title list draws it (collections 11a, 12a): its art, title, what it is,
 * its year and length, then Watched or the time left, and its ⋯. */
export function savedResourceRowItem(row: SavedResourceRowValue, runtime: NativeShellRuntime, actions: NativeSavedEntryActions,
  onMore: (anchor: RefObject<View | null>) => void, selection?: CardSelection): TitleListItem & Readonly<{id: string}> {
  const entry = row.media;
  const progress = entry.duration && entry.progressSeconds && !entry.watched ? Math.min(1, entry.progressSeconds / entry.duration) : undefined;
  const left = progress === undefined ? undefined : length(Math.max(0, (entry.duration ?? 0) - (entry.progressSeconds ?? 0)));
  const facts = [t('title.kind', {kind: entry.kind}), entry.year === undefined ? undefined : String(entry.year),
    progress === undefined && !entry.watched ? length(entry.duration) : undefined];
  return {id: row.id, title: entry.displayTitle || entry.title, facts: facts.filter((fact): fact is string => !!fact),
    square: squareKinds.has(entry.kind), watched: entry.watched, progress, remaining: left && t('next.kit.remaining', {time: left}),
    artwork: runtime.artwork(entry.posterUrl), selection, onNavigate: () => actions.open(entry), onMore,
    testID: `saved-row-${encodeURIComponent(row.id)}`};
}

/** One title of a saved view as the kit's poster grid draws it (collections 13a). */
export function savedViewGridItem(entry: ContentEntry, runtime: NativeShellRuntime, actions: NativeSavedEntryActions,
  selection?: CardSelection): PosterGridItem & Readonly<{id: string}> {
  const progress = entry.duration && entry.progressSeconds ? Math.min(1, entry.progressSeconds / entry.duration) : undefined;
  return {id: entry.id, title: entry.displayTitle || entry.title, caption: entry.year === undefined ? undefined : String(entry.year),
    artwork: runtime.artwork(entry.posterUrl), watched: entry.watched, progress, selection,
    onNavigate: () => actions.open(entry), onMore: anchor => actions.menu(entry, anchor, selection?.onEnter)};
}
