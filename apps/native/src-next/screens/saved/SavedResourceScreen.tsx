import React, {useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore, type RefObject} from 'react';
import {ScrollView, Text, View, useWindowDimensions} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {defaultI18n, type MessageId} from '../../../../../packages/i18n/src';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';
import type {Library} from '../../../../../packages/client-core/src/index';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {PersonalResource, PersonalSavedSnapshot, PersonalSavedIntent} from '../../../../../packages/client-core/src/personal-saved';
import {resourceContentManual, resourceContentQuery, type PlaylistOccurrence, type ResourceContentQuery, type ResourceOrderWitness, type SavedRoute, type SavedSnapshot} from '../../../../../packages/client-core/src/saved';
import {Button, Chip, Focusable, Switch} from '../../kit/controls';
import {AmbientArt, Artwork, MediaArt, StandardBackdrop} from '../../kit/artwork';
import {CardFrame, Parts} from '../../kit/cards/primitives';
import {PosterCard, SquareCard, type CardSelection} from '../../kit/cards';
import {SelectionCircle} from '../../kit/selection';
import {FocusRegion} from '../../kit/focus';
import {Banner, BottomSheet, Menu, ToastHost, type MenuEntry, type ToastMessage} from '../../kit/overlays';
import {useDockClearance} from '../../kit/dock/DockClearance';
import {StateView} from '../../kit/states';
import {Icon} from '../../kit/Icon';
import {alpha, browseType} from '../../kit/browse/presentation';
import {useTheme} from '../../kit/theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import {sameAuthority, type ShellAuthority} from '../../shell/model';
import type {NativeEntryMenuState} from '../destinations/entry-actions';
import {nativeDestinationFocusIds, type NativeDestinationActivationPort} from '../destinations/focus';
import type {NativeSavedViewDestination} from './view-model';
import type {NativeSavedSelectionPort} from './SavedScreen';
import type {NativeWatchMenuExtra} from '../watch/factory';
import {SavedLoading, SavedTail} from './SavedLoading';
import {savedNearEnd} from './paging';
import {SavedReorderList} from './SavedReorderList';
import {SavedSortMenu} from './SavedSortMenu';
import {homeCardText} from '../home/model';
import {SavedConfirmSheet, SavedNameSheet} from './SavedForms';
import {isReadingLibraryKind} from '../read/platform';
import {exactDurationSeconds, personalMosaic, playlistEntryMosaic, savedDuration, savedItemCount, savedLibraryName, savedPlaylistEyebrow,
  savedResourceEyebrow, savedTitleCount, savedViewConditionCount, savedVisibility, savedVisibilityHelp} from './presentation';

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

type Row = Readonly<{id: string; media: ContentEntry}>;
/** One row-sheet opening: what it names, the route it belongs to, and the row focus it took
 * once when it opened, so no later move can pull focus to a row the person has left. */
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
const resourceSortKeys = {title: 'web.saved.sort.title', year: 'web.saved.sort.year', duration: 'web.saved.sort.duration'} as const;

function membershipRows(isPlaylist: boolean, state: Readonly<{saved?: SavedSnapshot; personal?: PersonalSavedSnapshot}>): readonly Row[] {
  return isPlaylist
    ? (state.saved?.projection?.sections ?? []).flatMap(section => section.entries.flatMap(entry => entry.kind === 'playlist_entry' && !entry.hidden ? [{id: entry.id, media: entry.media}] : []))
    : (state.personal?.entries ?? []).flatMap(entry => !entry.hidden && entry.media ? [{id: entry.id, media: entry.media}] : []);
}

/** collections.html 11a–13c and 18–20. The page reads the actual Saved route
 * through its canonical service; commands go back through the same service. */
export function SavedResourceScreen({route, destination, activation, runtime, actions, selection, libraries, back, changed, showOnHome, openInLibrary, condition}: NativeSavedResourceProps) {
  const theme = useTheme(), insets = useSafeAreaInsets(), dimensions = useWindowDimensions(), instance = React.useId();
  const dockTop = useDockClearance();
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
  const canReorder = reorderGranted && manual && !!order.witness;
  useEffect(() => {if (!canReorder) setReordering(false);}, [canReorder]);
  const visibility = resource?.kind === 'collection' ? savedVisibility(resource, theme.isTV) : undefined;
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
    if (index < 0 || !removable) {closeOpening(menu); return;}
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
    const keepSheetOpen = theme.isTV, live = page();
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
        ...(theme.isTV && resource?.kind === 'collection' && resource.role === 'owner' ? {visibility: input.visibility} : {})}, still, done, sheet, input);
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
    const tvMoves = theme.isTV, moves = canReorder;
    return [
      {id: 'open', label: t('web.saved.goToItem'), icon: 'i-info', onSelect: () => {if (!openingLive(menu)) return; closeOpening(menu); actions.open(menu.entry);}},
      ...(theme.isTV ? [{id: 'item-actions', label: t('card.more', {title: menu.entry.title}), icon: 'i-more' as const,
        onSelect: () => {if (!openingLive(menu)) return; closeOpening(menu); actions.menu(menu.entry, undefined, selection?.item(menu.entry)?.onEnter);}}] : []),
      ...(theme.isTV && selection?.item(menu.entry) ? [{id: 'select', label: t('next.selection.select'), icon: 'i-check' as const,
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
      ...(index >= 0 && (isPlaylist ? playlist?.actions.includes('remove') : resource?.kind === 'collection' && resource.role !== 'viewer' && resource.actions.includes('entries'))
        ? [{id: 'remove', separator: true as const}, {id: 'remove-entry', label: t(isPlaylist ? 'web.saved.removeFromPlaylist' : 'web.saved.removeFromCollection'), icon: 'i-trash' as const, destructive: true, disabled: busy, onSelect: () => remove(menu)}]
        : []),
    ];
  };
  const openRowMenu = (row: Row) => {
    // Enter on the first action. Later moves preserve the viewer's current row.
    const next: RowMenu = {entry: row.media, entryId: row.id, route, ...(theme.isTV ? {focus: 'open' as const} : {})};
    opening.current = next;
    setRowMenuState(next);
  };
  const openEntryMenu = (row: Row, anchor?: RefObject<View | null>) => {
    if (!active.visible || !onPage(witness()) || !page().rows.some(value => value.id === row.id && value.media === row.media)) return;
    if (theme.isTV) {openRowMenu(row); return;}
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
    const changeableHere = !(theme.isTV && resource?.kind === 'view');
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
    if (!theme.isTV && !isPlaylist && resource?.kind === 'view' && resource.definition && library && !isReadingLibraryKind(library.kind) && openInLibrary
      && defaultI18n.has('next.saved.openInLibrary' as MessageId)) items.push({id: 'openInLibrary',
      label: t('next.saved.openInLibrary' as MessageId, {library: library.name}), icon: 'i-grid', onSelect: () => {if (!livePage()) return; pageOpening.current = undefined; setPageMenu(false); shortcut(openInLibrary);}});
    if (!theme.isTV && !isPlaylist && resource?.kind === 'view' && resource.onHome !== true && showOnHome) items.push({id: 'showOnHome',
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
  const width = theme.isTV ? 1920 - theme.gutter * 2 : dimensions.width - theme.gutter * 2;
  const focusIds = useMemo(() => nativeDestinationFocusIds(runtime.getSnapshot().authority), [runtime]);
  const region = `saved.resource.${theme.isTV ? `${instance}.` : ''}${route.view === 'playlist' ? route.playlistId : `${route.view}:${route.resourceId ?? ''}`}`;
  const viewport = useRef({offset: 0, height: 0, extent: 0});
  const hasMore = !!(isPlaylist ? saved?.pagination.next.length : personal?.nextCursor);
  const next = () => {
    if (!active.visible || !activation.getSnapshot().visible || dragging || !hasMore || snapshot.phase !== 'ready' || destination.getSnapshot() !== snapshot) return;
    const at = viewport.current, grid = resource?.kind === 'view', columns = grid ? theme.isTV ? 7 : 3 : 1;
    if (savedNearEnd(at.offset, at.height, at.extent, grid ? width / columns * 1.7 : theme.isTV ? 104 : 80, columns)) {
      void destination.next(isPlaylist ? saved?.pagination.next[0]?.sectionId : undefined).catch(report);
    }
  };
  const applyError = (error: unknown) => <StateView variant={theme.isTV ? 'centered' : 'inline'} {...runtime.error(error, () => {void destination.retry().catch(report);})}/>;
  return <View testID={`native-saved-resource-${isPlaylist ? 'playlist' : 'resource'}`} pointerEvents={active.visible ? 'auto' : 'none'} accessibilityElementsHidden={!active.visible}
    importantForAccessibility={active.visible ? 'auto' : 'no-hide-descendants'} style={{flex: 1}}><StandardBackdrop/>
    {theme.isTV ? null : <View pointerEvents="none" style={{position: 'absolute', top: 0, left: 0, right: 0, height: 760, overflow: 'hidden'}}>
      {!!artwork[0] && <AmbientArt source={runtime.ambient(artwork[0])} treatment="library"/>}
    </View>}
    <FocusRegion id={region} entry="saved-play" orientation="vertical" enabled={active.visible} edges={{left: focusIds.rail}} style={{flex: 1}}>
      {theme.isTV ? null : <View pointerEvents="box-none" style={{position: 'absolute', top: 0, left: 0, right: 0, zIndex: 10, paddingTop: insets.top, height: insets.top + 52,
        paddingHorizontal: 12, flexDirection: 'row', alignItems: 'center'}}>
        <Focusable focusId="saved-back" testID="saved-resource-back" accessibilityRole="button" accessibilityLabel={t('saved.title')} onPress={back}
          style={{width: 38, height: 38, borderRadius: 19, alignItems: 'center', justifyContent: 'center', backgroundColor: alpha(theme.colors.ink, .45)}}>
          <Icon name="i-left" size={22} color={theme.colors.text}/>
        </Focusable>
        {reordering && <Text accessibilityRole="header" style={[browseType(theme, 17, 700), {color: theme.colors.text, marginLeft: 12}]}>{t('next.saved.reordering')}</Text>}
        {reordering ? <Button testID="saved-reorder-done" variant="glass" size="small" disabled={busy || dragging} onPress={() => setReordering(false)} style={{marginLeft: 'auto'}}>{t('order.done')}</Button> : !stateError && <View style={{marginLeft: 'auto'}}>
          <Button testID="saved-resource-menu" focusId="saved-resource-menu" variant="glass" icon="i-more" iconOnly size="small"
            accessibilityLabel={t(isPlaylist ? 'web.saved.playlistOptions' : 'web.saved.collectionOptions')} accessibilityState={{expanded: pageMenu}} onPress={openPageMenu}
            style={{width: 38, height: 38, borderRadius: 19}}/>
        </View>}
      </View>}
      <ScrollView scrollEnabled={!dragging} scrollEventThrottle={80}
        onLayout={event => {viewport.current.height = event.nativeEvent.layout.height; next();}}
        onContentSizeChange={(_width, height) => {viewport.current.extent = height; next();}}
        onScroll={event => {viewport.current.offset = event.nativeEvent.contentOffset.y; next();}} contentContainerStyle={{paddingTop: insets.top + (theme.isTV ? 96 : 52), paddingBottom: 32 + insets.bottom}}>
        {stateError ? applyError(stateError) : !name ? <SavedLoading kind="resource"/> : <>
          {!reordering && <>
          <View style={theme.isTV ? {flexDirection: 'row', alignItems: 'flex-end', gap: 48, paddingHorizontal: theme.gutter, paddingTop: 16}
            : {alignItems: 'center', paddingHorizontal: theme.gutter, paddingTop: 40}}>
            <View style={{width: theme.isTV ? 280 : 152, height: theme.isTV ? 280 : 152, borderRadius: theme.isTV ? 16 : 14, overflow: 'hidden', backgroundColor: theme.colors.raised,
              flexDirection: 'row', flexWrap: 'wrap', gap: theme.isTV ? 3 : 2, alignSelf: theme.isTV ? undefined : 'center'}}>
              {artwork.slice(0, 4).map((path, index) => <Artwork key={`${path}:${index}`} source={runtime.artwork(path)} width={(theme.isTV ? 280 : 152) / 2 - 1} height={(theme.isTV ? 280 : 152) / 2 - 1}
                visible={active.visible} priority={index === 0} style={{borderRadius: 0}}/>)}
            </View>
            <View style={theme.isTV ? {flex: 1, minWidth: 0} : {alignItems: 'center', marginTop: 14}}>
              {eyebrow && <Text style={[browseType(theme, theme.isTV ? 24 : 13.5, 700), {color: theme.colors.accentStrong, marginBottom: theme.isTV ? 8 : 2}]}>{eyebrow}</Text>}
              <Text accessibilityRole="header" numberOfLines={2} style={[browseType(theme, theme.isTV ? 72 : 30, 800, -.04, theme.isTV ? 70 : 32), {color: theme.colors.text, textAlign: theme.isTV ? 'left' : 'center'}]}>{name}</Text>
              <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', gap: theme.isTV ? 20 : 12, marginTop: theme.isTV ? 12 : 6, justifyContent: theme.isTV ? 'flex-start' : 'center'}}>
                {countText && <Text style={[browseType(theme, theme.isTV ? 24 : 13, 600), {color: theme.colors.textSecondary}]}>{countText}</Text>}
                {!!duration && <Text style={[browseType(theme, theme.isTV ? 24 : 13), {color: theme.colors.textSecondary}]}>{savedDuration(duration)}</Text>}
                {!isPlaylist && visibility && (theme.isTV || resource?.role !== 'owner') && <View style={{flexDirection: 'row', alignItems: 'center', gap: 6}}>
                  <Icon name={visibility.icon} size={theme.isTV ? 22 : 13} color={visibility.strong ? theme.colors.accentStrong : theme.colors.textTertiary}/>
                  <Text style={[browseType(theme, theme.isTV ? 24 : 13, visibility.strong ? 700 : 500), {color: visibility.strong ? theme.colors.accentStrong : theme.colors.textTertiary}]}>{visibility.text}</Text>
                </View>}
              </View>
              {!!(isPlaylist ? playlist?.summary : resource?.summary) && <Text numberOfLines={theme.isTV ? 2 : 1} style={[browseType(theme, theme.isTV ? 22 : 13.5, 400, 0, theme.isTV ? 30 : 19.6),
                {color: theme.colors.textSecondary, textAlign: theme.isTV ? 'left' : 'center', marginTop: theme.isTV ? 10 : 6}]}>{isPlaylist ? playlist?.summary : resource?.summary}</Text>}
              {!!condition && <View style={{marginTop: theme.isTV ? 12 : 10, alignSelf: theme.isTV ? 'flex-start' : 'center', maxWidth: '100%'}}>
                <View style={{borderRadius: 999, borderWidth: 1, borderColor: theme.colors.line, backgroundColor: theme.colors.raised, paddingHorizontal: 12, paddingVertical: 6}}>
                  <Text numberOfLines={2} style={[browseType(theme, theme.isTV ? 20 : 12.5), {color: theme.colors.textSecondary, textAlign: theme.isTV ? 'left' : 'center'}]}>{condition}</Text>
                </View>
              </View>}
            </View>
          </View>
          {resource?.kind === 'collection' && resource.role === 'owner' && resource.actions.includes('update') && !theme.isTV && <View style={{paddingHorizontal: theme.gutter, paddingTop: 16, gap: 4}}>
            <Switch focusId="saved-visibility" checked={resource.visibility === 'server'} disabled={busy || personal?.pending} onCheckedChange={next => run(async () => {await destination.mutatePersonal({action: 'update', visibility: next ? 'server' : 'private'});})}>
              {t('web.saved.visibleToAll')}
            </Switch>
            <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{savedVisibilityHelp(resource)}</Text>
          </View>}
          <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.isTV ? 18 : 10, paddingHorizontal: theme.gutter, paddingTop: theme.isTV ? 24 : 14}}>
            <Button testID="saved-play" focusId="saved-play" variant="primary" icon="i-play" disabled={busy || !canPlayResource}
              onPress={() => play()} style={theme.isTV ? undefined : {height: 48, flex: 1}}>{t(theme.isTV && resource?.kind === 'collection' ? 'next.cards.playAll' : 'action.play')}</Button>
            {/* collections.html 18/20 draw Shuffle and Pin as labelled glass buttons, and More as
                a 72 by 14-radius square: the kit's hero glass button is that geometry already. */}
            <Button testID="saved-shuffle" focusId="saved-shuffle" variant="glass" icon="i-shuffle" iconOnly={!theme.isTV} disabled={busy || !canPlayResource || (count ?? 0) < 2} onPress={() => play(true)}
              accessibilityLabel={t('title.shuffle')} style={theme.isTV ? undefined : {width: 48, height: 48, borderRadius: 24}}>{t('title.shuffle')}</Button>
            <Button testID="saved-pin" focusId="saved-pin" variant="glass" icon={playlist?.pinned || resource?.pinned ? 'i-bookmark-f' : 'i-bookmark'} iconOnly={!theme.isTV} disabled={busy || !(isPlaylist ? playlist?.actions.includes('pin') : resource?.actions.includes('pin'))}
              onPress={pin} accessibilityLabel={t(playlist?.pinned || resource?.pinned ? 'pins.unpin' : 'pins.pin')}
              style={theme.isTV ? undefined : {width: 48, height: 48, borderRadius: 24}}>{t(playlist?.pinned || resource?.pinned ? 'pins.unpin' : 'pins.pin')}</Button>
            {theme.isTV && <Button testID="saved-resource-menu" focusId="saved-resource-menu" variant="glass" icon="i-more" iconOnly
              accessibilityLabel={t(isPlaylist ? 'web.saved.playlistOptions' : 'web.saved.collectionOptions')} accessibilityState={{expanded: pageMenu}}
              onPress={openPageMenu} style={{width: 72, height: 72, borderRadius: 14}}/>}
            {reorderGranted && !theme.isTV && <Button testID="saved-reorder" focusId="saved-reorder" variant={reordering ? 'primary' : 'glass'} icon={reordering ? 'i-check' : 'i-updown'}
              disabled={!canReorder} accessibilityHint={manual ? undefined : t('order.ownOrderOnly', {group: isPlaylist ? 'playlist' : 'collection'})}
              onPress={() => setReordering(value => !value)}>{t(reordering ? 'order.done' : 'order.reorder')}</Button>}
          </View>
          </>}
          {(!theme.isTV || (!isPlaylist && resource?.kind === 'view')) && <View style={{flexDirection: 'row', alignItems: 'center', gap: 8, paddingHorizontal: theme.gutter, paddingTop: 14}}>
            {!isPlaylist && resource?.kind === 'view' ? <>
              <Text style={[browseType(theme, 14.5, 700), {color: theme.colors.text}]}>{viewSortLabel(resource)}</Text>
              <Chip focusId="saved-view-info" icon="i-info" count={savedViewConditionCount(resource)} countStyle="badge" onPress={() => setInfoOpen(true)}>{t('web.saved.savedView')}</Chip>
            </> : <>
              {!!offers.sorts.length && <SavedSortMenu scope="saved-resource" focusId="saved-resource-sort" sorts={offers.sorts} sort={query.sort ?? 'position'} direction={query.direction}
                label={value => value.id === 'position' ? t('web.group.ownOrder', {group: isPlaylist ? 'playlist' : 'collection'}) : t(resourceSortKeys[value.id as keyof typeof resourceSortKeys] ?? 'web.saved.sort.other')}
                onChange={(sort, direction) => chooseQuery({sort: sort as ResourceContentQuery['sort'], direction, filter: query.filter})}/>}
              {offers.filters.some(filter => filter.options.length > 1) && <View ref={filtersAnchor} collapsable={false}>
                <Chip testID="saved-resource-filters" focusId="saved-resource-filters" icon="i-filter" selected={(query.filter ?? 'all') !== 'all'} onPress={() => setFiltersOpen(true)}>{t('next.browse.filters')}</Chip>
              </View>}
            </>}
            {!theme.isTV && theme.platform !== 'phone' && !reordering && selection?.start && <Chip testID="saved-resource-select" onPress={() => selection.start?.()}>{t('next.selection.select')}</Chip>}
            <Text style={[browseType(theme, 13, 600), {color: theme.colors.textTertiary, marginLeft: 'auto'}]}>{countText}</Text>
          </View>}
          {problem !== '' && <View style={{paddingHorizontal: theme.gutter, paddingTop: 12}}><Banner testID="saved-resource-problem" variant="notice" tone="error" placement="inline" message={problem}
            action={personal?.retryPending ? {label: t('action.tryAgain'), onPress: retryPersonal} : {label: t('action.dismiss'), onPress: () => setProblem('')}}/></View>}
          <View style={{marginTop: theme.isTV ? 24 : 8}}>
            {!rows.length && snapshot.phase !== 'loading' && <StateView variant={theme.isTV ? 'centered' : 'inline'} icon={isPlaylist ? 'i-queue' : 'i-stack'}
              title={t(isPlaylist ? 'web.saved.playlistEmpty' : resource?.kind === 'view' ? 'web.saved.viewEmpty' : 'web.saved.collectionEmpty')}
              body={isPlaylist ? t('web.saved.playlistEmptyBody') : undefined}/>}
            {reordering && !theme.isTV ? <SavedReorderList rows={rows} runtime={runtime} disabled={busy || snapshot.phase === 'loading' || !canReorder}
              onMove={moveRow} onPlace={placeRow} onDragging={setDragging}/>
              : !isPlaylist && resource?.kind === 'view'
                ? <SavedResourceViewGrid entries={media} runtime={runtime} actions={actions} selection={selection} tv={theme.isTV} windowWidth={theme.isTV ? 1920 : dimensions.width} gutter={theme.gutter}/>
                : rows.map(row => <SavedResourceRow key={row.id} row={row} width={width} runtime={runtime} actions={actions} selection={selection?.item(row.media)} library={savedLibraryName(libraries, row.media.libraryId)}
                  onMore={anchor => openEntryMenu(row, anchor)}/>)}
            {snapshot.phase === 'loading' && rows.length > 0 && <View style={{paddingHorizontal: theme.gutter, paddingVertical: 12}}><SavedTail row={resource?.kind !== 'view'} width={resource?.kind === 'view' ? width / (theme.isTV ? 7 : 3) : width}/></View>}
          </View>
          {/* collections.html 18's note: on a remote a row's own menu is where it moves or leaves. */}
          {theme.isTV && (isPlaylist ? playlist?.actions.includes('remove') === true && playlist.actions.includes('reorder') === true : resource?.kind === 'collection' && resource.actions.includes('entries') && resource.actions.includes('reorder')) && <View style={{flexDirection: 'row', alignItems: 'center', gap: 12, paddingHorizontal: theme.gutter, paddingTop: 28}}>
            <Icon name="i-hold" size={22} color={theme.colors.textTertiary}/>
            <Text style={[browseType(theme, 22), {color: theme.colors.textTertiary, flexShrink: 1}]}>{t('next.saved.native.holdRowActions')}</Text>
          </View>}

        </>}
      </ScrollView>
    </FocusRegion>
    {!reordering && selection?.render()}
    <Menu open={!stateError && filtersOpen} onOpenChange={setFiltersOpen} anchorRef={filtersAnchor} presentation="sheet" title={t('next.browse.filters')} testID="saved-resource-filter-menu"
      items={offers.filters.flatMap(filter => filter.options.map(option => ({id: `${filter.id}:${option.id}`, label: option.label, trailing: defaultI18n.number(option.count),
        selected: (query.filter ?? 'all') === option.id, onSelect: () => {setFiltersOpen(false); chooseQuery({...query, filter: option.id as ResourceContentQuery['filter']});}})))}/>
    <Menu open={!stateError && pageMenu} onOpenChange={open => {if (!open) pageOpening.current = undefined; setPageMenu(open);}} presentation={theme.isTV ? 'sideSheet' : 'sheet'} title={t(isPlaylist ? 'web.saved.playlistOptions' : 'web.saved.collectionOptions')}
      items={menuItems} testID="saved-resource-page-menu"/>
    <Menu open={!stateError && !!rowMenu} onOpenChange={open => {if (!open && rowMenu) closeOpening(rowMenu);}} presentation={theme.isTV ? 'sideSheet' : 'sheet'} title={rowMenu?.entry.title ?? ''}
      secondaryTitle={rowMenu ? <View style={{flexDirection: 'row', gap: 8}}>{[t('title.kind', {kind: rowMenu.entry.kind}), savedLibraryName(libraries, rowMenu.entry.libraryId),
        // collections.html 19's lead ends with the page the item is in.
        ...(theme.isTV && name ? [t('web.saved.inList', {name})] : [])].filter(Boolean).map(part =>
        <Text key={part} style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{part}</Text>)}</View> : undefined}
      items={rowMenu && !stateError ? rowMenuEntries(rowMenu) : []} initialFocusId={rowMenu?.focus} testID="saved-resource-row-menu"/>
    <SavedNameSheet open={!stateError && !!renaming} title={t(isPlaylist ? 'web.saved.renamePlaylist' : resource?.kind === 'view' ? 'web.saved.renameView' : 'web.saved.renameCollection')}
      action={t(personal?.retryPending ? 'action.tryAgain' : 'action.save')} initialName={personal?.retryPending && personalRetry.current?.sheet === renaming ? personalRetry.current?.draft?.name : name} initialSummary={personal?.retryPending && personalRetry.current?.sheet === renaming ? personalRetry.current?.draft?.summary : isPlaylist ? playlist?.summary : resource?.summary} withSummary={isPlaylist ? playlist?.actions.includes('summary') === true : true}
      withVisibility={theme.isTV && resource?.kind === 'collection' && resource.role === 'owner' && resource.actions.includes('update')} initialVisibility={personal?.retryPending && personalRetry.current?.sheet === renaming ? personalRetry.current?.draft?.visibility : resource?.visibility}
      busy={busy} locked={personal?.retryPending} error={problem || undefined} testID="saved-rename" onClose={() => {if (nameOpening.current === renaming) {nameOpening.current = undefined; setRenaming(undefined);}}} onSave={saveName}/>
    <BottomSheet open={infoOpen} onOpenChange={setInfoOpen} title={t('web.saved.savedView')} testID="saved-view-info" fitContent>
      <View style={{gap: 10}}>
        <Text style={[browseType(theme, 15, 700), {color: theme.colors.text}]}>{resource ? viewSortLabel(resource) : ''}</Text>
        {!!(resource?.summary) && <Text style={[browseType(theme, 14, 400, 0, 21), {color: theme.colors.textSecondary}]}>{resource.summary}</Text>}
        <Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{t('next.saved.viewsHelp')}</Text>
      </View>
    </BottomSheet>
    <SavedConfirmSheet open={!stateError && !!deleting} title={t('web.saved.deleteTitle', {name: name ?? ''})} destructive
      body={t(isPlaylist ? 'web.saved.deletePlaylistBody' : resource?.kind === 'view' ? 'web.saved.deleteViewBody' : 'web.saved.deleteCollectionBody')}
      action={t(personal?.retryPending ? 'action.tryAgain' : isPlaylist ? 'web.saved.deletePlaylistAction' : resource?.kind === 'view' ? 'web.saved.deleteViewAction' : 'web.saved.deleteCollectionAction')}
      busy={busy} error={problem || undefined} testID="saved-delete" onClose={() => {if (deleteOpening.current === deleting) {deleteOpening.current = undefined; setDeleting(undefined);}}} onConfirm={confirmDelete}/>
    {theme.isTV ? toast?.action && <View style={{position: 'absolute', left: theme.gutter, right: theme.gutter, bottom: 80}} pointerEvents="box-none">
      <Banner variant="notice" tone="info" placement="overlay" message={toast.message} action={{label: toast.action.label, onPress: toast.action.onPress}} visible/>
    </View> : <ToastHost toast={toast} onDismiss={() => setToast(null)} bottomOffset={0} obstructionTop={dockTop}/>}
  </View>;
}

function viewSortLabel(resource: PersonalResource): string {
  const sort = resource.definition?.sort?.[0];
  if (!sort) return t('web.saved.sort.other');
  return t(sortFieldId(sort.field));
}

function sortFieldId(field: string): 'web.saved.sort.title' | 'web.saved.sort.added' | 'web.saved.sort.recent' | 'web.saved.sort.year' | 'web.saved.sort.duration' | 'web.saved.sort.progress' | 'web.saved.sort.other' {
  if (field === 'title' || field === 'sort_title') return 'web.saved.sort.title';
  if (field === 'added' || field === 'added_at') return 'web.saved.sort.added';
  if (field === 'last_played' || field === 'recent') return 'web.saved.sort.recent';
  if (field === 'year') return 'web.saved.sort.year';
  if (field === 'duration') return 'web.saved.sort.duration';
  if (field === 'progress') return 'web.saved.sort.progress';
  return 'web.saved.sort.other';
}

/** A saved view is a browse result, so its body is the library grid rather than
 * the playlist row list (collections.html 13a and 21). */
function SavedResourceViewGrid({entries, runtime, actions, selection, tv, windowWidth, gutter}: {
  entries: readonly ContentEntry[]; runtime: NativeShellRuntime; actions: NativeSavedEntryActions; selection?: NativeSavedSelectionPort; tv: boolean; windowWidth: number; gutter: number;
}) {
  const columns = tv ? 7 : 3, gap = tv ? 26 : 10, width = (windowWidth - gutter * 2 - gap * (columns - 1)) / columns;
  return <View style={{flexDirection: 'row', flexWrap: 'wrap', gap, paddingHorizontal: gutter}}>{entries.map(entry =>
    <SavedResourceCard key={entry.id} entry={entry} runtime={runtime} actions={actions} selection={selection?.item(entry)} width={width}/>)}</View>;
}
function SavedResourceCard({entry, runtime, actions, selection, width}: {
  entry: ContentEntry; runtime: NativeShellRuntime; actions: NativeSavedEntryActions; selection?: CardSelection; width: number;
}) {
  const anchor = useRef<View>(null), text = homeCardText(entry, false, runtime.text), square = ['album', 'song', 'book', 'audiobook_file'].includes(entry.kind);
  const progress = entry.duration && entry.progressSeconds ? Math.min(1, entry.progressSeconds / entry.duration) : undefined;
  const props = {testID: `saved-view-entry-${encodeURIComponent(entry.id)}`, focusId: `saved-view-entry-${entry.id}`, title: entry.displayTitle || entry.title,
    caption: text.caption, artwork: runtime.artwork(entry.posterUrl), progress, watched: entry.watched, width, menuAnchorRef: anchor, selection,
    onNavigate: () => actions.open(entry), onPlay: entry.playback ? () => actions.play(entry) : undefined, onMore: () => actions.menu(entry, anchor, selection?.onEnter),
    moreExpanded: actions.isMenuOpen?.(entry)};
  return square ? <SquareCard {...props}/> : <PosterCard {...props}/>;
}

/** The frame's list row: art with its progress line, the title, what it is, the
 * library and its time left, and the row's own menu. */
function SavedResourceRow({row, width, runtime, actions, selection, library, onMore}: {
  row: Row; width: number; runtime: NativeShellRuntime; actions: NativeSavedEntryActions; selection?: CardSelection; library?: string; onMore: (anchor?: RefObject<View | null>) => void;
}) {
  const theme = useTheme(), anchor = useRef<View>(null), entry = row.media, square = ['album', 'song', 'book', 'audiobook_file'].includes(entry.kind);
  const text = homeCardText(entry, false, runtime.text);
  const watched = entry.watched === true, progress = entry.duration && entry.progressSeconds ? Math.min(1, entry.progressSeconds / entry.duration) : undefined;
  const remaining = entry.duration && entry.progressSeconds && !watched ? savedDuration(Math.max(0, entry.duration - entry.progressSeconds)) : undefined;
  return <CardFrame testID={`saved-row-${encodeURIComponent(row.id)}`} focusId={`saved-row-${row.id}`} title={entry.title} width={width} menuAnchorRef={anchor} selection={selection} onNavigate={() => actions.open(entry)}
    onPlay={entry.playback ? () => actions.play(entry) : undefined} onMore={() => onMore(anchor)} moreExpanded={actions.isMenuOpen?.(entry)}
    pressableStyle={{height: theme.isTV ? 104 : 80, borderRadius: theme.isTV ? 16 : 10, justifyContent: 'center'}}>
    {() => <View style={{height: theme.isTV ? 104 : 80, flexDirection: 'row', alignItems: 'center', gap: theme.isTV ? 28 : 12, paddingHorizontal: theme.isTV ? 20 : 4}}>
      {selection?.active && <SelectionCircle selected={selection.selected} selecting accessibilityLabel={t('next.selection.select')} interactive={false} variant="row"/>}
      <View style={{width: theme.isTV ? 76 : 48, height: theme.isTV ? 76 : square ? 56 : 72, alignItems: 'center', justifyContent: 'center'}}>
        <MediaArt kind={square ? 'square' : 'poster'} source={runtime.artwork(entry.posterUrl)} width={theme.isTV ? (square ? 76 : 51) : square ? 56 : 48}
          height={theme.isTV ? 76 : square ? 56 : 72} style={{borderRadius: theme.isTV ? 7 : 6}}/>
        {progress !== undefined && <View style={{position: 'absolute', left: theme.isTV ? 15 : 4, right: theme.isTV ? 15 : 4, bottom: theme.isTV ? 0 : 4, height: theme.isTV ? 5 : 3,
          borderRadius: 5, backgroundColor: alpha(theme.colors.ink, .7), overflow: 'hidden'}}><View style={{height: '100%', width: `${progress * 100}%`, backgroundColor: theme.colors.accentStrong}}/></View>}
      </View>
      <View style={{flex: 1, minWidth: 0, gap: 3}}>
        <Text numberOfLines={1} style={[browseType(theme, theme.isTV ? 28 : 15.5, 700, -.01), {color: theme.colors.text}]}>{entry.displayTitle || entry.title}</Text>
        <Parts parts={text.caption} style={[browseType(theme, theme.isTV ? 22 : 12.5, 400), {color: theme.colors.textTertiary}]}/>
        {(watched || remaining) && <Text numberOfLines={1} style={[browseType(theme, theme.isTV ? 22 : 12.5, 700), {color: watched ? theme.colors.textTertiary : theme.colors.accentStrong}]}>
          {watched ? t('saved.watched') : remaining}
        </Text>}
      </View>
      {theme.isTV && <Text numberOfLines={1} style={[browseType(theme, 22), {color: theme.colors.textTertiary, width: 360}]}>{library ?? ''}</Text>}
      {!theme.isTV && library && <Text numberOfLines={1} style={[browseType(theme, 12.5), {color: theme.colors.textTertiary, maxWidth: 96, textAlign: 'right'}]}>{library}</Text>}
      <Focusable testID={`saved-row-more-${encodeURIComponent(row.id)}`} focusId={`saved-row-more-${row.id}`} accessibilityRole="button" accessibilityLabel={t('card.more', {title: entry.title})}
        onPress={() => onMore(anchor)} style={{width: theme.isTV ? 56 : 40, height: theme.isTV ? 56 : 40, alignItems: 'center', justifyContent: 'center'}}>
        <Icon name="i-more" size={theme.isTV ? 30 : 20} color={theme.colors.textTertiary}/>
      </Focusable>
      <View pointerEvents="none" style={{position: 'absolute', left: theme.isTV ? 20 : 20, right: theme.isTV ? 20 : 4, bottom: 0, height: 1, backgroundColor: alpha(theme.colors.line, theme.isTV ? .55 : .3)}}/>
    </View>}
  </CardFrame>;
}
