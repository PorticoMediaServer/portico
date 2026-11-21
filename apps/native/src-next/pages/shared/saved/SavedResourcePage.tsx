import React, {useId, useMemo} from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {ResourceContentQuery} from '../../../../../../packages/client-core/src/saved';
import {AmbientArt} from '../../../kit/artwork';
import {Button, Chip} from '../../../kit/controls';
import {PanelBlock} from '../../../kit/frames';
import {SavedResourceFrame} from '../../../kit/frames/SavedResourceFrame';
import {RoundBarButton} from '../../../kit/layout/TopBar';
import {Banner, BottomSheet, Menu} from '../../../kit/overlays';
import {SavedConfirmSheet, SavedNameSheet} from '../../../kit/saved/SavedForms';
import {SavedLoading} from '../../../kit/saved/SavedLoading';
import {SavedReorderList} from '../../../kit/saved/SavedReorderList';
import {SavedResourceAction, SavedResourceBand, SavedResourceCaption, SavedResourceHero, SavedResourceHoldHint, SavedResourceInventory,
  SavedResourceMenuFacts, SavedResourceRow, SavedResourceUndo, SavedResourceViewGrid, SavedResourceViewInfo, SavedResourceVisibility,
  useSavedResourceLayout, viewSortLabel} from '../../../kit/saved/SavedResourceParts';
import {SavedSortMenu} from '../../../kit/saved/SavedSortMenu';
import {StateView} from '../../../kit/states';
import {useTheme} from '../../../kit/theme';
import {nativeDestinationFocusIds} from '../../../screens/destinations/focus';
import {savedLibraryName, savedViewConditionCount} from '../../../screens/saved/presentation';
import {resourceSortKeys, useSavedResource, type NativeSavedResourceProps} from './useSavedResource';

const t = defaultI18n.t;

export function SavedResourcePage(props: NativeSavedResourceProps) {
  const {route, runtime, actions, selection, libraries, back, condition} = props;
  const theme = useTheme(), instance = useId();
  const {active, phase, playlist, resource, personal, isPlaylist, rows, media, canPlayResource, count, countText, duration,
    artwork, name, eyebrow, query, manual, offers, chooseQuery, reorderGranted, canReorder, visibility, dragging, setDragging,
    reordering, setReordering, infoOpen, setInfoOpen, busy, problem, clearProblem, stateError, retry, next,
    play, pin, setVisibility, openEntryMenu, moveRow, placeRow, openPageMenu, retryPersonal, filtersOpen, setFiltersOpen, filtersAnchor,
    pageMenu, menuItems, setPageMenu, rowMenu, rowMenuItems, closeRowMenu, renaming, saveName, nameDraft, closeName,
    deleting, confirmDelete, closeDelete, toast, clearToast} = useSavedResource(props, theme.isTV);
  const grid = resource?.kind === 'view';
  const {width, columns, windowWidth, gutter, rowStride} = useSavedResourceLayout(grid);
  const focusIds = useMemo(() => nativeDestinationFocusIds(runtime.getSnapshot().authority), [runtime]);
  const region = `saved.resource.${theme.isTV ? `${instance}.` : ''}${route.view === 'playlist' ? route.playlistId : `${route.view}:${route.resourceId ?? ''}`}`;
  const sheets = <>
    <Menu open={!stateError && filtersOpen} onOpenChange={setFiltersOpen} anchorRef={filtersAnchor} presentation="sheet" title={t('next.browse.filters')} testID="saved-resource-filter-menu"
      items={offers.filters.flatMap(filter => filter.options.map(option => ({id: `${filter.id}:${option.id}`, label: option.label, trailing: defaultI18n.number(option.count),
        selected: (query.filter ?? 'all') === option.id, onSelect: () => {setFiltersOpen(false); chooseQuery({...query, filter: option.id as ResourceContentQuery['filter']});}})))}/>
    <Menu open={!stateError && pageMenu} onOpenChange={setPageMenu} presentation={theme.isTV ? 'sideSheet' : 'sheet'} title={t(isPlaylist ? 'web.saved.playlistOptions' : 'web.saved.collectionOptions')}
      items={menuItems} testID="saved-resource-page-menu"/>
    <Menu open={!stateError && !!rowMenu} onOpenChange={open => {if (!open) closeRowMenu();}} presentation={theme.isTV ? 'sideSheet' : 'sheet'} title={rowMenu?.entry.title ?? ''}
      secondaryTitle={rowMenu && <SavedResourceMenuFacts values={[t('title.kind', {kind: rowMenu.entry.kind}),
        savedLibraryName(libraries, rowMenu.entry.libraryId), theme.isTV && name ? t('web.saved.inList', {name}) : undefined]}/>}
      items={rowMenuItems} initialFocusId={rowMenu?.focus} testID="saved-resource-row-menu"/>
    <SavedNameSheet open={!stateError && !!renaming} title={t(isPlaylist ? 'web.saved.renamePlaylist' : resource?.kind === 'view' ? 'web.saved.renameView' : 'web.saved.renameCollection')}
      action={t(personal?.retryPending ? 'action.tryAgain' : 'action.save')} initialName={nameDraft?.name ?? name} initialSummary={nameDraft?.summary ?? (isPlaylist ? playlist?.summary : resource?.summary)} withSummary={isPlaylist ? playlist?.actions.includes('summary') === true : true}
      withVisibility={theme.isTV && resource?.kind === 'collection' && resource.role === 'owner' && resource.actions.includes('update')} initialVisibility={nameDraft?.visibility ?? resource?.visibility}
      busy={busy} locked={personal?.retryPending} error={problem || undefined} testID="saved-rename" onClose={closeName} onSave={saveName}/>
    <BottomSheet open={infoOpen} onOpenChange={setInfoOpen} title={t('web.saved.savedView')} testID="saved-view-info" fitContent>
      <SavedResourceViewInfo resource={resource}/>
    </BottomSheet>
    <SavedConfirmSheet open={!stateError && !!deleting} title={t('web.saved.deleteTitle', {name: name ?? ''})} destructive
      body={t(isPlaylist ? 'web.saved.deletePlaylistBody' : resource?.kind === 'view' ? 'web.saved.deleteViewBody' : 'web.saved.deleteCollectionBody')}
      action={t(personal?.retryPending ? 'action.tryAgain' : isPlaylist ? 'web.saved.deletePlaylistAction' : resource?.kind === 'view' ? 'web.saved.deleteViewAction' : 'web.saved.deleteCollectionAction')}
      busy={busy} error={problem || undefined} testID="saved-delete" onClose={closeDelete} onConfirm={confirmDelete}/>
    <SavedResourceUndo toast={toast} onDismiss={clearToast}/>
  </>;
  return <SavedResourceFrame testID={`native-saved-resource-${isPlaylist ? 'playlist' : 'resource'}`}
    visible={active.visible} region={region} rail={focusIds.rail}
    back={{label: t('saved.title'), onPress: back, testID: 'saved-resource-back'}}
    barTitle={reordering ? t('next.saved.reordering') : undefined}
    barAction={reordering ? <Button testID="saved-reorder-done" variant="glass" size="small" disabled={busy || dragging}
      onPress={() => setReordering(false)}>{t('order.done')}</Button>
      : !stateError && <RoundBarButton testID="saved-resource-menu" icon="i-more"
        label={t(isPlaylist ? 'web.saved.playlistOptions' : 'web.saved.collectionOptions')} onPress={openPageMenu}/>}
    backdrop={artwork[0] && <AmbientArt source={runtime.ambient(artwork[0])} treatment="library"/>}
    columns={columns} rowStride={rowStride}
    dragging={dragging} onNearEnd={next} sheets={sheets} footer={!reordering && selection?.render()}>
        {stateError ? <StateView variant={theme.isTV ? 'centered' : 'inline'} {...runtime.error(stateError, retry)}/> : !name ? <SavedLoading kind="resource"/> : <>
          {!reordering && <>
          <SavedResourceHero artwork={artwork} runtime={runtime} visible={active.visible} name={name} eyebrow={eyebrow}
            summary={isPlaylist ? playlist?.summary : resource?.summary} condition={condition} count={countText} duration={duration}
            visibility={!isPlaylist && (theme.isTV || resource?.role !== 'owner') ? visibility : undefined}/>
          {resource?.kind === 'collection' && resource.role === 'owner' && resource.actions.includes('update') && !theme.isTV &&
            <SavedResourceVisibility resource={resource} disabled={busy || personal?.pending} onChange={setVisibility}/>}
          <SavedResourceBand>
            <SavedResourceAction shape="primary" testID="saved-play" focusId="saved-play" variant="primary" icon="i-play" disabled={busy || !canPlayResource}
              onPress={() => play()}>{t(theme.isTV && resource?.kind === 'collection' ? 'next.cards.playAll' : 'action.play')}</SavedResourceAction>
            {/* collections.html 18/20 draw Shuffle and Pin as labelled glass buttons, and More as
                a 72 by 14-radius square: the kit's hero glass button is that geometry already. */}
            <SavedResourceAction shape="round" testID="saved-shuffle" focusId="saved-shuffle" variant="glass" icon="i-shuffle" iconOnly={!theme.isTV} disabled={busy || !canPlayResource || (count ?? 0) < 2} onPress={() => play(true)}
              accessibilityLabel={t('title.shuffle')}>{t('title.shuffle')}</SavedResourceAction>
            <SavedResourceAction shape="round" testID="saved-pin" focusId="saved-pin" variant="glass" icon={playlist?.pinned || resource?.pinned ? 'i-bookmark-f' : 'i-bookmark'} iconOnly={!theme.isTV} disabled={busy || !(isPlaylist ? playlist?.actions.includes('pin') : resource?.actions.includes('pin'))}
              onPress={pin} accessibilityLabel={t(playlist?.pinned || resource?.pinned ? 'pins.unpin' : 'pins.pin')}
             >{t(playlist?.pinned || resource?.pinned ? 'pins.unpin' : 'pins.pin')}</SavedResourceAction>
            {theme.isTV && <SavedResourceAction shape="more" testID="saved-resource-menu" focusId="saved-resource-menu" variant="glass" icon="i-more" iconOnly
              accessibilityLabel={t(isPlaylist ? 'web.saved.playlistOptions' : 'web.saved.collectionOptions')} accessibilityState={{expanded: pageMenu}}
              onPress={openPageMenu}/>}
            {reorderGranted && !theme.isTV && <Button testID="saved-reorder" focusId="saved-reorder" variant={reordering ? 'primary' : 'glass'} icon={reordering ? 'i-check' : 'i-updown'}
              disabled={!canReorder} accessibilityHint={manual ? undefined : t('order.ownOrderOnly', {group: isPlaylist ? 'playlist' : 'collection'})}
              onPress={() => setReordering(value => !value)}>{t(reordering ? 'order.done' : 'order.reorder')}</Button>}
          </SavedResourceBand>
          </>}
          {(!theme.isTV || (!isPlaylist && resource?.kind === 'view')) && <SavedResourceBand>
            {!isPlaylist && resource?.kind === 'view' ? <>
              <SavedResourceCaption>{viewSortLabel(resource)}</SavedResourceCaption>
              <Chip focusId="saved-view-info" icon="i-info" count={savedViewConditionCount(resource)} countStyle="badge" onPress={() => setInfoOpen(true)}>{t('web.saved.savedView')}</Chip>
            </> : <>
              {!!offers.sorts.length && <SavedSortMenu scope="saved-resource" focusId="saved-resource-sort" sorts={offers.sorts} sort={query.sort ?? 'position'} direction={query.direction}
                label={value => value.id === 'position' ? t('web.group.ownOrder', {group: isPlaylist ? 'playlist' : 'collection'}) : t(resourceSortKeys[value.id as keyof typeof resourceSortKeys] ?? 'web.saved.sort.other')}
                onChange={(sort, direction) => chooseQuery({sort: sort as ResourceContentQuery['sort'], direction, filter: query.filter})}/>}
              {offers.filters.some(filter => filter.options.length > 1) &&
                <Chip ref={filtersAnchor} testID="saved-resource-filters" focusId="saved-resource-filters" icon="i-filter" selected={(query.filter ?? 'all') !== 'all'} onPress={() => setFiltersOpen(true)}>{t('next.browse.filters')}</Chip>}
            </>}
            {!theme.isTV && theme.platform !== 'phone' && !reordering && selection?.start && <Chip testID="saved-resource-select" onPress={() => selection.start?.()}>{t('next.selection.select')}</Chip>}
            <SavedResourceCaption trailing>{countText}</SavedResourceCaption>
          </SavedResourceBand>}
          {!!problem && <PanelBlock><Banner testID="saved-resource-problem" variant="notice" tone="error" placement="inline" message={problem}
            action={personal?.retryPending ? {label: t('action.tryAgain'), onPress: retryPersonal} : {label: t('action.dismiss'), onPress: clearProblem}}/></PanelBlock>}
          <SavedResourceInventory tail={phase === 'loading' && rows.length ? {row: !grid, width: width / columns} : undefined}>
            {!rows.length && phase !== 'loading' && <StateView variant={theme.isTV ? 'centered' : 'inline'} icon={isPlaylist ? 'i-queue' : 'i-stack'}
              title={t(isPlaylist ? 'web.saved.playlistEmpty' : resource?.kind === 'view' ? 'web.saved.viewEmpty' : 'web.saved.collectionEmpty')}
              body={isPlaylist ? t('web.saved.playlistEmptyBody') : undefined}/>}
            {reordering && !theme.isTV ? <SavedReorderList rows={rows} runtime={runtime} disabled={busy || phase === 'loading' || !canReorder}
              onMove={moveRow} onPlace={placeRow} onDragging={setDragging}/>
              : !isPlaylist && resource?.kind === 'view'
                ? <SavedResourceViewGrid entries={media} runtime={runtime} actions={actions} selection={selection} columns={columns} windowWidth={windowWidth} gutter={gutter}/>
                : rows.map(row => <SavedResourceRow key={row.id} row={row} width={width} runtime={runtime} actions={actions} selection={selection?.item(row.media)} library={savedLibraryName(libraries, row.media.libraryId)}
                  onMore={anchor => openEntryMenu(row, anchor)}/>)}
          </SavedResourceInventory>
          {theme.isTV && (isPlaylist ? playlist?.actions.includes('remove') === true && playlist.actions.includes('reorder') === true
            : resource?.kind === 'collection' && resource.actions.includes('entries') && resource.actions.includes('reorder')) && <SavedResourceHoldHint/>}
        </>}

  </SavedResourceFrame>;
}
