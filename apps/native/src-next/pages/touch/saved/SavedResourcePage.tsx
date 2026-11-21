import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {ResourceContentQuery} from '../../../../../../packages/client-core/src/saved';
import {AmbientArt, CoverMosaic} from '../../../kit/artwork';
import {ChipRail} from '../../../kit/browse';
import {Chip} from '../../../kit/controls';
import {musicScroller, PanelBlock, PosterGridWindow, SheetFrame, StateRow, TitleFrame, TitleRowWindow} from '../../../kit/frames';
import {HeroMetadata} from '../../../kit/layout';
import {Banner, BottomSheet, Menu} from '../../../kit/overlays';
import {SavedConfirmSheet, SavedNameSheet} from '../../../kit/saved/SavedForms';
import {SavedReorderList} from '../../../kit/saved/SavedReorderList';
import {SavedResourceMenuFacts, SavedResourceUndo, SavedResourceViewInfo, SavedResourceVisibility, viewSortLabel}
  from '../../../kit/saved/SavedResourceParts';
import {SavedSortMenu} from '../../../kit/saved/SavedSortMenu';
import {savedLibraryName} from '../../../screens/saved/presentation';
import {durationText} from '../../../../../../packages/client-core/src/presentation/kit-model/text';
import {browseFailure} from '../../shared/library/useLibrary';
import {resourceSortKeys, savedResourceRowItem, savedViewGridItem, useSavedResource, type NativeSavedResourceProps}
  from '../../shared/saved/useSavedResource';

const t = defaultI18n.t;

/** A playlist, a collection or a saved view on a phone and a tablet (collections 11, 12, 13): `TitleFrame` with its covers'
 * mosaic, Play, Shuffle and Pin, the sort and Filters over its titles as rows (a saved view's as a grid), ⋯ for the page's
 * menu, and Reorder as a full page of its own. */
export function SavedResourcePage(props: NativeSavedResourceProps) {
  const {runtime, actions, selection, libraries, back, condition} = props;
  const view = useSavedResource(props, false);
  const {resource, playlist, isPlaylist, query, offers} = view;
  const savedView = !isPlaylist && resource?.kind === 'view';
  const owner = resource?.kind === 'collection' && resource.role === 'owner' && resource.actions.includes('update');
  const pinned = !!(playlist?.pinned || resource?.pinned);
  const canPin = isPlaylist ? playlist?.actions.includes('pin') : resource?.actions.includes('pin');
  const covers = view.artwork.slice(0, 4).map(path => runtime.artwork(path)).filter(cover => !!cover);
  const facts = [
    {id: 'count', text: view.countText ?? '', role: 'identity' as const},
    ...(view.duration ? [{id: 'length', text: durationText(view.duration, t) ?? '', role: 'context' as const}] : []),
    ...(!isPlaylist && !owner && view.visibility ? [{id: 'shared', text: view.visibility.text ?? '', role: 'context' as const}] : []),
  ].filter(fact => !!fact.text);
  const group = isPlaylist ? 'playlist' : 'collection';
  const sortLabel = (id: string) => id === 'position'
    ? t('web.group.ownOrder', {group})
    : t(resourceSortKeys[id as keyof typeof resourceSortKeys] ?? 'web.saved.sort.other');
  const rows = view.rows.map(row => savedResourceRowItem(row, runtime, actions, anchor => view.openEntryMenu(row, anchor),
    selection?.item(row.media)));
  const tools = savedView ? <>
    <Chip testID="saved-view-sort">{viewSortLabel(resource)}</Chip>
    <Chip focusId="saved-view-info" icon="i-info" onPress={() => view.setInfoOpen(true)}>{t('web.saved.savedView')}</Chip>
  </> : <>
    {!!offers.sorts.length && <SavedSortMenu scope="saved-resource" focusId="saved-resource-sort" sorts={offers.sorts}
      sort={query.sort ?? 'position'} direction={query.direction} label={value => sortLabel(value.id)}
      onChange={(sort, direction) => view.chooseQuery({sort: sort as ResourceContentQuery['sort'], direction, filter: query.filter})}/>}
    {offers.filters.some(filter => filter.options.length > 1) && <Chip ref={view.filtersAnchor} testID="saved-resource-filters"
      icon="i-filter" selected={(query.filter ?? 'all') !== 'all'} onPress={() => view.setFiltersOpen(true)}>{t('next.browse.filters')}</Chip>}
    {view.reorderGranted && <Chip testID="saved-reorder" icon="i-updown" disabled={!view.canReorder}
      onPress={() => view.setReordering(true)}>{t('order.reorder')}</Chip>}
  </>;
  const empty = {kind: 'empty' as const, icon: isPlaylist ? 'i-queue' as const : 'i-stack' as const,
    title: t(isPlaylist ? 'web.saved.playlistEmpty' : savedView ? 'web.saved.viewEmpty' : 'web.saved.collectionEmpty'),
    reason: isPlaylist ? t('web.saved.playlistEmptyBody') : undefined};
  const failure = view.stateError ? browseFailure(runtime.error(view.stateError, view.retry)) : undefined;
  return <>
    <TitleFrame testID={`native-saved-resource-${isPlaylist ? 'playlist' : 'resource'}`}
      back={{label: t('saved.title'), onPress: back, testID: 'saved-resource-back'}}
      topAction={view.name ? {icon: 'i-more', label: t(isPlaylist ? 'web.saved.playlistOptions' : 'web.saved.collectionOptions'),
        onPress: view.openPageMenu, testID: 'saved-resource-menu'} : undefined}
      art={<AmbientArt treatment="page" source={view.artwork[0] ? runtime.ambient(view.artwork[0]) : undefined}/>}
      cover={{shape: 'square', node: size => <CoverMosaic covers={covers} shape="square" width={size} height={size} visible={view.active.visible}/>}}
      kicker={view.eyebrow ? {label: view.eyebrow, accent: true} : undefined}
      title={view.name ?? ''}
      metadata={<HeroMetadata facts={facts}/>}
      synopsis={isPlaylist ? playlist?.summary : resource?.summary}
      note={condition}
      primary={{icon: 'i-play', label: t('action.play'), onPress: () => view.play(), disabled: view.busy || !view.canPlayResource, testID: 'saved-play'}}
      actions={[
        {icon: 'i-shuffle', label: t('title.shuffle'), onPress: () => view.play(true), testID: 'saved-shuffle',
          disabled: view.busy || !view.canPlayResource || (view.count ?? 0) < 2},
        {icon: pinned ? 'i-bookmark-f' : 'i-bookmark', label: t(pinned ? 'pins.unpin' : 'pins.pin'), active: pinned,
          onPress: view.pin, disabled: view.busy || !canPin, testID: 'saved-pin'},
      ]}
      state={failure ?? (!view.name ? {kind: 'loading'} : undefined)}
      scroller={musicScroller({onNearEnd: view.next, testID: 'saved-resource-scroll'})}>
      {owner && <PanelBlock>
        <SavedResourceVisibility resource={resource} disabled={view.busy || view.personal?.pending} onChange={view.setVisibility}/>
      </PanelBlock>}
      <PanelBlock>
        <ChipRail>{tools}</ChipRail>
        {!!view.problem && <Banner testID="saved-resource-problem" variant="notice" tone="error" placement="inline" message={view.problem}
          action={view.personal?.retryPending
            ? {label: t('action.tryAgain'), onPress: view.retryPersonal}
            : {label: t('action.dismiss'), onPress: view.clearProblem}}/>}
        {!view.rows.length && view.phase !== 'loading' && <StateRow {...empty}/>}
      </PanelBlock>
      {savedView
        ? <PosterGridWindow testID="saved-view-grid" items={view.media.map(entry => savedViewGridItem(entry, runtime, actions, selection?.item(entry)))}/>
        : <TitleRowWindow testID="saved-resource-rows" rows={rows}/>}
      {view.phase === 'loading' && view.rows.length > 0 && <PanelBlock><StateRow kind="loading"/></PanelBlock>}
    </TitleFrame>
    {selection?.render()}
    {view.reordering && <SheetFrame kind="full" open onOpenChange={open => {if (!open) view.setReordering(false);}}
      bar={{title: t('next.saved.reordering'), action: {label: t('order.done'), onPress: () => view.setReordering(false),
        disabled: view.busy || view.dragging, testID: 'saved-reorder-done'}}}>
      <SavedReorderList rows={view.rows} runtime={runtime} disabled={view.busy || view.phase === 'loading' || !view.canReorder}
        onMove={view.moveRow} onPlace={view.placeRow} onDragging={view.setDragging}/>
    </SheetFrame>}
    <Menu open={!view.stateError && view.filtersOpen} onOpenChange={view.setFiltersOpen} anchorRef={view.filtersAnchor} presentation="sheet"
      title={t('next.browse.filters')} testID="saved-resource-filter-menu"
      items={offers.filters.flatMap(filter => filter.options.map(option => ({
        id: `${filter.id}:${option.id}`, label: option.label, trailing: defaultI18n.number(option.count),
        selected: (query.filter ?? 'all') === option.id,
        onSelect: () => {
          view.setFiltersOpen(false);
          view.chooseQuery({...query, filter: option.id as ResourceContentQuery['filter']});
        }})))}/>
    <Menu open={!view.stateError && view.pageMenu} onOpenChange={view.setPageMenu} presentation="sheet"
      title={t(isPlaylist ? 'web.saved.playlistOptions' : 'web.saved.collectionOptions')} items={view.menuItems} testID="saved-resource-page-menu"/>
    <Menu open={!view.stateError && !!view.rowMenu} onOpenChange={open => {if (!open) view.closeRowMenu();}} presentation="sheet"
      title={view.rowMenu?.entry.title ?? ''} initialFocusId={view.rowMenu?.focus} items={view.rowMenuItems} testID="saved-resource-row-menu"
      secondaryTitle={view.rowMenu && <SavedResourceMenuFacts values={[t('title.kind', {kind: view.rowMenu.entry.kind}),
        savedLibraryName(libraries, view.rowMenu.entry.libraryId)]}/>}/>
    <SavedNameSheet open={!view.stateError && !!view.renaming}
      title={t(isPlaylist ? 'web.saved.renamePlaylist' : savedView ? 'web.saved.renameView' : 'web.saved.renameCollection')}
      action={t(view.personal?.retryPending ? 'action.tryAgain' : 'action.save')}
      initialName={view.nameDraft?.name ?? view.name} initialSummary={view.nameDraft?.summary ?? (isPlaylist ? playlist?.summary : resource?.summary)}
      withSummary={isPlaylist ? playlist?.actions.includes('summary') === true : true}
      busy={view.busy} locked={view.personal?.retryPending} error={view.problem || undefined} testID="saved-rename"
      onClose={view.closeName} onSave={view.saveName}/>
    <BottomSheet open={view.infoOpen} onOpenChange={view.setInfoOpen} title={t('web.saved.savedView')} testID="saved-view-info" fitContent>
      <SavedResourceViewInfo resource={resource}/>
    </BottomSheet>
    <SavedConfirmSheet open={!view.stateError && !!view.deleting} title={t('web.saved.deleteTitle', {name: view.name ?? ''})} destructive
      body={t(isPlaylist ? 'web.saved.deletePlaylistBody' : savedView ? 'web.saved.deleteViewBody' : 'web.saved.deleteCollectionBody')}
      action={t(view.personal?.retryPending ? 'action.tryAgain'
        : isPlaylist ? 'web.saved.deletePlaylistAction' : savedView ? 'web.saved.deleteViewAction' : 'web.saved.deleteCollectionAction')}
      busy={view.busy} error={view.problem || undefined} testID="saved-delete" onClose={view.closeDelete} onConfirm={view.confirmDelete}/>
    <SavedResourceUndo toast={view.toast} onDismiss={view.clearToast}/>
  </>;
}
