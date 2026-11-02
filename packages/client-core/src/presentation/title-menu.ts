/**
 * The title action menu on screens that are not Home (FX-S3, FX-S4): a library's Browse and
 * Discover rows, Search results, and the rows on a detail page. They all build their menu from
 * `entryActions`, so every surface offers the same actions in the same order. This file only
 * decides what an entry can do there; it never invents a grant.
 *
 * Two sources, never mixed: an entry that carries the server's `homeActions` is read through
 * `homeEntryCapabilities`; any other title is read from its Detail when the menu opens, by its
 * enabled Detail actions and personal state.
 */
import type {HomeActionPlatform} from '../home-selection.ts';
import {canRefreshMetadata, entryActions, refreshMetadataPath, type EntryActionGroup, type EntryActionId, type EntryCapabilities} from './entry-actions.ts';

/** The platform facts these web surfaces pass to `homeEntryCapabilities`. Playback is the surface's own; there is
 * no queue, Watch Together or download on a screen that is not Home, so none is offered. */
export const titleMenuPlatform: HomeActionPlatform = Object.freeze({platform: 'web', playbackAvailable: true, queueAvailable: false});

/** The titles the shell's playlist, collection, delete and metadata editors can act on one at a time. */
const leafKinds: ReadonlySet<string> = new Set(['movie', 'episode', 'song', 'audiobook_file']);
/** The containers whose personal state and metadata the server reads as one aggregate. */
const aggregateKinds: ReadonlySet<string> = new Set(['show', 'season', 'album', 'artist', 'book']);
export const isLeafKind = (kind: string | undefined): boolean => kind !== undefined && leafKinds.has(kind);
export const isAggregateKind = (kind: string | undefined): boolean => kind !== undefined && aggregateKinds.has(kind);

/** What a Detail read says about one title. */
export type TitleDetailFacts = Readonly<{
  kind: string;
  available?: boolean;
  actions: readonly Readonly<{id: string; enabled: boolean; playback?: Readonly<{itemId: string; startSeconds: number}>}>[];
  personal: Readonly<{watchlisted: boolean; favorite: boolean; watched: boolean; rating: number | null}>;
}>;

/** A title's Detail, as menu capabilities: each enabled Detail action is one row, personal state decides its wording. */
export function detailMenuCapabilities(detail: TitleDetailFacts): EntryCapabilities {
  const enabled = (id: string) => detail.actions.some(action => action.id === id && action.enabled);
  const start = detail.actions.find(action => action.enabled && (action.id === 'resume' || action.id === 'play') && action.playback);
  const progress = detail.actions.find(action => action.enabled && action.id === 'resume' && action.playback)?.playback?.startSeconds;
  return Object.freeze({
    kind: detail.kind,
    playable: detail.available === true && !!start,
    ...(progress !== undefined && progress > 0 ? {progressSeconds: progress} : {}),
    ...(enabled('watchlist') ? {watchlisted: detail.personal.watchlisted} : {}),
    ...(enabled('favorite') ? {favorite: detail.personal.favorite} : {}),
    ...(enabled('watched') ? {watched: detail.personal.watched} : {}),
    ...(enabled('rating') ? {rating: detail.personal.rating} : {}),
    addToPlaylist: enabled('add_to_playlist'),
    addToCollection: enabled('add_to_collection'),
    details: true,
    editMetadata: enabled('metadata_edit'),
    refreshMetadata: canRefreshMetadata(detail.kind),
    delete: enabled('delete'),
  });
}

/** What the opening screen can really carry out. A row without a port behind it is not drawn. */
export type TitleMenuPorts = Readonly<{
  play: boolean;
  /** The shell's playlist and collection pickers. */
  playlist: boolean;
  collection: boolean;
  /** The screen's metadata editor, which is offered only to a local owner. */
  edit: boolean;
  delete: boolean;
  /** A local owner may ask for a refresh. */
  refresh: boolean;
  /** The rating dialog reads the title's Detail, which needs its library. */
  rate: boolean;
}>;

/** Capabilities, narrowed to what this screen's ports carry out. Details is always offered. */
export function limitMenuCapabilities(caps: EntryCapabilities, ports: TitleMenuPorts): EntryCapabilities {
  const kind = caps.kind, leaf = isLeafKind(kind);
  // No queue, Watch Together, download, Continue Watching, recommendation or artist-separation row off Home.
  const next: {-readonly [K in keyof EntryCapabilities]: EntryCapabilities[K]} = {...caps};
  for (const key of ['queue', 'group', 'download', 'continueWatching', 'notInterested', 'separateArtist'] as const) delete next[key];
  // A leaf's personal state goes through its Detail, a show, season, album, artist or book's through the container
  // state; a collection or playlist has neither, and Rate reads the title's own Detail.
  if (!leaf && !isAggregateKind(kind)) { delete next.watchlisted; delete next.favorite; delete next.watched; }
  if (!ports.rate || !leaf) delete next.rating;
  return Object.freeze({
    ...next,
    playable: !!caps.playable && ports.play,
    addToPlaylist: !!caps.addToPlaylist && ports.playlist && leaf,
    addToCollection: !!caps.addToCollection && ports.collection && leaf,
    editMetadata: !!caps.editMetadata && ports.edit && (leaf || isAggregateKind(kind)),
    refreshMetadata: !!caps.refreshMetadata && ports.refresh && refreshMetadataPath(kind, 'x') !== undefined,
    delete: !!caps.delete && ports.delete && leaf,
    details: true,
  });
}

/** Rows no screen but Home can carry out: Play from beginning needs the playback target of the opening title. */
const homeOnly: ReadonlySet<EntryActionId> = new Set(['playFromStart']);

/** The menu's groups for one entry: the one shared model, minus the rows only Home can do. */
export function titleMenuGroups(caps: EntryCapabilities): readonly EntryActionGroup[] {
  return Object.freeze(entryActions(caps).map(group => ({...group, actions: group.actions.filter(action => !homeOnly.has(action.id))})).filter(group => group.actions.length));
}
