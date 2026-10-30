import React, {useRef, useState} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {ContentFilter} from '../../../../../packages/client-core/src/library-content';
import type {PlaylistListFilter} from '../../../../../packages/client-core/src/saved';
import {Focusable, Segmented} from '../../kit/controls';
import {MenuRow, Popover} from '../../kit/overlays';
import {Icon} from '../../kit/Icon';
import {sortDirectionKind} from '../../../../../packages/client-core/src/presentation/browse-toolbar';
import {alpha, browseType} from '../../kit/browse/presentation';
import {useTheme} from '../../kit/theme';

const t = defaultI18n.t;

export type PlaylistToolbarValue = Readonly<{sort: string; direction: 'asc' | 'desc'; filter: PlaylistListFilter}>;
type Sort = Readonly<{id: string; labelKey: string; directions: readonly ('asc' | 'desc')[]}>;

/** collections.html 15 draws one Sort chip, one Show chip and the count. Both chips open the
 * kit's menu; the sort list, its directions and the show options with their counts are the
 * server's own published policy, and choosing one sends that policy back unchanged. */
export function PlaylistToolbar({sorts, filters, value, enabled, focusScope, onSelect}: {
  sorts: readonly Sort[]; filters: readonly ContentFilter[]; value: PlaylistToolbarValue;
  enabled: boolean; focusScope: string; onSelect: (next: Partial<PlaylistToolbarValue>) => void;
}) {
  const theme = useTheme(), chip = {height: 64, paddingHorizontal: 26, borderRadius: 14, flexDirection: 'row' as const, alignItems: 'center' as const, gap: 10, backgroundColor: alpha(theme.colors.text, .08)};
  const sortAnchor = useRef<View>(null), filterAnchor = useRef<View>(null);
  const [sortOpen, setSortOpen] = useState(false), [filterOpen, setFilterOpen] = useState(false);
  const sortLabel = value.sort ? t('playlists.sort', {field: value.sort}) : t('web.saved.sort.other');
  const filterLabel = value.filter ? t('playlists.filter.option', {group: value.filter}) : t('playlists.filter.option', {group: 'all'});
  const activeSort = sorts.find(sort => sort.id === value.sort);
  // The published playlist sorts order a name, a date or a count, and the catalogue's direction
  // wording follows the kind it orders (A–Z, Oldest/Newest, Fewest/Most).
  const kind = (field: string) => field === 'title' ? 'alpha' : field === 'count' ? 'count' : field === 'created' || field === 'updated' ? 'date' : sortDirectionKind(field);
  return <>
    <Focusable ref={sortAnchor} focusId="tv-saved-sort" testID="tv-saved-sort" focusKind="chip" focusRadius={14} disabled={!enabled}
      accessibilityRole="button" accessibilityLabel={t('next.browse.native.sort')} accessibilityState={{expanded: sortOpen}} onPress={() => setSortOpen(true)} style={chip}>
      <Icon name="i-sort" size={26} color={theme.colors.textTertiary}/>
      <Text style={[browseType(theme, 22, 700), {color: theme.colors.text}]}>{sortLabel}</Text>
    </Focusable>
    <Focusable ref={filterAnchor} focusId="tv-saved-filter" testID="tv-saved-filter" focusKind="chip" focusRadius={14} disabled={!enabled}
      accessibilityRole="button" accessibilityLabel={t('playlists.filter')} accessibilityState={{expanded: filterOpen}} onPress={() => setFilterOpen(true)} style={chip}>
      <Icon name="i-filter" size={26} color={theme.colors.textTertiary}/>
      <Text style={[browseType(theme, 22, 700), {color: theme.colors.text}]}>{filterLabel}</Text>
    </Focusable>
    <Popover open={sortOpen} onOpenChange={setSortOpen} anchorRef={sortAnchor} returnFocusRef={sortAnchor} title={t('next.browse.native.sort')} width={560} placement="bottom-end" testID={`${focusScope}.sort-menu`}>
      <ScrollView style={{maxHeight: 680}}>{sorts.map(sort => {
        const flipped = value.direction === 'asc' ? 'desc' as const : 'asc' as const;
        return <MenuRow key={sort.id} focusId={`tv-saved-sort:${sort.id}`} label={t('playlists.sort', {field: sort.id})}
          selected={sort.id === value.sort} accessibilityRole="radio" accessibilityState={{checked: sort.id === value.sort}} variant="tv"
          onPress={() => {
            const next = sort.id === value.sort && sort.directions.includes(flipped) ? flipped : sort.directions.includes(value.direction) ? value.direction : sort.directions[0] ?? value.direction;
            onSelect({sort: sort.id, direction: next}); setSortOpen(false);
          }}/>;
      })}</ScrollView>
      {!!activeSort && activeSort.directions.length > 1 && <View style={{borderTopWidth: 1, borderTopColor: theme.colors.line, padding: 8, gap: 6}}>
        <Text style={[browseType(theme, 20, 700), {color: theme.colors.textTertiary}]}>{t('next.browse.native.order')}</Text>
        <Segmented variant="browse" value={value.direction} onValueChange={dir => onSelect({direction: dir as 'asc' | 'desc'})}
          itemProps={item => ({focusId: `tv-saved-order:${item.value}`})}
          items={activeSort.directions.map(dir => ({value: dir, label: t('web.filters.direction', {kind: kind(value.sort), dir})}))}/>
      </View>}
    </Popover>
    <Popover open={filterOpen} onOpenChange={setFilterOpen} anchorRef={filterAnchor} returnFocusRef={filterAnchor} title={t('playlists.filter')} width={560} placement="bottom-end" testID={`${focusScope}.filter-menu`}>
      <ScrollView style={{maxHeight: 680}}>{filters.flatMap(filter => filter.options.map(option => <MenuRow key={`${filter.id}:${option.id}`} focusId={`tv-saved-show:${filter.id}:${option.id}`}
        label={t('playlists.filter.option', {group: option.id})} trailing={defaultI18n.number(option.count)}
        selected={value.filter === option.id} accessibilityRole="radio" accessibilityState={{checked: value.filter === option.id}} variant="tv"
        onPress={() => {onSelect({filter: option.id as PlaylistListFilter}); setFilterOpen(false);}}/>))}</ScrollView>
    </Popover>
  </>;
}
