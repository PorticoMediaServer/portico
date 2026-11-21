import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {PlaylistListFilter} from '../../../../../../packages/client-core/src/saved';
import {Button, Chip} from '../../../kit/controls';
import {BrowseFrame} from '../../../kit/frames';
import {TopBar} from '../../../kit/layout/TopBar';
import {Banner, Menu} from '../../../kit/overlays';
import {SavedDirectoryBody} from '../../../kit/saved/SavedDirectoryParts';
import {SavedConfirmSheet, SavedNameSheet} from '../../../kit/saved/SavedForms';
import {SavedListTools} from '../../../kit/saved/SavedListParts';
import {SavedSortMenu} from '../../../kit/saved/SavedSortMenu';
import {StateView} from '../../../kit/states';
import {useSavedDirectory, type NativeSavedDirectoryProps} from './useSavedDirectory';

const t = defaultI18n.t;
const empty = {
  playlists: {title: 'web.saved.empty.playlists', body: 'web.saved.empty.playlistsBody'},
  collections: {title: 'web.saved.empty.collections', body: 'web.saved.empty.collectionsBody'},
  views: {title: 'web.saved.empty.views', body: 'web.saved.empty.viewsBody'},
} as const;

export function SavedDirectoryPage(props: NativeSavedDirectoryProps) {
  const {route, runtime, commands, inline, back} = props;
  const model = useSavedDirectory(props);
  const {active, error, loading, title, createEnabled, filters, sorts, query, rows, problem,
    creating, busy, menu, renaming, removing, commandBusy, paging} = model;
  const createTitle = t(route.view === 'playlists' ? 'web.saved.newPlaylist' : 'web.saved.newCollection');
  const newItem = createEnabled && <Button testID={`saved-new-${route.view}`} variant="icon" size="small" icon="i-plus"
    iconOnly accessibilityLabel={createTitle} onPress={model.showCreate}/>;
  const toolbar = route.view === 'playlists' && !error && (!!filters.length || !!sorts.length) && <SavedListTools>
    {!!sorts.length && <SavedSortMenu scope="saved-directory" sorts={sorts} sort={query?.sort} direction={query?.direction} onChange={model.chooseSort}/>}
    {filters.flatMap(filter => filter.options.map(option => <Chip key={`${filter.id}:${option.id}`} focusId={`dir-filter-${option.id}`}
      selected={(query?.category ?? 'all') === option.id} count={option.count}
      onPress={() => model.chooseFilter(option.id as PlaylistListFilter)}>{option.label}</Chip>))}
  </SavedListTools>;
  const state = error && !loading ? <StateView variant="inline" {...runtime.error(error, model.retry)}/>
    : !rows.length ? <StateView variant="inline" title={t(empty[route.view].title)} body={t(empty[route.view].body)}/> : undefined;
  const body = <SavedDirectoryBody testID={`native-saved-directory-${route.view}`} visible={active.visible} toolbar={<>
    {inline && newItem && <TopBar surface safeArea={false} trailing={newItem}/>}
    {toolbar}
  </>} help={route.view === 'views' ? t('next.saved.viewsHelp') : undefined}
    notice={problem ? <Banner variant="notice" tone="error" placement="inline" message={problem}
      action={{label: t('action.dismiss'), onPress: model.dismissProblem}}/> : undefined}
    state={state} rows={rows} loading={loading} paging={paging} onNearEnd={model.next} overlays={<>
      <SavedNameSheet open={creating} title={createTitle} action={t('web.saved.create')}
        withSummary withVisibility={route.view === 'collections'} busy={busy} error={problem || undefined}
        testID="saved-create" onClose={model.closeCreate} onSave={model.create}/>
      {!!commands && <Menu testID="saved-directory-menu" open={!!menu && !error} onOpenChange={model.closeMenu}
        anchorRef={menu?.anchor} returnFocusRef={menu?.anchor} title={menu?.target.resource.name} items={model.menuItems}/>}
      <SavedNameSheet open={!!renaming && !error} title={t(renaming?.resource.kind === 'view' ? 'web.saved.renameView' : 'web.saved.renameCollection')}
        action={t('action.save')} initialName={renaming?.resource.name ?? ''} initialSummary={renaming?.resource.summary ?? ''}
        withSummary busy={commandBusy} error={problem || undefined} testID="saved-directory-rename" onClose={model.closeRename} onSave={model.rename}/>
      <SavedConfirmSheet open={!!removing && !error} title={t('web.saved.deleteTitle', {name: removing?.resource.name ?? ''})} destructive busy={commandBusy}
        body={t(removing?.resource.kind === 'view' ? 'web.saved.deleteViewBody' : 'web.saved.deleteCollectionBody')}
        action={t(removing?.resource.kind === 'view' ? 'web.saved.deleteViewAction' : 'web.saved.deleteCollectionAction')}
        error={problem || undefined} testID="saved-directory-delete" onClose={model.closeDelete} onConfirm={model.remove}/>
    </>}/>;
  return inline ? body : <BrowseFrame header={{title, trailing: newItem, back: {label: t('saved.title'), onPress: back, testID: 'saved-directory-back'}}} grid={body}/>;
}
