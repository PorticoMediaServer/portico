import {watchedWords} from './card-caption.ts';
import {serviceText} from './service-text.ts';
/**
 * The detail page actions (CON-25): one model for web, iPhone/iPad and TV.
 * The same title shows the same actions in the same places: Play/Resume
 * primary with the remaining time, then Watchlist · Favorite · Watched as
 * toggles (the row every title has), and everything else in More, grouped the way the shared title ⋯ menu
 * groups it (entryActions). Built from the detail projection's
 * server-provided actions, as the pages do today. No React, no platform APIs.
 */
import type {IconId} from '../../../design/src/icons.ts';
import type {MessageId} from '../../../i18n/src/index.ts';
import type {DetailProjection} from '../detail.ts';
import {entryActions, type EntryActionGroup} from './entry-actions.ts';
import {formatDuration} from './content.ts';

export type DetailViewer = Readonly<{
  owner?: boolean;
  local?: boolean;
  /** TV drops Download and Manage (CON-24). */
  platform?: 'web' | 'phone' | 'tv';
  /**
   * What the title's card menu also knows, so the page offers the same actions with the same
   * answers (a client that leaves one out simply does not offer that action on the page):
   * a play queue to add to; a Watch Together group this viewer may add to; whether this server
   * and this title can be downloaded at all (`false` hides Download; absent keeps it offered);
   * and whether this viewer may delete the title.
   */
  queue?: boolean;
  group?: boolean;
  downloads?: boolean;
  canDelete?: boolean;
}>;

export type DetailPrimary = Readonly<{
  id: 'play' | 'resume';
  label: MessageId;
  icon: IconId;
  /** "42m", "1h 20m" — the pages render it as the caption ("42m left"). */
  remaining?: string;
  /** "42m left" — the caption itself. */
  caption?: string;
  playback: Readonly<{itemId: string; startSeconds: number}>;
}>;

export type DetailQuick = Readonly<{
  id: 'watchlist' | 'favorite' | 'watched';
  label: MessageId;
  icon: IconId;
  selected: boolean;
}>;

export type DetailActions = Readonly<{
  primary?: DetailPrimary;
  quick: readonly DetailQuick[];
  more: readonly EntryActionGroup[];
}>;

type DetailInput = Pick<DetailProjection, 'item' | 'personal' | 'actions'>;

const EDITABLE_KINDS = new Set([
  'movie',
  'episode',
  'song',
  'audiobook_file',
  'show',
  'season',
  'album',
  'artist',
  'book',
]);

export function detailActions(detail: DetailInput, viewer?: DetailViewer): DetailActions {
  const has = (id: string) => detail.actions.some(a => a.id === id && a.enabled);
  const play = detail.actions.find(a => a.enabled && a.playback && a.id !== 'start_over');
  const personal = detail.personal;
  const item = detail.item;
  const resume = (item.progressSeconds ?? 0) > 0 && (play?.playback?.startSeconds ?? 0) > 0;
  const remaining = resume && item.duration ? formatDuration(Math.max(0, item.duration - (item.progressSeconds ?? 0))) : '';
  const primary: DetailPrimary | undefined = play?.playback
    ? resume
      ? {
        id: 'resume',
        label: 'entry.resume',
        icon: 'play',
        ...(remaining ? {remaining, caption: serviceText('duration.remaining', {duration: remaining})} : {}),
        playback: play.playback,
      }
      : {id: 'play', label: 'entry.play', icon: 'play', playback: play.playback}
    : undefined;
  const quick: DetailQuick[] = [];
  if (has('watchlist')) {
    quick.push({
      id: 'watchlist',
      label: personal.watchlisted ? 'entry.removeFromWatchlist' : 'entry.addToWatchlist',
      icon: personal.watchlisted ? 'bookmarkFilled' : 'bookmark',
      selected: !!personal.watchlisted,
    });
  }
  if (has('favorite')) {
    quick.push({
      id: 'favorite',
      label: personal.favorite ? 'entry.removeFromFavorites' : 'entry.addToFavorites',
      icon: personal.favorite ? 'heartFilled' : 'heart',
      selected: !!personal.favorite,
    });
  }
  if (has('watched')) {
    quick.push({
      id: 'watched',
      label: personal.watched ? watchedWords(item.kind).unmark : watchedWords(item.kind).mark,
      icon: personal.watched ? 'watched' : 'check',
      selected: !!personal.watched,
    });
  }
  const isTV = viewer?.platform === 'tv';
  const canEdit = !!viewer?.owner && !!viewer?.local && !isTV && !!item.libraryId && EDITABLE_KINDS.has(item.kind);
  const hasRating = has('rating');
  const rating = detail.personal?.rating ?? null;
  const groups = entryActions({
    playable: !!play?.playback,
    progressSeconds: resume ? item.progressSeconds : 0,
    rating: hasRating ? rating : undefined,
    addToPlaylist: has('add_to_playlist'),
    addToCollection: has('add_to_collection'),
    queue: !!viewer?.queue,
    group: !!viewer?.group,
    download: !isTV && play?.playback && viewer?.downloads !== false ? 'media' : undefined,
    editMetadata: canEdit,
    refreshMetadata: canEdit,
    delete: !isTV && !!viewer?.canDelete && !!play?.playback,
  });
  const hidden = new Set(['play', 'resume', 'watchlist', 'favorite']);
  const more = groups
    .map(g => ({...g, actions: g.actions.filter(a => !hidden.has(a.id))}))
    .filter(g => g.actions.length);
  return Object.freeze({...(primary ? {primary} : {}), quick: Object.freeze(quick), more: Object.freeze(more)});
}
