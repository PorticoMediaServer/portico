import React from 'react';
import type {View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {ShowWorkspaceData, WorkspaceSeason} from '../../../../../packages/client-core/src/show-workspace';
import {SeasonSheet, type SeasonSheetRow} from '../../kit/layout/SeasonSheet';

export type NativeSeasonsSheetProps = Readonly<{
  open: boolean;
  onOpenChange: (open: boolean) => void;
  seasons: readonly WorkspaceSeason[];
  total: number;
  selectedId: string | null;
  onSelect: (seasonId: string) => void;
  /** The workspace's episode groups (tv-detail f5: an owner's Ranges become groups of 100; hidden seasons one list),
   *  listed after the seasons as web does. Without them, episodes past the first group can't be reached. */
  groups?: ShowWorkspaceData['groups'];
  selectedGroup?: string;
  onSelectGroup?: (group: string) => void;
  onMore?: () => void;
  hideWatched?: boolean;
  onHideWatched?: (value: boolean) => void;
  returnFocusRef?: React.RefObject<View | null>;
}>;

/** The show's seasons and groups as the kit's season sheet (kit/layout/SeasonSheet.tsx draws it). */
export function NativeSeasonsSheet(props: NativeSeasonsSheetProps) {
  const t = defaultI18n.t;
  const groups = props.onSelectGroup ? props.groups ?? [] : [];
  const selectedGroup = groups.find(group => group.id === props.selectedGroup)?.id;
  const rows: SeasonSheetRow[] = [
    ...props.seasons.map(season => {
      const watched = season.watchedCount, total = season.episodeCount;
      const complete = watched !== undefined && total !== undefined && total > 0 && watched >= total;
      const partial = watched !== undefined && total !== undefined && !complete && watched > 0;
      return {id: season.id, number: season.number, label: season.title || t('title.season', {number: season.number}),
        count: season.episodeCount !== undefined ? t('count.episodes', {count: season.episodeCount}) : undefined,
        progress: complete ? t('card.watched') : partial ? t('title.seasonProgress', {watched, total}) : undefined, complete, fraction: partial && total ? watched! / total : undefined};
    }),
    ...groups.map(group => ({id: `group:${group.id}`, label: group.title, count: group.count > 0 ? t('count.episodes', {count: group.count}) : undefined})),
  ];
  const selectedId = selectedGroup ? `group:${selectedGroup}` : props.selectedId;
  return <SeasonSheet open={props.open} onOpenChange={props.onOpenChange} rows={rows} total={props.total} selectedId={selectedId}
    onSelect={id => id.startsWith('group:') ? props.onSelectGroup?.(id.slice(6)) : props.onSelect(id)}
    onMore={props.onMore} hideWatched={props.hideWatched} onHideWatched={props.onHideWatched} returnFocusRef={props.returnFocusRef}/>;
}
