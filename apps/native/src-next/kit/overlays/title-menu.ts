import {defaultI18n} from '../../../../../packages/i18n/src';
import type {EntryActionGroup, EntryActionId} from '../../../../../packages/client-core/src/presentation/entry-actions';
import type {IconName} from '../Icon';
import type {MenuAction, MenuEntry} from './types';

/**
 * The title action menu's rows, drawn once (FX-S3, FX-S4). Home's More menu and the
 * Watch menu behind every other card (Library, Discover, Search, Saved, Collections,
 * Person, Genre, TV music, TV episodes, detail rows) both turn the shared model's
 * groups (client-core entryActions) into rows here, so the labels, glyphs, sections,
 * owner group and destructive Delete cannot drift between them. A host keeps its own
 * container (touch popover or sheet, TV side sheet) and its own surface rows
 * (Select, File info, Play a different version…, See all).
 */
export const titleMenuIcons: Readonly<Record<EntryActionId, IconName>> = Object.freeze({play: 'i-play', resume: 'i-play', playFromStart: 'i-restart', playNext: 'i-listplus', addToQueue: 'i-queue',
  watchWithGroup: 'i-people', addToGroupQueue: 'i-queue', watchlist: 'i-bookmark', favorite: 'i-heart', watched: 'i-check', removeFromContinueWatching: 'i-x', notInterested: 'i-eyeoff', rate: 'i-star',
  addToPlaylist: 'i-listplus', addToCollection: 'i-layers', download: 'i-download', details: 'i-info', editMetadata: 'i-edit', refreshMetadata: 'i-refresh', separateArtist: 'i-link', delete: 'i-trash'});

/** Phone and tablet draw one flat list in the mock-ups' order (select.html 17, tablet.html 9):
 * Play, My List, Watched, Favorites, Rate, playlist, collection, Download. Queue, Watch Together
 * and Play from beginning belong to the TV side sheet. */
const touchOrder: readonly EntryActionId[] = ['play', 'resume', 'watchlist', 'watched', 'favorite', 'rate', 'addToPlaylist', 'addToCollection', 'download', 'removeFromContinueWatching', 'notInterested'];
const ownerOrder: readonly EntryActionId[] = ['editMetadata', 'refreshMetadata', 'delete'];
const personalRows: ReadonlySet<EntryActionId> = new Set<EntryActionId>(['watchlist', 'favorite', 'watched']);

export type TitleMenuSurface = Readonly<{
  /** The TV side sheet keeps the model's groups; touch draws the flat mock-up list. */
  tv: boolean;
  /** Runs one model action for the menu that drew it; the host fences it. */
  run: (id: EntryActionId) => void;
  /** Personal toggles wait while a write is in flight. */
  busy?: boolean;
  /** Rows directly after Play (music: Start radio). */
  afterPlay?: readonly MenuAction[];
  /** TV only: rows that go with the personal and organize rows, above the separator (Go to season). */
  related?: readonly MenuAction[];
  /** The host's own rows in the order it draws them: Play a different version…, File info, Select… */
  surface?: readonly MenuAction[];
  /** TV only: rows that sit right after Details (tv-more.html 2: "Select, Details, File info"; tv-detail.html 7: "Play a different version…" directly above File info). Without a Details row they follow the surface rows. */
  afterDetails?: readonly MenuAction[];
  /** Rows after Details: a screen's own extras, and See all on TV. */
  trailing?: readonly MenuAction[];
}>;

const t = defaultI18n.t;
const isRow = (item: MenuEntry): item is MenuAction => !('separator' in item) && !('section' in item);

/** One model action as a menu row. */
export function titleMenuRow(action: EntryActionGroup['actions'][number], surface: Pick<TitleMenuSurface, 'tv' | 'run' | 'busy'>, owner: boolean): MenuAction {
  return {id: action.id, label: t(!surface.tv && action.id === 'resume' ? 'entry.play' : !surface.tv && action.id === 'rate' ? 'entry.rate' : action.label),
    icon: titleMenuIcons[action.id], selected: action.selected, destructive: action.destructive, tone: owner ? 'owner' : 'default',
    ...(surface.busy && personalRows.has(action.id) ? {disabled: true} : {}), onSelect: () => surface.run(action.id)};
}

/** The menu's rows for the shared model's groups. */
export function titleMenuItems(groups: readonly EntryActionGroup[], surface: TitleMenuSurface): MenuEntry[] {
  const own = surface.surface ?? [], after = surface.trailing ?? [];
  if (!surface.tv) {
    const rows = groups.flatMap(group => group.actions.map(action => titleMenuRow(action, surface, group.id === 'manage')));
    const regular = touchOrder.flatMap(id => rows.filter(row => row.id === id));
    const play = regular.findIndex(row => row.id === 'play' || row.id === 'resume');
    if (play >= 0) regular.splice(play + 1, 0, ...(surface.afterPlay ?? []));
    else regular.unshift(...(surface.afterPlay ?? []));
    const owner = ownerOrder.flatMap(id => rows.filter(row => row.id === id));
    return [...regular, ...own, ...rows.filter(row => row.id === 'details'), ...after,
      ...(owner.length ? [{id: 'group:manage', section: true as const, label: t('next.selection.owner'), tone: 'owner' as const}, ...owner] : [])];
  }
  const items: MenuEntry[] = [];
  for (const group of groups) {
    if (items.length) items.push({id: `separator:${group.id}`, separator: true});
    if (group.title) items.push({id: `group:${group.id}`, section: true, label: t(group.title), tone: group.id === 'manage' ? 'owner' : 'default'});
    for (const action of group.actions) {
      items.push(titleMenuRow(action, surface, group.id === 'manage'));
      if (action.id === 'play' || action.id === 'resume') items.push(...(surface.afterPlay ?? []));
    }
  }
  // Below the personal and organize rows, above Details and the owner group (select.html 23).
  const boundary = () => {
    const at = items.findIndex(item => ['separator:open', 'details', 'separator:manage', 'group:manage'].includes(item.id));
    return at < 0 ? items.length : at;
  };
  if (surface.related?.length) items.splice(boundary(), 0, ...surface.related);
  const tail = surface.afterDetails ?? [], details = items.findIndex(item => item.id === 'details');
  // Without a Details row the tail follows the surface rows, under the same separator.
  const drawn = details >= 0 ? own : [...own, ...tail];
  if (drawn.length) {
    const at = boundary();
    items.splice(at, 0, ...(at > 0 ? [{id: 'surface-separator', separator: true as const}] : []), ...drawn);
  }
  if (details >= 0 && tail.length) items.splice(items.findIndex(item => item.id === 'details') + 1, 0, ...tail);
  items.push(...after);
  return items;
}

/** The actions a built menu offers, for hosts and tests that compare menus row for row. */
export function titleMenuRowIds(items: readonly MenuEntry[]): string[] {return items.filter(isRow).map(item => item.id);}
