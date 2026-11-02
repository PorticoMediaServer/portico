import type {DetailProjection} from '../../../../../packages/client-core/src/detail';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import {entryActions, type EntryActionGroup, type EntryCapabilities} from '../../../../../packages/client-core/src/presentation/entry-actions';
import type {NativeWatchMenuEntry} from './factory';

const containers = ['show', 'season', 'album', 'artist', 'book', 'collection', 'playlist'];
export type NativeWatchMenuInput = Readonly<{
  entry: ContentEntry;
  /** The title's own live Detail read; without it the menu keeps only its independent rows. */
  detail?: DetailProjection;
  menu?: NativeWatchMenuEntry;
  /** Watch Together rows (TV): the viewer may add to the group queue, and may also start a title for the group. */
  together?: Readonly<{queue: boolean; watch: boolean}>;
}>;

/** What the Watch menu can do with this title, in the shared model's terms (EntryCapabilities).
 * The same type Home's More menu feeds the same model, so one title offers one set of rows:
 * a row is offered exactly when the title's Detail grants it and the menu has an executor for it. */
export function nativeWatchMenuCapabilities({entry, detail, menu, together}: NativeWatchMenuInput): EntryCapabilities {
  const personal = detail?.personal, has = (id: string) => detail?.actions.some(action => action.id === id && action.enabled) === true;
  const playable = !!menu?.play && detail?.actions.some(action => action.enabled && action.playback && action.id !== 'start_over') === true;
  return Object.freeze({
    kind: entry.kind,
    playable,
    ...(playable ? {progressSeconds: personal?.progressSeconds ?? entry.progressSeconds ?? 0} : {}),
    queue: !!menu?.enqueue,
    group: together?.queue === true,
    ...(personal && has('watchlist') ? {watchlisted: personal.watchlisted} : {}),
    ...(personal && has('favorite') ? {favorite: personal.favorite} : {}),
    ...(personal && has('watched') ? {watched: personal.watched ?? entry.watched ?? false} : {}),
    ...(personal && menu?.rate && has('rating') ? {rating: personal.rating ?? null} : {}),
    addToPlaylist: !!menu?.addToPlaylist,
    addToCollection: !!menu?.addToCollection,
    ...(menu?.download ? {download: containers.includes(entry.kind) ? 'container' as const : 'media' as const} : {}),
    details: !!menu?.details,
    editMetadata: !!menu?.editMetadata,
    refreshMetadata: !!menu?.refreshMetadata,
    delete: !!menu?.deleteTitle,
  });
}

/** The shared model's groups for this menu, without a row this menu has no executor for. */
export function nativeWatchMenuGroups(input: NativeWatchMenuInput): readonly EntryActionGroup[] {
  const groups = entryActions(nativeWatchMenuCapabilities(input)), menu = input.menu;
  const offered = (id: string) => id === 'playFromStart' ? !!menu?.playFromStart
    : id === 'watchWithGroup' ? input.together?.watch === true : id === 'addToGroupQueue' ? input.together?.queue === true : true;
  return Object.freeze(groups.map(group => ({...group, actions: group.actions.filter(action => offered(action.id))})).filter(group => group.actions.length));
}
