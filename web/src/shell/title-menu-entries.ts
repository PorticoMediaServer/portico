import {defaultI18n} from '@i18n';
import type {EntryActionGroup, EntryActionId} from '@core/presentation/entry-actions.ts';
import type {MenuEntry} from '../kit/overlays';
import type {IconName} from '../kit/Icon';

const t = defaultI18n.t;

/** One glyph per action, for every surface's title menu. */
export const menuIcons: Record<EntryActionId, IconName> = {
  play: 'i-play', resume: 'i-play', playFromStart: 'i-restart', playNext: 'i-queue', addToQueue: 'i-queue',
  watchWithGroup: 'i-together', addToGroupQueue: 'i-queue', watchlist: 'i-bookmark', favorite: 'i-heart',
  watched: 'i-check', removeFromContinueWatching: 'i-x', notInterested: 'i-eyeoff', rate: 'i-star',
  addToPlaylist: 'i-listadd', addToCollection: 'i-layers', download: 'i-download', details: 'i-info',
  editMetadata: 'i-edit', refreshMetadata: 'i-refresh', separateArtist: 'i-link', delete: 'i-trash',
};

export type TitleMenuEntryOptions = Readonly<{
  /** The viewer's rating on a half-star scale of ten, worded into the Rate row. */
  rating?: number | null;
  onSelect: (id: EntryActionId) => void;
  /** Select belongs to the surface, not the model: it joins Details in the "open" group (select.html 17). */
  select?: () => void;
  /** The title's own actions are still being read: Play, Select and Details are already here. */
  loading?: boolean;
}>;

/**
 * The model's groups as menu rows, with the sections and separators the drawn menu has: no heading before the
 * first group, the Together and Owner headings where the model titles them, a rule between the rest, and the
 * owner group marked owner-only. Home and every other title menu draw through this one function.
 */
export function titleMenuEntries(groups: readonly EntryActionGroup[], {rating, onSelect, select, loading}: TitleMenuEntryOptions): MenuEntry[] {
  const selectRow: MenuEntry | undefined = select ? {id: 'select', label: t('next.selection.select'), icon: 'i-select', onSelect: select} : undefined;
  const entries: MenuEntry[] = [];
  let placed = !selectRow;
  const place = () => { if (selectRow && !placed) { entries.push(selectRow); placed = true; } };
  for (const group of groups) {
    // Select is the last row before the owner's group when the entry has no Details row to sit beside.
    if (group.id === 'manage' && !placed) { entries.push({id: 'group:select', separator: true}); place(); }
    // A rule only divides two groups, so none leads the menu when Play is absent (a title that cannot play).
    entries.push(...(group.title ? [{id: 'group:' + group.id, section: true as const, label: t(group.title), ownerOnly: group.id === 'manage'}]
      : group.id === 'play' || !entries.length ? [] : [{id: 'group:' + group.id, separator: true as const}]));
    entries.push(...group.actions.map(action => ({id: action.id,
      label: action.id === 'rate' && rating != null ? t(action.label, {stars: defaultI18n.number(rating / 2)}) : t(action.label),
      icon: menuIcons[action.id], selected: action.selected, destructive: action.destructive, onSelect: () => onSelect(action.id)})));
    if (group.id === 'open') place();
  }
  if (!placed) { if (entries.length) entries.push({id: 'group:select', separator: true}); place(); }
  if (loading) entries.push({id: 'loading', label: t('next.states.loading'), icon: 'i-clock', disabled: true, onSelect: () => {}});
  return entries;
}
