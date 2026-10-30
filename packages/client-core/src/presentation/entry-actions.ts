import {encodedRoutePath} from "../../../contracts/src/route-paths.ts";
import {watchedWords} from './card-caption.ts';
/**
 * The entry actions menu (card "⋯", long press, right-click), one model for every client (CON-24).
 * Order and grouping follow menuSpec.entryGroups: play · together · personal · organize · open ·
 * manage, with the destructive action last. Labels are catalogue IDs and glyphs are icon-registry
 * IDs, so web and Apple say and draw the same thing (CON-16, CON-20, CON-22).
 *
 * The caller says what the server and device offer; the model never invents a capability.
 */
import type {IconId} from '../../../design/src/icons.ts';
import type {MessageId} from '../../../i18n/src/index.ts';

/**
 * Whether Refresh metadata can be offered on an entry. The server refreshes one playable title at
 * a time (a movie, an episode, a song, a book's file); it refuses a show, a season, an album, an
 * artist or a book as a whole, so the action is not offered there rather than offered and refused.
 */
export function canRefreshMetadata(kind: string | undefined): boolean {
  return kind === 'movie' || kind === 'episode' || kind === 'song' || kind === 'audiobook_file' || kind === 'extra';
}

/**
 * Where a refresh of this entry's metadata is asked for, or nothing when the server cannot
 * refresh that kind. A show has a route of its own; a playable title uses the item route. A
 * client offers Refresh metadata exactly where this answers.
 */
export function refreshMetadataPath(kind: string | undefined, id: string): string | undefined {
  if (kind === 'show') return encodedRoutePath("refresh_shows_metadata", {"id": encodeURIComponent(id)});
  return canRefreshMetadata(kind) ? encodedRoutePath("refresh_items_metadata", {"id": encodeURIComponent(id)}) : undefined;
}
export type EntryActionId =
  | 'play' | 'resume' | 'playFromStart' | 'playNext' | 'addToQueue'
  | 'watchWithGroup' | 'addToGroupQueue'
  | 'watchlist' | 'favorite' | 'watched' | 'removeFromContinueWatching' | 'notInterested' | 'rate'
  | 'addToPlaylist' | 'addToCollection' | 'download'
  | 'details'
  | 'editMetadata' | 'refreshMetadata' | 'separateArtist' | 'delete';

export type EntryActionGroupId = 'play' | 'together' | 'personal' | 'organize' | 'open' | 'manage';

export type EntryAction = Readonly<{
  id: EntryActionId;
  label: MessageId;
  icon: IconId;
  /** A toggle that is on (a favorite, on the Watchlist…): menus show a check or the filled glyph. */
  selected?: boolean;
  destructive?: boolean;
  /** Opens a further step (a picker, a dialog): "…" is already in the label. */
  furtherStep?: boolean;
}>;

export type EntryActionGroup = Readonly<{id: EntryActionGroupId; title?: MessageId; actions: readonly EntryAction[]}>;

/** What can be done with this entry, as the server's detail projection and the device report it. */
export type EntryCapabilities = Readonly<{
  /** Has something playable. */
  playable?: boolean;
  /** Seconds already watched; > 0 offers Resume and Play from beginning. */
  progressSeconds?: number;
  /** The device queue is available. */
  queue?: boolean;
  /** In a Watch Together group that lets this viewer add. */
  group?: boolean;
  /** Personal state for each personal action the server offers; omitted means not offered. */
  watchlisted?: boolean;
  favorite?: boolean;
  watched?: boolean;
  /** On the viewer's Continue Watching (started, not finished) and the server offers the watched
   * action, which clears the resume point (X-12). Omitted means not offered. */
  continueWatching?: boolean;
  /** A recommendation card: offers Not interested (the title leaves recommendations; Undo in the
   * notice). Only recommendation rows set it. */
  notInterested?: boolean;
  /** The viewer's star rating; `null` means rating is offered but none is set. Omitted means not offered. */
  rating?: number | null;
  addToPlaylist?: boolean;
  addToCollection?: boolean;
  /** `media` for a playable item, `container` for a show, season or album (Download all…). */
  download?: 'media' | 'container';
  /** The entry's kind, for how its played state is worded (a book is finished, music is played). */
  kind?: string;
  details?: boolean;
  editMetadata?: boolean;
  refreshMetadata?: boolean;
  /** An owner's "Not this artist…" on an album. */
  separateArtist?: boolean;
  delete?: boolean;
}>;

export function entryActions(c: EntryCapabilities): readonly EntryActionGroup[] {
  const resumes = !!c.playable && (c.progressSeconds ?? 0) > 0;
  const play: EntryAction[] = [];
  if (c.playable) {
    play.push({id: resumes ? 'resume' : 'play', label: resumes ? 'entry.resume' : 'entry.play', icon: 'play'});
    if (resumes) play.push({id: 'playFromStart', label: 'entry.playFromBeginning', icon: 'restart'});
    if (c.queue) {
      play.push({id: 'playNext', label: 'entry.playNext', icon: 'next'});
      play.push({id: 'addToQueue', label: 'entry.addToQueue', icon: 'queue'});
    }
  }
  const together: EntryAction[] = c.playable && c.group ? [
    {id: 'watchWithGroup', label: 'entry.watchWithGroup', icon: 'people'},
    {id: 'addToGroupQueue', label: 'entry.addToGroupQueue', icon: 'queue'},
  ] : [];
  const personal: EntryAction[] = [];
  if (c.watchlisted !== undefined) personal.push({id: 'watchlist', label: c.watchlisted ? 'entry.removeFromWatchlist' : 'entry.addToWatchlist', icon: c.watchlisted ? 'bookmarkFilled' : 'bookmark', selected: c.watchlisted});
  if (c.favorite !== undefined) personal.push({id: 'favorite', label: c.favorite ? 'entry.removeFromFavorites' : 'entry.addToFavorites', icon: c.favorite ? 'heartFilled' : 'heart', selected: c.favorite});
  if (c.watched !== undefined) personal.push({id: 'watched', label: c.watched ? watchedWords(c.kind).unmark : watchedWords(c.kind).mark, icon: c.watched ? 'eyeOff' : 'watched'});
  if (c.continueWatching) personal.push({id: 'removeFromContinueWatching', label: 'entry.removeFromContinueWatching', icon: 'close'});
  if (c.notInterested) personal.push({id: 'notInterested', label: 'entry.notInterested', icon: 'eyeOff'});
  if (c.rating !== undefined) personal.push({id: 'rate', label: c.rating != null ? 'title.rated' : 'entry.rate', icon: c.rating != null ? 'starFilled' : 'star', selected: c.rating != null, furtherStep: true});
  const organize: EntryAction[] = [];
  if (c.addToPlaylist) organize.push({id: 'addToPlaylist', label: 'entry.addToPlaylist', icon: 'listPlus', furtherStep: true});
  if (c.addToCollection) organize.push({id: 'addToCollection', label: 'entry.addToCollection', icon: 'collection', furtherStep: true});
  if (c.download) organize.push({id: 'download', label: c.download === 'container' ? 'entry.downloadAll' : 'entry.download', icon: 'download', furtherStep: true});
  const open: EntryAction[] = c.details ? [{id: 'details', label: 'action.details', icon: 'info'}] : [];
  const manage: EntryAction[] = [];
  if (c.editMetadata) manage.push({id: 'editMetadata', label: 'entry.editMetadata', icon: 'edit', furtherStep: true});
  if (c.refreshMetadata) manage.push({id: 'refreshMetadata', label: 'entry.refreshMetadata', icon: 'refresh'});
  if (c.separateArtist) manage.push({id: 'separateArtist', label: 'title.separateArtist.action', icon: 'link', furtherStep: true});
  if (c.delete) manage.push({id: 'delete', label: 'entry.delete', icon: 'trash', destructive: true, furtherStep: true});
  const groups: EntryActionGroup[] = [
    {id: 'play', actions: play},
    {id: 'together', title: 'entry.group.together', actions: together},
    {id: 'personal', actions: personal},
    {id: 'organize', actions: organize},
    {id: 'open', actions: open},
    {id: 'manage', title: 'entry.group.manage', actions: manage},
  ];
  return Object.freeze(groups.filter(g => g.actions.length));
}

/** The groups flattened, with the group each action starts (for menus that draw separators). */
export function entryActionList(groups: readonly EntryActionGroup[]): readonly (EntryAction & {group: EntryActionGroupId; groupStart: boolean; groupTitle?: MessageId})[] {
  return groups.flatMap(g => g.actions.map((a, i) => ({...a, group: g.id, groupStart: i === 0, ...(i === 0 && g.title ? {groupTitle: g.title} : {})})));
}
