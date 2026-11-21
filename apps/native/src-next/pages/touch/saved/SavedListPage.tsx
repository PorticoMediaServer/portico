import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {SavedListFilter} from '../../../../../../packages/client-core/src/saved';
import {Chip} from '../../../kit/controls';
import {BrowseFrame} from '../../../kit/frames';
import {SavedListBody, SavedListTools} from '../../../kit/saved/SavedListParts';
import {SavedSortMenu} from '../../../kit/saved/SavedSortMenu';
import {StateView} from '../../../kit/states';
import {useTheme} from '../../../kit/theme';
import {useSavedList, type NativeSavedListProps} from '../../shared/saved/useSavedList';

const t = defaultI18n.t;

export function SavedListPage(props: NativeSavedListProps) {
  const {route, runtime, actions, back, selection, inline} = props;
  const theme = useTheme();
  const {active, phase, projection, entries, history, error, loading, sorts, filters, title, count, empty, retry, chooseFilter, chooseSort, hasMore, next} = useSavedList(props);
  const select = theme.platform !== 'phone' && selection?.start ? <Chip icon="i-check" onPress={() => selection.start?.()}>{t('next.selection.select')}</Chip> : undefined;
  const toolbar = error ? undefined : route.view === 'history'
    ? select && <SavedListTools>{select}</SavedListTools>
    : (!!sorts.length || !!filters.length) && <SavedListTools>
      {!!sorts.length && <SavedSortMenu scope="saved-list" sorts={sorts} sort={projection?.query.sort} direction={projection?.query.direction} onChange={chooseSort}/>}
      {select}
      {filters.flatMap(filter => filter.options.map(option => <Chip key={`${filter.id}:${option.id}`} focusId={`list-filter-${option.id}`}
        count={option.count} selected={(projection?.query.category ?? 'all') === option.id}
        onPress={() => chooseFilter(option.id as SavedListFilter)}>{option.label}</Chip>))}
    </SavedListTools>;
  const state = error && !loading ? <StateView variant="inline" {...runtime.error(error, retry)}/>
    : !entries.length ? <StateView variant="inline" title={t(empty.title)} body={t(empty.body)}/> : undefined;
  const body = <SavedListBody testID={`native-saved-list-${route.view}`} visible={active.visible} entries={entries} history={history} runtime={runtime}
    actions={actions} selection={selection?.item} toolbar={toolbar} count={error ? undefined : count} loading={loading} state={state}
    paging={hasMore && phase === 'loading' && entries.length > 0} onNearEnd={next} footer={!error && selection?.render()}/>;
  return inline ? body : <BrowseFrame header={{title, back: {label: t('saved.title'), onPress: back, testID: 'saved-list-back'}}} grid={body}/>;
}
