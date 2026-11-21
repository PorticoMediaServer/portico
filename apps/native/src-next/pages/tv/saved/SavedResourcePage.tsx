import React from 'react';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {ResourceContentQuery} from '../../../../../../packages/client-core/src/saved';
import {durationText} from '../../../../../../packages/client-core/src/presentation/kit-model/text';
import {AmbientArt, CoverMosaic} from '../../../kit/artwork';
import {ChipRail} from '../../../kit/browse';
import {Chip} from '../../../kit/controls';
import {KeyHints} from '../../../kit/controls/KeyHints';
import {musicScroller, PanelBlock, PosterGridWindow, StateRow, TitleFrame, TitleRowWindow} from '../../../kit/frames';
import {HeroMetadata} from '../../../kit/layout';
import {Banner, BottomSheet, Menu} from '../../../kit/overlays';
import {SavedConfirmSheet, SavedNameSheet} from '../../../kit/saved/SavedForms';
import {SavedResourceMenuFacts, SavedResourceUndo, SavedResourceViewInfo, viewSortLabel} from '../../../kit/saved/SavedResourceParts';
import {savedLibraryName} from '../../../screens/saved/presentation';
import {browseFailure} from '../../shared/library/useLibrary';
import {savedResourceRowItem, savedViewGridItem, useSavedResource, type NativeSavedResourceProps} from '../../shared/saved/useSavedResource';

const t = defaultI18n.t;

/** A playlist, a collection or a saved view on a TV (collections 18, 19, 20, 21): `TitleFrame` with its covers' mosaic at the
 * left, Play (Play all on a collection), Shuffle, Pin and ⋯ for the page's menu; its titles as rows under the head (a saved
 * view's as a poster grid, with its sort and Filters). Hold OK on a row opens its sheet: Go to item, Move up, Move down,
 * Remove. A collection's owner changes who sees it in the rename sheet. Focus starts on Play. */
export function SavedResourcePage(props: NativeSavedResourceProps) {
  const {runtime, actions, selection, libraries, back, condition} = props;
  const view = useSavedResource(props, true);
  const {resource, playlist, isPlaylist} = view;
  const savedView = !isPlaylist && resource?.kind === 'view';
  const collection = !isPlaylist && resource?.kind === 'collection';
  const owner = collection && resource.role === 'owner' && resource.actions.includes('update');
  const pinned = !!(playlist?.pinned || resource?.pinned);
  const canPin = isPlaylist ? playlist?.actions.includes('pin') : resource?.actions.includes('pin');
  const canMoveRows = isPlaylist
    ? playlist?.actions.includes('remove') === true && playlist.actions.includes('reorder') === true
    : collection && resource.actions.includes('entries') && view.reorderGranted;
  const covers = view.artwork.slice(0, 4).map(path => runtime.artwork(path)).filter(cover => !!cover);
  const facts = [
    {id: 'count', text: view.countText ?? '', role: 'identity' as const},
    ...(view.duration ? [{id: 'length', text: durationText(view.duration, t) ?? '', role: 'context' as const}] : []),
    ...(collection && view.visibility ? [{id: 'shared', text: view.visibility.text ?? '', role: 'context' as const}] : []),
  ].filter(fact => !!fact.text);
  const rows = view.rows.map(row => savedResourceRowItem(row, runtime, actions, () => view.openEntryMenu(row),
    selection?.item(row.media)));
  const empty = {kind: 'empty' as const, icon: isPlaylist ? 'i-queue' as const : 'i-stack' as const,
    title: t(isPlaylist ? 'web.saved.playlistEmpty' : savedView ? 'web.saved.viewEmpty' : 'web.saved.collectionEmpty'),
    reason: isPlaylist ? t('web.saved.playlistEmptyBody') : undefined};
  const failure = view.stateError ? browseFailure(runtime.error(view.stateError, view.retry)) : undefined;
  const menuTitle = t(isPlaylist ? 'web.saved.playlistOptions' : 'web.saved.collectionOptions');
  return <>
    <TitleFrame testID={`native-tv-saved-resource-${isPlaylist ? 'playlist' : 'resource'}`} startFocus="saved-play"
      back={{label: t('saved.title'), onPress: back, testID: 'saved-resource-back'}}
      art={<AmbientArt treatment="cover" source={view.artwork[0] ? runtime.ambient(view.artwork[0]) : undefined}/>}
      cover={{shape: 'square', mosaic: true, node: size => <CoverMosaic covers={covers} shape="square" width={size} height={size} visible={view.active.visible}/>}}
      kicker={view.eyebrow ? {label: view.eyebrow, accent: true} : undefined}
      title={view.name ?? ''}
      metadata={<HeroMetadata facts={facts}/>}
      synopsis={isPlaylist ? playlist?.summary : resource?.summary}
      note={condition}
      primary={{icon: 'i-play', label: t(collection ? 'next.cards.playAll' : 'action.play'), onPress: () => view.play(),
        disabled: view.busy || !view.canPlayResource, testID: 'saved-play'}}
      actions={[
        {icon: 'i-shuffle', label: t('title.shuffle'), labelled: true, onPress: () => view.play(true), testID: 'saved-shuffle',
          disabled: view.busy || !view.canPlayResource || (view.count ?? 0) < 2},
        {icon: pinned ? 'i-bookmark-f' : 'i-bookmark', label: t(pinned ? 'pins.unpin' : 'pins.pin'), labelled: true, active: pinned,
          onPress: view.pin, disabled: view.busy || !canPin, testID: 'saved-pin'},
        ...(view.name ? [{icon: 'i-more' as const, label: menuTitle, onPress: view.openPageMenu, testID: 'saved-resource-menu'}] : []),
      ]}
      state={failure ?? (!view.name ? {kind: 'loading'} : undefined)}
      scroller={musicScroller({onNearEnd: view.next, testID: 'saved-resource-scroll'})}>
      {savedView && <PanelBlock>
        <ChipRail>
          <Chip testID="saved-view-sort">{viewSortLabel(resource)}</Chip>
          <Chip focusId="saved-view-info" icon="i-info" onPress={() => view.setInfoOpen(true)}>{t('web.saved.savedView')}</Chip>
        </ChipRail>
      </PanelBlock>}
      {(!!view.problem || !view.rows.length && view.phase !== 'loading') && <PanelBlock>
        {!!view.problem && <Banner testID="saved-resource-problem" variant="notice" tone="error" placement="inline" message={view.problem}
          action={view.personal?.retryPending
            ? {label: t('action.tryAgain'), onPress: view.retryPersonal}
            : {label: t('action.dismiss'), onPress: view.clearProblem}}/>}
        {!view.rows.length && view.phase !== 'loading' && <StateRow {...empty}/>}
      </PanelBlock>}
      {savedView
        ? <PosterGridWindow testID="saved-view-grid" items={view.media.map(entry => savedViewGridItem(entry, runtime, actions, selection?.item(entry)))}/>
        : <TitleRowWindow testID="saved-resource-rows" rows={rows} longPressOnly tvList/>}
      {view.phase === 'loading' && view.rows.length > 0 && <PanelBlock><StateRow kind="loading"/></PanelBlock>}
      {canMoveRows && view.rows.length > 0 && <PanelBlock>
        <KeyHints hints={[{key: t('next.layout.holdOk'), label: t('next.saved.native.rowActions')}]}/>
      </PanelBlock>}
    </TitleFrame>
    {selection?.render()}
    <Menu open={!view.stateError && view.pageMenu} onOpenChange={view.setPageMenu} presentation="sideSheet"
      title={menuTitle} items={view.menuItems} testID="saved-resource-page-menu"/>
    <Menu open={!view.stateError && !!view.rowMenu} onOpenChange={open => {if (!open) view.closeRowMenu();}} presentation="sideSheet"
      title={view.rowMenu?.entry.title ?? ''} initialFocusId={view.rowMenu?.focus} items={view.rowMenuItems} testID="saved-resource-row-menu"
      secondaryTitle={view.rowMenu && <SavedResourceMenuFacts values={[t('title.kind', {kind: view.rowMenu.entry.kind}),
        savedLibraryName(libraries, view.rowMenu.entry.libraryId), view.name ? t('web.saved.inList', {name: view.name}) : undefined]}/>}/>
    <SavedNameSheet open={!view.stateError && !!view.renaming}
      title={t(isPlaylist ? 'web.saved.renamePlaylist' : savedView ? 'web.saved.renameView' : 'web.saved.renameCollection')}
      action={t(view.personal?.retryPending ? 'action.tryAgain' : 'action.save')}
      initialName={view.nameDraft?.name ?? view.name}
      initialSummary={view.nameDraft?.summary ?? (isPlaylist ? playlist?.summary : resource?.summary)}
      withSummary={isPlaylist ? playlist?.actions.includes('summary') === true : true}
      withVisibility={owner} initialVisibility={view.nameDraft?.visibility ?? (collection ? resource.visibility : undefined)}
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
