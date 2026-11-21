import type React from 'react';
import {useCallback, useLayoutEffect, useMemo, useRef, useState} from 'react';
import type {QueuePlaybackView, QueuePlaybackWindow} from '../../../../../../packages/client-core/src/queue-controller';
import type {QueueEntry, PlaybackFence} from '../../../../../../packages/player-core/src';
import type {V1QueueEditIntent} from '../../../../../../packages/client-core/src/playback-v1/v1-queue-player';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {durationText} from '../../../../../../packages/client-core/src/presentation/kit-model/text';
import type {Artwork} from '../../../kit/artwork';
import type {FocusEdges} from '../../../kit/focus';
import type {NowPlayingScreenProps} from './audio-player';
import {useTVQueueSongMenu} from './useTVQueueSongMenu';
import {useStableFocusOrder} from '../../../kit/focus/useStableFocusOrder';
import type {MenuEntry} from '../../../kit/overlays';
import {committedPaintScope, useCommittedPaintPublication} from '../../../kit/overlays/CommittedPaint';
import type {ListeningQueuePaintData, ListeningQueuePaintRow} from '../../../kit/player/ListeningPanePaint';

const t = defaultI18n.t;
/** A queue run survives track fences but never another authority/run. */
export const nowPlayingQueueFocusScope = (fence: PlaybackFence) => JSON.stringify(fence.playback);

/** The listening owner's own queue, drawn: the entries the engine will play after
 * the one playing. Order, availability and durations are read from
 * `QueuePlaybackView`; only the drawn order is retained while native focus owns it.
 * The engine remains the sole queue owner. A row plays only through the queue's real command. */
export type NowPlayingQueueProps = Readonly<{
  view: QueuePlaybackView;
  /** Touch publisher only; its pure display leaf lives in the retained page. */
  paintOwner?: object;
  paintScope?: string;
  /** tablet-play9: this pane is the right column, 366 pt; narrow windows stack it.
   * The host passes its own artwork resolver so the TV rows can show the covers. */
  onPlay?: (queueId: string, entryId: string, revision: string) => void;
  artwork?: (path: string | undefined) => React.ComponentProps<typeof Artwork>['source'];
  /** tv#7: the host owns this pane's place in the player's focus composition. It names the one
   * region the rows live in and gives that region the parent edges (Right from the transport,
   * progress line and signal line arrives here). The region's entry is this queue's remembered
   * row, or the first row it actually renders, so entering the region lands there; nothing here
   * asks for focus on mount, so the player's own Pause keeps it. */
  regionId?: string;
  /** The actual listening fence, so no order can cross a viewer/playback lifetime. */
  focusScopeKey?: string;
  interactive?: boolean;
  menuScope?: Readonly<{fenceKey: string; lease?: object}>;
  edges?: FocusEdges;
  /** The guarded queue edit. `entry` is the accepted window's own fact for a row; without it
   * no row advertises an edit, so a drawn slot is never mistaken for admission. */
  queueEdit?: Readonly<{enabled: boolean; lease?: object; fenceKey: string;
    entry: (entryId: string) => Readonly<{position?: number; available?: boolean; segmentId?: string}> | undefined;
    apply: (intent: V1QueueEditIntent, queueId: string, expectedRevision: string) => void}>;
  testID?: string;
}>;

export type NowPlayingQueueRow = Readonly<{entryId: string; title: string; artist?: string; detail?: readonly string[]; posterUrl?: string; seconds?: number; current: boolean;
  position?: number; available?: boolean; segmentId?: string}>;
/** Everything one open sheet may act on; never read from a later render. */
type Row = NowPlayingQueueRow;
const queueRowKey = (row: Row) => row.entryId;
type RowOpening = Readonly<{row: Row; previous?: Row; next?: Row; queueId: string; revision: string; fenceKey: string; lease?: object;
  apply: (intent: V1QueueEditIntent, queueId: string, expectedRevision: string) => void}>;

const visibleEntries = (view: QueuePlaybackView): readonly Extract<QueueEntry, {hidden: false}>[] =>
  view.queue.entries.filter((entry): entry is Extract<QueueEntry, {hidden: false}> => !entry.hidden && !entry.removed);

/** The accepted window only when it is exactly the queue this view draws and each row appears
 * once; any mismatch or ambiguity leaves every fact unknown rather than borrowing a neighbour. */
export function nowPlayingQueueWindow(view: QueuePlaybackView): QueuePlaybackWindow | undefined {
  const window = view.window;
  if (!window || window.queueId !== view.queue.id || window.revision !== view.queue.revision
    || new Set(window.entries.map(entry => entry.entryId)).size !== window.entries.length) return;
  return window;
}

/** One row's own window facts, only when that entry id is unambiguous there. */
export function nowPlayingQueueEntry(window: QueuePlaybackWindow | undefined, entryId: string) {
  if (!window) return;
  const rows = window.entries.filter(entry => entry.entryId === entryId);
  return rows.length === 1 ? rows[0] : undefined;
}

/** The accepted queue window is the source of the playback context. */
export function nowPlayingSource(view: QueuePlaybackView | undefined): string | undefined {
  if (!view) return undefined;
  const window = nowPlayingQueueWindow(view);
  const entryId = view.queue.currentEntryId;
  const facts = entryId ? nowPlayingQueueEntry(window, entryId) : undefined;
  if (facts?.available) {
    const label = window?.segments.find(segment => segment.id === facts.segmentId && segment.label)?.label;
    if (label) return label;
  }
  const entry = view.queue.entries.find(item => item.id === entryId);
  const track = view.items.find(item => item.entryId === entryId);
  if (!entry || entry.hidden || entry.removed || !track || entry.itemId !== track.itemId) return;
  // The rebuilt queue projects plain item entries for album playback. The album on the
  // current entry is still a server fact. Named segments, such as playlists, take precedence.
  if (entry.sourceContext.kind === 'item' && !view.queue.radio || entry.sourceContext.kind === 'album' && entry.sourceContext.id === track.albumId) return track.album;
}

export function nowPlayingQueueRows(view: QueuePlaybackView, options: Readonly<{includePlayed?: boolean}> = {}): readonly Row[] {
  const entries = visibleEntries(view), window = nowPlayingQueueWindow(view);
  const at = entries.findIndex(entry => entry.id === view.queue.currentEntryId);
  // Up next is what the engine will actually play after this entry, in queue
  // order; entries already played are not repeated by wrapping the list.
  const ordered = !options.includePlayed && at >= 0 ? entries.slice(at) : entries;
  return ordered.flatMap(entry => {
    const item = view.items.find(value => value.entryId === entry.id);
    if (!item) return [];
    const detail = [item.artist, item.album && item.album !== item.title ? item.album : undefined]
      .filter((value): value is string => !!value);
    const seconds = item.durationMs !== undefined && item.durationMs > 0 ? item.durationMs / 1000 : undefined;
    // Position, availability and source come from the accepted window's own row, never from the
    // drawn array's index or from visible/not-removed; a missing fact stays unknown.
    const facts = nowPlayingQueueEntry(window, entry.id);
    return [{entryId: entry.id, title: item.title, ...(item.artist ? {artist: item.artist} : {}), ...(detail.length ? {detail} : {}), ...(item.posterUrl ? {posterUrl: item.posterUrl} : {}), ...(seconds ? {seconds} : {}),
      current: entry.id === view.queue.currentEntryId, ...(facts?.position !== undefined ? {position: facts.position} : {}),
      ...(facts?.available !== undefined ? {available: facts.available} : {}), ...(facts?.segmentId ? {segmentId: facts.segmentId} : {})}];
  });
}

/** The same accepted-window binding the touch presenter already uses; no command guard changes. */
export function nowPlayingQueueEditPort(view: QueuePlaybackView | undefined, fence: PlaybackFence, edit: NowPlayingScreenProps['queueEdit']): NowPlayingQueueProps['queueEdit'] {
  const window = view ? nowPlayingQueueWindow(view) : undefined;
  return edit?.enabled && window ? {enabled: true, lease: edit.lease, fenceKey: JSON.stringify(fence),
    entry: entryId => nowPlayingQueueEntry(window, entryId),
    apply: (intent, queueId, revision) => edit.apply(fence, intent, queueId, revision)} : undefined;
}

/** tv#7: bounded memory belongs to the actual queue run, including its viewer scope. */
const rememberedRow = new Map<string, string>();

export function useNowPlayingQueue({view, onPlay, artwork, regionId, focusScopeKey, interactive = true, menuScope, queueEdit, paintOwner, paintScope = '', testID = 'now-playing-queue'}: NowPlayingQueueProps, tv: boolean) {
  const rows = nowPlayingQueueRows(view), [rowMenu, setRowMenu] = useState<RowOpening>(), rowMenuRef = useRef<RowOpening | undefined>(undefined);
  const current = rows.filter(row => row.current), upcoming = rows.filter(row => !row.current), window = nowPlayingQueueWindow(view);
  // Only a real segment label names a run; an unlabeled run keeps the plain Up next heading
  // rather than inventing a source or borrowing the album the song happens to carry.
  const labels = new Map((window?.segments ?? []).flatMap(segment => segment.label ? [[segment.id, segment.label] as const] : []));
  const groups: {key: string; label: string; rows: Row[]}[] = [];
  for (const value of upcoming) {
    const source = value.segmentId ? labels.get(value.segmentId) : undefined, key = source ?? '', last = groups.at(-1);
    if (last && last.key === key) last.rows.push(value);
    else groups.push({key, label: source ?? t('player.queue'), rows: [value]});
  }
  const focusScope = useMemo(() => ({queueId: view.queue.id, focusScopeKey, regionId}), [view.queue.id, focusScopeKey, regionId]);
  const stable = useStableFocusOrder(queueRowKey, {regionId: tv ? regionId : undefined, scope: focusScope,
    items: upcoming, allItems: tv ? nowPlayingQueueRows(view, {includePlayed: true}) : rows,
    memberIds: new Set(visibleEntries(view).map(entry => entry.id))});
  const tvUpcoming = stable.map(row => row.current === (row.entryId === view.queue.currentEntryId) ? row : {...row, current: row.entryId === view.queue.currentEntryId});
  const songMenu = useTVQueueSongMenu({enabled: tv && interactive, queueId: view.queue.id, revision: view.queue.revision,
    context: focusScopeKey && menuScope ? {...menuScope, scopeKey: focusScopeKey} : undefined,
    upcomingIds: new Set(upcoming.map(row => row.entryId)), entry: entryId => nowPlayingQueueEntry(window, entryId),
    onPlay, edit: queueEdit});
  const count = view.total ?? rows.length;
  const seconds = view.durationKnown === false || view.totalDurationMs === undefined ? undefined : view.totalDurationMs / 1000;
  const summary = [t('title.songCount', {count}), durationText(seconds, t)].filter((value): value is string => !!value);
  const canPlay = typeof onPlay === 'function', editable = !!queueEdit?.enabled;
  // The live ref is the opening's authority: a retained callback may only act while it is still
  // that exact opening, so Cancel or a successor on the same view/fence/lease cannot be used.
  const retire = useCallback((opening: RowOpening) => {
    if (rowMenuRef.current === opening) rowMenuRef.current = undefined;
    setRowMenu(current => current === opening ? undefined : current);
  }, []);
  // Layout retirement for a changed view, fence or lease; the ref and the sheet leave together.
  useLayoutEffect(() => {
    const opening = rowMenuRef.current;
    if (!opening) return;
    if (!queueEdit?.enabled || queueEdit.fenceKey !== opening.fenceKey || (queueEdit.lease ?? undefined) !== opening.lease
      || view.queue.id !== opening.queueId || view.queue.revision !== opening.revision) retire(opening);
  }, [queueEdit?.enabled, queueEdit?.fenceKey, queueEdit?.lease, view.queue.id, view.queue.revision, retire]);
  useLayoutEffect(() => () => {rowMenuRef.current = undefined;}, []);
  const openMenu = (value: Row) => {
    if (!editable || !queueEdit || value.current || !upcoming.some(entry => entry.entryId === value.entryId)) return;
    // Only the accepted window admits a row and proves its neighbour: a visible slot or an
    // adjacent drawn array index is neither admission nor contiguity and can cross a gap.
    const facts = queueEdit.entry(value.entryId);
    if (!facts || facts.available !== true || facts.position === undefined) return;
    const position = facts.position, at = upcoming.findIndex(entry => entry.entryId === value.entryId);
    const neighbour = (offset: -1 | 1) => {
      const row = at >= 0 ? upcoming[at + offset] : undefined, other = row && queueEdit.entry(row.entryId);
      return row && other?.available === true && other.position === position + offset ? row : undefined;
    };
    const previous = neighbour(-1), next = neighbour(1);
    const opening: RowOpening = {row: value, ...(previous ? {previous} : {}), ...(next ? {next} : {}), queueId: view.queue.id, revision: view.queue.revision,
      fenceKey: queueEdit.fenceKey, ...(queueEdit.lease ? {lease: queueEdit.lease} : {}), apply: queueEdit.apply};
    rowMenuRef.current = opening;
    setRowMenu(opening);
  };
  // Disabled is presentation; each press also proves its captured neighbour is still the one
  // adjacent loaded position before it asks for anything.
  const menuItems = (opening: RowOpening): readonly MenuEntry[] => {
    const canUp = !!opening.previous, canDown = !!opening.next;
    return [
      {id: 'move-up', label: t('web.profile.moveUp'), disabled: !canUp, onSelect: () => {
        if (rowMenuRef.current !== opening) return;
        const previous = opening.previous;
        if (!previous) return;
        opening.apply({action: 'reorder', entryIds: [opening.row.entryId, previous.entryId]}, opening.queueId, opening.revision);
      }},
      {id: 'move-down', label: t('web.profile.moveDown'), disabled: !canDown, onSelect: () => {
        if (rowMenuRef.current !== opening) return;
        const next = opening.next;
        if (!next) return;
        opening.apply({action: 'reorder', entryIds: [next.entryId, opening.row.entryId]}, opening.queueId, opening.revision);
      }},
      {id: 'remove', label: t('web.together.removeFromQueue'), destructive: true,
        onSelect: () => {if (rowMenuRef.current !== opening) return; opening.apply({action: 'remove', entryId: opening.row.entryId}, opening.queueId, opening.revision);}},
    ];
  };
  const memoryKey = JSON.stringify([view.queue.id, focusScopeKey ?? queueEdit?.fenceKey ?? regionId ?? testID]);
  const remember = (entryId: string) => {
    const queueId = memoryKey;
    if (!rememberedRow.has(queueId) && rememberedRow.size >= 8) rememberedRow.delete(rememberedRow.keys().next().value!);
    rememberedRow.set(queueId, entryId);
  };
  const paintSemantic = useMemo(() => committedPaintScope(paintOwner, paintScope, view.queue.id, view.queue.revision), [paintOwner, paintScope, view.queue.id, view.queue.revision]);
  const displayRow = (value: Row): ListeningQueuePaintRow => {
    const facts = value.current ? undefined : queueEdit?.entry(value.entryId);
    return {id: value.entryId, title: value.title, detail: value.detail, seconds: value.seconds, current: value.current,
      source: artwork?.(value.posterUrl) ?? undefined, playable: canPlay && !value.current,
      menu: editable && !!facts && facts.available === true && facts.position !== undefined};
  };
  const display: ListeningQueuePaintData | undefined = tv ? undefined : {
    current: current.map(displayRow), groups: groups.map(group => ({key: group.key, label: group.label, rows: group.rows.map(displayRow)})), summary,
  };
  const paintActions = {play: canPlay ? (id: string) => onPlay!(view.queue.id, id, view.queue.revision) : undefined,
    menu: (id: string) => {const value = rows.find(row => row.entryId === id);if (value) openMenu(value);}};
  useCommittedPaintPublication(paintOwner, 'listening-queue', paintScope, paintSemantic,
    display && paintOwner ? {data: display, actions: paintActions} : undefined);
  const complete = view.queue.entries.every(entry => !entry.hidden) && view.durationKnown !== false;
  const remaining = view.remainingCount ?? (complete ? upcoming.length : undefined);
  const upcomingSeconds = complete && upcoming.length > 0 && upcoming.every(row => row.seconds !== undefined)
    ? upcoming.reduce((total, row) => total + (row.seconds ?? 0), 0) : undefined;
  const tvSummary = [remaining === undefined ? undefined : t('title.songCount', {count: remaining}),
    upcomingSeconds === undefined ? undefined : durationText(upcomingSeconds, t)].filter((value): value is string => !!value);
  // Known read-only rows remain native stops; their real command admission is independent.
  const remembered = rememberedRow.get(memoryKey);
  const entry = remembered && tvUpcoming.some(row => row.entryId === remembered) ? remembered : tvUpcoming[0]?.entryId;
  return {display, paintActions, rowMenu, retire, menuItems, tvUpcoming, tvSummary, entry, songMenu, remember, canPlay,
    play: (entryId: string) => onPlay?.(view.queue.id, entryId, view.queue.revision), labels};
}
